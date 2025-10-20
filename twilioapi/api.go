package twilioapi

import (
	twilioopenapi "github.com/twilio/twilio-go/rest/api/v2010"

	"twimulator/engine"
	"twimulator/model"
)

// Client provides a Twilio-like REST API facade
type Client struct {
	engine engine.Engine
}

// NewClient creates a new Twilio API client
func NewClient(e engine.Engine) *Client {
	return &Client{engine: e}
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
	return c.engine.CreateIncomingPhoneNumber(params)
}

// ListIncomingPhoneNumber returns provisioned numbers for an account
func (c *Client) ListIncomingPhoneNumber(params *twilioopenapi.ListIncomingPhoneNumberParams) ([]twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	return c.engine.ListIncomingPhoneNumber(params)
}

// UpdateIncomingPhoneNumber updates a provisioned phone number
func (c *Client) UpdateIncomingPhoneNumber(sid string, params *twilioopenapi.UpdateIncomingPhoneNumberParams) (*twilioopenapi.ApiV2010IncomingPhoneNumber, error) {
	return c.engine.UpdateIncomingPhoneNumber(sid, params)
}

// DeleteIncomingPhoneNumber removes a provisioned number
func (c *Client) DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error {
	return c.engine.DeleteIncomingPhoneNumber(sid, params)
}

// CreateApplication provisions a Twilio application for an account
func (c *Client) CreateApplication(params *twilioopenapi.CreateApplicationParams) (*twilioopenapi.ApiV2010Application, error) {
	return c.engine.CreateApplication(params)
}

// CreateQueue creates a queue for an account
func (c *Client) CreateQueue(params *twilioopenapi.CreateQueueParams) (*twilioopenapi.ApiV2010Queue, error) {
	return c.engine.CreateQueue(params)
}

// CreateCall creates a new call via the engine using Twilio's generated params
func (c *Client) CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateCall(params)
}

// CreateIncomingCall simulates an incoming call to a provisioned number with an application
func (c *Client) CreateIncomingCall(accountSID model.SID, from string, to string) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateIncomingCall(accountSID, from, to)
}

// UpdateCall proxies call updates to the engine
func (c *Client) UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.UpdateCall(sid, params)
}

// FetchCall retrieves a call via Twilio-compatible API
func (c *Client) FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.FetchCall(sid, params)
}

// FetchConference retrieves a conference by SID
func (c *Client) FetchConference(sid string, params *twilioopenapi.FetchConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	return c.engine.FetchConference(sid, params)
}

// ListConference returns conferences for an account
func (c *Client) ListConference(params *twilioopenapi.ListConferenceParams) ([]twilioopenapi.ApiV2010Conference, error) {
	return c.engine.ListConference(params)
}

// UpdateConference updates a conference
func (c *Client) UpdateConference(sid string, params *twilioopenapi.UpdateConferenceParams) (*twilioopenapi.ApiV2010Conference, error) {
	return c.engine.UpdateConference(sid, params)
}

// FetchParticipant retrieves a participant from a conference
func (c *Client) FetchParticipant(conferenceSid string, callSid string, params *twilioopenapi.FetchParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	return c.engine.FetchParticipant(conferenceSid, callSid, params)
}

// UpdateParticipant updates a participant in a conference
func (c *Client) UpdateParticipant(conferenceSid string, callSid string, params *twilioopenapi.UpdateParticipantParams) (*twilioopenapi.ApiV2010Participant, error) {
	return c.engine.UpdateParticipant(conferenceSid, callSid, params)
}

// FetchRecording retrieves a recording by SID
func (c *Client) FetchRecording(sid string, params *twilioopenapi.FetchRecordingParams) (*twilioopenapi.ApiV2010Recording, error) {
	return c.engine.FetchRecording(sid, params)
}

// AnswerCall explicitly answers a ringing call
func (c *Client) AnswerCall(sid string) error {
	return c.engine.AnswerCall(model.SID(sid))
}

// SetCallBusy marks a call as busy
func (c *Client) SetCallBusy(sid string) error {
	return c.engine.SetCallBusy(model.SID(sid))
}

// SetCallFailed marks a call as failed
func (c *Client) SetCallFailed(sid string) error {
	return c.engine.SetCallFailed(model.SID(sid))
}

// HangupCall terminates a call
func (c *Client) HangupCall(sid string) error {
	return c.engine.Hangup(model.SID(sid))
}
