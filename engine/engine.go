package engine

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	twilioopenapi "github.com/twilio/twilio-go/rest/api/v2010"

	"twimulator/httpstub"
	"twimulator/model"
)

// Engine is the main interface for the Twilio Voice simulator
type Engine interface {
	// Subaccount management
	CreateAccount(params *twilioopenapi.CreateAccountParams) (*twilioopenapi.ApiV2010Account, error)
	ListAccount(params *twilioopenapi.ListAccountParams) ([]twilioopenapi.ApiV2010Account, error)
	CreateIncomingPhoneNumber(params *twilioopenapi.CreateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error)
	ListIncomingPhoneNumber(params *twilioopenapi.ListIncomingPhoneNumberParams) ([]twilioopenapi.ApiV2010IncomingPhoneNumber, error)
	DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error
	CreateApplication(params *twilioopenapi.CreateApplicationParams) (*twilioopenapi.ApiV2010Application, error)

	// Core lifecycle
	CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error)
	UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error)
	Hangup(callSID model.SID) error
	SendDigits(callSID model.SID, digits string) error

	// Introspection
	FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error)
	ListCalls(filter CallFilter) []*model.Call
	GetQueue(accountSID model.SID, name string) (*model.Queue, bool)
	GetConference(accountSID model.SID, name string) (*model.Conference, bool)
	Snapshot() *StateSnapshot

	// Time control
	SetAutoTime(enabled bool)
	Advance(d time.Duration)
	Clock() Clock

	// Shutdown
	Close() error
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
	SubAccounts map[model.SID]*model.SubAccount `json:"sub_accounts"`
	Timestamp   time.Time                       `json:"timestamp"`
}

// EngineImpl is the concrete implementation of Engine
type EngineImpl struct {
	mu         sync.RWMutex
	clock      Clock
	webhook    httpstub.WebhookClient
	apiVersion string

	// Subaccount management
	subAccounts     map[model.SID]*model.SubAccount
	incomingNumbers map[model.SID]map[string]*incomingNumber
	applications    map[model.SID]map[model.SID]*applicationRecord

	// Resources are scoped by subaccount SID
	calls       map[model.SID]*model.Call                  // All calls across all subaccounts
	queues      map[model.SID]map[string]*model.Queue      // subAccountSID -> queue name -> Queue
	conferences map[model.SID]map[string]*model.Conference // subAccountSID -> conf name -> Conference

	// Call runners
	runners map[model.SID]*CallRunner
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// EngineOption configures the engine
type EngineOption func(*EngineImpl)

type incomingNumber struct {
	SID         model.SID
	PhoneNumber string
	CreatedAt   time.Time
}

type applicationRecord struct {
	SID                  model.SID
	FriendlyName         string
	VoiceMethod          string
	VoiceURL             string
	StatusCallbackMethod string
	StatusCallback       string
	CreatedAt            time.Time
}

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

// NewEngine creates a new engine instance
func NewEngine(opts ...EngineOption) *EngineImpl {
	ctx, cancel := context.WithCancel(context.Background())

	e := &EngineImpl{
		clock:           NewManualClock(time.Time{}), // default to manual
		webhook:         httpstub.NewDefaultWebhookClient(10 * time.Second),
		apiVersion:      "2010-04-01",
		subAccounts:     make(map[model.SID]*model.SubAccount),
		incomingNumbers: make(map[model.SID]map[string]*incomingNumber),
		applications:    make(map[model.SID]map[model.SID]*applicationRecord),
		calls:           make(map[model.SID]*model.Call),
		queues:          make(map[model.SID]map[string]*model.Queue),
		conferences:     make(map[model.SID]map[string]*model.Conference),
		runners:         make(map[model.SID]*CallRunner),
		ctx:             ctx,
		cancel:          cancel,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// CreateAccount creates a new simulated Twilio subaccount
func (e *EngineImpl) CreateAccount(params *twilioopenapi.CreateAccountParams) (*twilioopenapi.ApiV2010Account, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	friendlyName := ""
	if params != nil && params.FriendlyName != nil {
		friendlyName = *params.FriendlyName
	}

	now := e.clock.Now()
	sid := model.NewSubAccountSID()
	authToken := model.NewAuthToken()

	subAccount := &model.SubAccount{
		SID:          sid,
		FriendlyName: friendlyName,
		Status:       "active",
		CreatedAt:    now,
		AuthToken:    authToken,
	}

	e.subAccounts[subAccount.SID] = subAccount

	// Initialize resource maps for this subaccount
	e.queues[subAccount.SID] = make(map[string]*model.Queue)
	e.conferences[subAccount.SID] = make(map[string]*model.Conference)
	e.incomingNumbers[subAccount.SID] = make(map[string]*incomingNumber)
	e.applications[subAccount.SID] = make(map[model.SID]*applicationRecord)

	sidStr := string(sid)
	authTokenCopy := authToken
	status := subAccount.Status
	friendlyCopy := friendlyName
	created := now.UTC().Format(time.RFC1123Z)

	return &twilioopenapi.ApiV2010Account{
		Sid:          &sidStr,
		AuthToken:    &authTokenCopy,
		FriendlyName: &friendlyCopy,
		Status:       &status,
		DateCreated:  &created,
	}, nil
}

// ListAccount returns Twilio-style account representations filtered by optional friendly name
func (e *EngineImpl) ListAccount(params *twilioopenapi.ListAccountParams) ([]twilioopenapi.ApiV2010Account, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var friendly string
	if params != nil && params.FriendlyName != nil {
		friendly = *params.FriendlyName
	}

	matches := make([]*model.SubAccount, 0)
	for _, sa := range e.subAccounts {
		if friendly != "" && sa.FriendlyName != friendly {
			continue
		}
		matches = append(matches, sa)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].CreatedAt.Equal(matches[j].CreatedAt) {
			return matches[i].SID < matches[j].SID
		}
		return matches[i].CreatedAt.Before(matches[j].CreatedAt)
	})

	results := make([]twilioopenapi.ApiV2010Account, len(matches))
	for i, sa := range matches {
		sidStr := string(sa.SID)
		authToken := sa.AuthToken
		friendlyCopy := sa.FriendlyName
		status := sa.Status
		created := sa.CreatedAt.UTC().Format(time.RFC1123Z)
		results[i] = twilioopenapi.ApiV2010Account{
			Sid:          &sidStr,
			AuthToken:    &authToken,
			FriendlyName: &friendlyCopy,
			Status:       &status,
			DateCreated:  &created,
		}
	}

	return results, nil
}

// CreateCall initiates a new call using Twilio-compatible parameters
func (e *EngineImpl) CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil {
		return nil, fmt.Errorf("params is required")
	}

	accountSID := ""
	if params.PathAccountSid != nil {
		accountSID = *params.PathAccountSid
	}
	if accountSID == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}

	answerURL := ""
	if params.Url != nil {
		answerURL = *params.Url
	}
	if answerURL == "" {
		return nil, fmt.Errorf("Url is required")
	}

	from := ""
	if params.From != nil {
		from = *params.From
	}
	if from == "" {
		return nil, fmt.Errorf("From number is required")
	}

	to := ""
	if params.To != nil {
		to = *params.To
	}

	timeout := 30 * time.Second
	if params.Timeout != nil && *params.Timeout > 0 {
		timeout = time.Duration(*params.Timeout) * time.Second
	}

	statusCallback := ""
	if params.StatusCallback != nil {
		statusCallback = *params.StatusCallback
	}

	statusEvents := []string{}
	if params.StatusCallbackEvent != nil {
		statusEvents = append(statusEvents, (*params.StatusCallbackEvent)...)
	}

	callToken := ""
	if params.CallToken != nil {
		callToken = *params.CallToken
	}

	accountSIDModel := model.SID(accountSID)

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.subAccounts[accountSIDModel]; !exists {
		return nil, fmt.Errorf("subaccount %s not found", accountSID)
	}

	numbers := e.incomingNumbers[accountSIDModel]
	if numbers == nil || numbers[from] == nil {
		return nil, fmt.Errorf("from number %s not provisioned for account %s", from, accountSID)
	}

	now := e.clock.Now()
	call := &model.Call{
		SID:            model.NewCallSID(),
		AccountSID:     accountSIDModel,
		From:           from,
		To:             to,
		Direction:      model.Outbound,
		Status:         model.CallQueued,
		StartAt:        now,
		Timeline:       []model.Event{},
		Variables:      make(map[string]string),
		AnswerURL:      answerURL,
		StatusCallback: statusCallback,
	}

	if len(statusEvents) > 0 {
		call.Variables["status_callback_event"] = strings.Join(statusEvents, ",")
	}
	if callToken != "" {
		call.Variables["call_token"] = callToken
	}

	// Preserve machine detection flag if provided
	if params.MachineDetection != nil {
		call.Variables["machine_detection"] = *params.MachineDetection
	}

	call.Timeline = append(call.Timeline, model.NewEvent(
		now,
		"call.created",
		map[string]any{
			"sid":    call.SID,
			"from":   call.From,
			"to":     call.To,
			"status": call.Status,
		},
	))

	e.calls[call.SID] = call

	runner := NewCallRunner(call, e, timeout)
	e.runners[call.SID] = runner

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		runner.Run(e.ctx)
	}()

	return buildAPICallResponse(call, e.apiVersion), nil
}

// CreateIncomingPhoneNumber provisions a phone number for an account
func (e *EngineImpl) CreateIncomingPhoneNumber(params *twilioopenapi.CreateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil {
		return nil, fmt.Errorf("params is required")
	}

	accountSID := ""
	if params.PathAccountSid != nil {
		accountSID = *params.PathAccountSid
	}
	if accountSID == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}

	phone := ""
	if params.PhoneNumber != nil {
		phone = *params.PhoneNumber
	}
	if phone == "" {
		return nil, fmt.Errorf("PhoneNumber is required")
	}

	accountSIDModel := model.SID(accountSID)

	e.mu.Lock()
	defer e.mu.Unlock()

	subAccount, exists := e.subAccounts[accountSIDModel]
	if !exists {
		return nil, fmt.Errorf("subaccount %s not found", accountSID)
	}

	numbers := e.incomingNumbers[accountSIDModel]
	if numbers == nil {
		numbers = make(map[string]*incomingNumber)
		e.incomingNumbers[accountSIDModel] = numbers
	}
	if _, exists := numbers[phone]; exists {
		return nil, fmt.Errorf("phone number %s already exists", phone)
	}

	now := e.clock.Now()
	sid := model.NewPhoneNumberSID()
	record := &incomingNumber{
		SID:         sid,
		PhoneNumber: phone,
		CreatedAt:   now,
	}
	numbers[phone] = record
	subAccount.IncomingNumbers = append(subAccount.IncomingNumbers, phone)

	phoneCopy := phone
	sidStr := string(sid)
	created := now.UTC().Format(time.RFC1123Z)

	return &twilioopenapi.ApiV2010IncomingPhoneNumber{
		Sid:         &sidStr,
		PhoneNumber: &phoneCopy,
		DateCreated: &created,
	}, nil
}

// ListIncomingPhoneNumber returns provisioned numbers for an account
func (e *EngineImpl) ListIncomingPhoneNumber(params *twilioopenapi.ListIncomingPhoneNumberParams) ([]twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)

	filterPhone := ""
	if params.PhoneNumber != nil {
		filterPhone = *params.PhoneNumber
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	if _, exists := e.subAccounts[accountSID]; !exists {
		return nil, fmt.Errorf("subaccount %s not found", accountSID)
	}

	numbers := e.incomingNumbers[accountSID]
	if numbers == nil {
		return []twilioopenapi.ApiV2010IncomingPhoneNumber{}, nil
	}

	result := make([]twilioopenapi.ApiV2010IncomingPhoneNumber, 0)
	for phone, rec := range numbers {
		if filterPhone != "" && phone != filterPhone {
			continue
		}
		phoneCopy := phone
		sidStr := string(rec.SID)
		created := rec.CreatedAt.UTC().Format(time.RFC1123Z)
		result = append(result, twilioopenapi.ApiV2010IncomingPhoneNumber{
			Sid:         &sidStr,
			PhoneNumber: &phoneCopy,
			DateCreated: &created,
		})
	}

	return result, nil
}

// DeleteIncomingPhoneNumber removes a provisioned number
func (e *EngineImpl) DeleteIncomingPhoneNumber(sid string, _ *twilioopenapi.DeleteIncomingPhoneNumberParams) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for accountSID, numbers := range e.incomingNumbers {
		for phone, rec := range numbers {
			if string(rec.SID) == sid {
				delete(numbers, phone)
				sa := e.subAccounts[accountSID]
				if sa != nil {
					filtered := make([]string, 0, len(sa.IncomingNumbers))
					for _, n := range sa.IncomingNumbers {
						if n != phone {
							filtered = append(filtered, n)
						}
					}
					sa.IncomingNumbers = filtered
				}
				return nil
			}
		}
	}
	return fmt.Errorf("incoming phone number %s not found", sid)
}

// CreateApplication registers a Twilio Application for an account
func (e *EngineImpl) CreateApplication(params *twilioopenapi.CreateApplicationParams) (*twilioopenapi.ApiV2010Application, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}

	accountSID := model.SID(*params.PathAccountSid)
	friendly := ""
	if params.FriendlyName != nil {
		friendly = *params.FriendlyName
	}
	voiceURL := ""
	if params.VoiceUrl != nil {
		voiceURL = *params.VoiceUrl
	}
	voiceMethod := ""
	if params.VoiceMethod != nil {
		voiceMethod = *params.VoiceMethod
	}
	statusCallback := ""
	if params.StatusCallback != nil {
		statusCallback = *params.StatusCallback
	}
	statusCallbackMethod := ""
	if params.StatusCallbackMethod != nil {
		statusCallbackMethod = *params.StatusCallbackMethod
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	subAccount, exists := e.subAccounts[accountSID]
	if !exists {
		return nil, fmt.Errorf("subaccount %s not found", accountSID)
	}

	recMap := e.applications[accountSID]
	if recMap == nil {
		recMap = make(map[model.SID]*applicationRecord)
		e.applications[accountSID] = recMap
	}

	now := e.clock.Now()
	sid := model.NewApplicationSID()
	rec := &applicationRecord{
		SID:                  sid,
		FriendlyName:         friendly,
		VoiceMethod:          voiceMethod,
		VoiceURL:             voiceURL,
		StatusCallbackMethod: statusCallbackMethod,
		StatusCallback:       statusCallback,
		CreatedAt:            now,
	}
	recMap[sid] = rec
	subAccount.Applications = append(subAccount.Applications, model.Application{
		SID:                  string(sid),
		FriendlyName:         friendly,
		VoiceMethod:          voiceMethod,
		VoiceURL:             voiceURL,
		StatusCallbackMethod: statusCallbackMethod,
		StatusCallback:       statusCallback,
		CreatedAt:            now,
	})

	sidStr := string(sid)
	dateCreated := now.UTC().Format(time.RFC1123Z)

	return &twilioopenapi.ApiV2010Application{
		Sid:          &sidStr,
		FriendlyName: &friendly,
		DateCreated:  &dateCreated,
	}, nil
}

// UpdateCall applies updates to an existing call (status, callback URL, etc.)
func (e *EngineImpl) UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	e.mu.Lock()
	call, exists := e.calls[model.SID(sid)]
	if !exists {
		e.mu.Unlock()
		return nil, fmt.Errorf("call %s not found", sid)
	}
	runner := e.runners[call.SID]
	now := e.clock.Now()
	updatedFields := make(map[string]any)
	needHangup := false

	if params != nil {
		if params.Url != nil {
			call.AnswerURL = *params.Url
			updatedFields["url"] = *params.Url
		}
		if params.StatusCallback != nil {
			call.StatusCallback = *params.StatusCallback
			updatedFields["status_callback"] = *params.StatusCallback
		}
		if params.Status != nil {
			status := strings.ToLower(*params.Status)
			switch status {
			case "completed", "canceled", "cancelled":
				if call.Status != model.CallCompleted {
					needHangup = true
				}
				updatedFields["status"] = status
			}
		}
	}

	if len(updatedFields) > 0 {
		call.Timeline = append(call.Timeline, model.NewEvent(now, "call.updated", updatedFields))
	}

	if needHangup {
		e.updateCallStatus(call, model.CallCompleted)
		end := now
		call.EndedAt = &end
	}

	resp := buildAPICallResponse(call, e.apiVersion)
	e.mu.Unlock()

	if needHangup && runner != nil {
		runner.Hangup()
	}

	return resp, nil
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

// FetchCall returns a Twilio-style call response
func (e *EngineImpl) FetchCall(sid string, _ *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	call, exists := e.calls[model.SID(sid)]
	if !exists {
		return nil, fmt.Errorf("call %s not found", sid)
	}
	return buildAPICallResponse(call, e.apiVersion), nil
}

// GetCallState exposes the internal call model for inspection (tests, console)
func (e *EngineImpl) GetCallState(callSID model.SID) (*model.Call, bool) {
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

// GetQueue retrieves a queue by name and subaccount
func (e *EngineImpl) GetQueue(accountSID model.SID, name string) (*model.Queue, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if queues, exists := e.queues[accountSID]; exists {
		queue, found := queues[name]
		return queue, found
	}
	return nil, false
}

// GetConference retrieves a conference by name and subaccount
func (e *EngineImpl) GetConference(accountSID model.SID, name string) (*model.Conference, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if confs, exists := e.conferences[accountSID]; exists {
		conf, found := confs[name]
		return conf, found
	}
	return nil, false
}

// Snapshot returns a deep copy of the current state
func (e *EngineImpl) Snapshot() *StateSnapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap := &StateSnapshot{
		Calls:       make(map[model.SID]*model.Call),
		Queues:      make(map[string]*model.Queue),
		Conferences: make(map[string]*model.Conference),
		SubAccounts: make(map[model.SID]*model.SubAccount),
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

	// Flatten queues from all subaccounts into single map
	for _, queues := range e.queues {
		for name, queue := range queues {
			queueCopy := *queue
			queueCopy.Members = append([]model.SID{}, queue.Members...)
			queueCopy.Timeline = append([]model.Event{}, queue.Timeline...)
			snap.Queues[name] = &queueCopy
		}
	}

	// Flatten conferences from all subaccounts into single map
	for _, confs := range e.conferences {
		for name, conf := range confs {
			confCopy := *conf
			confCopy.Participants = append([]model.SID{}, conf.Participants...)
			confCopy.Timeline = append([]model.Event{}, conf.Timeline...)
			snap.Conferences[name] = &confCopy
		}
	}

	for sid, sa := range e.subAccounts {
		saCopy := *sa
		if sa.IncomingNumbers != nil {
			saCopy.IncomingNumbers = append([]string{}, sa.IncomingNumbers...)
		}
		if sa.Applications != nil {
			apps := make([]model.Application, len(sa.Applications))
			copy(apps, sa.Applications)
			saCopy.Applications = apps
		}
		snap.SubAccounts[sid] = &saCopy
	}

	return snap
}

func buildAPICallResponse(call *model.Call, apiVersion string) *twilioopenapi.ApiV2010Call {
	sid := string(call.SID)
	accountSid := string(call.AccountSID)
	status := string(call.Status)
	direction := "outbound-api"
	if call.Direction == model.Inbound {
		direction = "inbound"
	}
	dateCreated := call.StartAt.UTC().Format(time.RFC1123Z)
	startTime := dateCreated
	resp := &twilioopenapi.ApiV2010Call{
		Sid:         &sid,
		AccountSid:  &accountSid,
		Status:      &status,
		Direction:   &direction,
		ApiVersion:  &apiVersion,
		DateCreated: &dateCreated,
		StartTime:   &startTime,
	}
	if call.From != "" {
		from := call.From
		resp.From = &from
	}
	if call.To != "" {
		to := call.To
		resp.To = &to
	}
	if call.EndedAt != nil {
		end := call.EndedAt.UTC().Format(time.RFC1123Z)
		resp.EndTime = &end
		duration := fmt.Sprintf("%.0f", call.EndedAt.Sub(call.StartAt).Seconds())
		resp.Duration = &duration
	}
	return resp
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
	form.Set("AccountSid", string(call.AccountSID))
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

// getOrCreateQueue gets or creates a queue for a subaccount
func (e *EngineImpl) getOrCreateQueue(accountSID model.SID, name string) *model.Queue {
	// Ensure the subaccount map exists
	if _, exists := e.queues[accountSID]; !exists {
		e.queues[accountSID] = make(map[string]*model.Queue)
	}

	if queue, exists := e.queues[accountSID][name]; exists {
		return queue
	}

	queue := &model.Queue{
		Name:       name,
		SID:        model.NewQueueSID(),
		AccountSID: accountSID,
		Members:    []model.SID{},
		Timeline:   []model.Event{},
	}
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		e.clock.Now(),
		"queue.created",
		map[string]any{"name": name, "sid": queue.SID, "account_sid": accountSID},
	))
	e.queues[accountSID][name] = queue
	return queue
}

// getOrCreateConference gets or creates a conference for a subaccount
func (e *EngineImpl) getOrCreateConference(accountSID model.SID, name string) *model.Conference {
	// Ensure the subaccount map exists
	if _, exists := e.conferences[accountSID]; !exists {
		e.conferences[accountSID] = make(map[string]*model.Conference)
	}

	if conf, exists := e.conferences[accountSID][name]; exists {
		return conf
	}

	conf := &model.Conference{
		Name:         name,
		SID:          model.NewConferenceSID(),
		AccountSID:   accountSID,
		Participants: []model.SID{},
		Status:       model.ConferenceCreated,
		Timeline:     []model.Event{},
		CreatedAt:    e.clock.Now(),
	}
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		e.clock.Now(),
		"conference.created",
		map[string]any{"name": name, "sid": conf.SID, "account_sid": accountSID},
	))
	e.conferences[accountSID][name] = conf
	return conf
}
