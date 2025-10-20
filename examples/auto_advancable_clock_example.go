package main

import (
	"fmt"
	"log"
	"time"

	openapi "github.com/twilio/twilio-go/rest/api/v2010"

	"twimulator/engine"
)

// This example demonstrates AutoAdvancableClock which combines:
// - Real-time progression (like AutoClock)
// - Manual time advancement capability (like ManualClock)
//
// Use cases:
// - Demos where calls execute naturally but you want to skip waiting periods
// - Development where you want natural execution but need to test timeouts quickly
// - Testing scenarios that need both automatic and controlled time progression

func main() {
	// Create engine with AutoAdvancableClock
	e := engine.NewEngine(engine.WithAutoAdvancableClock())
	defer e.Close()

	// Create a subaccount
	accountParams := (&openapi.CreateAccountParams{}).SetFriendlyName("Demo Account")
	account, err := e.CreateAccount(accountParams)
	if err != nil {
		log.Fatalf("Failed to create account: %v", err)
	}
	accountSID := *account.Sid

	// Provision a number
	numberParams := (&openapi.CreateIncomingPhoneNumberParams{}).
		SetPathAccountSid(accountSID).
		SetPhoneNumber("+15551234567")
	_, err = e.CreateIncomingPhoneNumber(numberParams)
	if err != nil {
		log.Fatalf("Failed to provision number: %v", err)
	}

	fmt.Println("=== AutoAdvancableClock Demo ===")
	fmt.Println()

	// Record start time
	start := e.Clock().Now()
	fmt.Printf("Start time: %s\n", start.Format(time.RFC3339))

	// Let 1 second of real time pass
	fmt.Println("\nWaiting 1 second (real time)...")
	time.Sleep(1 * time.Second)

	after1s := e.Clock().Now()
	fmt.Printf("After 1s real time: %s (elapsed: %v)\n",
		after1s.Format(time.RFC3339),
		after1s.Sub(start))

	// Now advance manually by 1 hour
	fmt.Println("\nAdvancing clock manually by 1 hour...")
	e.Advance(1 * time.Hour)

	afterAdvance := e.Clock().Now()
	fmt.Printf("After advancing 1 hour: %s (elapsed: %v)\n",
		afterAdvance.Format(time.RFC3339),
		afterAdvance.Sub(start))

	// Real time continues to pass
	fmt.Println("\nWaiting another 500ms (real time)...")
	time.Sleep(500 * time.Millisecond)

	final := e.Clock().Now()
	fmt.Printf("Final time: %s (total elapsed: %v)\n",
		final.Format(time.RFC3339),
		final.Sub(start))

	fmt.Println()
	fmt.Println("Notice how the clock progresses naturally with real time,")
	fmt.Println("but you can also manually advance it to skip ahead!")
}
