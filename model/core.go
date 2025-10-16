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

// CallStatus represents the current status of a call
type CallStatus string

const (
	CallQueued     CallStatus = "queued"
	CallRinging    CallStatus = "ringing"
	CallInProgress CallStatus = "in-progress"
	CallCompleted  CallStatus = "completed"
	CallBusy       CallStatus = "busy"
	CallFailed     CallStatus = "failed"
	CallNoAnswer   CallStatus = "no-answer"
)

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
	SID             SID               `json:"sid"`
	AccountSID      SID               `json:"account_sid"`
	From            string            `json:"from"`
	To              string            `json:"to"`
	Direction       Direction         `json:"direction"`
	Status          CallStatus        `json:"status"`
	StartAt         time.Time         `json:"start_at"`
	AnsweredAt      *time.Time        `json:"answered_at,omitempty"`
	EndedAt         *time.Time        `json:"ended_at,omitempty"`
	ParentCallSID   *SID              `json:"parent_call_sid,omitempty"`
	CurrentEndpoint string            `json:"current_endpoint"` // "queue:{name}", "conference:{name}", "gather", ""
	Timeline        []Event           `json:"timeline"`
	Variables       map[string]string `json:"variables"`
	AnswerURL       string            `json:"answer_url"`
	StatusCallback  string            `json:"status_callback,omitempty"`
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

// Event represents a timeline event for a call, queue, or conference
type Event struct {
	Time   time.Time      `json:"time"`
	Type   string         `json:"type"` // "webhook.request", "webhook.response", "status.changed", etc.
	Detail map[string]any `json:"detail"`
}

// SubAccount represents a Twilio subaccount
type SubAccount struct {
	SID             SID           `json:"sid"`
	FriendlyName    string        `json:"friendly_name"`
	Status          string        `json:"status"` // "active", "suspended", "closed"
	CreatedAt       time.Time     `json:"created_at"`
	AuthToken       string        `json:"auth_token"`
	IncomingNumbers []string      `json:"incoming_numbers"`
	Applications    []Application `json:"applications"`
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
)

// NewCallSID generates a new Call SID (CA prefix)
func NewCallSID() SID {
	counter := atomic.AddUint64(&callCounter, 1)
	// Mix counter with random bytes for uniqueness
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("CA%08x%s", counter, hex.EncodeToString(b)[:8]))
}

// NewConferenceSID generates a new Conference SID (CF prefix)
func NewConferenceSID() SID {
	counter := atomic.AddUint64(&conferenceCounter, 1)
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("CF%08x%s", counter, hex.EncodeToString(b)[:8]))
}

// NewQueueSID generates a new Queue SID (QU prefix)
func NewQueueSID() SID {
	counter := atomic.AddUint64(&queueCounter, 1)
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("QU%08x%s", counter, hex.EncodeToString(b)[:8]))
}

// NewApplicationSID generates a new Application SID (AP prefix)
func NewApplicationSID() SID {
	counter := atomic.AddUint64(&applicationCounter, 1)
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("AP%08x%s", counter, hex.EncodeToString(b)[:8]))
}

// NewSubAccountSID generates a new SubAccount SID (AC prefix)
func NewSubAccountSID() SID {
	counter := atomic.AddUint64(&subAccountCounter, 1)
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("AC%08x%s", counter, hex.EncodeToString(b)[:8]))
}

// NewPhoneNumberSID generates a new Incoming Phone Number SID (PN prefix)
func NewPhoneNumberSID() SID {
	counter := atomic.AddUint64(&phoneNumberCounter, 1)
	b := make([]byte, 4)
	rand.Read(b)
	return SID(fmt.Sprintf("PN%08x%s", counter, hex.EncodeToString(b)[:8]))
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
