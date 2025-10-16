package engine

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sync"
	"time"

	"twimulator/httpstub"
	"twimulator/model"
)

// Engine is the main interface for the Twilio Voice simulator
type Engine interface {
	// Core lifecycle
	CreateCall(params CreateCallParams) (*model.Call, error)
	Hangup(callSID model.SID) error
	SendDigits(callSID model.SID, digits string) error

	// Introspection
	GetCall(callSID model.SID) (*model.Call, bool)
	ListCalls(filter CallFilter) []*model.Call
	GetQueue(name string) (*model.Queue, bool)
	GetConference(name string) (*model.Conference, bool)
	Snapshot() *StateSnapshot

	// Time control
	SetAutoTime(enabled bool)
	Advance(d time.Duration)
	Clock() Clock

	// Shutdown
	Close() error
}

// CreateCallParams defines parameters for creating a call
type CreateCallParams struct {
	From             string
	To               string
	AnswerURL        string
	StatusCallback   string
	MachineDetection bool
	Timeout          time.Duration
	Vars             map[string]string
}

// CallFilter allows filtering calls
type CallFilter struct {
	To     string
	From   string
	Status *model.CallStatus
}

// StateSnapshot is a JSON-serializable snapshot of the engine state
type StateSnapshot struct {
	Calls       map[model.SID]*model.Call       `json:"calls"`
	Queues      map[string]*model.Queue         `json:"queues"`
	Conferences map[string]*model.Conference    `json:"conferences"`
	Timestamp   time.Time                       `json:"timestamp"`
}

// EngineImpl is the concrete implementation of Engine
type EngineImpl struct {
	mu          sync.RWMutex
	clock       Clock
	webhook     httpstub.WebhookClient
	accountSID  string
	apiVersion  string

	calls       map[model.SID]*model.Call
	queues      map[string]*model.Queue
	conferences map[string]*model.Conference

	// Call runners
	runners map[model.SID]*CallRunner
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// EngineOption configures the engine
type EngineOption func(*EngineImpl)

// WithManualClock configures the engine to use a manual clock
func WithManualClock() EngineOption {
	return func(e *EngineImpl) {
		e.clock = NewManualClock(time.Time{})
	}
}

// WithAutoClock configures the engine to use real time
func WithAutoClock() EngineOption {
	return func(e *EngineImpl) {
		e.clock = NewAutoClock()
	}
}

// WithClock sets a specific clock implementation
func WithClock(clock Clock) EngineOption {
	return func(e *EngineImpl) {
		e.clock = clock
	}
}

// WithWebhookClient sets the webhook client
func WithWebhookClient(client httpstub.WebhookClient) EngineOption {
	return func(e *EngineImpl) {
		e.webhook = client
	}
}

// WithAccountSID sets the account SID
func WithAccountSID(sid string) EngineOption {
	return func(e *EngineImpl) {
		e.accountSID = sid
	}
}

// NewEngine creates a new engine instance
func NewEngine(opts ...EngineOption) *EngineImpl {
	ctx, cancel := context.WithCancel(context.Background())

	e := &EngineImpl{
		clock:       NewManualClock(time.Time{}), // default to manual
		webhook:     httpstub.NewDefaultWebhookClient(10 * time.Second),
		accountSID:  "AC00000000000000000000000000000000",
		apiVersion:  "2010-04-01",
		calls:       make(map[model.SID]*model.Call),
		queues:      make(map[string]*model.Queue),
		conferences: make(map[string]*model.Conference),
		runners:     make(map[model.SID]*CallRunner),
		ctx:         ctx,
		cancel:      cancel,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// CreateCall initiates a new call
func (e *EngineImpl) CreateCall(params CreateCallParams) (*model.Call, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if params.Timeout == 0 {
		params.Timeout = 30 * time.Second
	}

	if params.AnswerURL == "" {
		return nil, fmt.Errorf("AnswerURL is required")
	}

	call := &model.Call{
		SID:            model.NewCallSID(),
		From:           params.From,
		To:             params.To,
		Direction:      model.Outbound,
		Status:         model.CallQueued,
		StartAt:        e.clock.Now(),
		Timeline:       []model.Event{},
		Variables:      params.Vars,
		AnswerURL:      params.AnswerURL,
		StatusCallback: params.StatusCallback,
	}

	if call.Variables == nil {
		call.Variables = make(map[string]string)
	}

	// Add creation event
	call.Timeline = append(call.Timeline, model.NewEvent(
		e.clock.Now(),
		"call.created",
		map[string]any{
			"sid":    call.SID,
			"from":   call.From,
			"to":     call.To,
			"status": call.Status,
		},
	))

	e.calls[call.SID] = call

	// Start runner
	runner := NewCallRunner(call, e, params.Timeout)
	e.runners[call.SID] = runner

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		runner.Run(e.ctx)
	}()

	return call, nil
}

// Hangup terminates a call
func (e *EngineImpl) Hangup(callSID model.SID) error {
	e.mu.Lock()
	call, exists := e.calls[callSID]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("call %s not found", callSID)
	}
	runner := e.runners[callSID]
	e.mu.Unlock()

	if runner != nil {
		runner.Hangup()
	}

	// Update call status
	e.mu.Lock()
	if call.Status != model.CallCompleted {
		e.updateCallStatus(call, model.CallCompleted)
		now := e.clock.Now()
		call.EndedAt = &now
	}
	e.mu.Unlock()

	return nil
}

// SendDigits sends DTMF digits to a call (for Gather)
func (e *EngineImpl) SendDigits(callSID model.SID, digits string) error {
	e.mu.RLock()
	runner, exists := e.runners[callSID]
	e.mu.RUnlock()

	if !exists {
		return fmt.Errorf("call %s not found", callSID)
	}

	runner.SendDigits(digits)
	return nil
}

// GetCall retrieves a call by SID
func (e *EngineImpl) GetCall(callSID model.SID) (*model.Call, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	call, exists := e.calls[callSID]
	return call, exists
}

// ListCalls returns all calls matching the filter
func (e *EngineImpl) ListCalls(filter CallFilter) []*model.Call {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*model.Call
	for _, call := range e.calls {
		if filter.To != "" && call.To != filter.To {
			continue
		}
		if filter.From != "" && call.From != filter.From {
			continue
		}
		if filter.Status != nil && call.Status != *filter.Status {
			continue
		}
		result = append(result, call)
	}
	return result
}

// GetQueue retrieves a queue by name
func (e *EngineImpl) GetQueue(name string) (*model.Queue, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	queue, exists := e.queues[name]
	return queue, exists
}

// GetConference retrieves a conference by name
func (e *EngineImpl) GetConference(name string) (*model.Conference, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	conf, exists := e.conferences[name]
	return conf, exists
}

// Snapshot returns a deep copy of the current state
func (e *EngineImpl) Snapshot() *StateSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap := &StateSnapshot{
		Calls:       make(map[model.SID]*model.Call),
		Queues:      make(map[string]*model.Queue),
		Conferences: make(map[string]*model.Conference),
		Timestamp:   e.clock.Now(),
	}

	for sid, call := range e.calls {
		callCopy := *call
		callCopy.Timeline = append([]model.Event{}, call.Timeline...)
		callCopy.Variables = make(map[string]string)
		for k, v := range call.Variables {
			callCopy.Variables[k] = v
		}
		snap.Calls[sid] = &callCopy
	}

	for name, queue := range e.queues {
		queueCopy := *queue
		queueCopy.Members = append([]model.SID{}, queue.Members...)
		queueCopy.Timeline = append([]model.Event{}, queue.Timeline...)
		snap.Queues[name] = &queueCopy
	}

	for name, conf := range e.conferences {
		confCopy := *conf
		confCopy.Participants = append([]model.SID{}, conf.Participants...)
		confCopy.Timeline = append([]model.Event{}, conf.Timeline...)
		snap.Conferences[name] = &confCopy
	}

	return snap
}

// SetAutoTime switches between manual and auto time
func (e *EngineImpl) SetAutoTime(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if enabled {
		if _, ok := e.clock.(*AutoClock); !ok {
			log.Println("Switching to auto clock")
			e.clock = NewAutoClock()
		}
	} else {
		if _, ok := e.clock.(*ManualClock); !ok {
			log.Println("Switching to manual clock")
			e.clock = NewManualClock(e.clock.Now())
		}
	}
}

// Advance advances the manual clock (no-op for auto clock)
func (e *EngineImpl) Advance(d time.Duration) {
	e.mu.RLock()
	clock := e.clock
	e.mu.RUnlock()

	if mc, ok := clock.(*ManualClock); ok {
		mc.Advance(d)
	}
}

// Clock returns the current clock
func (e *EngineImpl) Clock() Clock {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.clock
}

// Close shuts down the engine
func (e *EngineImpl) Close() error {
	e.cancel()
	e.wg.Wait()
	return nil
}

// Internal helper to update call status and notify
func (e *EngineImpl) updateCallStatus(call *model.Call, newStatus model.CallStatus) {
	if call.Status == newStatus {
		return
	}

	oldStatus := call.Status
	call.Status = newStatus

	// Add timeline event
	call.Timeline = append(call.Timeline, model.NewEvent(
		e.clock.Now(),
		"status.changed",
		map[string]any{
			"from": oldStatus,
			"to":   newStatus,
		},
	))

	// Trigger status callback
	if call.StatusCallback != "" {
		go e.sendStatusCallback(call)
	}
}

// sendStatusCallback posts to the status callback URL
func (e *EngineImpl) sendStatusCallback(call *model.Call) {
	form := e.buildCallbackForm(call)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, body, headers, err := e.webhook.POST(ctx, call.StatusCallback, form)

	// Log the webhook
	e.mu.Lock()
	call.Timeline = append(call.Timeline, model.NewEvent(
		e.clock.Now(),
		"webhook.status_callback",
		map[string]any{
			"url":     call.StatusCallback,
			"status":  status,
			"error":   err,
			"headers": headers,
			"body":    string(body),
		},
	))
	e.mu.Unlock()
}

// buildCallbackForm builds form data for Twilio-style callbacks
func (e *EngineImpl) buildCallbackForm(call *model.Call) url.Values {
	form := url.Values{}
	form.Set("CallSid", string(call.SID))
	form.Set("AccountSid", e.accountSID)
	form.Set("From", call.From)
	form.Set("To", call.To)
	form.Set("CallStatus", string(call.Status))
	form.Set("Direction", string(call.Direction))
	form.Set("ApiVersion", e.apiVersion)
	form.Set("Timestamp", e.clock.Now().Format(time.RFC3339))

	if call.ParentCallSID != nil {
		form.Set("ParentCallSid", string(*call.ParentCallSID))
	}

	return form
}

// getOrCreateQueue gets or creates a queue
func (e *EngineImpl) getOrCreateQueue(name string) *model.Queue {
	if queue, exists := e.queues[name]; exists {
		return queue
	}

	queue := &model.Queue{
		Name:     name,
		SID:      model.NewQueueSID(),
		Members:  []model.SID{},
		Timeline: []model.Event{},
	}
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		e.clock.Now(),
		"queue.created",
		map[string]any{"name": name, "sid": queue.SID},
	))
	e.queues[name] = queue
	return queue
}

// getOrCreateConference gets or creates a conference
func (e *EngineImpl) getOrCreateConference(name string) *model.Conference {
	if conf, exists := e.conferences[name]; exists {
		return conf
	}

	conf := &model.Conference{
		Name:         name,
		SID:          model.NewConferenceSID(),
		Participants: []model.SID{},
		Status:       model.ConferenceCreated,
		Timeline:     []model.Event{},
		CreatedAt:    e.clock.Now(),
	}
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		e.clock.Now(),
		"conference.created",
		map[string]any{"name": name, "sid": conf.SID},
	))
	e.conferences[name] = conf
	return conf
}
