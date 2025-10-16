package httpstub

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// WebhookClient defines the interface for making webhook HTTP calls
type WebhookClient interface {
	POST(ctx context.Context, url string, form url.Values) (status int, body []byte, headers http.Header, err error)
}

// DefaultWebhookClient is the default implementation using http.Client
type DefaultWebhookClient struct {
	client  *http.Client
	timeout time.Duration
}

// NewDefaultWebhookClient creates a new default webhook client
func NewDefaultWebhookClient(timeout time.Duration) *DefaultWebhookClient {
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &DefaultWebhookClient{
		client: &http.Client{
			Timeout: timeout,
		},
		timeout: timeout,
	}
}

// POST makes an HTTP POST request with form data
func (c *DefaultWebhookClient) POST(ctx context.Context, targetURL string, form url.Values) (status int, body []byte, headers http.Header, err error) {
	req, err := http.NewRequestWithContext(ctx, "POST", targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Twimulator/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, resp.Header, fmt.Errorf("failed to read response body: %w", err)
	}

	return resp.StatusCode, body, resp.Header, nil
}

// MockWebhookClient is a test double for capturing webhook calls
type MockWebhookClient struct {
	Calls []MockCall
	// ResponseFunc allows tests to control responses
	ResponseFunc func(url string, form url.Values) (status int, body []byte, headers http.Header, err error)
}

// MockCall records a webhook call
type MockCall struct {
	URL     string
	Form    url.Values
	Time    time.Time
	Context context.Context
}

// NewMockWebhookClient creates a new mock client
func NewMockWebhookClient() *MockWebhookClient {
	return &MockWebhookClient{
		Calls: make([]MockCall, 0),
		ResponseFunc: func(url string, form url.Values) (int, []byte, http.Header, error) {
			// Default: return empty TwiML response
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		},
	}
}

// POST records the call and returns the configured response
func (m *MockWebhookClient) POST(ctx context.Context, targetURL string, form url.Values) (status int, body []byte, headers http.Header, err error) {
	m.Calls = append(m.Calls, MockCall{
		URL:     targetURL,
		Form:    form,
		Time:    time.Now(),
		Context: ctx,
	})

	if m.ResponseFunc != nil {
		return m.ResponseFunc(targetURL, form)
	}

	return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
}

// Reset clears all recorded calls
func (m *MockWebhookClient) Reset() {
	m.Calls = make([]MockCall, 0)
}

// GetCallsTo returns all calls to a specific URL
func (m *MockWebhookClient) GetCallsTo(url string) []MockCall {
	var result []MockCall
	for _, call := range m.Calls {
		if call.URL == url {
			result = append(result, call)
		}
	}
	return result
}
