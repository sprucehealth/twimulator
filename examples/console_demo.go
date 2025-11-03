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

	"github.com/sprucehealth/twimulator/console"
	"github.com/sprucehealth/twimulator/engine"
	"github.com/sprucehealth/twimulator/model"
)

type subAccountInfo struct {
	SID          model.SID
	FriendlyName string
	Account      *model.SubAccount
}

func main() {
	// Create engine with auto clock for real-time operation
	e := engine.NewEngine(engine.WithAutoClock())
	defer e.Close()

	// Create two subaccounts for our demo
	subAccounts := make([]subAccountInfo, 2)

	for i := 0; i < 2; i++ {
		accountName := fmt.Sprintf("Demo SubAccount %d", i+1)
		accountParams := (&openapi.CreateAccountParams{}).SetFriendlyName(accountName)
		account, err := e.CreateAccount(accountParams)
		if err != nil {
			log.Fatalf("Failed to create subaccount %d: %v", i+1, err)
		}
		if account.Sid == nil {
			log.Fatalf("CreateAccount returned no SID for account %d", i+1)
		}
		subAccountSID := model.SID(*account.Sid)
		snap, err := e.Snapshot(subAccountSID)
		if err != nil {
			log.Fatalf("Failed to get snapshot for account %d: %v", i+1, err)
		}
		subAccount, ok := snap.SubAccounts[subAccountSID]
		if !ok {
			log.Fatalf("Subaccount %s not found after creation", subAccountSID)
		}

		// Provision phone numbers for this subaccount
		baseNumber := 1000 + (i * 100)
		provisionNumber := func(phone string) {
			params := (&openapi.CreateIncomingPhoneNumberParams{}).
				SetPathAccountSid(string(subAccountSID)).
				SetPhoneNumber(phone)
			if _, err := e.CreateIncomingPhoneNumber(params); err != nil {
				log.Fatalf("Failed to provision number %s: %v", phone, err)
			}
		}

		for j := 1; j <= 6; j++ {
			phone := fmt.Sprintf("+15551%06d", baseNumber+j)
			provisionNumber(phone)
		}

		subAccounts[i] = subAccountInfo{
			SID:          subAccountSID,
			FriendlyName: accountName,
			Account:      subAccount,
		}
		log.Printf("Created subaccount %d: %s (%s)", i+1, subAccount.FriendlyName, subAccount.SID)
	}

	// Start test HTTP server (shared by both subaccounts)
	testSrv := createTwiMLServer()
	defer testSrv.Close()
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
	log.Println("Creating demo scenarios for both subaccounts...")
	log.Println("")

	// Run scenarios for both subaccounts
	for i := 0; i < 2; i++ {
		accountIdx := i
		go runScenario(e, subAccounts[accountIdx], testSrv, accountIdx+1)
	}

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cs.Stop(ctx)
}

func createTwiMLServer() *httptest.Server {
	mux := http.NewServeMux()

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

	testSrv := httptest.NewServer(mux)

	// Gather demo handler (needs server URL)
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

	return testSrv
}

func runScenario(e *engine.EngineImpl, account subAccountInfo, testSrv *httptest.Server, accountNum int) {
	prefix := fmt.Sprintf("[Account %d]", accountNum)

	// Stagger start times for each account
	time.Sleep(time.Duration(accountNum-1) * 500 * time.Millisecond)

	createCall := func(params *openapi.CreateCallParams) *model.Call {
		params.SetPathAccountSid(string(account.SID))
		apiCall, err := e.CreateCall(params)
		if err != nil {
			log.Fatalf("%s Failed to create call: %v", prefix, err)
		}
		if apiCall.Sid == nil {
			log.Fatalf("%s CreateCall did not return SID", prefix)
		}
		sid := model.SID(*apiCall.Sid)
		call, ok := e.GetCallState(account.SID, sid)
		if !ok {
			log.Fatalf("%s Call %s not found after creation", prefix, sid)
		}
		return call
	}

	newCallParams := func(from, to, url string) *openapi.CreateCallParams {
		params := &openapi.CreateCallParams{}
		params.SetPathAccountSid(string(account.SID))
		if from != "" {
			params.SetFrom(from)
		}
		if to != "" {
			params.SetTo(to)
		}
		params.SetUrl(url)
		return params
	}

	time.Sleep(500 * time.Millisecond)

	log.Printf("%s 1. Creating inbound customer call...", prefix)
	baseNumber := 1000 + ((accountNum - 1) * 100)
	params1 := newCallParams(fmt.Sprintf("+15551%06d", baseNumber+1), "+18005551000", testSrv.URL+"/voice/inbound")
	params1.SetStatusCallback(testSrv.URL + "/voice/status")
	call1 := createCall(params1)
	log.Printf("%s    Created call %s", prefix, call1.SID)

	time.Sleep(2 * time.Second)
	e.AnswerCall(account.SID, call1.SID)
	log.Printf("%s    Answered call %s", prefix, call1.SID)

	time.Sleep(2 * time.Second)
	log.Printf("%s 2. Creating agent call to handle queue...", prefix)
	call2 := createCall(newCallParams(fmt.Sprintf("+15551%06d", baseNumber+2), "+18005551001", testSrv.URL+"/voice/agent"))
	log.Printf("%s    Created call %s", prefix, call2.SID)
	time.Sleep(3 * time.Second)
	e.AnswerCall(account.SID, call2.SID)
	log.Printf("%s    Answered call %s", prefix, call2.SID)

	time.Sleep(2 * time.Second)

	log.Printf("%s 3. Creating conference calls...", prefix)
	for i := 1; i <= 3; i++ {
		from := fmt.Sprintf("+15551%06d", baseNumber+2+i)
		call := createCall(newCallParams(from, "+18005551002", testSrv.URL+"/voice/conference"))
		log.Printf("%s    Created conference call %s", prefix, call.SID)
		time.Sleep(500 * time.Millisecond)
		e.AnswerCall(account.SID, call.SID)
		log.Printf("%s    Answered call %s", prefix, call.SID)
	}

	time.Sleep(2 * time.Second)

	log.Printf("%s 4. Creating gather demo call...", prefix)
	call3 := createCall(newCallParams(fmt.Sprintf("+15551%06d", baseNumber+6), "+18005551003", testSrv.URL+"/voice/gather"))
	log.Printf("%s    Created call %s", prefix, call3.SID)

	time.Sleep(1 * time.Second)
	e.AnswerCall(account.SID, call3.SID)
	log.Printf("%s    Answered call %s", prefix, call3.SID)
	time.Sleep(2 * time.Second)
	log.Printf("%s 5. Simulating digit press (2 for support)...", prefix)
	e.SendDigits(account.SID, call3.SID, "2")
	time.Sleep(2 * time.Second)

	log.Printf("%s 6. Creating voicemail/record demo call...", prefix)
	call4 := createCall(newCallParams(fmt.Sprintf("+15551%06d", baseNumber+5), "+18005551004", testSrv.URL+"/voice/record"))
	log.Printf("%s    Created call %s", prefix, call4.SID)
	time.Sleep(1 * time.Second)
	e.AnswerCall(account.SID, call4.SID)
	log.Printf("%s    Answered call %s", prefix, call4.SID)
	time.Sleep(3 * time.Second)
	log.Printf("%s    Simulating caller leaving message (waiting for timeout)...", prefix)
	time.Sleep(6 * time.Second)
	log.Printf("%s    Recording completed for call %s", prefix, call4.SID)

	log.Printf("%s === Scenario complete! ===", prefix)
	if accountNum == 2 {
		log.Println("")
		log.Println("=== All demo scenarios complete! ===")
		log.Println("Visit http://localhost:8089 to explore the console")
		log.Println("Press Ctrl+C to exit")
	}
}
