# AHOY!
## Twimulator

A Twilio simulator/emulator for testing Twilio voice applications locally without making real API calls or incurring costs.

## Status
⚠️ This repository is in active development and may not be ready for use.

## Features

- **Full TwiML Support**: Execute TwiML verbs including Say, Play, Pause, Gather, Dial, Record, Enqueue, Redirect, and Hangup
- **Call Management**: Create outbound calls, handle inbound calls, manage call state and status
- **Queue System**: Support for call queues with FIFO ordering
- **Conference Calls**: Multi-party conference support
- **Time Control**: Manual, auto-advancing, and real-time clock modes for testing
- **Webhook Simulation**: Mock HTTP client for testing webhook callbacks
- **Status Callbacks**: Trigger status callback events for call lifecycle events
- **TwiML Tracking**: Track executed TwiML verbs for easy integration testing

## Installation

```bash
go get github.com/yourusername/twimulator
```

## Quick Start

### Basic Example

```go
package main

import (
    "context"
    "net/http"
    "net/url"

    "twimulator/engine"
    "twimulator/httpstub"
)

func main() {
    // Create a mock webhook client
    mock := httpstub.NewMockWebhookClient()
    mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
        return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="alice">Hello from Twimulator!</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
    }

    // Create engine with manual clock for testing
    e := engine.NewEngine(
        engine.WithWebhookClient(mock),
        engine.WithManualClock(),
    )
    defer e.Close()

    // Create a subaccount
    account := e.CreateSubAccount("My Test Account")

    // Provision phone numbers
    e.ProvisionNumber(account.SID, "+15551234567")
    e.ProvisionNumber(account.SID, "+15559999999")

    // Create an outbound call
    params := engine.CreateCallParams{
        From: "+15551234567",
        To:   "+15559999999",
        URL:  "http://example.com/voice",
    }
    call, _ := e.CreateCall(account.SID, params)

    // Answer the call (for inbound simulation)
    e.AnswerCall(account.SID, call.SID)

    // Advance time to process the call
    e.Advance(1 * time.Second)

    // Get call state
    state, _ := e.GetCallState(account.SID, call.SID)
    fmt.Printf("Call status: %s\n", state.Status)
}
```

## Core Concepts

### Engine Modes

Twimulator supports three clock modes for different testing scenarios:

#### 1. Manual Clock (Recommended for Testing)
```go
e := engine.NewEngine(engine.WithManualClock())

// Manually advance time
e.Advance(5 * time.Second)
```

#### 2. Auto-Advancing Clock
```go
e := engine.NewEngine(engine.WithAutoAdvancableClock())

// Time advances automatically when timers are set
```

#### 3. Real-Time Clock
```go
e := engine.NewEngine(engine.WithAutoClock())

// Uses real system time
```

### TwiML Execution

Twimulator executes TwiML just like Twilio:

```go
mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
    return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="alice">Welcome to our service</Say>
  <Gather input="dtmf" timeout="5" numDigits="1" action="/gather">
    <Say>Press 1 for sales, 2 for support</Say>
  </Gather>
  <Say>We didn't receive any input</Say>
  <Hangup/>
</Response>`), make(http.Header), nil
}
```

### Sending DTMF Digits

```go
// Send digits during a Gather
e.SendDigits(accountSID, callSID, "1")
```

### Call Queues

```go
// Enqueue a caller
// TwiML:
// <Enqueue>support</Enqueue>

// Dial into the queue from another call
// TwiML:
// <Dial><Queue>support</Queue></Dial>

// Get queue state
queue, _ := e.GetQueue(accountSID, "support")
fmt.Printf("Queue members: %d\n", len(queue.Members))
```

### Conference Calls

```go
// TwiML for joining a conference:
// <Dial><Conference>my-room</Conference></Dial>

// Get conference state
conf, _ := e.GetConference(accountSID, "my-room")
fmt.Printf("Participants: %d\n", len(conf.Participants))
```

## Testing with TwiML Tracking

Twimulator tracks all executed TwiML verbs, making it easy to verify your application's behavior:

### Simple Comparison

```go
func TestMyVoiceApp(t *testing.T) {
    // ... setup engine and create call ...

    // Get call state
    got, _ := e.GetCallState(accountSID, call.SID)

    // Define expected TwiML sequence
    expected := []any{
        &twiml.Say{Text: "Hello world", Voice: "alice", Language: ""},
        &twiml.Pause{Length: 2 * time.Second},
        &twiml.Hangup{},
    }

    // One-line comparison!
    if !reflect.DeepEqual(got.ExecutedTwiML, expected) {
        t.Errorf("ExecutedTwiML mismatch:\nGot:  %#v\nWant: %#v",
            got.ExecutedTwiML, expected)
    }
}
```

### Field-by-Field Comparison (for better error messages)

```go
if record, ok := got.ExecutedTwiML[0].(*twiml.Record); ok {
    if record.MaxLength != 60*time.Second {
        t.Errorf("Expected MaxLength=60s, got %v", record.MaxLength)
    }
    if record.Action != "http://test/record-done" {
        t.Errorf("Expected Action='http://test/record-done', got %s", record.Action)
    }
}
```

### Comparable Types (can use `==` operator)

Most TwiML types support direct comparison with `==`:
- `Say`, `Play`, `Pause`, `Record`, `Enqueue`, `Redirect`, `Hangup`

Types with children need `reflect.DeepEqual`:
- `Gather`, `Dial`, `Response`

### Important Note on Nested Children

When TwiML verbs like `Gather` or `Dial` have nested children (Say, Play, Pause), those children are **also tracked individually** in `ExecutedTwiML`:

```go
// This TwiML:
// <Gather><Say>Press 1</Say></Gather>

// Produces this ExecutedTwiML:
expected := []any{
    &twiml.Gather{
        Children: []twiml.Node{
            &twiml.Say{Text: "Press 1"},
        },
    },
    &twiml.Say{Text: "Press 1"}, // Also tracked individually!
}
```

## Status Callbacks

Configure status callbacks to receive call lifecycle events:

```go
params := engine.CreateCallParams{
    From:                 "+15551234567",
    To:                   "+15559999999",
    URL:                  "http://example.com/voice",
    StatusCallback:       "http://example.com/status",
    StatusCallbackEvents: []string{"initiated", "ringing", "answered", "completed"},
}
```

## Event Timeline

Every call maintains a detailed timeline of events:

```go
state, _ := e.GetCallState(accountSID, call.SID)

for _, event := range state.Timeline {
    fmt.Printf("[%s] %s: %+v\n", event.Time, event.Type, event.Detail)
}

// Example output:
// [2024-01-01 10:00:00] call.created: map[from:+15551234567 to:+15559999999]
// [2024-01-01 10:00:01] call.ringing: map[]
// [2024-01-01 10:00:02] call.answered: map[]
// [2024-01-01 10:00:03] twiml.say: map[text:Hello voice:alice]
// [2024-01-01 10:00:04] twiml.hangup: map[]
// [2024-01-01 10:00:04] call.completed: map[]
```

## API Reference

### Engine Creation

```go
// Create engine with options
e := engine.NewEngine(
    engine.WithManualClock(),              // Clock mode
    engine.WithWebhookClient(mockClient),  // Custom webhook client
)
```

### Account Management

```go
// Create subaccount
account := e.CreateSubAccount(friendlyName string)

// List accounts
accounts := e.ListAccounts(params ListAccountParams)

// Provision numbers
e.ProvisionNumber(accountSID, phoneNumber string)

// List numbers
numbers := e.ListIncomingPhoneNumbers(accountSID, params ListParams)
```

### Call Management

```go
// Create call
call, err := e.CreateCall(accountSID, params CreateCallParams)

// Answer call (inbound simulation)
err := e.AnswerCall(accountSID, callSID)

// Hangup call
err := e.Hangup(accountSID, callSID)

// Get call state
state, ok := e.GetCallState(accountSID, callSID)

// List calls
calls := e.ListCalls(accountSID, params ListCallParams)

// Update call
err := e.UpdateCall(accountSID, callSID, params UpdateCallParams)
```

### Queue Management

```go
// Get queue
queue, ok := e.GetQueue(accountSID, queueName string)

// List queues
queues := e.ListQueues(accountSID)
```

### Conference Management

```go
// Get conference
conf, ok := e.GetConference(accountSID, conferenceName string)

// List conferences
conferences := e.ListConferences(accountSID)
```

### DTMF Input

```go
// Send digits
err := e.SendDigits(accountSID, callSID, digits string)
```

### Snapshots

```go
// Get snapshot of all call/queue/conference state
snapshot, err := e.Snapshot(accountSID)
```

## Testing Examples

### Test with Gather and Action Callback

```go
func TestGatherWithAction(t *testing.T) {
    mock := httpstub.NewMockWebhookClient()

    mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
        if targetURL == "http://test/answer" {
            return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" numDigits="1" action="http://test/gather">
    <Say>Press 1</Say>
  </Gather>
</Response>`), make(http.Header), nil
        }
        if targetURL == "http://test/gather" {
            digits := form.Get("Digits")
            return 200, []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>You pressed %s</Say>
  <Hangup/>
</Response>`, digits)), make(http.Header), nil
        }
        return 404, []byte("Not found"), make(http.Header), nil
    }

    e := engine.NewEngine(
        engine.WithWebhookClient(mock),
        engine.WithManualClock(),
    )
    defer e.Close()

    account := e.CreateSubAccount("Test")
    e.ProvisionNumber(account.SID, "+1234")

    params := engine.CreateCallParams{
        From: "+1234",
        To:   "+5678",
        URL:  "http://test/answer",
    }
    call, _ := e.CreateCall(account.SID, params)

    // Answer and let gather start
    time.Sleep(10 * time.Millisecond)
    e.AnswerCall(account.SID, call.SID)
    e.Advance(1 * time.Second)

    // Send digits
    e.SendDigits(account.SID, call.SID, "1")
    e.Advance(2 * time.Second)
    time.Sleep(100 * time.Millisecond)

    // Verify
    state, _ := e.GetCallState(account.SID, call.SID)

    expected := []any{
        &twiml.Gather{/* ... */},
        &twiml.Say{Text: "Press 1"},
        &twiml.Say{Text: "You pressed 1"},
        &twiml.Hangup{},
    }

    if !reflect.DeepEqual(state.ExecutedTwiML, expected) {
        t.Errorf("Mismatch")
    }
}
```

### Test Queue Behavior

```go
func TestQueueFlow(t *testing.T) {
    mock := httpstub.NewMockWebhookClient()
    mock.ResponseFunc = func(targetURL string, form url.Values) (int, []byte, http.Header, error) {
        if targetURL == "http://test/enqueue" {
            return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Enqueue>support</Enqueue>
</Response>`), make(http.Header), nil
        }
        if targetURL == "http://test/dial-queue" {
            return 200, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Queue>support</Queue></Dial>
</Response>`), make(http.Header), nil
        }
        return 200, []byte(`<Response></Response>`), make(http.Header), nil
    }

    e := engine.NewEngine(
        engine.WithWebhookClient(mock),
        engine.WithManualClock(),
    )
    defer e.Close()

    account := e.CreateSubAccount("Test")
    e.ProvisionNumber(account.SID, "+1111", "+2222")

    // Create first call - enqueues
    call1, _ := e.CreateCall(account.SID, engine.CreateCallParams{
        From: "+1111",
        To:   "+9999",
        URL:  "http://test/enqueue",
    })

    time.Sleep(10 * time.Millisecond)
    e.AnswerCall(account.SID, call1.SID)
    e.Advance(1 * time.Second)

    // Verify in queue
    queue, _ := e.GetQueue(account.SID, "support")
    if len(queue.Members) != 1 {
        t.Errorf("Expected 1 queue member, got %d", len(queue.Members))
    }

    // Create second call - dials queue
    call2, _ := e.CreateCall(account.SID, engine.CreateCallParams{
        From: "+2222",
        To:   "+8888",
        URL:  "http://test/dial-queue",
    })

    time.Sleep(10 * time.Millisecond)
    e.AnswerCall(account.SID, call2.SID)
    e.Advance(2 * time.Second)

    // Both should be in progress (connected)
    state1, _ := e.GetCallState(account.SID, call1.SID)
    state2, _ := e.GetCallState(account.SID, call2.SID)

    if state1.Status != model.CallInProgress {
        t.Errorf("Expected call1 in-progress, got %s", state1.Status)
    }
    if state2.Status != model.CallInProgress {
        t.Errorf("Expected call2 in-progress, got %s", state2.Status)
    }
}
```

## Project Structure

```
twimulator/
├── engine/          # Core call engine and TwiML execution
├── httpstub/        # Mock HTTP client for testing
├── model/           # Data models (Call, Queue, Conference, etc.)
├── twiml/           # TwiML parser and AST
└── twilioapi/       # Twilio REST API compatibility layer
```

## Features vs Twilio

| Feature | Twimulator | Notes |
|---------|-----------|-------|
| Basic TwiML Verbs | ✅ | Say, Play, Pause, Hangup |
| Gather | ✅ | DTMF input, action callbacks |
| Record | ✅ | With timeout, maxLength, action |
| Dial | ✅ | Number, Client, Queue, Conference |
| Enqueue | ✅ | Call queues with FIFO |
| Redirect | ✅ | Fetch new TwiML |
| Conference | ✅ | Multi-party conferences |
| Status Callbacks | ✅ | Configurable events |
| Webhook Callbacks | ✅ | Via mock client |
| Time Control | ✅ | Manual/auto/real-time modes |
| TwiML Tracking | ✅ | For easy testing |
| SMS/MMS | ❌ | Voice only |
| Media Streams | ❌ | Future consideration |
| SIP | ❌ | Future consideration |

