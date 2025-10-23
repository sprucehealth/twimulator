package engine_test

import (
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"twimulator/engine"
	"twimulator/httpstub"
	"twimulator/twiml"
)

// TestTwiMLSimpleComparison demonstrates the simplest way to compare TwiML:
// Create an expected slice and compare it in one go with reflect.DeepEqual
func TestTwiMLSimpleComparison(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="alice">Hello world</Say>
  <Pause length="2"/>
  <Play>http://test/media/welcome.mp3</Play>
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

	// Get call state
	got, ok := e.GetCallState(subAccount.SID, call.SID)
	if !ok {
		t.Fatal("Call not found")
	}

	// Advance more to ensure Play completes
	e.Advance(3 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Re-fetch call state after more time
	got, ok = e.GetCallState(subAccount.SID, call.SID)
	if !ok {
		t.Fatal("Call not found")
	}

	// Define expected TwiML sequence - that's it!
	expected := []any{
		&twiml.Say{
			Text:     "Hello world",
			Voice:    "alice",
			Language: "",
		},
		&twiml.Pause{
			Length: 2 * time.Second,
		},
		&twiml.Play{
			URL: "http://test/media/welcome.mp3",
		},
	}

	// One-line comparison of the entire ExecutedTwiML slice!
	if !reflect.DeepEqual(got.ExecutedTwiML, expected) {
		t.Errorf("ExecutedTwiML mismatch:\nGot:  %#v\nWant: %#v",
			got.ExecutedTwiML, expected)
	} else {
		t.Log("✓ Successfully compared entire ExecutedTwiML slice in one line!")
	}
}

// TestTwiMLWithGatherSimpleComparison shows comparing complex TwiML with nested children
func TestTwiMLWithGatherSimpleComparison(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://test/answer" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Welcome</Say>
  <Gather input="dtmf" timeout="5" numDigits="1" action="http://test/gather">
    <Say>Press 1 for sales</Say>
    <Say>Press 2 for support</Say>
  </Gather>
  <Say>Goodbye</Say>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://test/gather" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Hangup/>
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

	// Send digits to trigger gather action
	err = e.SendDigits(subAccount.SID, call.SID, "1")
	if err != nil {
		t.Fatal(err)
	}
	e.Advance(2 * time.Second)
	time.Sleep(200 * time.Millisecond)

	// Get call state
	got, ok := e.GetCallState(subAccount.SID, call.SID)
	if !ok {
		t.Fatal("Call not found")
	}

	// Define expected TwiML with nested children
	// Note: Nested children are ALSO tracked individually when executed
	// Also note: "Goodbye" is NOT executed because gathering digits triggers
	// the action callback, which jumps to new TwiML
	expected := []any{
		&twiml.Say{Text: "Welcome", Voice: "", Language: ""},
		&twiml.Gather{
			Input:     "dtmf",
			Timeout:   "5",
			NumDigits: 1,
			Action:    "http://test/gather",
			Method:    "POST",
			Children: []twiml.Node{
				&twiml.Say{Text: "Press 1 for sales", Voice: "", Language: ""},
				&twiml.Say{Text: "Press 2 for support", Voice: "", Language: ""},
			},
		},
		// The nested Say verbs are also tracked individually:
		&twiml.Say{Text: "Press 1 for sales", Voice: "", Language: ""},
		&twiml.Say{Text: "Press 2 for support", Voice: "", Language: ""},
		// Note: "Goodbye" is skipped because digits were entered
		&twiml.Hangup{}, // From the gather action callback
	}

	// One-line comparison - works with nested structures too!
	if !reflect.DeepEqual(got.ExecutedTwiML, expected) {
		t.Errorf("ExecutedTwiML mismatch:\nGot:  %#v\nWant: %#v",
			got.ExecutedTwiML, expected)

		// Optional: print each item for easier debugging
		t.Logf("Got %d items, want %d items", len(got.ExecutedTwiML), len(expected))
		for i := 0; i < len(got.ExecutedTwiML) || i < len(expected); i++ {
			var g, w any
			if i < len(got.ExecutedTwiML) {
				g = got.ExecutedTwiML[i]
			}
			if i < len(expected) {
				w = expected[i]
			}
			if !reflect.DeepEqual(g, w) {
				t.Logf("  [%d] Got:  %#v", i, g)
				t.Logf("  [%d] Want: %#v", i, w)
			}
		}
	}

	t.Log("✓ Successfully compared complex TwiML with nested children!")
}
