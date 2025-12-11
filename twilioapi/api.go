// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package twilioapi

import (
	"time"

	twilioopenapi "github.com/twilio/twilio-go/rest/api/v2010"

	"github.com/sprucehealth/twimulator/engine"
	"github.com/sprucehealth/twimulator/model"
)

// Client provides a Twilio-like REST API facade
type Client struct {
	subaccountSID string
	engine        engine.Engine
}

// NewClient creates a new Twilio API client
func NewClient(subaccountSID string, e engine.Engine) *Client {
	return &Client{
		subaccountSID: subaccountSID,
		engine:        e,
	}
}

// CreateAccount delegates to the engine's account creation for drop-in Twilio compatibility
func (c *Client) CreateAccount(params *twilioopenapi.CreateAccountParams) (*twilioopenapi.ApiV2010Account, error) {
	return c.engine.CreateAccount(params)
}

// ListAccount delegates to the engine's Twilio-compatible listing implementation
func (c *Client) ListAccount(params *twilioopenapi.ListAccountParams) ([]twilioopenapi.ApiV2010Account, error) {
	return c.engine.ListAccount(params)
}

// CreateIncomingPhoneNumber provisions a number for the account
func (c *Client) CreateIncomingPhoneNumber(params *twilioopenapi.CreateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil {
		params = &twilioopenapi.CreateIncomingPhoneNumberParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateIncomingPhoneNumber(params)
}

// ListIncomingPhoneNumber returns provisioned numbers for an account
func (c *Client) ListIncomingPhoneNumber(params *twilioopenapi.ListIncomingPhoneNumberParams) ([]twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil {
		params = &twilioopenapi.ListIncomingPhoneNumberParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.ListIncomingPhoneNumber(params)
}

// UpdateIncomingPhoneNumber updates a provisioned phone number
func (c *Client) UpdateIncomingPhoneNumber(sid string, params *twilioopenapi.UpdateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	if params == nil {
		params = &twilioopenapi.UpdateIncomingPhoneNumberParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.UpdateIncomingPhoneNumber(sid, params)
}

// DeleteIncomingPhoneNumber removes a provisioned number
func (c *Client) DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error {
	if params == nil {
		params = &twilioopenapi.DeleteIncomingPhoneNumberParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.DeleteIncomingPhoneNumber(sid, params)
}

// CreateApplication provisions a Twilio application for an account
func (c *Client) CreateApplication(params *twilioopenapi.CreateApplicationParams) (*twilioopenapi.ApiV2010Application, error) {
	if params == nil {
		params = &twilioopenapi.CreateApplicationParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateApplication(params)
}

// CreateQueue creates a queue for an account
func (c *Client) CreateQueue(params *twilioopenapi.CreateQueueParams) (*twilioopenapi.ApiV2010Queue, error) {
	if params == nil {
		params = &twilioopenapi.CreateQueueParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateQueue(params)
}

// CreateAddress creates an address for an account
func (c *Client) CreateAddress(params *twilioopenapi.CreateAddressParams) (*twilioopenapi.ApiV2010Address, error) {
	if params == nil {
		params = &twilioopenapi.CreateAddressParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateAddress(params)
}

// CreateNewSigningKey creates a new API signing key for an account
func (c *Client) CreateNewSigningKey(params *twilioopenapi.CreateNewSigningKeyParams) (*twilioopenapi.ApiV2010NewSigningKey, error) {
	if params == nil {
		params = &twilioopenapi.CreateNewSigningKeyParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateNewSigningKey(params)
}

// CreateCall creates a new call via the engine using Twilio's generated params
func (c *Client) CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil {
		params = &twilioopenapi.CreateCallParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateCall(params)
}

// CreateIncomingCall simulates an incoming call to a provisioned number with an application
func (c *Client) CreateIncomingCall(from string, to string) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateIncomingCall(model.SID(c.subaccountSID), from, to)
}

// CreateOutgoingSoftphoneCall simulates an outgoing call from twilio softphones
func (c *Client) CreateOutgoingSoftphoneCall(from string, to string, accessToken string, params map[string]string) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateIncomingCallFromSoftphone(model.SID(c.subaccountSID), from, to, accessToken, params)
}

// CreateOutgoingSIPCall simulates an outgoing call from a sip phone
func (c *Client) CreateOutgoingSIPCall(fromSIP string, toSIP string) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateIncomingCallFromSIP(model.SID(c.subaccountSID), fromSIP, toSIP)
}

// UpdateCall proxies call updates to the engine
func (c *Client) UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil {
		params = &twilioopenapi.UpdateCallParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.UpdateCall(sid, params)
}

// FetchCall retrieves a call via Twilio-compatible API
func (c *Client) FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error) {
	if params == nil {
		params = &twilioopenapi.FetchCallParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.FetchCall(sid, params)
}

// FetchConference retrieves a conference by SID
func (c *Client) FetchConference(sid string, params *twilioopenapi.FetchConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	if params == nil {
		params = &twilioopenapi.FetchConferenceParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.FetchConference(sid, params)
}

// ListConference returns conferences for an account
func (c *Client) ListConference(params *twilioopenapi.ListConferenceParams) ([]twilioopenapi.ApiV2010Conference, error) {
	if params == nil {
		params = &twilioopenapi.ListConferenceParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.ListConference(params)
}

// UpdateConference updates a conference
func (c *Client) UpdateConference(sid string, params *twilioopenapi.UpdateConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	if params == nil {
		params = &twilioopenapi.UpdateConferenceParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.UpdateConference(sid, params)
}

// FetchParticipant retrieves a participant from a conference
func (c *Client) FetchParticipant(conferenceSid string, callSid string, params *twilioopenapi.FetchParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	if params == nil {
		params = &twilioopenapi.FetchParticipantParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.FetchParticipant(conferenceSid, callSid, params)
}

// UpdateParticipant updates a participant in a conference
func (c *Client) UpdateParticipant(conferenceSid string, callSid string, params *twilioopenapi.UpdateParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	if params == nil {
		params = &twilioopenapi.UpdateParticipantParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.UpdateParticipant(conferenceSid, callSid, params)
}

// FetchRecording retrieves a recording by SID
func (c *Client) FetchRecording(sid string, params *twilioopenapi.FetchRecordingParams) (*twilioopenapi.ApiV2010Recording, error) {
	if params == nil {
		params = &twilioopenapi.FetchRecordingParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.FetchRecording(sid, params)
}

// AnswerCall explicitly answers a ringing call
func (c *Client) AnswerCall(sid model.SID) error {
	return c.engine.AnswerCall(model.SID(c.subaccountSID), sid)
}

// SetCallBusy marks a call as busy
func (c *Client) SetCallBusy(sid model.SID) error {
	return c.engine.SetCallBusy(model.SID(c.subaccountSID), sid)
}

// SetCallFailed marks a call as failed
func (c *Client) SetCallFailed(sid model.SID) error {
	return c.engine.SetCallFailed(model.SID(c.subaccountSID), sid)
}

// HangupCall terminates a call
func (c *Client) HangupCall(sid model.SID) error {
	return c.engine.Hangup(model.SID(c.subaccountSID), sid)
}

// Snapshot returns the current state snapshot for the client's subaccount
func (c *Client) Snapshot() (*engine.StateSnapshot, error) {
	return c.engine.Snapshot(model.SID(c.subaccountSID))
}

func (c *Client) SetClock(clock engine.Clock) error {
	return c.engine.SetClockForAccount(model.SID(c.subaccountSID), clock)
}

func (c *Client) AdvanceClock(d time.Duration) error {
	return c.engine.AdvanceForAccount(model.SID(c.subaccountSID), d)
}

func (c *Client) SendDigits(callSID model.SID, digits string) error {
	return c.engine.SendDigits(model.SID(c.subaccountSID), callSID, digits)
}

// SetCallRecording associates a recording file with a call for Dial/Conference recording callbacks
// filePath: path to the recording file (can be one of the example recordings)
// duration: duration of the recording in seconds
// Returns the recording SID
func (c *Client) SetCallRecording(callSID model.SID, filePath string, duration int) (model.SID, error) {
	return c.engine.SetCallRecording(model.SID(c.subaccountSID), callSID, filePath, duration)
}

// SetCallVoicemail associates a voicemail recording with a call for Record verb
// filePath: path to the recording file (can be one of the example recordings)
// duration: duration of the recording in seconds
// Returns the recording SID
func (c *Client) SetCallVoicemail(callSID model.SID, filePath string, duration int) (model.SID, error) {
	return c.engine.SetCallVoicemail(model.SID(c.subaccountSID), callSID, filePath, duration)
}

// GetRecording retrieves a recording by SID
func (c *Client) GetRecording(recordingSID model.SID) (*model.Recording, error) {
	return c.engine.GetRecording(model.SID(c.subaccountSID), recordingSID)
}

// CreateSipDomain creates a new SIP domain for the client's subaccount
func (c *Client) CreateSipDomain(params *twilioopenapi.CreateSipDomainParams) (*twilioopenapi.ApiV2010SipDomain, error) {
	if params == nil {
		params = &twilioopenapi.CreateSipDomainParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateSipDomain(params)
}

// ListSipCredentialList returns SIP credential lists for the client's subaccount
func (c *Client) ListSipCredentialList(params *twilioopenapi.ListSipCredentialListParams) ([]twilioopenapi.ApiV2010SipCredentialList, error) {
	if params == nil {
		params = &twilioopenapi.ListSipCredentialListParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.ListSipCredentialList(params)
}

// CreateSipCredentialList creates a new SIP credential list for the client's subaccount
func (c *Client) CreateSipCredentialList(params *twilioopenapi.CreateSipCredentialListParams) (*twilioopenapi.ApiV2010SipCredentialList, error) {
	if params == nil {
		params = &twilioopenapi.CreateSipCredentialListParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateSipCredentialList(params)
}

// CreateSipAuthCallsCredentialListMapping creates a mapping between a credential list and a SIP domain for calls
func (c *Client) CreateSipAuthCallsCredentialListMapping(DomainSid string, params *twilioopenapi.CreateSipAuthCallsCredentialListMappingParams) (*twilioopenapi.ApiV2010SipAuthCallsCredentialListMapping, error) {
	if params == nil {
		params = &twilioopenapi.CreateSipAuthCallsCredentialListMappingParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateSipAuthCallsCredentialListMapping(DomainSid, params)
}

// CreateSipAuthRegistrationsCredentialListMapping creates a mapping between a credential list and a SIP domain for registrations
func (c *Client) CreateSipAuthRegistrationsCredentialListMapping(DomainSid string, params *twilioopenapi.CreateSipAuthRegistrationsCredentialListMappingParams) (*twilioopenapi.ApiV2010SipAuthRegistrationsCredentialListMapping, error) {
	if params == nil {
		params = &twilioopenapi.CreateSipAuthRegistrationsCredentialListMappingParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateSipAuthRegistrationsCredentialListMapping(DomainSid, params)
}

// PageSipAuthCallsCredentialListMapping returns a page of auth calls credential list mappings for a SIP domain
func (c *Client) PageSipAuthCallsCredentialListMapping(DomainSid string, params *twilioopenapi.ListSipAuthCallsCredentialListMappingParams, pageToken, pageNumber string) (*twilioopenapi.ListSipAuthCallsCredentialListMappingResponse, error) {
	if params == nil {
		params = &twilioopenapi.ListSipAuthCallsCredentialListMappingParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.PageSipAuthCallsCredentialListMapping(DomainSid, params, pageToken, pageNumber)
}

// CreateSipCredential creates a new SIP credential within a credential list
func (c *Client) CreateSipCredential(CredentialListSid string, params *twilioopenapi.CreateSipCredentialParams) (*twilioopenapi.ApiV2010SipCredential, error) {
	if params == nil {
		params = &twilioopenapi.CreateSipCredentialParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.CreateSipCredential(CredentialListSid, params)
}

// ListSipCredential returns all SIP credentials for a credential list
func (c *Client) ListSipCredential(CredentialListSid string, params *twilioopenapi.ListSipCredentialParams) ([]twilioopenapi.ApiV2010SipCredential, error) {
	if params == nil {
		params = &twilioopenapi.ListSipCredentialParams{}
	}
	params.PathAccountSid = &c.subaccountSID
	return c.engine.ListSipCredential(CredentialListSid, params)
}
