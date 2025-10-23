package model

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"
)

// SID represents a Twilio-like Session ID with a prefix
type SID string

func (s SID) String() string {
	return string(s)
}

// CallStatus represents the current status of a call
type CallStatus string

const (
	CallInitiated  CallStatus = "initiated"
	CallQueued     CallStatus = "queued"
	CallRinging    CallStatus = "ringing"
	CallInProgress CallStatus = "in-progress"
	CallCompleted  CallStatus = "completed"
	CallBusy       CallStatus = "busy"
	CallFailed     CallStatus = "failed"
	CallNoAnswer   CallStatus = "no-answer"
	CallCanceled   CallStatus = "canceled"
	CallAnswered   CallStatus = "answered"
)

func (s CallStatus) IsTerminal() bool {
	switch s {
	case CallCompleted, CallCanceled, CallFailed, CallNoAnswer, CallBusy:
		return true
	case CallRinging, CallInProgress, CallQueued, CallAnswered, CallInitiated:
		return false
	default:
		panic(fmt.Sprintf("unknown call status: %s", s))
	}
}

// Direction represents whether a call is inbound or outbound
type Direction string

const (
	Inbound  Direction = "inbound"
	Outbound Direction = "outbound"
)

// ConferenceStatus represents the status of a conference
type ConferenceStatus string

const (
	ConferenceCreated    ConferenceStatus = "created"
	ConferenceInProgress ConferenceStatus = "in-progress"
	ConferenceCompleted  ConferenceStatus = "completed"
)

// Call represents a voice call
type Call struct {
	SID                  SID               `json:"sid"`
	AccountSID           SID               `json:"account_sid"`
	From                 string            `json:"from"`
	To                   string            `json:"to"`
	Direction            Direction         `json:"direction"`
	Status               CallStatus        `json:"status"`
	StartAt              time.Time         `json:"start_at"`
	AnsweredAt           *time.Time        `json:"answered_at,omitempty"`
	EndedAt              *time.Time        `json:"ended_at,omitempty"`
	ParentCallSID        *SID              `json:"parent_call_sid,omitempty"`
	CurrentEndpoint      string            `json:"current_endpoint"` // "queue:{name}", "conference:{name}", "gather", ""
	Timeline             []Event           `json:"timeline"`
	ExecutedTwiML        []any             `json:"executed_twiml,omitempty"` // Track executed TwiML verbs for testing
	Variables            map[string]string `json:"variables"`
	Url                  string            `json:"url"`
	StatusCallback       string            `json:"status_callback,omitempty"`
	StatusCallbackEvents []CallStatus      `json:"status_callback_events,omitempty"` // Events to trigger callbacks for
}

// Queue represents a call queue
type Queue struct {
	Name       string  `json:"name"`
	SID        SID     `json:"sid"`
	AccountSID SID     `json:"account_sid"`
	Members    []SID   `json:"members"` // Call SIDs in queue
	Timeline   []Event `json:"timeline"`
}

// Conference represents a conference room
type Conference struct {
	Name         string           `json:"name"`
	SID          SID              `json:"sid"`
	AccountSID   SID              `json:"account_sid"`
	Participants []SID            `json:"participants"` // Call SIDs in conference
	Status       ConferenceStatus `json:"status"`
	Timeline     []Event          `json:"timeline"`
	CreatedAt    time.Time        `json:"created_at"`
	EndedAt      *time.Time       `json:"ended_at,omitempty"`
}

// ParticipantState represents the state of a call within a specific conference
type ParticipantState struct {
	Muted          bool   `json:"muted"`
	Hold           bool   `json:"hold"`
	HoldUrl        string `json:"hold_url,omitempty"`
	HoldMethod     string `json:"hold_method,omitempty"`
	AnnounceUrl    string `json:"announce_url,omitempty"`
	AnnounceMethod string `json:"announce_method,omitempty"`
}

// Event represents a timeline event for a call, queue, or conference
type Event struct {
	Time   time.Time      `json:"time"`
	Type   string         `json:"type"` // "webhook.request", "webhook.response", "status.changed", etc.
	Detail map[string]any `json:"detail"`
}

// SubAccount represents a Twilio subaccount
type SubAccount struct {
	SID             SID              `json:"sid"`
	FriendlyName    string           `json:"friendly_name"`
	Status          string           `json:"status"` // "active", "suspended", "closed"
	CreatedAt       time.Time        `json:"created_at"`
	AuthToken       string           `json:"auth_token"`
	IncomingNumbers []IncomingNumber `json:"incoming_numbers"`
	Applications    []Application    `json:"applications"`
}

// IncomingNumber represents a provisioned phone number
type IncomingNumber struct {
	SID                 string    `json:"sid"`
	PhoneNumber         string    `json:"phone_number"`
	VoiceApplicationSID *string   `json:"voice_application_sid,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// Application represents a Twilio application tied to a subaccount
type Application struct {
	SID                  string    `json:"sid"`
	FriendlyName         string    `json:"friendly_name,omitempty"`
	VoiceMethod          string    `json:"voice_method,omitempty"`
	VoiceURL             string    `json:"voice_url,omitempty"`
	StatusCallbackMethod string    `json:"status_callback_method,omitempty"`
	StatusCallback       string    `json:"status_callback,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

// SID generators with atomic counters for determinism
var (
	callCounter        uint64
	conferenceCounter  uint64
	queueCounter       uint64
	subAccountCounter  uint64
	phoneNumberCounter uint64
	applicationCounter uint64
	recordingCounter   uint64
)

// NewCallSID generates a new Call SID (CAFAKE prefix, 34 chars total)
func NewCallSID() SID {
	counter := atomic.AddUint64(&callCounter, 1)
	// Generate 14 random hex characters to make total length 34
	// CAFAKE (6) + 14 hex chars + counter hex (14) = 34
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("CAFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewConferenceSID generates a new Conference SID (CFFAKE prefix, 34 chars total)
func NewConferenceSID() SID {
	counter := atomic.AddUint64(&conferenceCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("CFFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewQueueSID generates a new Queue SID (QUFAKE prefix, 34 chars total)
func NewQueueSID() SID {
	counter := atomic.AddUint64(&queueCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("QUFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewApplicationSID generates a new Application SID (APFAKE prefix, 34 chars total)
func NewApplicationSID() SID {
	counter := atomic.AddUint64(&applicationCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("APFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSubAccountSID generates a new SubAccount SID (ACFAKE prefix, 34 chars total)
func NewSubAccountSID() SID {
	counter := atomic.AddUint64(&subAccountCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("ACFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewPhoneNumberSID generates a new Incoming Phone Number SID (PNFAKE prefix, 34 chars total)
func NewPhoneNumberSID() SID {
	counter := atomic.AddUint64(&phoneNumberCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("PNFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewRecordingSID generates a new Recording SID (REFAKE prefix, 34 chars total)
func NewRecordingSID() SID {
	counter := atomic.AddUint64(&recordingCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("REFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewAuthToken generates a pseudo-random auth token for subaccounts
func NewAuthToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewEvent creates a new timeline event
func NewEvent(t time.Time, eventType string, detail map[string]any) Event {
	if detail == nil {
		detail = make(map[string]any)
	}
	return Event{
		Time:   t,
		Type:   eventType,
		Detail: detail,
	}
}
