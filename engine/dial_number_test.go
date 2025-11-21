// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

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

func TestDialNumberParallel(t *testing.T) {
	urlCallCount := 0

	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			// Parent call dials multiple numbers in parallel
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="10">
    <Number statusCallback="http://test/status1" statusCallbackEvent="answered completed">+15551111111</Number>
    <Number statusCallback="http://test/status2" url="http://test/child-url">+15552222222</Number>
    <Number>+15553333333</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/status1" || targetURL == "http://test/status2" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/child-url" {
			urlCallCount++
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Child call answered</Say>
</Response>`), make(http.Header), nil
		}
		// Default empty response for child calls
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	// Provision the parent number
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000")
	// Provision the numbers that will be dialed
	mustProvisionNumbers(t, e, subAccount.SID, "+15551111111", "+15552222222", "+15553333333")

	// Create parent call
	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	// Advance to let child calls be created
	e.Advance(1 * time.Second)
	time.Sleep(500 * time.Millisecond)

	// Get all calls for this account to find child calls
	snap, err := e.Snapshot(subAccount.SID)
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}

	// Find child calls (should be 3) - they have the same From as parent, but different SIDs
	childCalls := make([]*model.Call, 0)
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID && call.From == parentCall.From {
			childCalls = append(childCalls, call)
		}
	}

	if len(childCalls) < 3 {
		t.Logf("Parent call: SID=%s, From=%s, To=%s, Status=%s", parentCall.SID, parentCall.From, parentCall.To, parentCall.Status)
		for _, call := range snap.Calls {
			t.Logf("Call: SID=%s, From=%s, To=%s, Status=%s", call.SID, call.From, call.To, call.Status)
		}
		// Check parent call timeline for errors
		parentState, _ := e.GetCallState(subAccount.SID, parentCall.SID)
		for _, event := range parentState.Timeline {
			if event.Type == "error" || event.Type == "dial.create_call_failed" {
				t.Logf("Error event: %s - %v", event.Type, event.Detail)
			}
		}
		t.Fatalf("Expected at least 3 child calls to be created, got %d", len(childCalls))
	}

	// Answer the second child call (+15552222222)
	var answeredCall *model.Call
	for _, call := range childCalls {
		if call.To == "+15552222222" {
			answeredCall = call
			break
		}
	}

	if answeredCall == nil {
		t.Fatal("Could not find child call to +15552222222")
	}

	// Give child calls time to fetch their initial TwiML
	e.Advance(500 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	if err := e.AnswerCall(subAccount.SID, answeredCall.SID); err != nil {
		t.Fatal(err)
	}

	// Advance to let the bridge happen and TwiML to execute
	e.Advance(1 * time.Second)
	time.Sleep(200 * time.Millisecond)

	// Additional advance to let hangups complete
	e.Advance(1 * time.Second)
	time.Sleep(300 * time.Millisecond)

	// Verify the URL callback was called for the answered child
	if urlCallCount == 0 {
		t.Error("Expected child URL to be called when child answered")
	}

	// Verify other child calls were hung up (canceled is acceptable)
	snap, _ = e.Snapshot(subAccount.SID)
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID && call.SID != answeredCall.SID {
			// Other child calls should be completed/hung up/canceled
			if call.Status != model.CallCompleted && call.Status != model.CallBusy && call.Status != model.CallFailed && call.Status != model.CallCanceled {
				t.Errorf("Expected non-answered child call %s to be completed/canceled, got %s", call.SID, call.Status)
			}
		}
	}

	// Verify parent is in-progress (bridged)
	parentState, _ := e.GetCallState(subAccount.SID, parentCall.SID)
	if parentState.Status != model.CallInProgress {
		t.Errorf("Expected parent call to be in-progress after bridge, got %s", parentState.Status)
	}
}

func TestDialNumberTimeout(t *testing.T) {
	actionCallbackCalled := false

	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="2" action="http://test/dial-action">
    <Number>+15551111111</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/dial-action" {
			actionCallbackCalled = true
			// Check for no-answer status
			if form.Get("DialCallStatus") != "no-answer" {
				t.Errorf("Expected DialCallStatus=no-answer, got %s", form.Get("DialCallStatus"))
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response><Say>No answer</Say></Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000", "+15551111111")

	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	// Let child call be created first
	e.Advance(500 * time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	// Advance past dial timeout without answering child
	e.Advance(3 * time.Second)
	time.Sleep(500 * time.Millisecond)

	if !actionCallbackCalled {
		t.Error("Dial action callback was not called after timeout")
	}

	// Verify parent call has dial.no_answer event
	parentState, _ := e.GetCallState(subAccount.SID, parentCall.SID)
	hasNoAnswer := false
	for _, event := range parentState.Timeline {
		if event.Type == "dial.no_answer" {
			hasNoAnswer = true
			break
		}
	}
	if !hasNoAnswer {
		t.Error("Expected dial.no_answer event in parent call timeline")
	}
}

func TestDialNumberHangupOnStar(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="10" hangupOnStar="true">
    <Number>+15551111111</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000", "+15551111111")

	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(100 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	// Find and answer child call
	snap, _ := e.Snapshot(subAccount.SID)
	var childCall *model.Call
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID {
			childCall = call
			break
		}
	}

	if childCall != nil {
		if err := e.AnswerCall(subAccount.SID, childCall.SID); err != nil {
			t.Fatal(err)
		}

		e.Advance(100 * time.Millisecond)
		time.Sleep(50 * time.Millisecond)

		// Parent presses * to hang up
		err := e.SendDigits(subAccount.SID, parentCall.SID, "*")
		if err != nil {
			t.Fatalf("failed to send digits: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		// Verify hangup on star event
		parentState, _ := e.GetCallState(subAccount.SID, parentCall.SID)
		hasHangupOnStar := false
		for _, event := range parentState.Timeline {
			if event.Type == "dial.hangup_on_star" {
				hasHangupOnStar = true
				break
			}
		}
		if !hasHangupOnStar {
			t.Error("Expected dial.hangup_on_star event in parent call timeline")
		}
	}
}

func TestDialNumberParentHangup(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="10">
    <Number>+15551111111</Number>
    <Number>+15552222222</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000", "+15551111111", "+15552222222")

	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(100 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	// Hangup parent before any child answers
	err := e.Hangup(subAccount.SID, parentCall.SID)
	if err != nil {
		t.Fatalf("failed to hangup parent: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify all child calls are completed or canceled
	snap, _ := e.Snapshot(subAccount.SID)
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID {
			if call.Status != model.CallCompleted && call.Status != model.CallBusy && call.Status != model.CallFailed && call.Status != model.CallCanceled {
				t.Errorf("Expected child call %s to be completed/canceled after parent hangup, got %s", call.SID, call.Status)
			}
		}
	}
}

func TestDialNumberStatusCallback(t *testing.T) {
	statusCallbackCalls := make(map[string][]string) // URL -> list of events

	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="10">
    <Number statusCallback="http://test/status" statusCallbackEvent="initiated ringing answered completed">+15551111111</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/status" {
			callStatus := form.Get("CallStatus")
			if callStatus != "" {
				statusCallbackCalls[targetURL] = append(statusCallbackCalls[targetURL], callStatus)
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000", "+15551111111")

	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(500 * time.Millisecond)
	time.Sleep(500 * time.Millisecond)

	// Find child call
	snap, _ := e.Snapshot(subAccount.SID)
	var childCall *model.Call
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID {
			childCall = call
			break
		}
	}

	if childCall == nil {
		t.Fatal("Could not find child call")
	}

	// Answer child call
	if err := e.AnswerCall(subAccount.SID, childCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(1 * time.Second)
	time.Sleep(500 * time.Millisecond)

	// Hangup to complete the call
	if err := e.Hangup(subAccount.SID, childCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(500 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Verify status callbacks were called
	events := statusCallbackCalls["http://test/status"]
	if len(events) == 0 {
		t.Error("Expected status callbacks to be called")
	} else {
		t.Logf("Status callback events: %v", events)
		// Should have received events like initiated, ringing, answered, completed
		hasAnswered := false
		for _, event := range events {
			if event == "in-progress" || event == "answered" {
				hasAnswered = true
				break
			}
		}
		if !hasAnswered {
			t.Error("Expected to receive answered/in-progress status callback")
		}
	}
}

func TestDialNumberURL(t *testing.T) {
	urlCallCount := 0

	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/parent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial timeout="10">
    <Number url="http://test/child-twiml">+15551111111</Number>
  </Dial>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/child-twiml" {
			urlCallCount++
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>This is the child call</Say>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Dial Test")
	mustProvisionNumbers(t, e, subAccount.SID, "+15550000000", "+15551111111")

	parentCall := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+15550000000", "+19999999999", "http://test/parent"))

	time.Sleep(10 * time.Millisecond)
	if err := e.AnswerCall(subAccount.SID, parentCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(500 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	// Find child call
	snap, _ := e.Snapshot(subAccount.SID)
	var childCall *model.Call
	for _, call := range snap.Calls {
		if call.SID != parentCall.SID {
			childCall = call
			break
		}
	}

	if childCall == nil {
		t.Fatal("Could not find child call")
	}

	// Answer child call
	if err := e.AnswerCall(subAccount.SID, childCall.SID); err != nil {
		t.Fatal(err)
	}

	e.Advance(1 * time.Second)
	time.Sleep(300 * time.Millisecond)

	// Verify the URL was called to provide TwiML for the child
	if urlCallCount == 0 {
		t.Error("Expected child URL to be called when child call was created/answered")
	} else {
		t.Logf("Child URL was called %d time(s)", urlCallCount)
	}
}
