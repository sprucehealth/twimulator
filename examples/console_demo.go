package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"time"

	openapi "github.com/twilio/twilio-go/rest/api/v2010"

	"twimulator/console"
	"twimulator/engine"
	"twimulator/model"
)

func main() {
	// Create engine with auto clock for real-time operation
	e := engine.NewEngine(engine.WithAutoClock())
	defer e.Close()

	// Create a subaccount for our demo
	accountParams := (&openapi.CreateAccountParams{}).SetFriendlyName("Demo SubAccount")
	account, err := e.CreateAccount(accountParams)
	if err != nil {
		log.Fatalf("Failed to create subaccount: %v", err)
	}
	if account.Sid == nil {
		log.Fatalf("CreateAccount returned no SID")
	}
	subAccountSID := model.SID(*account.Sid)
	snap := e.Snapshot()
	subAccount, ok := snap.SubAccounts[subAccountSID]
	if !ok {
		log.Fatalf("Subaccount %s not found after creation", subAccountSID)
	}

	provisionNumber := func(phone string) {
		params := (&openapi.CreateIncomingPhoneNumberParams{}).
			SetPathAccountSid(string(subAccountSID)).
			SetPhoneNumber(phone)
		if _, err := e.CreateIncomingPhoneNumber(params); err != nil {
			log.Fatalf("Failed to provision number %s: %v", phone, err)
		}
	}

	for _, phone := range []string{
		"+15551234001",
		"+15551234002",
		"+15551234003",
		"+15551234004",
		"+15551234005",
		"+15551234099",
	} {
		provisionNumber(phone)
	}
	log.Printf("Created subaccount: %s (%s)", subAccount.FriendlyName, subAccount.SID)

	// Start a test HTTP server that serves TwiML
	mux := http.NewServeMux()

	// Create the test server first so we can reference its URL
	testSrv := httptest.NewServer(mux)
	defer testSrv.Close()

	// Inbound call handler - enqueues caller
	mux.HandleFunc("/voice/inbound", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Inbound call from %s to %s", r.FormValue("From"), r.FormValue("To"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Welcome to support. Please hold while we connect you.</Say>
  <Enqueue>support</Enqueue>
</Response>`)
	})

	// Agent handler - dials queue
	mux.HandleFunc("/voice/agent", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Agent call from %s", r.FormValue("From"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Connecting you to the next caller.</Say>
  <Dial><Queue>support</Queue></Dial>
</Response>`)
	})

	// Conference demo handler
	mux.HandleFunc("/voice/conference", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Conference call from %s", r.FormValue("From"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Joining the conference room.</Say>
  <Dial><Conference>demo-room</Conference></Dial>
</Response>`)
	})

	// Gather demo handler
	mux.HandleFunc("/voice/gather", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Gather call from %s", r.FormValue("From"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="10" numDigits="1" action="%s/voice/gather-result">
    <Say>Press 1 for sales, 2 for support, or 3 for billing.</Say>
  </Gather>
  <Say>We did not receive your selection. Goodbye.</Say>
  <Hangup/>
</Response>`, testSrv.URL)
	})

	mux.HandleFunc("/voice/gather-result", func(w http.ResponseWriter, r *http.Request) {
		digits := r.FormValue("Digits")
		log.Printf("Gathered digits: %s", digits)
		w.Header().Set("Content-Type", "text/xml")

		var message string
		switch digits {
		case "1":
			message = "You selected sales."
		case "2":
			message = "You selected support."
		case "3":
			message = "You selected billing."
		default:
			message = "Invalid selection."
		}

		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>%s Thank you for calling. Goodbye.</Say>
  <Hangup/>
</Response>`, message)
	})

	// Record demo handler
	mux.HandleFunc("/voice/record", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Record call from %s", r.FormValue("From"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Please leave a message after the beep. Press the pound key when finished.</Say>
  <Record maxLength="30" playBeep="true" action="%s/voice/record-done" timeout="5" transcribe="true"/>
</Response>`, testSrv.URL)
	})

	mux.HandleFunc("/voice/record-done", func(w http.ResponseWriter, r *http.Request) {
		recordingSid := r.FormValue("RecordingSid")
		recordingUrl := r.FormValue("RecordingUrl")
		recordingStatus := r.FormValue("RecordingStatus")
		recordingDuration := r.FormValue("RecordingDuration")

		log.Printf("Recording completed: SID=%s, URL=%s, Status=%s, Duration=%s seconds",
			recordingSid, recordingUrl, recordingStatus, recordingDuration)

		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Thank you for your message. Goodbye.</Say>
  <Hangup/>
</Response>`)
	})

	// Status callback handler
	mux.HandleFunc("/voice/status", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Status callback: CallSid=%s Status=%s", r.FormValue("CallSid"), r.FormValue("CallStatus"))
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("Test TwiML server running at %s", testSrv.URL)

	// Start console server
	cs, err := console.NewConsoleServer(e, ":8089")
	if err != nil {
		log.Fatalf("Failed to create console server: %v", err)
	}

	go func() {
		if err := cs.Start(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Console server error: %v", err)
		}
	}()

	// Give the server a moment to start
	time.Sleep(100 * time.Millisecond)

	log.Println("=== Twimulator Demo ===")
	log.Println("Console UI: http://localhost:8089")
	log.Println("")
	log.Println("Creating demo scenario...")
	log.Println("")

	createCall := func(params *openapi.CreateCallParams) *model.Call {
		apiCall, err := e.CreateCall(params)
		if err != nil {
			log.Fatalf("Failed to create call: %v", err)
		}
		if apiCall.Sid == nil {
			log.Fatalf("CreateCall did not return SID")
		}
		sid := model.SID(*apiCall.Sid)
		call, ok := e.GetCallState(sid)
		if !ok {
			log.Fatalf("Call %s not found after creation", sid)
		}
		return call
	}

	newCallParams := func(from, to, url string) *openapi.CreateCallParams {
		params := &openapi.CreateCallParams{}
		params.SetPathAccountSid(string(subAccount.SID))
		if from != "" {
			params.SetFrom(from)
		}
		if to != "" {
			params.SetTo(to)
		}
		params.SetUrl(url)
		return params
	}

	// Scenario: Queue and Conference demo
	go func() {
		time.Sleep(500 * time.Millisecond)

		log.Println("1. Creating inbound customer call...")
		params1 := newCallParams("+15551234001", "+18005551000", testSrv.URL+"/voice/inbound")
		params1.SetStatusCallback(testSrv.URL + "/voice/status")
		call1 := createCall(params1)
		log.Printf("   Created call %s\n", call1.SID)

		time.Sleep(2 * time.Second)
		e.AnswerCall(call1.SID)
		log.Printf("   Answered call %s\n", call1.SID)
		time.Sleep(2 * time.Second)
		log.Println("2. Creating agent call to handle queue...")
		call2 := createCall(newCallParams("+15551234002", "+18005551001", testSrv.URL+"/voice/agent"))
		log.Printf("   Created call %s\n", call2.SID)
		time.Sleep(3 * time.Second)
		e.AnswerCall(call2.SID)
		log.Printf("   Answered call %s\n", call2.SID)
		time.Sleep(2 * time.Second)

		log.Println("3. Creating conference calls...")
		for i := 1; i <= 3; i++ {
			from := fmt.Sprintf("+1555123400%d", i+2)
			call := createCall(newCallParams(from, "+18005551002", testSrv.URL+"/voice/conference"))
			log.Printf("   Created conference call %s\n", call.SID)
			time.Sleep(500 * time.Millisecond)
			e.AnswerCall(call.SID)
			log.Printf("   Answered call %s\n", call.SID)
		}

		time.Sleep(2 * time.Second)

		log.Println("4. Creating gather demo call...")
		call3 := createCall(newCallParams("+15551234099", "+18005551003", testSrv.URL+"/voice/gather"))
		log.Printf("   Created call %s\n", call3.SID)

		time.Sleep(1 * time.Second)
		e.AnswerCall(call3.SID)
		log.Printf("   Answered call %s\n", call3.SID)
		time.Sleep(2 * time.Second)
		log.Println("5. Simulating digit press (2 for support)...")
		e.SendDigits(call3.SID, "2")
		time.Sleep(2 * time.Second)

		log.Println("6. Creating voicemail/record demo call...")
		call4 := createCall(newCallParams("+15551234005", "+18005551004", testSrv.URL+"/voice/record"))
		log.Printf("   Created call %s\n", call4.SID)
		time.Sleep(1 * time.Second)
		e.AnswerCall(call4.SID)
		log.Printf("   Answered call %s\n", call4.SID)
		time.Sleep(3 * time.Second)
		log.Println("   Simulating caller leaving message (waiting for timeout)...")
		time.Sleep(6 * time.Second)
		log.Printf("   Recording completed for call %s\n", call4.SID)

		log.Println("")
		log.Println("=== Demo scenario complete! ===")
		log.Println("Visit http://localhost:8089 to explore the console")
		log.Println("Press Ctrl+C to exit")
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs.Stop(ctx)
}
