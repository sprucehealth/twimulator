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

var _ Engine = &EngineImpl{}

// Engine is the main interface for the Twilio Voice simulator
type Engine interface {
	// Subaccount management
	CreateAccount(params *twilioopenapi.CreateAccountParams) (*twilioopenapi.ApiV2010Account, error)
	ListAccount(params *twilioopenapi.ListAccountParams) ([]twilioopenapi.ApiV2010Account, error)
	CreateIncomingPhoneNumber(params *twilioopenapi.CreateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error)
	ListIncomingPhoneNumber(params *twilioopenapi.ListIncomingPhoneNumberParams) ([]twilioopenapi.ApiV2010IncomingPhoneNumber, error)
	UpdateIncomingPhoneNumber(sid string, params *twilioopenapi.UpdateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error)
	DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error
	CreateApplication(params *twilioopenapi.CreateApplicationParams) (*twilioopenapi.ApiV2010Application, error)
	CreateQueue(params *twilioopenapi.CreateQueueParams) (*twilioopenapi.ApiV2010Queue, error)

	// Core lifecycle
	CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error)
	CreateIncomingCall(accountSID model.SID, from string, to string) (*twilioopenapi.ApiV2010Call, error)
	UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error)
	AnswerCall(subaccountSID model.SID, callSID model.SID) error
	SetCallBusy(subaccountSID model.SID, callSID model.SID) error
	SetCallFailed(subaccountSID model.SID, callSID model.SID) error
	Hangup(subaccountSID model.SID, callSID model.SID) error
	SendDigits(subaccountSID model.SID, callSID model.SID, digits string) error

	// Introspection
	FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error)
	FetchConference(sid string, params *twilioopenapi.FetchConferenceParams) (*twilioopenapi.ApiV2010Conference, error)
	ListConference(params *twilioopenapi.ListConferenceParams) ([]twilioopenapi.ApiV2010Conference, error)
	UpdateConference(sid string, params *twilioopenapi.UpdateConferenceParams) (*twilioopenapi.ApiV2010Conference, error)
	FetchParticipant(conferenceSid string, callSid string, params *twilioopenapi.FetchParticipantParams) (*twilioopenapi.ApiV2010Participant, error)
	UpdateParticipant(conferenceSid string, callSid string, params *twilioopenapi.UpdateParticipantParams) (*twilioopenapi.ApiV2010Participant, error)
	FetchRecording(sid string, params *twilioopenapi.FetchRecordingParams) (*twilioopenapi.ApiV2010Recording, error)
	ListCalls(filter CallFilter) []*model.Call
	GetQueue(accountSID model.SID, name string) (*model.Queue, bool)
	GetConference(accountSID model.SID, name string) (*model.Conference, bool)
	Snapshot(accountSID model.SID) (*StateSnapshot, error)
	SnapshotAll() []*StateSnapshot

	SetClockForAccount(accountSID model.SID, clock Clock) error
	AdvanceForAccount(accountSID model.SID, d time.Duration) error
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
	engine     Engine
	accountSID model.SID

	Calls       map[model.SID]*model.Call       `json:"calls"`
	Queues      map[string]*model.Queue         `json:"queues"`
	Conferences map[string]*model.Conference    `json:"conferences"`
	SubAccounts map[model.SID]*model.SubAccount `json:"sub_accounts"`
	Errors      []error                         `json:"errors"`
	Timestamp   time.Time                       `json:"timestamp"`
}

// subAccountState holds all state for a single subaccount with its own lock
type subAccountState struct {
	mu sync.RWMutex

	account *model.SubAccount
	clock   Clock

	// Resources scoped to this subaccount
	incomingNumbers map[string]*incomingNumber
	applications    map[model.SID]*applicationRecord
	calls           map[model.SID]*model.Call
	queues          map[string]*model.Queue
	conferences     map[string]*model.Conference
	runners         map[model.SID]*CallRunner
	errors          []error

	// Participant states scoped by (conferenceSID, callSID)
	participantStates map[model.SID]map[model.SID]*model.ParticipantState
}

// EngineImpl is the concrete implementation of Engine
type EngineImpl struct {
	// Global mutex ONLY for subaccount map mutations
	subAccountsMu sync.RWMutex
	subAccounts   map[model.SID]*subAccountState
	timeout       time.Duration

	// Immutable or globally-shared state (no lock needed after init)
	defaultClock Clock
	webhook      httpstub.WebhookClient
	apiVersion   string
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
}

// EngineOption configures the engine
type EngineOption func(*EngineImpl)

type incomingNumber struct {
	SID              model.SID
	PhoneNumber      string
	VoiceApplication *model.SID
	CreatedAt        time.Time
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
		e.defaultClock = NewManualClock(time.Time{})
	}
}

// WithAutoClock configures the engine to use real time
func WithAutoClock() EngineOption {
	return func(e *EngineImpl) {
		e.defaultClock = NewAutoClock()
	}
}

// WithAutoAdvancableClock configures the engine to use real time with manual advance capability
func WithAutoAdvancableClock() EngineOption {
	return func(e *EngineImpl) {
		e.defaultClock = NewAutoAdvancableClock()
	}
}

// WithClock sets a specific clock implementation
func WithClock(clock Clock) EngineOption {
	return func(e *EngineImpl) {
		e.defaultClock = clock
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

	timeout := 40 * time.Second
	e := &EngineImpl{
		timeout:      timeout,
		defaultClock: NewManualClock(time.Time{}), // default to manual
		webhook:      httpstub.NewDefaultWebhookClient(timeout),
		apiVersion:   "2010-04-01",
		subAccounts:  make(map[model.SID]*subAccountState),
		ctx:          ctx,
		cancel:       cancel,
	}

	for _, opt := range opts {
		opt(e)
	}

	return e
}

// CreateAccount creates a new simulated Twilio subaccount
func (e *EngineImpl) CreateAccount(params *twilioopenapi.CreateAccountParams) (*twilioopenapi.ApiV2010Account, error) {
	friendlyName := ""
	if params != nil && params.FriendlyName != nil {
		friendlyName = *params.FriendlyName
	}

	sid := model.NewSubAccountSID()
	now := e.defaultClock.Now()
	authToken := model.NewAuthToken()

	subAccount := &model.SubAccount{
		SID:          sid,
		FriendlyName: friendlyName,
		Status:       "active",
		CreatedAt:    now,
		AuthToken:    authToken,
	}

	// Create new subaccount state
	state := &subAccountState{
		clock:             e.defaultClock,
		account:           subAccount,
		incomingNumbers:   make(map[string]*incomingNumber),
		applications:      make(map[model.SID]*applicationRecord),
		calls:             make(map[model.SID]*model.Call),
		queues:            make(map[string]*model.Queue),
		conferences:       make(map[string]*model.Conference),
		runners:           make(map[model.SID]*CallRunner),
		participantStates: make(map[model.SID]map[model.SID]*model.ParticipantState),
	}

	// Only lock when adding to subaccounts map
	e.subAccountsMu.Lock()
	e.subAccounts[sid] = state
	e.subAccountsMu.Unlock()

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
	var friendly string
	if params != nil && params.FriendlyName != nil {
		friendly = *params.FriendlyName
	}

	e.subAccountsMu.RLock()
	states := make([]*subAccountState, 0, len(e.subAccounts))
	for _, state := range e.subAccounts {
		states = append(states, state)
	}
	e.subAccountsMu.RUnlock()

	matches := make([]*model.SubAccount, 0)
	for _, state := range states {
		state.mu.RLock()
		sa := state.account
		if friendly != "" && sa.FriendlyName != friendly {
			state.mu.RUnlock()
			continue
		}
		matches = append(matches, sa)
		state.mu.RUnlock()
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

	url := ""
	if params.Url != nil {
		url = *params.Url
	}
	if url == "" {
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

	statusEvents := []model.CallStatus{}
	if params.StatusCallbackEvent != nil {
		for _, eventStr := range *params.StatusCallbackEvent {
			status := model.CallStatus(eventStr)
			// Validate that the status is a valid CallStatus
			if !isValidCallStatus(status) {
				return nil, fmt.Errorf("invalid status callback event: %s", eventStr)
			}
			statusEvents = append(statusEvents, status)
		}
	}

	callToken := ""
	if params.CallToken != nil {
		callToken = *params.CallToken
	}

	accountSIDModel := model.SID(accountSID)

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSIDModel]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSIDModel)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.incomingNumbers[from] == nil {
		return nil, fmt.Errorf("from number %s not provisioned for account %s", from, accountSID)
	}

	now := state.clock.Now()
	call := &model.Call{
		SID:                  model.NewCallSID(),
		AccountSID:           accountSIDModel,
		From:                 from,
		To:                   to,
		Direction:            model.Outbound,
		Status:               model.CallInitiated,
		StartAt:              now,
		Timeline:             []model.Event{},
		Variables:            make(map[string]string),
		Url:                  url,
		StatusCallback:       statusCallback,
		StatusCallbackEvents: statusEvents,
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

	state.calls[call.SID] = call

	runner := NewCallRunner(call, state, e, timeout)
	state.runners[call.SID] = runner

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		runner.Run(e.ctx)
	}()

	return buildAPICallResponse(call, e.apiVersion), nil
}

// CreateIncomingCall simulates an incoming call to a number with an application
func (e *EngineImpl) CreateIncomingCall(accountSID model.SID, from string, to string) (*twilioopenapi.ApiV2010Call, error) {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	// Find the incoming number
	incomingNum := state.incomingNumbers[to]
	if incomingNum == nil {
		return nil, fmt.Errorf("to number %s not provisioned for account %s", to, accountSID)
	}

	// Validate the number has an application configured
	if incomingNum.VoiceApplication == nil {
		return nil, fmt.Errorf("number %s does not have a voice application configured", to)
	}

	// Get the application configuration
	app := state.applications[*incomingNum.VoiceApplication]
	if app == nil {
		return nil, fmt.Errorf("application %s not found for account %s", *incomingNum.VoiceApplication, accountSID)
	}

	// Validate application has VoiceURL configured
	if app.VoiceURL == "" {
		return nil, fmt.Errorf("application %s does not have a VoiceURL configured", app.SID)
	}

	// Create the call with application's configuration
	now := state.clock.Now()
	call := &model.Call{
		SID:                  model.NewCallSID(),
		AccountSID:           accountSID,
		From:                 from,
		To:                   to,
		Direction:            model.Inbound,
		Status:               model.CallInitiated,
		StartAt:              now,
		Timeline:             []model.Event{},
		Variables:            make(map[string]string),
		Url:                  app.VoiceURL,
		StatusCallback:       app.StatusCallback,
		StatusCallbackEvents: []model.CallStatus{model.CallCompleted}, // Twiml application only sends the completed event
	}

	call.Timeline = append(call.Timeline, model.NewEvent(
		now,
		"call.created",
		map[string]any{
			"sid":         call.SID,
			"from":        call.From,
			"to":          call.To,
			"status":      call.Status,
			"application": app.SID,
		},
	))

	state.calls[call.SID] = call

	timeout := 30 * time.Second
	runner := NewCallRunner(call, state, e, timeout)
	state.runners[call.SID] = runner

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

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSIDModel]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSIDModel)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	if _, exists := state.incomingNumbers[phone]; exists {
		return nil, fmt.Errorf("phone number %s already exists", phone)
	}

	var voiceAppSID *model.SID
	appValue := ""
	if params.VoiceApplicationSid != nil && *params.VoiceApplicationSid != "" {
		appSID := model.SID(*params.VoiceApplicationSid)
		if state.applications[appSID] == nil {
			return nil, fmt.Errorf("application %s not found for account %s", appSID, accountSID)
		}
		voiceAppSID = &appSID
		appValue = string(appSID)
	}

	now := state.clock.Now()
	sid := model.NewPhoneNumberSID()
	record := &incomingNumber{
		SID:              sid,
		PhoneNumber:      phone,
		VoiceApplication: voiceAppSID,
		CreatedAt:        now,
	}
	state.incomingNumbers[phone] = record

	var appStrPtr *string
	if voiceAppSID != nil {
		appCopy := appValue
		appStrPtr = &appCopy
	}

	state.account.IncomingNumbers = append(state.account.IncomingNumbers, model.IncomingNumber{
		SID:                 string(sid),
		PhoneNumber:         phone,
		VoiceApplicationSID: appStrPtr,
		CreatedAt:           now,
	})

	phoneCopy := phone
	sidStr := string(sid)
	created := now.UTC().Format(time.RFC1123Z)

	resp := &twilioopenapi.ApiV2010IncomingPhoneNumber{
		Sid:         &sidStr,
		PhoneNumber: &phoneCopy,
		DateCreated: &created,
	}
	if voiceAppSID != nil {
		appCopy := appValue
		resp.VoiceApplicationSid = &appCopy
	}
	return resp, nil
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

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.RLock()
	defer state.mu.RUnlock()

	result := make([]twilioopenapi.ApiV2010IncomingPhoneNumber, 0)
	for phone, rec := range state.incomingNumbers {
		if filterPhone != "" && phone != filterPhone {
			continue
		}
		phoneCopy := phone
		sidStr := string(rec.SID)
		created := rec.CreatedAt.UTC().Format(time.RFC1123Z)
		entry := twilioopenapi.ApiV2010IncomingPhoneNumber{
			Sid:         &sidStr,
			PhoneNumber: &phoneCopy,
			DateCreated: &created,
		}
		if rec.VoiceApplication != nil {
			appCopy := string(*rec.VoiceApplication)
			entry.VoiceApplicationSid = &appCopy
		}
		result = append(result, entry)
	}

	return result, nil
}

// UpdateIncomingPhoneNumber updates a provisioned phone number
func (e *EngineImpl) UpdateIncomingPhoneNumber(sid string, params *twilioopenapi.UpdateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Find the incoming number by SID across all subaccounts
	var foundNumber *incomingNumber
	var foundPhone string
	state.mu.RLock()
	for phone, rec := range state.incomingNumbers {
		if string(rec.SID) == sid {
			foundNumber = rec
			foundPhone = phone
		}
	}
	state.mu.RUnlock()

	if foundNumber == nil {
		return nil, notFoundError(model.SID(sid))
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	// Update VoiceApplicationSid if provided
	if params.VoiceApplicationSid != nil {
		appSIDStr := *params.VoiceApplicationSid
		if appSIDStr == "" {
			// Clear the application association
			foundNumber.VoiceApplication = nil
		} else {
			// Validate the application exists for this account
			appSID := model.SID(appSIDStr)
			if state.applications[appSID] == nil {
				return nil, fmt.Errorf("application %s not found for account %s", appSID, state.account.SID)
			}
			foundNumber.VoiceApplication = &appSID
		}

		// Update the SubAccount's IncomingNumbers list
		for i := range state.account.IncomingNumbers {
			if state.account.IncomingNumbers[i].SID == string(foundNumber.SID) {
				if foundNumber.VoiceApplication != nil {
					appCopy := string(*foundNumber.VoiceApplication)
					state.account.IncomingNumbers[i].VoiceApplicationSID = &appCopy
				} else {
					state.account.IncomingNumbers[i].VoiceApplicationSID = nil
				}
				break
			}
		}
	}

	// Build response
	sidStr := string(foundNumber.SID)
	phoneCopy := foundPhone
	created := foundNumber.CreatedAt.UTC().Format(time.RFC1123Z)
	resp := &twilioopenapi.ApiV2010IncomingPhoneNumber{
		Sid:         &sidStr,
		PhoneNumber: &phoneCopy,
		DateCreated: &created,
	}
	if foundNumber.VoiceApplication != nil {
		appCopy := string(*foundNumber.VoiceApplication)
		resp.VoiceApplicationSid = &appCopy
	}

	return resp, nil
}

// DeleteIncomingPhoneNumber removes a provisioned number
func (e *EngineImpl) DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(accountSID)
	}

	// Find and delete the incoming number
	state.mu.Lock()
	defer state.mu.Unlock()
	for phone, rec := range state.incomingNumbers {
		if string(rec.SID) == sid {
			delete(state.incomingNumbers, phone)
			filtered := make([]model.IncomingNumber, 0, len(state.account.IncomingNumbers))
			for _, n := range state.account.IncomingNumbers {
				if n.PhoneNumber != phone {
					filtered = append(filtered, n)
				}
			}
			state.account.IncomingNumbers = filtered
			return nil
		}
	}
	return notFoundError(model.SID(sid))
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

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	now := state.clock.Now()
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
	state.applications[sid] = rec
	state.account.Applications = append(state.account.Applications, model.Application{
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

// CreateQueue creates a queue for an account
func (e *EngineImpl) CreateQueue(params *twilioopenapi.CreateQueueParams) (*twilioopenapi.ApiV2010Queue, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}

	accountSID := model.SID(*params.PathAccountSid)
	friendlyName := ""
	if params.FriendlyName != nil {
		friendlyName = *params.FriendlyName
	}
	if friendlyName == "" {
		return nil, fmt.Errorf("FriendlyName is required")
	}

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	// Check if queue with this name already exists
	if _, found := state.queues[friendlyName]; found {
		return nil, fmt.Errorf("queue %s already exists for account %s", friendlyName, accountSID)
	}

	// Create the queue
	queue := e.getOrCreateQueueLocked(state, accountSID, friendlyName)

	sidStr := string(queue.SID)
	accountSIDStr := string(accountSID)

	return &twilioopenapi.ApiV2010Queue{
		Sid:        &sidStr,
		AccountSid: &accountSIDStr,
	}, nil
}

// UpdateCall applies updates to an existing call (status, callback URL, etc.)
func (e *EngineImpl) UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	callSID := model.SID(sid)

	state.mu.RLock()
	call, exists := state.calls[callSID]
	state.mu.RUnlock()
	if !exists {
		return nil, notFoundError(callSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()

	runner := state.runners[call.SID]
	now := state.clock.Now()
	updatedFields := make(map[string]any)
	needHangup := false
	urlUpdated := false

	if params.Url != nil {
		call.Url = *params.Url
		updatedFields["url"] = *params.Url
		urlUpdated = true
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

	if len(updatedFields) > 0 {
		call.Timeline = append(call.Timeline, model.NewEvent(now, "call.updated", updatedFields))
	}

	if needHangup {
		e.updateCallStatusLocked(state, call, model.CallCompleted)
		end := now
		call.EndedAt = &end
	}

	resp := buildAPICallResponse(call, e.apiVersion)

	if needHangup && runner != nil {
		runner.Hangup()
	}

	// If URL was updated, signal the runner to fetch and execute new TwiML
	if urlUpdated && runner != nil && !needHangup {
		runner.UpdateURL(*params.Url)
	}

	return resp, nil
}

// AnswerCall explicitly answers a ringing call
func (e *EngineImpl) AnswerCall(subaccountSID, callSID model.SID) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(subaccountSID)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	call, exists := state.calls[callSID]
	if !exists {
		return notFoundError(callSID)
	}
	if call.Status != model.CallRinging {
		return fmt.Errorf("call %s is not in ringing state (current: %s)", callSID, call.Status)
	}
	runner := state.runners[callSID]

	if runner != nil {
		select {
		case runner.answerCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// SetCallBusy marks a call as busy
func (e *EngineImpl) SetCallBusy(subaccountSID, callSID model.SID) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(subaccountSID)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	call, exists := state.calls[callSID]
	if !exists {
		return notFoundError(callSID)
	}
	if call.Status != model.CallRinging {
		return fmt.Errorf("call %s is not in ringing state (current: %s)", callSID, call.Status)
	}
	runner := state.runners[callSID]

	if runner != nil {
		select {
		case runner.busyCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// SetCallFailed marks a call as failed
func (e *EngineImpl) SetCallFailed(subaccountSID, callSID model.SID) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(subaccountSID)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	call, exists := state.calls[callSID]
	if !exists {
		return notFoundError(callSID)
	}
	if call.Status != model.CallRinging {
		return fmt.Errorf("call %s is not in ringing state (current: %s)", callSID, call.Status)
	}
	runner := state.runners[callSID]

	if runner != nil {
		select {
		case runner.failedCh <- struct{}{}:
		default:
		}
	}

	return nil
}

// Hangup terminates a call
func (e *EngineImpl) Hangup(subaccountSID, callSID model.SID) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(subaccountSID)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	call, exists := state.calls[callSID]
	if !exists {
		return notFoundError(callSID)
	}
	runner := state.runners[callSID]

	if runner != nil {
		runner.Hangup()
	}

	// Update call status
	if call.Status != model.CallCompleted {
		e.updateCallStatusLocked(state, call, model.CallCompleted)
		now := state.clock.Now()
		call.EndedAt = &now
	}

	return nil
}

// SendDigits sends DTMF digits to a call (for Gather)
func (e *EngineImpl) SendDigits(subaccountSID, callSID model.SID, digits string) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(subaccountSID)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	_, exists = state.calls[callSID]
	if !exists {
		return notFoundError(callSID)
	}
	runner := state.runners[callSID]
	if runner == nil {
		return notFoundError(callSID)
	}

	runner.SendDigits(digits)
	return nil
}

// FetchCall returns a Twilio-style call response
func (e *EngineImpl) FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	callSID := model.SID(sid)

	state.mu.RLock()
	defer state.mu.RUnlock()
	call, exists := state.calls[callSID]
	if !exists {
		return nil, notFoundError(callSID)
	}
	resp := buildAPICallResponse(call, e.apiVersion)

	return resp, nil
}

// FetchConference returns a Twilio-style conference response by SID
func (e *EngineImpl) FetchConference(sid string, params *twilioopenapi.FetchConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	for _, conf := range state.conferences {
		if string(conf.SID) == sid {
			sidStr := string(conf.SID)
			status := string(conf.Status)
			return &twilioopenapi.ApiV2010Conference{
				Sid:    &sidStr,
				Status: &status,
			}, nil
		}
	}

	return nil, notFoundError(model.SID(sid))
}

// ListConference returns conferences filtered by optional friendly name
func (e *EngineImpl) ListConference(params *twilioopenapi.ListConferenceParams) ([]twilioopenapi.ApiV2010Conference, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}

	accountSID := model.SID(*params.PathAccountSid)
	friendlyName := ""
	if params.FriendlyName != nil {
		friendlyName = *params.FriendlyName
	}

	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.RLock()
	defer state.mu.RUnlock()

	result := make([]twilioopenapi.ApiV2010Conference, 0)
	for _, conf := range state.conferences {
		// Filter by friendly name if provided (friendly name is the conference Name)
		if friendlyName != "" && conf.Name != friendlyName {
			continue
		}

		sidStr := string(conf.SID)
		status := string(conf.Status)

		result = append(result, twilioopenapi.ApiV2010Conference{
			Sid:    &sidStr,
			Status: &status,
		})
	}

	return result, nil
}

// UpdateConference updates a conference's status
func (e *EngineImpl) UpdateConference(sid string, params *twilioopenapi.UpdateConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	state.mu.Lock()
	defer state.mu.Lock()

	var conf *model.Conference
	for _, cnf := range state.conferences {
		if string(cnf.SID) == sid {
			conf = cnf
			break
		}
	}

	if conf == nil {
		return nil, notFoundError(model.SID(sid))
	}

	// Update status if provided
	if params.Status != nil {
		statusStr := strings.ToLower(*params.Status)
		switch statusStr {
		case "completed":
			conf.Status = model.ConferenceCompleted
			now := state.clock.Now()
			conf.EndedAt = &now
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				now,
				"conference.ended",
				map[string]any{
					"reason": "updated_via_api",
				},
			))
		case "in-progress":
			conf.Status = model.ConferenceInProgress
		}
	}

	// Note: AnnounceUrl and AnnounceMethod are in params but not used for now
	// as per requirements

	sidStr := string(conf.SID)
	status := string(conf.Status)

	return &twilioopenapi.ApiV2010Conference{
		Sid:    &sidStr,
		Status: &status,
	}, nil
}

// FetchParticipant retrieves a participant from a conference
func (e *EngineImpl) FetchParticipant(conferenceSid string, callSid string, params *twilioopenapi.FetchParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	state.mu.Lock()
	defer state.mu.Lock()

	var conf *model.Conference
	for _, cnf := range state.conferences {
		if string(cnf.SID) == conferenceSid {
			conf = cnf
			break
		}
	}

	if conf == nil {
		return nil, notFoundError(model.SID(conferenceSid))
	}

	// Check if the call is a participant in this conference
	callSIDModel := model.SID(callSid)
	isParticipant := false
	for _, participantSID := range conf.Participants {
		if participantSID == callSIDModel {
			isParticipant = true
			break
		}
	}

	if !isParticipant {
		return nil, fmt.Errorf("call %s is not a participant in conference %s", callSid, conferenceSid)
	}

	// Return participant with CallSid and ConferenceSid
	callSidStr := callSid
	conferenceSidStr := conferenceSid

	return &twilioopenapi.ApiV2010Participant{
		CallSid:       &callSidStr,
		ConferenceSid: &conferenceSidStr,
	}, nil
}

// UpdateParticipant updates a participant in a conference
func (e *EngineImpl) UpdateParticipant(conferenceSid string, callSid string, params *twilioopenapi.UpdateParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	if params == nil || params.PathAccountSid == nil || *params.PathAccountSid == "" {
		return nil, fmt.Errorf("PathAccountSid is required")
	}
	accountSID := model.SID(*params.PathAccountSid)
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	state.mu.Lock()
	defer state.mu.Lock()

	var conf *model.Conference
	for _, cnf := range state.conferences {
		if string(cnf.SID) == conferenceSid {
			conf = cnf
			break
		}
	}

	if conf == nil {
		return nil, notFoundError(model.SID(conferenceSid))
	}

	// Check if the call is a participant in this conference
	callSIDModel := model.SID(callSid)
	isParticipant := false
	for _, participantSID := range conf.Participants {
		if participantSID == callSIDModel {
			isParticipant = true
			break
		}
	}

	if !isParticipant {
		return nil, fmt.Errorf("call %s is not a participant in conference %s", callSid, conferenceSid)
	}

	// Get the call for timeline events
	call, exists := state.calls[callSIDModel]
	if !exists {
		return nil, notFoundError(callSIDModel)
	}

	// Get or create participant state for this (conference, call) pair
	conferenceSIDModel := model.SID(conferenceSid)
	if state.participantStates[conferenceSIDModel] == nil {
		state.participantStates[conferenceSIDModel] = make(map[model.SID]*model.ParticipantState)
	}

	partState := state.participantStates[conferenceSIDModel][callSIDModel]
	if partState == nil {
		partState = &model.ParticipantState{}
		state.participantStates[conferenceSIDModel][callSIDModel] = partState
	}

	// Update participant state
	now := state.clock.Now()
	updatedFields := make(map[string]any)

	if params.Muted != nil {
		partState.Muted = *params.Muted
		updatedFields["muted"] = *params.Muted
	}

	if params.Hold != nil {
		partState.Hold = *params.Hold
		updatedFields["hold"] = *params.Hold
	}

	if params.HoldUrl != nil {
		partState.HoldUrl = *params.HoldUrl
		updatedFields["hold_url"] = *params.HoldUrl
	}

	if params.HoldMethod != nil {
		partState.HoldMethod = *params.HoldMethod
		updatedFields["hold_method"] = *params.HoldMethod
	}

	if params.AnnounceUrl != nil {
		partState.AnnounceUrl = *params.AnnounceUrl
		updatedFields["announce_url"] = *params.AnnounceUrl
	}

	if params.AnnounceMethod != nil {
		partState.AnnounceMethod = *params.AnnounceMethod
		updatedFields["announce_method"] = *params.AnnounceMethod
	}

	// Add timeline event to the call if any fields were updated
	if len(updatedFields) > 0 {
		updatedFields["conference_sid"] = conferenceSid
		call.Timeline = append(call.Timeline, model.NewEvent(
			now,
			"participant.updated",
			updatedFields,
		))
	}

	// Return participant with CallSid and ConferenceSid
	callSidStr := callSid
	conferenceSidStr := conferenceSid

	return &twilioopenapi.ApiV2010Participant{
		CallSid:       &callSidStr,
		ConferenceSid: &conferenceSidStr,
	}, nil
}

// FetchRecording returns a recording with status "absent" (recordings not implemented)
func (e *EngineImpl) FetchRecording(sid string, _ *twilioopenapi.FetchRecordingParams) (*twilioopenapi.ApiV2010Recording, error) {
	sidStr := sid
	status := "absent"

	return &twilioopenapi.ApiV2010Recording{
		Sid:    &sidStr,
		Status: &status,
	}, nil
}

// GetCallState exposes the internal call model for inspection (tests, console)
func (e *EngineImpl) GetCallState(subaccountSID, callSID model.SID) (*model.Call, bool) {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[subaccountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	call, exists := state.calls[callSID]
	if !exists {
		return nil, false
	}
	return call, true
}

// ListCalls returns all calls matching the filter
func (e *EngineImpl) ListCalls(filter CallFilter) []*model.Call {
	e.subAccountsMu.RLock()
	states := make([]*subAccountState, 0, len(e.subAccounts))
	for _, state := range e.subAccounts {
		states = append(states, state)
	}
	e.subAccountsMu.RUnlock()

	var result []*model.Call
	for _, state := range states {
		state.mu.RLock()
		for _, call := range state.calls {
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
		state.mu.RUnlock()
	}
	return result
}

// GetQueue retrieves a queue by name and subaccount
func (e *EngineImpl) GetQueue(accountSID model.SID, name string) (*model.Queue, bool) {
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	queue, found := state.queues[name]
	return queue, found
}

// GetConference retrieves a conference by name and subaccount
func (e *EngineImpl) GetConference(accountSID model.SID, name string) (*model.Conference, bool) {
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, false
	}

	state.mu.RLock()
	defer state.mu.RUnlock()

	conf, found := state.conferences[name]
	return conf, found
}

// Snapshot returns a deep copy of the current state for a specific subaccount
func (e *EngineImpl) Snapshot(accountSID model.SID) (*StateSnapshot, error) {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil, notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.RLock()
	defer state.mu.RUnlock()

	snap := &StateSnapshot{
		engine:      e,
		accountSID:  accountSID,
		Calls:       make(map[model.SID]*model.Call),
		Queues:      make(map[string]*model.Queue),
		Conferences: make(map[string]*model.Conference),
		SubAccounts: make(map[model.SID]*model.SubAccount),
		Timestamp:   state.clock.Now(),
	}

	// Only include calls for this subaccount
	for sid, call := range state.calls {
		callCopy := *call
		callCopy.Timeline = append([]model.Event{}, call.Timeline...)
		callCopy.Variables = make(map[string]string)
		for k, v := range call.Variables {
			callCopy.Variables[k] = v
		}
		snap.Calls[sid] = &callCopy
	}

	// Only include queues for this subaccount
	for name, queue := range state.queues {
		queueCopy := *queue
		queueCopy.Members = append([]model.SID{}, queue.Members...)
		queueCopy.Timeline = append([]model.Event{}, queue.Timeline...)
		snap.Queues[name] = &queueCopy
	}

	// Only include conferences for this subaccount
	for name, conf := range state.conferences {
		confCopy := *conf
		confCopy.Participants = append([]model.SID{}, conf.Participants...)
		confCopy.Timeline = append([]model.Event{}, conf.Timeline...)
		snap.Conferences[name] = &confCopy
	}

	// Copy errors state.errors
	snap.Errors = append(snap.Errors, state.errors...)

	// Only include this subaccount
	saCopy := *state.account
	if state.account.IncomingNumbers != nil {
		saCopy.IncomingNumbers = append([]model.IncomingNumber{}, state.account.IncomingNumbers...)
	}
	if state.account.Applications != nil {
		apps := make([]model.Application, len(state.account.Applications))
		copy(apps, state.account.Applications)
		saCopy.Applications = apps
	}
	snap.SubAccounts[accountSID] = &saCopy

	return snap, nil
}

// SnapshotAll returns a deep copy of the current state for all subaccounts
func (e *EngineImpl) SnapshotAll() []*StateSnapshot {
	e.subAccountsMu.RLock()
	accountSIDs := make([]model.SID, 0, len(e.subAccounts))
	for sid := range e.subAccounts {
		accountSIDs = append(accountSIDs, sid)
	}
	e.subAccountsMu.RUnlock()

	snapshots := make([]*StateSnapshot, 0, len(accountSIDs))
	for _, sid := range accountSIDs {
		snap, err := e.Snapshot(sid)
		if err != nil {
			// Skip accounts that no longer exist (race condition)
			continue
		}
		snapshots = append(snapshots, snap)
	}

	return snapshots
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
	// No lock needed - defaultClock is atomically replaced and reads are safe
	if enabled {
		if _, ok := e.defaultClock.(*AutoClock); !ok {
			log.Println("Switching to auto clock")
			e.defaultClock = NewAutoClock()
		}
	} else {
		if _, ok := e.defaultClock.(*ManualClock); !ok {
			log.Println("Switching to manual clock")
			e.defaultClock = NewManualClock(e.defaultClock.Now())
		}
	}
}

// Advance advances the manual clock or auto-advancable clock (no-op for pure auto clock)
func (e *EngineImpl) Advance(d time.Duration) {
	clock := e.defaultClock

	if mc, ok := clock.(*ManualClock); ok {
		mc.Advance(d)
	} else if aac, ok := clock.(*AutoAdvancableClock); ok {
		aac.Advance(d)
	}
}

// Clock returns the current clock
func (e *EngineImpl) Clock() Clock {
	return e.defaultClock
}

// Close shuts down the engine
func (e *EngineImpl) Close() error {
	e.cancel()
	e.wg.Wait()

	if aac, ok := e.defaultClock.(*AutoAdvancableClock); ok {
		aac.Stop()
	}
	e.subAccountsMu.Lock()
	defer e.subAccountsMu.Unlock()
	for _, sa := range e.subAccounts {
		if aac, ok := sa.clock.(*AutoAdvancableClock); ok {
			aac.Stop()
		}
	}
	return nil
}

// getClockForAccount returns the clock for a specific subaccount,
// or the default clock if no subaccount-specific clock is set
//func (e *EngineImpl) getClockForAccount(accountSID model.SID) Clock {
//	if clock, exists := e.subAccountClocks[accountSID]; exists {
//		return clock
//	}
//	return e.defaultClock
//}

// findCallState searches all subaccounts for a call and returns its state and the call.
// Returns nil if the call is not found.
//func (e *EngineImpl) findCallState(callSID model.SID) (*subAccountState, *model.Call) {
//	e.subAccountsMu.RLock()
//	defer e.subAccountsMu.RUnlock()
//
//	for _, state := range e.subAccounts {
//		state.mu.RLock()
//		call, exists := state.calls[callSID]
//		if exists {
//			state.mu.RUnlock()
//			return state, call
//		}
//		state.mu.RUnlock()
//	}
//	return nil, nil
//}

// SetClockForAccount sets a custom clock for a specific subaccount (testing only)
func (e *EngineImpl) SetClockForAccount(accountSID model.SID, clock Clock) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()
	state.clock = clock
	return nil
}

// AdvanceForAccount advances the clock for a specific subaccount.
// Returns an error if no custom clock has been set for this account.
func (e *EngineImpl) AdvanceForAccount(accountSID model.SID, d time.Duration) error {
	// Get subaccount state
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return notFoundError(accountSID)
	}

	// Lock only this subaccount
	state.mu.Lock()
	defer state.mu.Unlock()
	clock := state.clock
	if mc, ok := clock.(*ManualClock); ok {
		mc.Advance(d)
	} else if aac, ok := clock.(*AutoAdvancableClock); ok {
		aac.Advance(d)
	}

	return nil
}

// updateCallStatusLocked updates call status. Caller must hold state.mu.
func (e *EngineImpl) updateCallStatusLocked(state *subAccountState, call *model.Call, newStatus model.CallStatus) {
	if call.Status == newStatus {
		return
	}
	if call.Status.IsTerminal() {
		return
	}
	oldStatus := call.Status
	if oldStatus == model.CallRinging && newStatus == model.CallCompleted {
		newStatus = model.CallCanceled
	}
	call.Status = newStatus

	// Add timeline event
	call.Timeline = append(call.Timeline, model.NewEvent(
		state.clock.Now(),
		"status.changed",
		map[string]any{
			"from": oldStatus,
			"to":   newStatus,
		},
	))

	// Trigger status callback if configured and user is interested in this event
	if call.StatusCallback != "" && e.shouldSendStatusCallback(call, newStatus) {
		// Note: this must stay asynchronous to avoid deadlocks
		go e.sendStatusCallback(state, call)
	} else {
		// skipped status callback
		call.Timeline = append(call.Timeline, model.NewEvent(
			state.clock.Now(),
			"webhook.status_callback_skipped",
			map[string]any{
				"from": oldStatus,
				"to":   newStatus,
			}))
	}
}

// isValidCallStatus checks if a CallStatus value is one of the valid constants
func isValidCallStatus(status model.CallStatus) bool {
	switch status {
	case model.CallInitiated, model.CallQueued, model.CallRinging,
		model.CallInProgress, model.CallCompleted, model.CallBusy,
		model.CallFailed, model.CallNoAnswer, model.CallCanceled,
		model.CallAnswered:
		return true
	default:
		return false
	}
}

// shouldSendStatusCallback checks if the status callback should be sent for this event
func (e *EngineImpl) shouldSendStatusCallback(call *model.Call, status model.CallStatus) bool {
	// If no events specified, send for all status changes (default behavior)
	if len(call.StatusCallbackEvents) == 0 {
		return true
	}

	// Check if this status is in the requested events list
	for _, event := range call.StatusCallbackEvents {
		if event == status {
			return true
		}
		if status.IsTerminal() && event.IsTerminal() {
			return true
		}
	}

	return false
}

// sendStatusCallback posts to the status callback URL
func (e *EngineImpl) sendStatusCallback(state *subAccountState, call *model.Call) {
	form := e.buildCallbackForm(state.clock, call)

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout)
	defer cancel()

	status, body, headers, err := e.webhook.POST(ctx, call.StatusCallback, form)

	state.mu.Lock()
	defer state.mu.Unlock()

	// Log the webhook - find and lock the subaccount
	call.Timeline = append(call.Timeline, model.NewEvent(
		state.clock.Now(),
		"webhook.status_callback",
		map[string]any{
			"url":         call.StatusCallback,
			"call_status": string(call.Status),
			"status":      status,
			"error":       err,
			"headers":     headers,
			"body":        string(body),
		},
	))
}

// buildCallbackForm builds form data for Twilio-style callbacks
func (e *EngineImpl) buildCallbackForm(clock Clock, call *model.Call) url.Values {
	form := url.Values{}
	form.Set("CallSid", string(call.SID))
	form.Set("AccountSid", string(call.AccountSID))
	form.Set("From", call.From)
	form.Set("To", call.To)
	form.Set("CallStatus", string(call.Status))
	form.Set("Direction", string(call.Direction))
	form.Set("ApiVersion", e.apiVersion)
	form.Set("Timestamp", clock.Now().Format(time.RFC3339))

	if call.ParentCallSID != nil {
		form.Set("ParentCallSid", string(*call.ParentCallSID))
	}

	return form
}

// getOrCreateQueueLocked gets or creates a queue for a subaccount. Caller must hold state.mu.
func (e *EngineImpl) getOrCreateQueueLocked(state *subAccountState, accountSID model.SID, name string) *model.Queue {
	if queue, exists := state.queues[name]; exists {
		return queue
	}

	queue := &model.Queue{
		Name:       name,
		SID:        model.NewQueueSID(),
		AccountSID: accountSID,
		Members:    []model.SID{},
		Timeline:   []model.Event{},
	}
	now := state.clock.Now()
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		now,
		"queue.created",
		map[string]any{"name": name, "sid": queue.SID, "account_sid": accountSID},
	))
	state.queues[name] = queue
	return queue
}

// getOrCreateConferenceLocked gets or creates a conference for a subaccount. Caller must hold state.mu.
func (e *EngineImpl) getOrCreateConferenceLocked(state *subAccountState, accountSID model.SID, name string) *model.Conference {
	if conf, exists := state.conferences[name]; exists {
		return conf
	}

	now := state.clock.Now()
	conf := &model.Conference{
		Name:         name,
		SID:          model.NewConferenceSID(),
		AccountSID:   accountSID,
		Participants: []model.SID{},
		Status:       model.ConferenceCreated,
		Timeline:     []model.Event{},
		CreatedAt:    now,
	}
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		now,
		"conference.created",
		map[string]any{"name": name, "sid": conf.SID, "account_sid": accountSID},
	))
	state.conferences[name] = conf
	return conf
}

// Helper methods for CallRunner to use without needing to manage locks

// getOrCreateQueue is a public wrapper that handles locking
func (e *EngineImpl) getOrCreateQueue(accountSID model.SID, name string) *model.Queue {
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	return e.getOrCreateQueueLocked(state, accountSID, name)
}

// getOrCreateConference is a public wrapper that handles locking
func (e *EngineImpl) getOrCreateConference(accountSID model.SID, name string) *model.Conference {
	e.subAccountsMu.RLock()
	state, exists := e.subAccounts[accountSID]
	e.subAccountsMu.RUnlock()

	if !exists {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	return e.getOrCreateConferenceLocked(state, accountSID, name)
}

//
//// lockCall locks the subaccount containing the call and returns an unlock function
//func (e *EngineImpl) lockCall(callSID model.SID) func() {
//	state, _ := e.findCallState(callSID)
//	if state != nil {
//		state.mu.Lock()
//		return func() { state.mu.Unlock() }
//	}
//	return func() {}
//}
//
//// rlockCall read-locks the subaccount containing the call and returns an unlock function
//func (e *EngineImpl) rlockCall(callSID model.SID) func() {
//	state, _ := e.findCallState(callSID)
//	if state != nil {
//		state.mu.RLock()
//		return func() { state.mu.RUnlock() }
//	}
//	return func() {}
//}
//
//// getRunner retrieves a call runner by call SID
//func (e *EngineImpl) getRunner(callSID model.SID) *CallRunner {
//	state, _ := e.findCallState(callSID)
//	if state == nil {
//		return nil
//	}
//
//	state.mu.RLock()
//	defer state.mu.RUnlock()
//	return state.runners[callSID]
//}
//
//// getCallBySID retrieves a call by SID
//func (e *EngineImpl) getCallBySID(callSID model.SID) *model.Call {
//	state, call := e.findCallState(callSID)
//	if state == nil || call == nil {
//		return nil
//	}
//	return call
//}
