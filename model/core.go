// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

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
	ChildCallSIDs        []SID             `json:"child_call_sids,omitempty"`
	CurrentEndpoint      string            `json:"current_endpoint"` // "queue:{name}", "conference:{name}", "gather", ""
	Timeline             []Event           `json:"timeline"`
	ExecutedTwiML        []any             `json:"executed_twiml,omitempty"` // Track executed TwiML verbs for testing
	Variables            map[string]string `json:"variables"`
	Url                  string            `json:"url"`
	Method               string            `json:"method"`
	StatusCallback       string            `json:"status_callback,omitempty"`
	StatusCallbackEvents []CallStatus      `json:"status_callback_events,omitempty"` // Events to trigger callbacks for
	InitialParams        map[string]string `json:"initial_params,omitempty"`
	SIPDomainSID         string            `json:"sip_domain_sid,omitempty"`

	// CallbackQueue serializes status callbacks for this call
	// This is not serialized to JSON as it's internal state
	CallbackQueue chan func() `json:"-"`
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
	Name                 string           `json:"name"`
	SID                  SID              `json:"sid"`
	AccountSID           SID              `json:"account_sid"`
	Participants         []SID            `json:"participants"` // Call SIDs in conference
	Status               ConferenceStatus `json:"status"`
	Timeline             []Event          `json:"timeline"`
	CreatedAt            time.Time        `json:"created_at"`
	EndedAt              *time.Time       `json:"ended_at,omitempty"`
	StatusCallback       string           `json:"status_callback,omitempty"`
	StatusCallbackEvents []string         `json:"status_callback_events,omitempty"` // "start", "end", "join", "leave"

	// CallbackQueue serializes status callbacks for this conference
	// This is not serialized to JSON as it's internal state
	CallbackQueue chan func() `json:"-"`
}

// ParticipantState represents the state of a call within a specific conference
type ParticipantState struct {
	Muted                  bool   `json:"muted"`
	Hold                   bool   `json:"hold"`
	HoldUrl                string `json:"hold_url,omitempty"`
	HoldMethod             string `json:"hold_method,omitempty"`
	AnnounceUrl            string `json:"announce_url,omitempty"`
	AnnounceMethod         string `json:"announce_method,omitempty"`
	StartConferenceOnEnter bool   `json:"start_conference_on_enter"`
	EndConferenceOnExit    bool   `json:"end_conference_on_exit"`
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
	Addresses       []Address        `json:"addresses"`
	SigningKeys     []SigningKey     `json:"signing_keys"`
	SipDomains      []SipDomain      `json:"sip_domains"`
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

// Address represents a Twilio address resource
type Address struct {
	SID              SID       `json:"sid"`
	AccountSID       SID       `json:"account_sid"`
	CustomerName     string    `json:"customer_name"`
	Street           string    `json:"street"`
	StreetSecondary  string    `json:"street_secondary,omitempty"`
	City             string    `json:"city"`
	Region           string    `json:"region"`
	PostalCode       string    `json:"postal_code"`
	IsoCountry       string    `json:"iso_country"`
	FriendlyName     string    `json:"friendly_name,omitempty"`
	EmergencyEnabled bool      `json:"emergency_enabled"`
	Validated        bool      `json:"validated"`
	Verified         bool      `json:"verified"`
	CreatedAt        time.Time `json:"date_created"`
	UpdatedAt        time.Time `json:"date_updated"`
}

// SigningKey represents a Twilio API signing key
type SigningKey struct {
	SID          string    `json:"sid"`
	FriendlyName string    `json:"friendly_name,omitempty"`
	Secret       string    `json:"secret"`
	CreatedAt    time.Time `json:"date_created"`
	UpdatedAt    time.Time `json:"date_updated"`
}

// Recording represents a call or voicemail recording
type Recording struct {
	SID        SID       `json:"sid"`
	AccountSID SID       `json:"account_sid"`
	CallSID    *SID      `json:"call_sid,omitempty"` // nil for voicemail recordings
	FilePath   string    `json:"file_path"`          // Path to the recording file
	Duration   int       `json:"duration"`           // Duration in seconds
	Status     string    `json:"status"`             // "completed", "absent", etc.
	CreatedAt  time.Time `json:"date_created"`
}

// SipDomain represents a Twilio SIP Domain
type SipDomain struct {
	SID                       SID                                         `json:"sid"`
	AccountSID                SID                                         `json:"account_sid"`
	DomainName                string                                      `json:"domain_name"`
	FriendlyName              string                                      `json:"friendly_name"`
	VoiceUrl                  string                                      `json:"voice_url,omitempty"`
	VoiceMethod               string                                      `json:"voice_method,omitempty"`
	VoiceStatusCallbackUrl    string                                      `json:"voice_status_callback_url,omitempty"`
	VoiceStatusCallbackMethod string                                      `json:"voice_status_callback_method,omitempty"`
	SipRegistration           bool                                        `json:"sip_registration"`
	Secure                    bool                                        `json:"secure"` // SRTP
	AuthCallsMappings         []SipAuthCallsCredentialListMapping         `json:"auth_calls_mappings"`
	AuthRegistrationsMappings []SipAuthRegistrationsCredentialListMapping `json:"auth_registrations_mappings"`
	CreatedAt                 time.Time                                   `json:"date_created"`
	UpdatedAt                 time.Time                                   `json:"date_updated"`
}

// SipCredentialList represents a Twilio SIP Credential List
type SipCredentialList struct {
	SID          SID       `json:"sid"`
	AccountSID   SID       `json:"account_sid"`
	FriendlyName string    `json:"friendly_name"`
	CreatedAt    time.Time `json:"date_created"`
	UpdatedAt    time.Time `json:"date_updated"`
}

// SipCredential represents a Twilio SIP Credential
type SipCredential struct {
	SID               SID       `json:"sid"`
	AccountSID        SID       `json:"account_sid"`
	CredentialListSID SID       `json:"credential_list_sid"`
	Username          string    `json:"username"`
	Password          string    `json:"password"` // Stored, but not returned in API responses
	CreatedAt         time.Time `json:"date_created"`
	UpdatedAt         time.Time `json:"date_updated"`
}

// SipAuthCallsCredentialListMapping represents mapping of credential list to SIP domain for calls
type SipAuthCallsCredentialListMapping struct {
	SID               SID       `json:"sid"`
	AccountSID        SID       `json:"account_sid"`
	DomainSID         SID       `json:"domain_sid"`
	CredentialListSID SID       `json:"credential_list_sid"`
	CreatedAt         time.Time `json:"date_created"`
	UpdatedAt         time.Time `json:"date_updated"`
}

// SipAuthRegistrationsCredentialListMapping represents mapping of credential list to SIP domain for registrations
type SipAuthRegistrationsCredentialListMapping struct {
	SID               SID       `json:"sid"`
	AccountSID        SID       `json:"account_sid"`
	DomainSID         SID       `json:"domain_sid"`
	CredentialListSID SID       `json:"credential_list_sid"`
	CreatedAt         time.Time `json:"date_created"`
	UpdatedAt         time.Time `json:"date_updated"`
}

// SID generators with atomic counters for determinism
var (
	callCounter                        uint64
	conferenceCounter                  uint64
	queueCounter                       uint64
	subAccountCounter                  uint64
	phoneNumberCounter                 uint64
	applicationCounter                 uint64
	recordingCounter                   uint64
	addressCounter                     uint64
	signingKeyCounter                  uint64
	sipDomainCounter                   uint64
	sipCredentialListCounter           uint64
	sipCredentialCounter               uint64
	sipAuthCallsMappingCounter         uint64
	sipAuthRegistrationsMappingCounter uint64
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

// NewAddressSID generates a new Address SID (ADFAKE prefix, 34 chars total)
func NewAddressSID() SID {
	counter := atomic.AddUint64(&addressCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("ADFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSigningKeySID generates a new Signing Key SID (SKFAKE prefix, 34 chars total)
func NewSigningKeySID() string {
	counter := atomic.AddUint64(&signingKeyCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return fmt.Sprintf("SKFAKE%014x%s", counter, hex.EncodeToString(b)[:14])
}

// NewSigningKeySecret generates a pseudo-random secret for signing keys
func NewSigningKeySecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewAuthToken generates a pseudo-random auth token for subaccounts
func NewAuthToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// NewSipDomainSID generates a new SIP Domain SID (SDFAKE prefix, 34 chars total)
func NewSipDomainSID() SID {
	counter := atomic.AddUint64(&sipDomainCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("SDFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSipCredentialListSID generates a new SIP Credential List SID (CLFAKE prefix, 34 chars total)
func NewSipCredentialListSID() SID {
	counter := atomic.AddUint64(&sipCredentialListCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("CLFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSipCredentialSID generates a new SIP Credential SID (CRFAKE prefix, 34 chars total)
func NewSipCredentialSID() SID {
	counter := atomic.AddUint64(&sipCredentialCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("CRFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSipAuthCallsMappingSID generates a new SIP Auth Calls Mapping SID (CMFAKE prefix, 34 chars total)
func NewSipAuthCallsMappingSID() SID {
	counter := atomic.AddUint64(&sipAuthCallsMappingCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("CMFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
}

// NewSipAuthRegistrationsMappingSID generates a new SIP Auth Registrations Mapping SID (RMFAKE prefix, 34 chars total)
func NewSipAuthRegistrationsMappingSID() SID {
	counter := atomic.AddUint64(&sipAuthRegistrationsMappingCounter, 1)
	b := make([]byte, 7)
	rand.Read(b)
	return SID(fmt.Sprintf("RMFAKE%014x%s", counter, hex.EncodeToString(b)[:14]))
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
