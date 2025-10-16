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

	"twimulator/console"
	"twimulator/engine"
)

func main() {
	// Create engine with auto clock for real-time operation
	e := engine.NewEngine(engine.WithAutoClock())
	defer e.Close()

	// Start a test HTTP server that serves TwiML
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

	// Gather demo handler
	mux.HandleFunc("/voice/gather", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Gather call from %s", r.FormValue("From"))
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="10" numDigits="1" action="http://localhost:8088/voice/gather-result">
    <Say>Press 1 for sales, 2 for support, or 3 for billing.</Say>
  </Gather>
  <Say>We did not receive your selection. Goodbye.</Say>
  <Hangup/>
</Response>`)
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

	// Status callback handler
	mux.HandleFunc("/voice/status", func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Status callback: CallSid=%s Status=%s", r.FormValue("CallSid"), r.FormValue("CallStatus"))
		w.WriteHeader(http.StatusOK)
	})

	testSrv := httptest.NewServer(mux)
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
	log.Println("Creating demo scenario...")
	log.Println("")

	// Scenario: Queue and Conference demo
	go func() {
		time.Sleep(500 * time.Millisecond)

		log.Println("1. Creating inbound customer call...")
		call1, _ := e.CreateCall(engine.CreateCallParams{
			From:           "+15551234001",
			To:             "+18005551000",
			AnswerURL:      testSrv.URL + "/voice/inbound",
			StatusCallback: testSrv.URL + "/voice/status",
		})
		log.Printf("   Created call %s\n", call1.SID)

		time.Sleep(2 * time.Second)

		log.Println("2. Creating agent call to handle queue...")
		call2, _ := e.CreateCall(engine.CreateCallParams{
			From:      "+15551234002",
			To:        "+18005551001",
			AnswerURL: testSrv.URL + "/voice/agent",
		})
		log.Printf("   Created call %s\n", call2.SID)

		time.Sleep(3 * time.Second)

		log.Println("3. Creating conference calls...")
		for i := 1; i <= 3; i++ {
			from := fmt.Sprintf("+1555123400%d", i+2)
			call, _ := e.CreateCall(engine.CreateCallParams{
				From:      from,
				To:        "+18005551002",
				AnswerURL: testSrv.URL + "/voice/conference",
			})
			log.Printf("   Created conference call %s\n", call.SID)
			time.Sleep(500 * time.Millisecond)
		}

		time.Sleep(2 * time.Second)

		log.Println("4. Creating gather demo call...")
		call3, _ := e.CreateCall(engine.CreateCallParams{
			From:      "+15551234099",
			To:        "+18005551003",
			AnswerURL: testSrv.URL + "/voice/gather",
		})
		log.Printf("   Created call %s\n", call3.SID)

		time.Sleep(1 * time.Second)

		log.Println("5. Simulating digit press (2 for support)...")
		e.SendDigits(call3.SID, "2")

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
