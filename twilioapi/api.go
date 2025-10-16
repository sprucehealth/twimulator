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

// DeleteIncomingPhoneNumber removes a provisioned number
func (c *Client) DeleteIncomingPhoneNumber(sid string, params *twilioopenapi.DeleteIncomingPhoneNumberParams) error {
	return c.engine.DeleteIncomingPhoneNumber(sid, params)
}

// CreateCall creates a new call via the engine using Twilio's generated params
func (c *Client) CreateCall(params *twilioopenapi.CreateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.CreateCall(params)
}

// UpdateCall proxies call updates to the engine
func (c *Client) UpdateCall(sid string, params *twilioopenapi.UpdateCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.UpdateCall(sid, params)
}

// FetchCall retrieves a call via Twilio-compatible API
func (c *Client) FetchCall(sid string, params *twilioopenapi.FetchCallParams) (*twilioopenapi.ApiV2010Call, error) {
	return c.engine.FetchCall(sid, params)
}

// HangupCall terminates a call
func (c *Client) HangupCall(sid string) error {
	return c.engine.Hangup(model.SID(sid))
}

// QueueResponse represents a queue
type QueueResponse struct {
	SID         string   `json:"sid"`
	Name        string   `json:"name"`
	CurrentSize int      `json:"current_size"`
	Members     []string `json:"members"`
}

// GetQueue retrieves a queue by name and subaccount
func (c *Client) GetQueue(accountSID string, name string) (*QueueResponse, bool) {
	queue, exists := c.engine.GetQueue(model.SID(accountSID), name)
	if !exists {
		return nil, false
	}

	members := make([]string, len(queue.Members))
	for i, sid := range queue.Members {
		members[i] = string(sid)
	}

	return &QueueResponse{
		SID:         string(queue.SID),
		Name:        queue.Name,
		CurrentSize: len(queue.Members),
		Members:     members,
	}, true
}

// ConferenceResponse represents a conference
type ConferenceResponse struct {
	SID          string   `json:"sid"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	Participants []string `json:"participants"`
}

// GetConference retrieves a conference by name and subaccount
func (c *Client) GetConference(accountSID string, name string) (*ConferenceResponse, bool) {
	conf, exists := c.engine.GetConference(model.SID(accountSID), name)
	if !exists {
		return nil, false
	}

	participants := make([]string, len(conf.Participants))
	for i, sid := range conf.Participants {
		participants[i] = string(sid)
	}

	return &ConferenceResponse{
		SID:          string(conf.SID),
		Name:         conf.Name,
		Status:       string(conf.Status),
		Participants: participants,
	}, true
}
