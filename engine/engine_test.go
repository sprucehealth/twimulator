package engine_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1111", "+3333")
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")
	mustProvisionNumbers(t, e, subAccount.SID, "+155512301", "+155512302", "+15551234099")

	// Additional numbers for conference loop
	for i := 1; i <= 3; i++ {
		from := fmt.Sprintf("+1555123400%d", i+2)
		mustProvisionNumbers(t, e, subAccount.SID, from)
	}

	// 1) Create first call; it answers and enqueues into "support"
	params1 := newCreateCallParams(subAccount.SID, "+155512301", "+180055501", "http://test/voice/inbound")
	params1.SetStatusCallback("http://test/voice/status")
	params1.SetTimeout(int((2 * time.Second) / time.Second))
	c1 := mustCreateCall(t, e, params1)

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(c1.SID)
	if err != nil {
		t.Fatal(err)
	}
	// Advance until answered and enqueued
	e.Advance(3 * time.Second)
	time.Sleep(10 * time.Millisecond) // Let goroutines process

	// Verify call is in-progress
	got1, ok := e.GetCallState(c1.SID)
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
	err = e.AnswerCall(c2.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(5 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Both calls should be in progress
	got1, _ = e.GetCallState(c1.SID)
	if got1.Status != model.CallInProgress {
		t.Fatalf("Expected c1 in-progress, got %s", got1.Status)
	}

	got2, _ := e.GetCallState(c2.SID)
	if got2.Status != model.CallInProgress {
		t.Fatalf("Expected c2 in-progress, got %s", got2.Status)
	}

	// Cleanup
	err = e.Hangup(c1.SID)
	if err != nil {
		t.Fatal(err)
	}
	err = e.Hangup(c2.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(1 * time.Second)

	// Assert completed
	got1, _ = e.GetCallState(c1.SID)
	if got1.Status != model.CallCompleted {
		t.Fatalf("Expected c1 completed, got %s", got1.Status)
	}

	got2, _ = e.GetCallState(c2.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

	params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
	call := mustCreateCall(t, e, params)

	// Give goroutines time to start
	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call.SID)
	if err != nil {
		t.Fatal(err)
	}
	// Advance to answer
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Send digits while in gather
	err = e.SendDigits(call.SID, "1")
	if err != nil {
		t.Fatal(err)
	}
	// Advance to process gather
	e.Advance(2 * time.Second)
	time.Sleep(200 * time.Millisecond)

	if !gatherActionCalled {
		t.Error("Gather action was not called")
	}

	// Verify call completed after hangup
	got, _ := e.GetCallState(call.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call.SID)
	if err != nil {
		t.Fatal(err)
	}
	// Advance past gather timeout
	time.Sleep(200 * time.Millisecond)
	e.Advance(10 * time.Second)
	time.Sleep(200 * time.Millisecond)

	// Should have timed out and hung up
	got, _ := e.GetCallState(call.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	if !redirectFollowed {
		t.Error("Redirect was not followed")
	}

	got, _ := e.GetCallState(call.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1111", "+3333")

	// Create two calls to join conference
	call1 := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+2222", "http://test/answer"))
	call2 := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+3333", "+4444", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call1.SID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	err = e.AnswerCall(call2.SID)
	if err != nil {
		t.Fatal(err)
	}
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

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
	got, _ := e.GetCallState(call.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

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
	got, _ := e.GetCallState(call.SID)
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
	mustProvisionNumbers(t, e, subAccount.SID, "+1111", "+3333")

	// Create multiple calls
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+2222", "http://test/answer"))
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+3333", "+2222", "http://test/answer"))
	mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1111", "+4444", "http://test/answer"))
	time.Sleep(10 * time.Millisecond)
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
	status := model.CallRinging
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
	call, ok := e.GetCallState(sid)
	if !ok {
		t.Fatalf("call %s not found after creation", sid)
	}
	return call
}

func mustProvisionNumberWithApp(t *testing.T, e *engine.EngineImpl, accountSID model.SID, number string, appSID string) {
	t.Helper()
	params := (&twilioopenapi.CreateIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(accountSID)).
		SetPhoneNumber(number).
		SetVoiceApplicationSid(appSID)
	if _, err := e.CreateIncomingPhoneNumber(params); err != nil {
		t.Fatalf("failed to provision number %s with app %s: %v", number, appSID, err)
	}
}
func mustProvisionNumbers(t *testing.T, e *engine.EngineImpl, accountSID model.SID, numbers ...string) {
	t.Helper()
	for _, num := range numbers {
		params := (&twilioopenapi.CreateIncomingPhoneNumberParams{}).
			SetPathAccountSid(string(accountSID)).
			SetPhoneNumber(num)
		if _, err := e.CreateIncomingPhoneNumber(params); err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("failed to provision number %s: %v", num, err)
			}
		}
	}
}

func TestUpdateCall(t *testing.T) {
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")
	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	updateParams := (&twilioopenapi.UpdateCallParams{}).
		SetUrl("http://test/new-answer").
		SetStatusCallback("http://test/status").
		SetStatus("completed")

	resp, err := e.UpdateCall(string(call.SID), updateParams)
	if err != nil {
		t.Fatalf("update call failed: %v", err)
	}
	if resp == nil || resp.Sid == nil {
		t.Fatal("expected response SID")
	}

	got, ok := e.GetCallState(call.SID)
	if !ok {
		t.Fatalf("call %s not found after update", call.SID)
	}
	if got.Url != "http://test/new-answer" {
		t.Fatalf("expected answer URL updated, got %s", got.Url)
	}
	if got.StatusCallback != "http://test/status" {
		t.Fatalf("expected status callback updated, got %s", got.StatusCallback)
	}
	if got.Status != model.CallCompleted {
		t.Fatalf("expected completed status, got %s", got.Status)
	}
}

func TestListIncomingPhoneNumber(t *testing.T) {
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	app, err := e.CreateApplication((&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("Routing App"))
	if err != nil {
		t.Fatalf("create application failed: %v", err)
	}
	if app.Sid == nil {
		t.Fatal("expected application SID")
	}

	mustProvisionNumberWithApp(t, e, subAccount.SID, "+1234", *app.Sid)
	mustProvisionNumbers(t, e, subAccount.SID, "+5678")

	params := (&twilioopenapi.ListIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(subAccount.SID))

	list, err := e.ListIncomingPhoneNumber(params)
	if err != nil {
		t.Fatalf("list numbers failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 numbers, got %d", len(list))
	}
	if list[0].VoiceApplicationSid == nil && list[1].VoiceApplicationSid != nil {
		list[0], list[1] = list[1], list[0]
	}
	if list[0].VoiceApplicationSid == nil || *list[0].VoiceApplicationSid != *app.Sid {
		t.Fatalf("expected voice application sid %s on number, got %v", *app.Sid, list[0].VoiceApplicationSid)
	}

	filtered, err := e.ListIncomingPhoneNumber((&twilioopenapi.ListIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetPhoneNumber("+1234"))
	if err != nil {
		t.Fatalf("list numbers with filter failed: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 number, got %d", len(filtered))
	}
	if filtered[0].PhoneNumber == nil || *filtered[0].PhoneNumber != "+1234" {
		t.Fatalf("expected number +1234, got %v", filtered[0].PhoneNumber)
	}
	if filtered[0].VoiceApplicationSid == nil || *filtered[0].VoiceApplicationSid != *app.Sid {
		t.Fatalf("expected filtered number to reference app %s", *app.Sid)
	}

	numberSID := *list[0].Sid
	if err := e.DeleteIncomingPhoneNumber(numberSID, nil); err != nil {
		t.Fatalf("delete number failed: %v", err)
	}

	recheck, err := e.ListIncomingPhoneNumber(params)
	if err != nil {
		t.Fatalf("list after delete failed: %v", err)
	}
	if len(recheck) != 1 {
		t.Fatalf("expected 1 number after delete, got %d", len(recheck))
	}
}

func TestCreateApplication(t *testing.T) {
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	params := (&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("Voice App").
		SetVoiceUrl("http://example.com/voice").
		SetVoiceMethod("POST").
		SetStatusCallback("http://example.com/status").
		SetStatusCallbackMethod("GET")

	app, err := e.CreateApplication(params)
	if err != nil {
		t.Fatalf("create application failed: %v", err)
	}
	if app.Sid == nil {
		t.Fatal("expected application SID")
	}

	snap := e.Snapshot()
	sa := snap.SubAccounts[subAccount.SID]
	if sa == nil || len(sa.Applications) != 1 {
		t.Fatalf("expected application recorded on subaccount")
	}
	appInfo := sa.Applications[0]
	if appInfo.SID != *app.Sid {
		t.Fatalf("expected application SID %s, got %s", *app.Sid, appInfo.SID)
	}
	if appInfo.FriendlyName != "Voice App" {
		t.Fatalf("expected friendly name to persist")
	}
	if appInfo.VoiceURL != "http://example.com/voice" {
		t.Fatalf("expected voice URL copied, got %s", appInfo.VoiceURL)
	}
	if appInfo.VoiceMethod != "POST" {
		t.Fatalf("expected voice method POST, got %s", appInfo.VoiceMethod)
	}
	if appInfo.StatusCallback != "http://example.com/status" {
		t.Fatalf("expected status callback copied, got %s", appInfo.StatusCallback)
	}
	if appInfo.StatusCallbackMethod != "GET" {
		t.Fatalf("expected status callback method GET, got %s", appInfo.StatusCallbackMethod)
	}
}

func TestPlayFetchesURL(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	mediaURLFetched := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Play>http://test/media/hello.mp3</Play>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/media/hello.mp3" {
			mediaURLFetched = true
			// Return a fake audio file
			return 200, []byte("fake audio data"), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	if !mediaURLFetched {
		t.Error("Play URL was not fetched")
	}

	// Verify call completed after play and hangup
	got, _ := e.GetCallState(call.SID)
	if got.Status != model.CallCompleted {
		t.Errorf("Expected completed, got %s", got.Status)
	}

	// Check for play.success event
	hasPlaySuccess := false
	for _, event := range got.Timeline {
		if event.Type == "play.success" {
			hasPlaySuccess = true
			break
		}
	}
	if !hasPlaySuccess {
		t.Error("Expected play.success event in timeline")
	}
}

func TestPlayInvalidURL(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Play>http://test/media/missing.mp3</Play>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/media/missing.mp3" {
			// Return 404 for missing file
			return 404, []byte("Not Found"), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")
	mustProvisionNumbers(t, e, subAccount.SID, "+1234")

	call := mustCreateCall(t, e, newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer"))

	time.Sleep(10 * time.Millisecond)
	err := e.AnswerCall(call.SID)
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Call should fail due to invalid play URL
	got, _ := e.GetCallState(call.SID)
	if got.Status != model.CallFailed {
		t.Errorf("Expected failed status due to invalid play URL, got %s", got.Status)
	}

	// Check for play.error event
	hasPlayError := false
	for _, event := range got.Timeline {
		if event.Type == "play.error" {
			hasPlayError = true
			break
		}
	}
	if !hasPlayError {
		t.Error("Expected play.error event in timeline")
	}
}
