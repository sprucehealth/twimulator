package twilioapi

import (
	"fmt"
	"time"

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

// CreateSubAccount creates a new subaccount
func (c *Client) CreateSubAccount(req CreateSubAccountRequest) (*SubAccountResponse, error) {
	subAccount, err := c.engine.CreateSubAccount(req.FriendlyName)
	if err != nil {
		return nil, err
	}

	return &SubAccountResponse{
		SID:          string(subAccount.SID),
		FriendlyName: subAccount.FriendlyName,
		Status:       subAccount.Status,
		CreatedAt:    subAccount.CreatedAt,
	}, nil
}

// GetSubAccount retrieves a subaccount by SID
func (c *Client) GetSubAccount(sid string) (*SubAccountResponse, bool) {
	subAccount, exists := c.engine.GetSubAccount(model.SID(sid))
	if !exists {
		return nil, false
	}

	return &SubAccountResponse{
		SID:          string(subAccount.SID),
		FriendlyName: subAccount.FriendlyName,
		Status:       subAccount.Status,
		CreatedAt:    subAccount.CreatedAt,
	}, true
}

// ListSubAccounts lists all subaccounts
func (c *Client) ListSubAccounts() []*SubAccountResponse {
	subAccounts := c.engine.ListSubAccounts()
	responses := make([]*SubAccountResponse, len(subAccounts))
	for i, sa := range subAccounts {
		responses[i] = &SubAccountResponse{
			SID:          string(sa.SID),
			FriendlyName: sa.FriendlyName,
			Status:       sa.Status,
			CreatedAt:    sa.CreatedAt,
		}
	}
	return responses
}

// CreateSubAccountRequest represents the request to create a subaccount
type CreateSubAccountRequest struct {
	FriendlyName string
}

// SubAccountResponse represents a subaccount
type SubAccountResponse struct {
	SID          string    `json:"sid"`
	FriendlyName string    `json:"friendly_name"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
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
	SID            string    `json:"sid"`
	From           string    `json:"from"`
	To             string    `json:"to"`
	Status         string    `json:"status"`
	Direction      string    `json:"direction"`
	StartTime      time.Time `json:"start_time"`
	AnsweredTime   *time.Time `json:"answered_time,omitempty"`
	EndTime        *time.Time `json:"end_time,omitempty"`
	ParentCallSID  *string    `json:"parent_call_sid,omitempty"`
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
	SID          string   `json:"sid"`
	Name         string   `json:"name"`
	CurrentSize  int      `json:"current_size"`
	Members      []string `json:"members"`
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
