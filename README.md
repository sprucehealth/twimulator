# Twimulator

**Twimulator** is a Go library that simulates Twilio Voice for integration testing. It executes TwiML, drives webhooks, and provides inspectable state—all without placing real calls or incurring costs.

Think of it as a **TwiML executor + webhook driver** that mimics the parts of Twilio Voice your application relies on during testing.

## Features

- **TwiML Execution**: Simulates `<Say>`, `<Play>`, `<Pause>`, `<Gather>`, `<Dial>`, `<Enqueue>`, `<Conference>`, `<Redirect>`, and `<Hangup>`
- **Webhook Integration**: Automatically POSTs to your app's answer URLs and status callbacks with Twilio-like parameters
- **State Inspection**: Export full state as JSON for assertions (calls, queues, conferences, timelines)
- **Deterministic Time Control**: Manual clock with `Advance()` for precise, fast, repeatable tests
- **Web Console**: Optional embedded HTTP UI showing call timelines, TwiML execution, and webhook logs
- **Zero External Dependencies**: Uses only Go standard library (plus a minimal router for console)

## Installation

```bash
go get github.com/yourusername/twimulator
```

## Quick Start

### Basic Usage

```go
package main

import (
	"time"
	"twimulator/engine"
)

func main() {
	// Create engine with manual clock for testing
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	// Create a call
	call, _ := e.CreateCall(engine.CreateCallParams{
		From:      "+15551234567",
		To:        "+18005551234",
		AnswerURL: "http://localhost:8080/voice",
		Timeout:   30 * time.Second,
	})

	// Advance time to trigger call execution
	e.Advance(5 * time.Second)

	// Inspect call state
	got, _ := e.GetCall(call.SID)
	println("Call status:", string(got.Status))
}
```

### With httptest.Server

```go
func TestVoiceFlow(t *testing.T) {
	// Setup your app's HTTP handler
	mux := http.NewServeMux()
	mux.HandleFunc("/voice", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Hello from test!</Say>
  <Hangup/>
</Response>`)
	})

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Create engine
	e := engine.NewEngine(engine.WithManualClock())
	defer e.Close()

	// Make a call to your test server
	call, _ := e.CreateCall(engine.CreateCallParams{
		From:      "+1234",
		To:        "+5678",
		AnswerURL: testServer.URL + "/voice",
	})

	// Advance time
	time.Sleep(10 * time.Millisecond) // Let goroutines start
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond) // Let them process

	// Assert
	got, _ := e.GetCall(call.SID)
	if got.Status != model.CallCompleted {
		t.Errorf("Expected completed, got %s", got.Status)
	}
}
```

## Supported TwiML Verbs

### `<Say>`
Logs text-to-speech. No actual audio generated.

```xml
<Say voice="alice" language="en-US">Hello World</Say>
```

### `<Play>`
Logs audio URL. No actual playback.

```xml
<Play>https://example.com/audio.mp3</Play>
```

### `<Pause>`
Waits for specified duration using the engine's clock.

```xml
<Pause length="3"/>
```

### `<Gather>`
Collects DTMF input via `SendDigits()` API or times out.

```xml
<Gather input="dtmf" timeout="5" numDigits="1" action="/gather-result">
  <Say>Press 1 to continue</Say>
</Gather>
```

Test code:
```go
e.SendDigits(callSID, "1")
```

### `<Dial>`
Connects to different targets:

**Phone Number:**
```xml
<Dial>+15551234567</Dial>
```

**Queue:**
```xml
<Dial><Queue>support</Queue></Dial>
```

**Conference:**
```xml
<Dial><Conference>my-room</Conference></Dial>
```

**Client:**
```xml
<Dial><Client>alice</Client></Dial>
```

### `<Enqueue>`
Adds caller to a queue (alias for `<Dial><Queue>`).

```xml
<Enqueue>support</Enqueue>
```

### `<Redirect>`
Fetches new TwiML from a URL and continues execution.

```xml
<Redirect>https://example.com/new-twiml</Redirect>
```

### `<Hangup>`
Ends the call immediately.

```xml
<Hangup/>
```

## Call Status Lifecycle

Calls progress through states similar to Twilio:

1. **queued** → Call created
2. **ringing** → Simulated ring (immediate in tests)
3. **in-progress** → Call answered, executing TwiML
4. **completed** / **no-answer** / **failed** → Final states

Status callbacks fire on transitions if `StatusCallback` is set.

## Webhook Request Format

Webhook POSTs include Twilio-like form parameters:

- `CallSid`: The call's SID
- `AccountSid`: Configurable account SID (default: AC00...)
- `From`, `To`: Phone numbers
- `CallStatus`: Current status
- `Direction`: `inbound` or `outbound`
- `ApiVersion`: `2010-04-01`
- `Digits`: For Gather action callbacks
- Custom variables from `CreateCallParams.Vars`

## Time Control

### Manual Clock (for tests)

```go
e := engine.NewEngine(engine.WithManualClock())

// Time only advances when you call:
e.Advance(5 * time.Second)
```

This makes tests:
- **Fast**: No real sleeping
- **Deterministic**: Same sequence every time
- **Controllable**: Step through events precisely

### Auto Clock (for demos)

```go
e := engine.NewEngine(engine.WithAutoClock())
// Uses real time.Now() and time.Sleep()
```

## State Inspection

Get a full snapshot of the engine:

```go
snap := e.Snapshot()

// Access calls
for sid, call := range snap.Calls {
	fmt.Printf("Call %s: %s\n", sid, call.Status)
	for _, event := range call.Timeline {
		fmt.Printf("  [%s] %s\n", event.Time, event.Type)
	}
}

// Access queues
for name, queue := range snap.Queues {
	fmt.Printf("Queue %s: %d members\n", name, len(queue.Members))
}

// Access conferences
for name, conf := range snap.Conferences {
	fmt.Printf("Conference %s: %s, %d participants\n",
		name, conf.Status, len(conf.Participants))
}

// Snapshot is JSON-serializable for golden file testing
```

## Web Console

The optional console provides a Twilio-like UI for browsing calls, queues, and conferences.

```go
import "twimulator/console"

cs, err := console.NewConsoleServer(e, ":8089")
if err != nil {
	log.Fatal(err)
}

go cs.Start()
// Visit http://localhost:8089
```

Features:
- Call list with status, timeline
- Individual call details with TwiML execution log
- Webhook request/response inspection
- Queue and conference listings
- JSON snapshot endpoint at `/api/snapshot`

## Example: Queue + Conference Flow

```go
func TestEnqueueAndDequeue(t *testing.T) {
	mock := httpstub.NewMockWebhookClient()
	mock.ResponseFunc = func(url string, form url.Values) (int, []byte, http.Header, error) {
		if url == "http://test/customer" {
			return 200, []byte(`<Response><Enqueue>support</Enqueue></Response>`), nil, nil
		}
		if url == "http://test/agent" {
			return 200, []byte(`<Response><Dial><Queue>support</Queue></Dial></Response>`), nil, nil
		}
		return 200, []byte(`<Response></Response>`), nil, nil
	}

	e := engine.NewEngine(
		engine.WithManualClock(),
		engine.WithWebhookClient(mock),
	)
	defer e.Close()

	// Customer calls in
	c1, _ := e.CreateCall(engine.CreateCallParams{
		From:      "+1111",
		To:        "+2222",
		AnswerURL: "http://test/customer",
	})

	time.Sleep(10 * time.Millisecond)
	e.Advance(1 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Verify in queue
	queue, _ := e.GetQueue("support")
	assert.Equal(t, 1, len(queue.Members))

	// Agent calls to handle queue
	c2, _ := e.CreateCall(engine.CreateCallParams{
		From:      "+3333",
		To:        "+4444",
		AnswerURL: "http://test/agent",
	})

	time.Sleep(10 * time.Millisecond)
	e.Advance(2 * time.Second)
	time.Sleep(10 * time.Millisecond)

	// Both should be connected
	call1, _ := e.GetCall(c1.SID)
	call2, _ := e.GetCall(c2.SID)

	assert.Equal(t, model.CallInProgress, call1.Status)
	assert.Equal(t, model.CallInProgress, call2.Status)
}
```

## Running the Demo

```bash
go run examples/console_demo.go
```

This starts an interactive demo with:
1. Inbound call enqueuing
2. Agent handling queue
3. Conference room with 3 participants
4. Gather flow with digit input

Visit http://localhost:8089 to explore the console.

## Testing Best Practices

1. **Use Manual Clock**: Faster, deterministic tests
2. **Add Small Sleeps**: Give goroutines time to start/process with manual clock
   ```go
   time.Sleep(10 * time.Millisecond)
   e.Advance(duration)
   time.Sleep(10 * time.Millisecond)
   ```
3. **Use httptest.Server**: Test your actual HTTP handlers
4. **Snapshot for Assertions**: Export and compare full state
5. **Mock Status Callbacks**: Use `httpstub.MockWebhookClient` to verify callback flow

## API Reference

### Engine

```go
type Engine interface {
	CreateCall(params CreateCallParams) (*model.Call, error)
	Hangup(callSID model.SID) error
	SendDigits(callSID model.SID, digits string) error

	GetCall(callSID model.SID) (*model.Call, bool)
	ListCalls(filter CallFilter) []*model.Call
	GetQueue(name string) (*model.Queue, bool)
	GetConference(name string) (*model.Conference, bool)
	Snapshot() *StateSnapshot

	SetAutoTime(enabled bool)
	Advance(d time.Duration)
	Clock() Clock

	Close() error
}
```

### CreateCallParams

```go
type CreateCallParams struct {
	From             string
	To               string
	AnswerURL        string
	StatusCallback   string
	MachineDetection bool
	Timeout          time.Duration
	Vars             map[string]string
}
```

### Models

```go
type Call struct {
	SID             SID
	From, To        string
	Direction       Direction
	Status          CallStatus
	StartAt         time.Time
	AnsweredAt      *time.Time
	EndedAt         *time.Time
	CurrentEndpoint string
	Timeline        []Event
	Variables       map[string]string
}

type Queue struct {
	Name     string
	SID      SID
	Members  []SID
	Timeline []Event
}

type Conference struct {
	Name         string
	SID          SID
	Participants []SID
	Status       ConferenceStatus
	Timeline     []Event
}
```

## Architecture

```
twimulator/
├── model/       # Core data types (Call, Queue, Conference, Event)
├── twiml/       # TwiML parser (XML → AST)
├── engine/      # TwiML executor, scheduler, clock
├── httpstub/    # Webhook client (real & mock)
├── twilioapi/   # REST API façade (optional)
├── console/     # Web UI (embedded templates/static)
└── examples/    # Demo programs
```

## Concurrency & Safety

- All engine methods are thread-safe (uses RWMutex)
- Each call runs in its own goroutine
- Manual clock coordinates time advancement
- Call `Close()` to wait for all goroutines to finish

## Limitations (MVP)

- **No actual audio/media**: No RTP, SIP, or real-time voice
- **Simplified queuing**: Queue-to-conference bridging is basic
- **No recording**: `<Record>` not implemented
- **No SMS/messaging**: Voice calls only
- **Limited Dial semantics**: Child call legs are logged but not fully simulated

## Contributing

This is an MVP implementation. Contributions welcome for:
- Additional TwiML verbs (`<Record>`, `<Refer>`, etc.)
- Enhanced queue/conference logic
- Performance optimizations
- More comprehensive tests

## License

MIT License - see LICENSE file

## Acknowledgments

Inspired by Twilio's Voice API. This is an independent testing library and is not affiliated with Twilio Inc.
