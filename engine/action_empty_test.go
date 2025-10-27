package engine_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sprucehealth/twimulator/engine"
	"github.com/sprucehealth/twimulator/httpstub"
	"github.com/sprucehealth/twimulator/model"
)

// TestGatherActionEmptyResponse verifies that when a Gather action callback
// returns an empty TwiML response, the call is hung up
func TestGatherActionEmptyResponse(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	actionCalled := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="5" numDigits="1" action="http://test/gather-action">
    <Say>Press 1</Say>
  </Gather>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/gather-action" {
			actionCalled = true
			// Return empty TwiML response - this should cause hangup
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}
		return 404, []byte("Not found"), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithWebhookClient(mock),
		engine.WithManualClock(),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	mustProvisionNumbers(t, e, subAccount.SID, "+15551234567", "+15559999999")

	params := newCreateCallParams(subAccount.SID, "+15551234567", "+15559999999", "http://test/answer")
	call := mustCreateCall(t, e, params)

	// Give call time to start and answer
	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(subAccount.SID, call.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Send digits to trigger the gather action
	err = e.SendDigits(subAccount.SID, call.SID, "1")
	if err != nil {
		t.Fatalf("Failed to send digits: %v", err)
	}

	// Advance time to allow action callback to be processed
	e.Advance(1 * time.Second)
	time.Sleep(50 * time.Millisecond)

	// Verify the action was called
	if !actionCalled {
		t.Error("Expected gather action to be called")
	}

	// Verify call is completed (hung up due to empty TwiML)
	got, ok := e.GetCallState(subAccount.SID, call.SID)
	if !ok {
		t.Fatal("Call not found")
	}

	if got.Status != model.CallCompleted {
		t.Errorf("Expected call to be completed after empty action response, got status: %s", got.Status)
	}

	// Verify there's an action.empty_response event in the timeline
	hasEmptyResponseEvent := false
	for _, event := range got.Timeline {
		if event.Type == "action.empty_response" {
			hasEmptyResponseEvent = true
			break
		}
	}
	if !hasEmptyResponseEvent {
		t.Error("Expected action.empty_response event in timeline")
	}
}

// TestRecordActionEmptyResponse verifies that when a Record action callback
// returns an empty TwiML response, the call is hung up
func TestRecordActionEmptyResponse(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	actionCalled := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Record timeout="1" maxLength="5" action="http://test/record-action"/>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/record-action" {
			actionCalled = true
			// Return empty TwiML response - this should cause hangup
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}
		return 404, []byte("Not found"), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithWebhookClient(mock),
		engine.WithManualClock(),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	mustProvisionNumbers(t, e, subAccount.SID, "+15551234567", "+15559999999")

	params := newCreateCallParams(subAccount.SID, "+15551234567", "+15559999999", "http://test/answer")
	call := mustCreateCall(t, e, params)

	// Give call time to start and answer
	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(subAccount.SID, call.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Advance past the record max length to trigger action
	e.Advance(6 * time.Second)
	time.Sleep(50 * time.Millisecond)

	// Verify the action was called
	if !actionCalled {
		t.Error("Expected record action to be called")
	}

	// Verify call is completed (hung up due to empty TwiML)
	got, ok := e.GetCallState(subAccount.SID, call.SID)
	if !ok {
		t.Fatal("Call not found")
	}

	if got.Status != model.CallCompleted {
		t.Errorf("Expected call to be completed after empty action response, got status: %s", got.Status)
	}

	// Verify there's an action.empty_response event in the timeline
	hasEmptyResponseEvent := false
	for _, event := range got.Timeline {
		if event.Type == "action.empty_response" {
			hasEmptyResponseEvent = true
			break
		}
	}
	if !hasEmptyResponseEvent {
		t.Error("Expected action.empty_response event in timeline")
	}
}
