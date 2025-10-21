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

func TestCreateIncomingCall(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://example.com/voice" {
			// Verify request parameters for incoming call
			from := form.Get("From")
			to := form.Get("To")
			direction := form.Get("Direction")

			if from != "+15551234567" {
				t.Errorf("Expected From=+15551234567, got %s", from)
			}
			if to != "+15559999999" {
				t.Errorf("Expected To=+15559999999, got %s", to)
			}
			if direction != "inbound" {
				t.Errorf("Expected Direction=inbound, got %s", direction)
			}

			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Welcome to our service</Say>
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

	// Create subaccount
	subAccount := createTestSubAccount(t, e, "Test Account")

	// Create application with VoiceURL and StatusCallback
	appParams := (&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("Inbound Voice App").
		SetVoiceUrl("http://example.com/voice").
		SetVoiceMethod("POST").
		SetStatusCallback("http://example.com/status").
		SetStatusCallbackMethod("POST")

	app, err := e.CreateApplication(appParams)
	if err != nil {
		t.Fatalf("create application failed: %v", err)
	}
	if app.Sid == nil {
		t.Fatal("expected application SID")
	}

	// Provision a number with the application
	numberParams := (&twilioopenapi.CreateIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetPhoneNumber("+15559999999").
		SetVoiceApplicationSid(*app.Sid)

	_, err = e.CreateIncomingPhoneNumber(numberParams)
	if err != nil {
		t.Fatalf("create incoming phone number failed: %v", err)
	}

	// Create an incoming call to the provisioned number
	apiCall, err := e.CreateIncomingCall(subAccount.SID, "+15551234567", "+15559999999")
	if err != nil {
		t.Fatalf("create incoming call failed: %v", err)
	}

	if apiCall.Sid == nil {
		t.Fatal("expected call SID")
	}

	callSID := model.SID(*apiCall.Sid)
	call, ok := e.GetCallState(subAccount.SID, callSID)
	if !ok {
		t.Fatalf("call %s not found after creation", callSID)
	}

	// Verify call properties
	if call.Direction != model.Inbound {
		t.Errorf("Expected direction inbound, got %s", call.Direction)
	}
	if call.From != "+15551234567" {
		t.Errorf("Expected from +15551234567, got %s", call.From)
	}
	if call.To != "+15559999999" {
		t.Errorf("Expected to +15559999999, got %s", call.To)
	}
	if call.Url != "http://example.com/voice" {
		t.Errorf("Expected URL from application, got %s", call.Url)
	}
	if call.StatusCallback != "http://example.com/status" {
		t.Errorf("Expected StatusCallback from application, got %s", call.StatusCallback)
	}

	// Answer the call and let it execute
	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Verify call completed
	call, _ = e.GetCallState(subAccount.SID, callSID)
	if call.Status != model.CallCompleted {
		t.Errorf("Expected call completed, got %s", call.Status)
	}

	// Verify timeline has expected events
	hasCreated := false
	for _, event := range call.Timeline {
		if event.Type == "call.created" {
			hasCreated = true
			// Verify application info in event detail
			// The application SID is stored as model.SID type in the detail
			if appSID, ok := event.Detail["application"].(model.SID); !ok || string(appSID) != *app.Sid {
				t.Errorf("Expected application %s in event detail, got %v (type %T)", *app.Sid, event.Detail["application"], event.Detail["application"])
			}
			break
		}
	}
	if !hasCreated {
		t.Error("Expected call.created event in timeline")
	}
}

func TestCreateIncomingCallValidation(t *testing.T) {
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	// Test: non-existent subaccount
	_, err := e.CreateIncomingCall("ACFAKE00000000000000000000000000", "+1111", "+2222")
	if err == nil {
		t.Error("Expected error for non-existent subaccount")
	}

	// Test: number not provisioned
	_, err = e.CreateIncomingCall(subAccount.SID, "+1111", "+2222")
	if err == nil {
		t.Error("Expected error for non-provisioned number")
	}

	// Provision number without application
	mustProvisionNumbers(t, e, subAccount.SID, "+15559999999")

	// Test: number without application
	_, err = e.CreateIncomingCall(subAccount.SID, "+1111", "+15559999999")
	if err == nil {
		t.Error("Expected error for number without application")
	}

	// Create application without VoiceURL
	app, err := e.CreateApplication((&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("No Voice URL App"))
	if err != nil {
		t.Fatalf("create application failed: %v", err)
	}

	// Update number to have application
	numberParams := (&twilioopenapi.CreateIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetPhoneNumber("+15558888888").
		SetVoiceApplicationSid(*app.Sid)
	_, err = e.CreateIncomingPhoneNumber(numberParams)
	if err != nil {
		t.Fatalf("create incoming phone number failed: %v", err)
	}

	// Test: application without VoiceURL
	_, err = e.CreateIncomingCall(subAccount.SID, "+1111", "+15558888888")
	if err == nil {
		t.Error("Expected error for application without VoiceURL")
	}

	// Create application with VoiceURL
	app2, err := e.CreateApplication((&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("Valid App").
		SetVoiceUrl("http://example.com/voice"))
	if err != nil {
		t.Fatalf("create application 2 failed: %v", err)
	}

	// Provision number with valid application
	numberParams2 := (&twilioopenapi.CreateIncomingPhoneNumberParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetPhoneNumber("+15557777777").
		SetVoiceApplicationSid(*app2.Sid)
	_, err = e.CreateIncomingPhoneNumber(numberParams2)
	if err != nil {
		t.Fatalf("create incoming phone number 2 failed: %v", err)
	}

	// Test: successful call creation
	apiCall, err := e.CreateIncomingCall(subAccount.SID, "+15551234567", "+15557777777")
	if err != nil {
		t.Errorf("Expected successful call creation, got error: %v", err)
	}
	if apiCall == nil || apiCall.Sid == nil {
		t.Error("Expected valid call response")
	}
}

func TestCreateIncomingCallWithStatusCallback(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	statusCallbackCalled := false

	mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
		if targetURL == "http://example.com/voice" {
			return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Hello</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
		}
		if targetURL == "http://example.com/status" {
			statusCallbackCalled = true
			return 200, []byte("OK"), make(http.Header), nil
		}
		return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?><Response></Response>`), make(http.Header), nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	subAccount := createTestSubAccount(t, e, "Test Account")

	// Create application with StatusCallback
	app, err := e.CreateApplication((&twilioopenapi.CreateApplicationParams{}).
		SetPathAccountSid(string(subAccount.SID)).
		SetFriendlyName("App with Status").
		SetVoiceUrl("http://example.com/voice").
		SetStatusCallback("http://example.com/status"))
	if err != nil {
		t.Fatalf("create application failed: %v", err)
	}

	// Provision number
	mustProvisionNumberWithApp(t, e, subAccount.SID, "+15559999999", *app.Sid)

	// Create incoming call
	apiCall, err := e.CreateIncomingCall(subAccount.SID, "+15551234567", "+15559999999")
	if err != nil {
		t.Fatalf("create incoming call failed: %v", err)
	}

	callSID := model.SID(*apiCall.Sid)

	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Verify status callback was called
	if !statusCallbackCalled {
		t.Error("Expected status callback to be called")
	}

	// Verify call has status callback events in timeline
	call, _ := e.GetCallState(subAccount.SID, callSID)
	hasStatusCallback := false
	for _, event := range call.Timeline {
		if event.Type == "webhook.status_callback" {
			hasStatusCallback = true
			break
		}
	}
	if !hasStatusCallback {
		t.Error("Expected status callback event in timeline")
	}
}
