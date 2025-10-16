I want you to implement a Go library that **simulates Twilio Voice** for integration testing, without placing real calls or incurring cost. Think of it as a **TwiML executor + webhook driver** that mimics the parts of Twilio Voice we rely on. It should let tests create “calls,” drive TwiML, invoke my app’s webhooks at the right times, and expose inspectable state (calls, queues, conferences, participants, events) for assertions. Also include an optional lightweight “Twilio-like console” web UI to browse a call timeline.

## Goals & scope (MVP)

* **Primary goal:** Deterministic, fast, in-process simulation of Twilio Voice behavior used in our app.
* **We do not need actual media/audio**. No RTP/SIP. Only states, lifecycles, and TwiML verb handling.
* **Supported TwiML (MVP):**

    * `<Say/>` (no audio; just log)
    * `<Play/>` (no audio; just log)
    * `<Pause/>`
    * `<Gather>` (simulate DTMF input via API)
    * `<Dial>` with `<Number>`, `<Client>` (logical only), `<Queue>`, `<Conference>`
    * `<Enqueue>`, `<Queue>` (basic queuing)
    * `<Conference>` (participants join/leave, events)
    * `<Redirect>`
    * `<Hangup/>`
* **Status callbacks & webhooks:** Simulate `CallStatus` transitions (queued → ringing → in-progress → completed) and invoke:

    * Answer URL (TwiML fetch)
    * Status callbacks on state changes
    * Queue/conference events (join, leave, end)
* **State inspection:** Export current/full state as JSON for assertions: calls, queues, conferences, participants, events, and per-call timelines.
* **Time control:** Deterministic fake clock with both **auto time** and **manual step/tick** modes so tests can advance time precisely.
* **Logging & “console”:** Library logs every event; optional embedded HTTP UI shows a Twilio-like console (calls list, call detail, timeline, request/response log, TwiML executed).
* **Zero external dependencies** outside standard lib (plus a tiny, permissive router if needed). No network calls except webhooks to my app.

## High-level design

### Package layout

```
twivoice/                 // module root
  engine/                 // TwiML executor & scheduler
  model/                  // data models & JSON schemas
  httpstub/               // webhook client & test server helpers
  console/                // optional web UI (embed templates/static)
  twiml/                  // minimal TwiML parser (XML -> AST)
  twilioapi/              // tiny façade mirroring Twilio REST we use (Create Call, etc.)
```

### Key types & interfaces (please implement)

```go
// model/core.go
type SID string // e.g., "CAxxxxxxxx", "CFxxxxxxxx", "QUxxxxxxxx", "ACxxxxxxxx"
type CallStatus string
const (
  CallQueued CallStatus = "queued"
  CallRinging CallStatus = "ringing"
  CallInProgress CallStatus = "in-progress"
  CallCompleted CallStatus = "completed"
  CallBusy CallStatus = "busy"
  CallFailed CallStatus = "failed"
  CallNoAnswer CallStatus = "no-answer"
)

type Direction string
const (Inbound Direction = "inbound"; Outbound Direction = "outbound")

// SubAccount represents a Twilio subaccount (all resources are scoped to a subaccount)
type SubAccount struct {
  SID          SID
  FriendlyName string
  Status       string // "active", "suspended", "closed"
  CreatedAt    time.Time
}

type Call struct {
  SID            SID
  AccountSID     SID    // SubAccount that owns this call
  From, To       string
  Direction      Direction
  Status         CallStatus
  StartAt, AnsweredAt, EndedAt time.Time
  ParentCallSID  *SID // for Dial legs
  CurrentEndpoint string // "queue:{name}", "conference:{name}", "gather", ""
  Timeline       []Event
  Variables      map[string]string // arbitrary per-call vars (like Digits)
}

type Queue struct {
  Name       string
  SID        SID
  AccountSID SID    // SubAccount that owns this queue
  Members    []SID  // Call SIDs
  Timeline   []Event
}

type Conference struct {
  Name         string
  SID          SID
  AccountSID   SID    // SubAccount that owns this conference
  Participants []SID  // Call SIDs
  Status       string // "created","in-progress","completed"
  Timeline     []Event
}

type Event struct {
  Time   time.Time
  Type   string // "webhook.request","webhook.response","status.changed","joined.queue","joined.conference", etc.
  Detail map[string]any
}
```

```go
// engine/engine.go
type Clock interface {
  Now() time.Time
  Sleep(d time.Duration) // respects manual/auto mode
  Advance(d time.Duration) // manual advancement
}

type Engine interface {
  // SubAccount management
  CreateAccount(params *openapi.CreateAccountParams) (*openapi.ApiV2010Account, error)
  ListAccount(params *openapi.ListAccountParams) ([]openapi.ApiV2010Account, error)

  // Core lifecycle
  CreateCall(params *openapi.CreateCallParams) (*openapi.ApiV2010Call, error)
  Hangup(callSID model.SID) error
  SendDigits(callSID model.SID, digits string) error // feeds <Gather>

  // Introspection
  GetCall(callSID model.SID) (*model.Call, bool)
  ListCalls(filter CallFilter) []model.Call
  GetQueue(accountSID model.SID, name string) (*model.Queue, bool)  // Queues are scoped by subaccount
  GetConference(accountSID model.SID, name string) (*model.Conference, bool)  // Conferences are scoped by subaccount
  Snapshot() *StateSnapshot // JSON-serializable

  // Scheduling/time
  SetAutoTime(enabled bool)
  Advance(d time.Duration)
}

// Calls are created with Twilio's generated `openapi.CreateCallParams` type.
// We care about the following fields:
//   PathAccountSid (required)
//   From / To
//   Url (TwiML answer URL)
//   StatusCallback / StatusCallbackEvent
//   Timeout (seconds)
//   CallToken (optional)


type CallFilter struct { To, From string; Status *model.CallStatus }

// console/server.go
type ConsoleServer struct {
  Addr string // e.g. ":8089"
}
func NewConsoleServer(e Engine, addr string) *ConsoleServer
func (s *ConsoleServer) Start() error
func (s *ConsoleServer) Stop(ctx context.Context) error
```

```go
// httpstub/webhook.go
type WebhookClient interface {
  POST(ctx context.Context, url string, form url.Values) (status int, body []byte, headers http.Header, err error)
}
```

```go
// twiml/ast.go
// Minimal AST for supported verbs.
type Node interface{ isNode() }
type Response struct{ Children []Node }
type Say struct{ Text string; Voice, Language string }
type Play struct{ URL string }
type Pause struct{ Length time.Duration }
type Gather struct{ Input string; Timeout time.Duration; NumDigits int; Action string; Children []Node }
type Dial struct{ Number, Client, Queue, Conference string; Action string }
type Enqueue struct{ Queue string }
type Redirect struct{ URL string }
type Hangup struct{}
```

### TwiML parsing & execution

* Parse XML into the above AST (ignore unsupported attrs; store them if helpful).
* **Execution model:** A **CallRunner** goroutine per call that:

    1. Transitions `queued → ringing` immediately on `CreateCall`.
    2. After `Timeout`, if unanswered, go to `no-answer`. Otherwise, simulate answer:

        * `ringing → in-progress`
        * POST to `AnswerURL` with Twilio-like form body (`CallSid`, `From`, `To`, `CallStatus`, etc.).
    3. Parse returned TwiML and execute sequentially.
* **Verb semantics (simulate only):**

    * `Say/Play/Pause`: append timeline entries; `Pause` waits using the **Clock**.
    * `Gather`: set call state `CurrentEndpoint="gather"`, wait until either `NumDigits` received via `SendDigits` or `Timeout`; if `Action` is set, POST to it with `Digits=...` and then continue with returned TwiML (like Redirect).
    * `Dial`:

        * If `.Queue` present: move caller into that queue (create if missing), timeline `joined.queue`. Trigger any waiting dequeues (e.g., a second call Dialing the same Queue).
        * If `.Conference` present: ensure conference exists; add participant; set conference `in-progress` when 2+ participants; support leaving on `Hangup`.
        * If `.Number` / `.Client`: create a **child outbound call** (a “leg”) with its own AnswerURL (`Action` if provided). Parent call waits until child completes or `Action` returns alternate flow.
    * `Enqueue`: alias of `Dial Queue`.
    * `Redirect`: fetch TwiML from `URL` and continue.
    * `Hangup`: mark call completed.
* **Status callbacks:** When status changes (ringing, in-progress, completed), POST to `StatusCallback` if provided. For queues/conferences, support optional callbacks set at creation time.

### Webhook request shapes

* Send form fields similar to Twilio:

    * `CallSid`, `AccountSid` (use fake), `From`, `To`, `Direction`, `CallStatus`, `ApiVersion`, `Timestamp`.
    * For Gather Action: include `Digits`.
    * For Conference/Queue events: `ConferenceSid`, `ConferenceName`, `StatusCallbackEvent`.
* Do not require exact parity; just be consistent and documented.

### Deterministic time & scheduling

* `Clock` default is **ManualClock**: no real sleeps. `Advance(d)` drains due timers in order and runs their callbacks synchronously, enabling step-wise tests.
* Optional **AutoClock** uses real time for exploratory runs.
* All scheduled webhook invocations and TwiML `Pause` depend on `Clock`.

### Concurrency & safety

* Use a central **Scheduler** that owns the fake clock queue.
* Calls/Queues/Conferences stored in a state struct protected by a RWMutex. All mutations emit timeline events.
* Provide `Snapshot()` that deep-copies state into JSON-ready structs.

### Tiny Twilio REST façade (only what we need)

* `twilioapi.CreateCall(params)` → delegates to `Engine.CreateCall` and returns a Twilio-like response struct `{ Sid, Status, To, From, ... }`.
* Include convenience helpers to assert states in tests.

### Console web UI (optional, but please scaffold)

* `/` lists Calls with Sid/From/To/Status and link to details.
* `/calls/{sid}` shows timeline (waterfall), current state, executed TwiML (pretty-printed), webhooks (request/response bodies).
* `/queues`, `/conferences` simple listings.
* Use Go `embed` for HTML/CSS; keep it dependency-light.

## Example test usage (write this as a real _*_test.go)

```go
func Test_EnqueueAndConferenceFlow(t *testing.T) {
  e := engine.NewEngine(engine.WithManualClock(), engine.WithConsoleDisabled())
  // our app is running locally on httptest.Server; AnswerURL points to it.

  // Create a subaccount for this test
  acct, _ := e.CreateAccount((&openapi.CreateAccountParams{}).SetFriendlyName("Test Account"))
  snap := e.Snapshot()
  subAccount := snap.SubAccounts[model.SID(*acct.Sid)]

  // 1) Create first call; it answers and enqueues into "support"
  params1 := (&openapi.CreateCallParams{}).
    SetPathAccountSid(string(subAccount.SID)).
    SetFrom("+155512301").
    SetTo("+180055501").
    SetUrl(testSrv.URL + "/voice/inbound").
    SetStatusCallback(testSrv.URL + "/voice/status").
    SetTimeout(2)
  apiCall1, _ := e.CreateCall(params1)
  c1SID := model.SID(*apiCall1.Sid)
  c1, _ := e.GetCall(c1SID)

  // Advance until answered and gather runs
  e.Advance(3 * time.Second)

  // 2) Create second call that dials the same queue -> bridges to conference
  apiCall2, _ := e.CreateCall((&openapi.CreateCallParams{}).
    SetPathAccountSid(string(subAccount.SID)).
    SetFrom("+155512302").
    SetTo("+180055502").
    SetUrl(testSrv.URL + "/voice/agent"))
  c2SID := model.SID(*apiCall2.Sid)
  c2, _ := e.GetCall(c2SID)
  e.Advance(5 * time.Second)

  snap = e.Snapshot()
  got1, _ := e.GetCall(c1.SID)
  if got1.Status != model.CallInProgress { t.Fatal("expected in-progress") }

  // GetConference now requires accountSID to scope the lookup
  conf, _ := e.GetConference(subAccount.SID, "support-room")
  if len(conf.Participants) != 2 { t.Fatalf("expected 2 participants") }

  // simulate DTMF to exit gather if needed
  _ = e.SendDigits(c1.SID, "1")
  e.Advance(1 * time.Second)

  // cleanup
  _ = e.Hangup(c1.SID); _ = e.Hangup(c2.SID)
  e.Advance(1 * time.Second)

  // assert completed
  got1, _ = e.GetCall(c1.SID)
  if got1.Status != model.CallCompleted { t.Fatal("expected completed") }

  _ = snap // optionally marshal to JSON and compare golden
}
```

## Webhook driving examples

* **AnswerURL**: engine posts form data, expects XML TwiML. Example response:

```xml
<Response>
  <Enqueue>support</Enqueue>
</Response>
```

* **Agent flow**:

```xml
<Response>
  <Dial><Queue>support</Queue></Dial>
</Response>
```

* **Gather**:

```xml
<Response>
  <Gather input="dtmf" timeout="5" numDigits="1" action="https://app.local/gather-done">
    <Say>Press 1 to continue</Say>
  </Gather>
  <Say>No input; goodbye</Say>
  <Hangup/>
</Response>
```

Library should wait for `SendDigits` or timeout; then POST `Digits` to `action`.

## Status transitions (document and implement)

* **Outbound CreateCall**: `queued` (immediate) → `ringing` (immediate) → after `Timeout` either `no-answer` or “answered”:

    * On “answer”: `in-progress`, invoke AnswerURL.
* **Hangup**: `completed` and issue final `StatusCallback`.
* **Queue**: on `Enqueue`/`Dial Queue`, add caller to queue; on a matching `Dial Queue` from another call, bridge both into a **Conference** named after the queue or given name.
* **Conference**: `created` on first participant, `in-progress` when two or more, `completed` when last leaves.

## SubAccount Architecture

**IMPLEMENTED**: Following Twilio's real architecture, all resources are scoped to SubAccounts:

* **SubAccounts** (`AC` prefix): Container for all resources. Created via `engine.CreateAccount(params)` returning a Twilio `ApiV2010Account` (then look up the internal `SubAccount` if needed).
* **Resource Scoping**:
  * Each `Call`, `Queue`, and `Conference` has an `AccountSID` field
  * Queues and conferences are stored in nested maps: `map[AccountSID]map[name]*Resource`
  * Two different subaccounts can have queues/conferences with the same name without conflict
* **API Requirements**:
  * `CreateCall` requires `AccountSID` parameter (validated to exist)
  * `GetQueue/GetConference` require both `accountSID` and `name` parameters
* **Multi-Tenancy**: Enables testing of multi-tenant applications where different customers/organizations are isolated

## API parity notes

* Don't chase 1:1 Twilio parity. Be consistent and **document** our fields.
* SIDs can be short pseudo-random strings with prefixes: `CA` (call), `CF` (conference), `QU` (queue), `AC` (subaccount).
* SubAccount support matches Twilio's actual architecture for proper testing of multi-tenant scenarios.

## Deliverables

1. Production-quality Go library with the structure above.
2. Unit tests covering:

    * CreateCall → AnswerURL → TwiML execution
    * Gather with digits and timeout
    * Queue + Conference bridging
    * Redirect
    * Hangup & callbacks ordering
    * Snapshot JSON stability (golden)
3. Example program that spins up the console UI and runs a small scenario.
4. README with:

    * Concept overview
    * Supported verbs
    * Status callback shapes
    * How to run in manual vs auto time
    * How to integrate with `httptest.Server` for app webhooks

## Coding style & constraints

* Go ≥ 1.22, `go test ./...` green.
* Favor standard lib; avoid heavy deps.
* Clear, structured logs with SIDs and timestamps.
* Race-free; `go test -race` should pass.

---

Please implement the above, making pragmatic choices where unspecified, but keep the public API close to what’s described so we can drop-in replace Twilio calls in tests.
