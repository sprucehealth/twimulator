package twilioapi

import (
	"fmt"
	"time"

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

// CreateCallRequest represents the request to create a call
type CreateCallRequest struct {
	AccountSID       string // Required: SubAccount SID
	From             string
	To               string
	URL              string // AnswerURL
	StatusCallback   string
	MachineDetection string
	Timeout          int // in seconds
}

// CallResponse represents a Twilio-like call response
type CallResponse struct {
	SID           string     `json:"sid"`
	From          string     `json:"from"`
	To            string     `json:"to"`
	Status        string     `json:"status"`
	Direction     string     `json:"direction"`
	StartTime     time.Time  `json:"start_time"`
	AnsweredTime  *time.Time `json:"answered_time,omitempty"`
	EndTime       *time.Time `json:"end_time,omitempty"`
	ParentCallSID *string    `json:"parent_call_sid,omitempty"`
}

// CreateCall creates a new call via the engine
func (c *Client) CreateCall(req CreateCallRequest) (*CallResponse, error) {
	if req.AccountSID == "" {
		return nil, fmt.Errorf("AccountSID is required")
	}

	timeout := time.Duration(req.Timeout) * time.Second
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	params := engine.CreateCallParams{
		AccountSID:       model.SID(req.AccountSID),
		From:             req.From,
		To:               req.To,
		AnswerURL:        req.URL,
		StatusCallback:   req.StatusCallback,
		MachineDetection: req.MachineDetection != "",
		Timeout:          timeout,
	}

	call, err := c.engine.CreateCall(params)
	if err != nil {
		return nil, err
	}

	return c.callToResponse(call), nil
}

// GetCall retrieves a call by SID
func (c *Client) GetCall(sid string) (*CallResponse, bool) {
	call, exists := c.engine.GetCall(model.SID(sid))
	if !exists {
		return nil, false
	}
	return c.callToResponse(call), true
}

// ListCalls lists all calls
func (c *Client) ListCalls() []*CallResponse {
	calls := c.engine.ListCalls(engine.CallFilter{})
	responses := make([]*CallResponse, len(calls))
	for i, call := range calls {
		responses[i] = c.callToResponse(call)
	}
	return responses
}

// HangupCall terminates a call
func (c *Client) HangupCall(sid string) error {
	return c.engine.Hangup(model.SID(sid))
}

func (c *Client) callToResponse(call *model.Call) *CallResponse {
	resp := &CallResponse{
		SID:          string(call.SID),
		From:         call.From,
		To:           call.To,
		Status:       string(call.Status),
		Direction:    string(call.Direction),
		StartTime:    call.StartAt,
		AnsweredTime: call.AnsweredAt,
		EndTime:      call.EndedAt,
	}
	if call.ParentCallSID != nil {
		parentSID := string(*call.ParentCallSID)
		resp.ParentCallSID = &parentSID
	}
	return resp
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
