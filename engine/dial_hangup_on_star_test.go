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

func TestDialHangupOnStar(t *testing.T) {
	t.Run("Dial number with hangupOnStar=true", func(t *testing.T) {
		mock := httpstub.NewMockWebhookClient()

		mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
			if targetURL == "http://test/answer" {
				return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial hangupOnStar="true">+15551234567</Dial>
</Response>`), make(http.Header), nil
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}

		e := engine.NewEngine(
			engine.WithWebhookClient(mock),
			engine.WithManualClock(),
		)
		defer e.Close()

		subAccount := createTestSubAccount(t, e, "Test Account")
		mustProvisionNumbers(t, e, subAccount.SID, "+1234", "+5678")

		params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
		call := mustCreateCall(t, e, params)

		// Answer the call
		time.Sleep(10 * time.Millisecond)
		err := e.AnswerCall(subAccount.SID, call.SID)
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(10 * time.Millisecond)

		// Verify call is in progress
		got, ok := e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallInProgress {
			t.Fatalf("Expected call in progress, got %s", got.Status)
		}

		// Send star digit to trigger hangup
		err = e.SendDigits(subAccount.SID, call.SID, "*")
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(100 * time.Millisecond)

		// Verify call is now completed
		got, ok = e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallCompleted {
			t.Errorf("Expected call completed after star digit, got %s", got.Status)
		}

		// Verify hangup_on_star event in timeline
		hasHangupOnStar := false
		for _, event := range got.Timeline {
			if event.Type == "dial.hangup_on_star" {
				hasHangupOnStar = true
				if digits, ok := event.Detail["digits"].(string); ok {
					if digits != "*" {
						t.Errorf("Expected digits '*', got %q", digits)
					}
				}
				break
			}
		}
		if !hasHangupOnStar {
			t.Error("Expected dial.hangup_on_star event in timeline")
		}
	})

	t.Run("Dial number with hangupOnStar=false should ignore star", func(t *testing.T) {
		mock := httpstub.NewMockWebhookClient()

		mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
			if targetURL == "http://test/answer" {
				return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial hangupOnStar="false" timeout="5">+15551234567</Dial>
</Response>`), make(http.Header), nil
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}

		e := engine.NewEngine(
			engine.WithWebhookClient(mock),
			engine.WithManualClock(),
		)
		defer e.Close()

		subAccount := createTestSubAccount(t, e, "Test Account")
		mustProvisionNumbers(t, e, subAccount.SID, "+1234", "+5678")

		params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
		call := mustCreateCall(t, e, params)

		// Answer the call
		time.Sleep(10 * time.Millisecond)
		err := e.AnswerCall(subAccount.SID, call.SID)
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(10 * time.Millisecond)

		// Send star digit - should be ignored
		err = e.SendDigits(subAccount.SID, call.SID, "*")
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(100 * time.Millisecond)

		// Verify call is still in progress (not hung up by star)
		got, ok := e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallInProgress {
			t.Errorf("Expected call still in progress, got %s", got.Status)
		}

		// Verify NO hangup_on_star event in timeline
		for _, event := range got.Timeline {
			if event.Type == "dial.hangup_on_star" {
				t.Error("Did not expect dial.hangup_on_star event when hangupOnStar=false")
			}
		}
	})

	t.Run("Dial conference with hangupOnStar=true", func(t *testing.T) {
		mock := httpstub.NewMockWebhookClient()

		mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
			if targetURL == "http://test/answer" {
				return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial hangupOnStar="true"><Conference>test-room</Conference></Dial>
</Response>`), make(http.Header), nil
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}

		e := engine.NewEngine(
			engine.WithWebhookClient(mock),
			engine.WithManualClock(),
		)
		defer e.Close()

		subAccount := createTestSubAccount(t, e, "Test Account")
		mustProvisionNumbers(t, e, subAccount.SID, "+1234", "+5678")

		params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
		call := mustCreateCall(t, e, params)

		// Answer the call
		time.Sleep(10 * time.Millisecond)
		err := e.AnswerCall(subAccount.SID, call.SID)
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(10 * time.Millisecond)

		// Send star digit to trigger hangup
		err = e.SendDigits(subAccount.SID, call.SID, "*")
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(100 * time.Millisecond)

		// Verify call is now completed
		got, ok := e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallCompleted {
			t.Errorf("Expected call completed after star digit, got %s", got.Status)
		}
	})

	t.Run("Dial with hangupOnStar=true and mixed digits", func(t *testing.T) {
		mock := httpstub.NewMockWebhookClient()

		mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
			if targetURL == "http://test/answer" {
				return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial hangupOnStar="true">+15551234567</Dial>
</Response>`), make(http.Header), nil
			}
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
		}

		e := engine.NewEngine(
			engine.WithWebhookClient(mock),
			engine.WithManualClock(),
		)
		defer e.Close()

		subAccount := createTestSubAccount(t, e, "Test Account")
		mustProvisionNumbers(t, e, subAccount.SID, "+1234", "+5678")

		params := newCreateCallParams(subAccount.SID, "+1234", "+5678", "http://test/answer")
		call := mustCreateCall(t, e, params)

		// Answer the call
		time.Sleep(10 * time.Millisecond)
		err := e.AnswerCall(subAccount.SID, call.SID)
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(10 * time.Millisecond)

		// Send some digits first
		err = e.SendDigits(subAccount.SID, call.SID, "1")
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(100 * time.Millisecond)
		time.Sleep(10 * time.Millisecond)

		// Call should still be in progress
		got, ok := e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallInProgress {
			t.Fatalf("Expected call still in progress, got %s", got.Status)
		}

		// Now send star along with other digits
		err = e.SendDigits(subAccount.SID, call.SID, "23*45")
		if err != nil {
			t.Fatal(err)
		}
		e.Advance(1 * time.Second)
		time.Sleep(100 * time.Millisecond)

		// Verify call is now completed (star should trigger hangup)
		got, ok = e.GetCallState(subAccount.SID, call.SID)
		if !ok {
			t.Fatal("Call not found")
		}
		if got.Status != model.CallCompleted {
			t.Errorf("Expected call completed after star in mixed digits, got %s", got.Status)
		}
	})
}
