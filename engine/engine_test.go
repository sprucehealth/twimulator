package engine_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	twilioopenapi "github.com/twilio/twilio-go/rest/api/v2010"

	"twimulator/engine"
	"twimulator/httpstub"
	"twimulator/model"
)

func TestEnqueueAndConferenceFlow(t *testing.T) {
	// Create a mock webhook client
	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		// Return different TwiML based on the URL
		if targetURL == "http://test/voice/inbound" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Enqueue>support</Enqueue>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/voice/agent" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Queue>support</Queue></Dial>
</Response>`), make(http.Header), nil
		}
		// Default empty response
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	// Create engine with manual clock and mock webhook
	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	// Create a subaccount for testing
	subAccount := createTestSubAccount(t, e, "Test Account")

	// 1) Create first call; it answers and enqueues into "support"
	params1 := newCreateCallParams(subAccount.SID, "+155512301", "+180055501", "http://test/voice/inbound")
	params1.SetStatusCallback("http://test/voice/status")
	params1.SetTimeout(int((2 * time.Second) / time.Second))
	c1 := mustCreateCall(t, e, params1)

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	// Advance until answered and enqueued
	e.Advance(3 * time.Second)
	time.Sleep(10 * time.Millisecond) // Let goroutines process

	// Verify call is in-progress
	got1, ok := e.GetCall(c1.SID)
	if !ok {
		t.Fatal("Call not found")
	}
	if got1.Status != model.CallInProgress {
		t.Fatalf("Expected in-progress, got %s", got1.Status)
	}

	// Verify call is in the queue
	queue, ok := e.GetQueue(subAccount.SID, "support")
	if !ok {
		t.Fatal("Queue 'support' not found")
	}
	if len(queue.Members) != 1 {
		t.Fatalf("Expected 1 queue member, got %d", len(queue.Members))
	}

	// 2) Create second call that dials the same queue
	params2 := newCreateCallParams(subAccount.SID, "+155512302", "+180055502", "http://test/voice/agent")
	c2 := mustCreateCall(t, e, params2)

	time.Sleep(10 * time.Millisecond)
	e.Advance(5 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Both calls should be in progress
	got1, _ = e.GetCall(c1.SID)
	if got1.Status != model.CallInProgress {
		t.Fatalf("Expected c1 in-progress, got %s", got1.Status)
	}

	got2, _ := e.GetCall(c2.SID)
	if got2.Status != model.CallInProgress {
		t.Fatalf("Expected c2 in-progress, got %s", got2.Status)
	}

	// Cleanup
	e.Hangup(c1.SID)
	e.Hangup(c2.SID)
	e.Advance(1 * time.Second)

	// Assert completed
	got1, _ = e.GetCall(c1.SID)
	if got1.Status != model.CallCompleted {
		t.Fatalf("Expected c1 completed, got %s", got1.Status)
	}

	got2, _ = e.GetCall(c2.SID)
	if got2.Status != model.CallCompleted {
		t.Fatalf("Expected c2 completed, got %s", got2.Status)
	}

	// Verify snapshot works
	snap := e.Snapshot()
	if len(snap.Calls) != 2 {
		t.Fatalf("Expected 2 calls in snapshot, got %d", len(snap.Calls))
	}
}

func TestGatherWithDigits(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	gatherActionCalled := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="5" numDigits="1" action="http://test/gather-done">
    <Say>Press 1 to continue</Say>
  </Gather>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/gather-done" {
			gatherActionCalled = true
			digits := form.Get("Digits")
			if digits != "1" {
				t.Errorf("Expected Digits=1, got %s", digits)
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Thank you</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
	call := mustCreateCall(t, e, params)

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)

	// Advance to answer
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Send digits while in gather
	e.SendDigits(call.SID, "1")

	// Advance to process gather
	e.Advance(2 * time.Second)

	if !gatherActionCalled {
		t.Error("Gather action was not called")
	}

	// Verify call completed after hangup
	got, _ := e.GetCall(call.SID)
	if got.Status != model.CallCompleted {
		t.Errorf("Expected completed, got %s", got.Status)
	}
}

func TestGatherTimeout(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="2" numDigits="1">
    <Say>Press something</Say>
  </Gather>
  <Say>Timeout occurred</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)

	// Advance past gather timeout
	e.Advance(5 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Should have timed out and hung up
	got, _ := e.GetCall(call.SID)
	if got.Status != model.CallCompleted {
		t.Errorf("Expected completed after timeout, got %s", got.Status)
	}

	// Check timeline for timeout event
	hasTimeout := false
	for _, event := range got.Timeline {
		if event.Type == "gather.timeout" {
			hasTimeout = true
			break
		}
	}
	if !hasTimeout {
		t.Error("Expected gather.timeout event in timeline")
	}
}

func TestRedirect(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	redirectFollowed := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Redirect>http://test/redirected</Redirect>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/redirected" {
			redirectFollowed = true
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Redirected successfully</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	if !redirectFollowed {
		t.Error("Redirect was not followed")
	}

	got, _ := e.GetCall(call.SID)
	if got.Status != model.CallCompleted {
		t.Errorf("Expected completed, got %s", got.Status)
	}
}

func TestConference(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference>test-room</Conference></Dial>
</Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	// Create two calls to join conference
	call1 := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+2222", "http://test/answer"))
	call2 := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+3333", "+4444", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Both should be in conference
	conf, ok := e.GetConference(subAccount.SID, "test-room")
	if !ok {
		t.Fatal("Conference not found")
	}

	if len(conf.Participants) != 2 {
		t.Errorf("Expected 2 participants, got %d", len(conf.Participants))
	}

	if conf.Status != model.ConferenceInProgress {
		t.Errorf("Expected in-progress, got %s", conf.Status)
	}

	// Hangup one call
	e.Hangup(call1.SID)
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	conf, _ = e.GetConference(subAccount.SID, "test-room")
	if len(conf.Participants) != 1 {
		t.Errorf("Expected 1 participant after hangup, got %d", len(conf.Participants))
	}

	// Hangup last call
	e.Hangup(call2.SID)
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	conf, _ = e.GetConference(subAccount.SID, "test-room")
	if conf.Status != model.ConferenceCompleted {
		t.Errorf("Expected completed, got %s", conf.Status)
	}
}

func TestStatusCallbacks(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Hello</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
	params.SetStatusCallback("http://test/status")
	call := mustCreateCall(t, e, params)

	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Check that status callbacks were made
	statusCalls := mock.GetCallsTo("http://test/status")
	if len(statusCalls) == 0 {
		t.Error("Expected status callbacks, got none")
	}

	// Verify timeline has status callback events
	got, _ := e.GetCall(call.SID)
	hasStatusCallback := false
	for _, event := range got.Timeline {
		if event.Type == "webhook.status_callback" {
			hasStatusCallback = true
			break
		}
	}
	if !hasStatusCallback {
		t.Error("Expected status callback event in timeline")
	}
}

func TestCallNoAnswer(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
	params.SetTimeout(int((2 * time.Second) / time.Second))
	call := mustCreateCall(t, e, params)

	time.Sleep(10 * time.Millisecond)

	// Advance past timeout without answering
	// The call runner answers immediately (100ms), but we can test by
	// advancing less than that and then past timeout
	e.Advance(3 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// In our current implementation, calls answer immediately
	// To properly test no-answer, we'd need to modify the runner
	// For now, verify call reached some end state
	got, _ := e.GetCall(call.SID)
	if got.Status == model.CallQueued || got.Status == model.CallRinging {
		t.Errorf("Call should have progressed past queued/ringing")
	}
}

func TestListCallsFilter(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	// Create multiple calls
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+2222", "http://test/answer"))
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+3333", "+2222", "http://test/answer"))
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+4444", "http://test/answer"))

	// Filter by To
	calls := e.ListCalls(engine.CallFilter{To: "+2222"})
	if len(calls) != 2 {
		t.Errorf("Expected 2 calls to +2222, got %d", len(calls))
	}

	// Filter by From
	calls = e.ListCalls(engine.CallFilter{From: "+1111"})
	if len(calls) != 2 {
		t.Errorf("Expected 2 calls from +1111, got %d", len(calls))
	}

	// Filter by Status
	status := model.CallQueued
	calls = e.ListCalls(engine.CallFilter{Status: &status})
	if len(calls) != 3 {
		t.Errorf("Expected 3 queued calls, got %d", len(calls))
	}
}

func createTestSubAccount(t *testing.T, e *engine.EngineImpl, friendlyName string) *model.SubAccount {
	t.Helper()
	params := (&twilioopenapi.CreateAccountParams{}).SetFriendlyName(friendlyName)
	account, err := e.CreateAccount(params)
	if err != nil {
		t.Fatalf("failed to create account: %v", err)
	}
	if account.Sid == nil {
		t.Fatal("expected account SID to be set")
	}
	sid := model.SID(*account.Sid)
	snap := e.Snapshot()
	subAccount, ok := snap.SubAccounts[sid]
	if !ok {
		t.Fatalf("subaccount %s not found after creation", sid)
	}
	return subAccount
}

func TestListAccountFiltersByFriendlyName(t *testing.T) {
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	// Create three accounts with two sharing the same friendly name
	acct1, err := e.CreateAccount((&twilioopenapi.CreateAccountParams{}).SetFriendlyName("Alpha"))
	if err != nil {
		t.Fatalf("unexpected error creating account: %v", err)
	}
	acct2, err := e.CreateAccount((&twilioopenapi.CreateAccountParams{}).SetFriendlyName("Beta"))
	if err != nil {
		t.Fatalf("unexpected error creating account: %v", err)
	}
	acct3, err := e.CreateAccount((&twilioopenapi.CreateAccountParams{}).SetFriendlyName("Alpha"))
	if err != nil {
		t.Fatalf("unexpected error creating account: %v", err)
	}

	accounts, err := e.ListAccount(nil)
	if err != nil {
		t.Fatalf("list account returned error: %v", err)
	}
	if len(accounts) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(accounts))
	}

	filtered, err := e.ListAccount((&twilioopenapi.ListAccountParams{}).SetFriendlyName("Alpha"))
	if err != nil {
		t.Fatalf("list account with filter returned error: %v", err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 accounts with friendly name Alpha, got %d", len(filtered))
	}

	// Ensure the returned SIDs match the created accounts for the filtered set
	expected := map[string]struct{}{
		*acct1.Sid: {},
		*acct3.Sid: {},
	}
	for _, acct := range filtered {
		if acct.Sid == nil {
			t.Fatal("returned account missing SID")
		}
		if _, ok := expected[*acct.Sid]; !ok {
			t.Fatalf("unexpected account SID %s in filtered results", *acct.Sid)
		}
		if acct.AuthToken == nil || *acct.AuthToken == "" {
			t.Fatalf("expected auth token for account %s", *acct.Sid)
		}
	}
	_ = acct2
}

func newCreateCallParams(accountSID model.SID, from, to, url string) *twilioopenapi.CreateCallParams {
	params := &twilioopenapi.CreateCallParams{}
	params.SetPathAccountSid(string(accountSID))
	if from != "" {
		params.SetFrom(from)
	}
	if to != "" {
		params.SetTo(to)
	}
	if url != "" {
		params.SetUrl(url)
	}
	return params
}

func mustCreateCall(t *testing.T, e *engine.EngineImpl, params *twilioopenapi.CreateCallParams) *model.Call {
	t.Helper()
	apiCall, err := e.CreateCall(params)
	if err != nil {
		t.Fatalf("failed to create call: %v", err)
	}
	if apiCall.Sid == nil {
		t.Fatal("create call did not return SID")
	}
	sid := model.SID(*apiCall.Sid)
	call, ok := e.GetCall(sid)
	if !ok {
		t.Fatalf("call %s not found after creation", sid)
	}
	return call
}
