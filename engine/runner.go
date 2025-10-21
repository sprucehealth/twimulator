package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	"twimulator/model"
	"twimulator/twiml"
)

// ErrCallHungup is returned when a Hangup verb is executed
var ErrCallHungup = errors.New("call hungup via Hangup verb")

// CallRunner executes TwiML for a call
type CallRunner struct {
	call    *model.Call
	clock   Clock
	state   *subAccountState
	engine  *EngineImpl
	timeout time.Duration

	// State for gather
	gatherCh  chan string
	hangupCh  chan struct{}
	answerCh  chan struct{}
	busyCh    chan struct{}
	failedCh  chan struct{}
	dequeueCh chan string // for explicit dequeue with result
	done      chan struct{}
}

// NewCallRunner creates a new call runner
func NewCallRunner(call *model.Call, state *subAccountState, engine *EngineImpl, timeout time.Duration) *CallRunner {
	return &CallRunner{
		call:      call,
		clock:     state.clock,
		state:     state,
		engine:    engine,
		timeout:   timeout,
		gatherCh:  make(chan string, 1),
		hangupCh:  make(chan struct{}, 1),
		answerCh:  make(chan struct{}, 1),
		busyCh:    make(chan struct{}, 1),
		failedCh:  make(chan struct{}, 1),
		dequeueCh: make(chan string, 1),
		done:      make(chan struct{}),
	}
}

// Run executes the call lifecycle
func (r *CallRunner) Run(ctx context.Context) {
	defer close(r.done)
	// Transition to ringing
	r.updateStatus(model.CallRinging)

	if r.call.Direction == model.Inbound {
		// backend auto-answers an inbound call
		r.answer(ctx)
		return
	}

	// Wait for explicit answer, busy, failed, or timeout
	select {
	case <-ctx.Done():
		return
	case <-r.hangupCh:
		r.updateStatus(model.CallCompleted)
		return
	case <-r.busyCh:
		r.updateStatus(model.CallBusy)
		return
	case <-r.failedCh:
		r.updateStatus(model.CallFailed)
		return
	case <-r.clock.After(r.timeout):
		r.updateStatus(model.CallNoAnswer)
		return
	case <-r.answerCh:
		// Answer the call
		r.answer(ctx)
	}
}

func (r *CallRunner) answer(ctx context.Context) {
	answerNow := func() {
		r.updateStatus(model.CallInProgress)
		now := r.clock.Now()
		r.state.mu.Lock()
		r.call.AnsweredAt = &now
		r.state.mu.Unlock()
	}
	if r.call.Direction != model.Inbound {
		// an outbound call is answered first, and then it's url is fetched
		answerNow()
	}
	// Fetch initial TwiML
	twimlResp, err := r.fetchTwiML(ctx, r.call.Url, url.Values{})
	if err != nil {
		log.Printf("Failed to fetch Url for call %s: %v", r.call.SID, err)
		r.updateStatus(model.CallFailed)
		return
	}
	if r.call.Direction == model.Inbound {
		// backend answers the inbound call when twiml is fetched
		answerNow()
	}

	// Execute TwiML
	if err := r.executeTwiML(ctx, twimlResp, r.call.Url); err != nil {
		// Check if this is a normal hangup or an actual error
		if errors.Is(err, ErrCallHungup) {
			// Normal hangup via <Hangup/> verb - call already completed by executeHangup
			return
		}
		// Actual error - mark call as failed
		log.Printf("TwiML execution error for call %s: %v", r.call.SID, err)
		r.updateStatus(model.CallFailed)
		return
	}

	// TwiML execution completed, wait for hangup
	select {
	case <-ctx.Done():
		return
	case <-r.hangupCh:
		r.updateStatus(model.CallCompleted)
		now := r.clock.Now()
		r.state.mu.Lock()
		r.call.EndedAt = &now
		r.state.mu.Unlock()
		return
	}
}

func (r *CallRunner) fetchTwiML(ctx context.Context, targetURL string, form url.Values) (*twiml.Response, error) {
	// Build form with call parameters
	callForm := r.buildCallForm()
	for k, v := range form {
		callForm[k] = v
	}

	// Log webhook request
	r.addEvent("webhook.request", map[string]any{
		"url":  targetURL,
		"form": callForm,
	})

	// Make request
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status, body, headers, err := r.engine.webhook.POST(reqCtx, targetURL, callForm)
	if err != nil {
		r.addEvent("webhook.error", map[string]any{
			"url":   targetURL,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("webhook request failed: %w", err)
	}

	// Log response
	r.addEvent("webhook.response", map[string]any{
		"url":     targetURL,
		"status":  status,
		"headers": headers,
		"body":    string(body),
	})

	// Parse TwiML
	resp, err := twiml.Parse(body)
	if err != nil {
		r.addEvent("twiml.parse_error", map[string]any{
			"error": err.Error(),
			"body":  string(body),
		})
		return nil, fmt.Errorf("failed to parse TwiML: %w", err)
	}

	return resp, nil
}

func (r *CallRunner) executeTwiML(ctx context.Context, resp *twiml.Response, currentTwimlDocumentURL string) error {
	terminated := false
	for _, node := range resp.Children {
		if terminated {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.hangupCh:
			return nil
		default:
		}

		if err := r.executeNode(ctx, node, currentTwimlDocumentURL, &terminated); err != nil {
			return err
		}
	}
	return nil
}

func (r *CallRunner) executeNode(ctx context.Context, node twiml.Node, currentTwimlDocumentURL string, terminated *bool) error {
	switch n := node.(type) {
	case *twiml.Say:
		return r.executeSay(n)
	case *twiml.Play:
		return r.executePlay(ctx, n)
	case *twiml.Pause:
		return r.executePause(ctx, n)
	case *twiml.Gather:
		return r.executeGather(ctx, n, currentTwimlDocumentURL, terminated)
	case *twiml.Dial:
		return r.executeDial(ctx, n, currentTwimlDocumentURL)
	case *twiml.Enqueue:
		return r.executeEnqueue(ctx, n, currentTwimlDocumentURL)
	case *twiml.Redirect:
		return r.executeRedirect(ctx, n)
	case *twiml.Record:
		return r.executeRecord(ctx, n, currentTwimlDocumentURL, terminated)
	case *twiml.Hangup:
		return r.executeHangup(false)
	default:
		log.Printf("Unknown TwiML node type: %T", node)
	}
	return nil
}

func (r *CallRunner) executeSay(say *twiml.Say) error {
	r.trackTwiML(say)
	r.addEvent("twiml.say", map[string]any{
		"text":     say.Text,
		"voice":    say.Voice,
		"language": say.Language,
	})
	return nil
}

func (r *CallRunner) executePlay(ctx context.Context, play *twiml.Play) error {
	r.trackTwiML(play)
	// Log the play attempt
	r.addEvent("twiml.play", map[string]any{
		"url": play.URL,
	})

	// Fetch the media URL to ensure it's accessible
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status, _, _, err := r.engine.webhook.GET(reqCtx, play.URL)
	if err != nil {
		r.addEvent("play.error", map[string]any{
			"url":   play.URL,
			"error": err.Error(),
		})
		return fmt.Errorf("failed to fetch play URL %s: %w", play.URL, err)
	}

	// Check for non-2xx status codes
	if status < 200 || status >= 300 {
		r.addEvent("play.error", map[string]any{
			"url":    play.URL,
			"status": status,
		})
		return fmt.Errorf("play URL %s returned status %d", play.URL, status)
	}

	r.addEvent("play.success", map[string]any{
		"url":    play.URL,
		"status": status,
	})

	return nil
}

func (r *CallRunner) executePause(ctx context.Context, pause *twiml.Pause) error {
	r.trackTwiML(pause)
	r.addEvent("twiml.pause", map[string]any{
		"length": pause.Length.Seconds(),
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		return nil
	case <-r.clock.After(pause.Length):
		return nil
	}
}

func (r *CallRunner) executeGather(ctx context.Context, gather *twiml.Gather, currentTwimlDocumentURL string, terminated *bool) error {
	r.trackTwiML(gather)
	r.addEvent("twiml.gather", map[string]any{
		"input":      gather.Input,
		"timeout":    gather.Timeout.Seconds(),
		"num_digits": gather.NumDigits,
		"action":     gather.Action,
	})

	r.state.mu.Lock()
	r.call.CurrentEndpoint = "gather"
	r.state.mu.Unlock()

	// Execute nested children while gathering
	for _, child := range gather.Children {
		if *terminated {
			break
		}
		switch n := child.(type) {
		case *twiml.Say:
			if err := r.executeSay(n); err != nil {
				return err
			}
		case *twiml.Play:
			if err := r.executePlay(ctx, n); err != nil {
				return err
			}
		case *twiml.Pause:
			if err := r.executePause(ctx, n); err != nil {
				return err
			}
		default:
			nodeType := fmt.Sprintf("%T", child)
			r.addEvent("gather.invalid_child", map[string]any{"node": nodeType})
			return fmt.Errorf("gather cannot contain %s", nodeType)
		}
	}

	if *terminated {
		return nil
	}

	// Wait for digits or timeout
	var digits string
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		return nil
	case digits = <-r.gatherCh:
		// Got digits
		r.addEvent("gather.digits", map[string]any{"digits": digits})
	case <-r.clock.After(gather.Timeout):
		// Timeout
		digits = ""
		r.addEvent("gather.timeout", map[string]any{})
	}
	if digits == "" {
		return nil
	}

	r.state.mu.Lock()
	r.call.CurrentEndpoint = ""
	r.call.Variables["Digits"] = digits
	r.state.mu.Unlock()

	// Call action callback with gathered digits
	form := url.Values{}
	form.Set("Digits", digits)

	return r.executeActionCallback(ctx, gather.Action, form, currentTwimlDocumentURL)
}

func (r *CallRunner) executeDial(ctx context.Context, dial *twiml.Dial, currentTwimlDocumentURL string) error {
	r.trackTwiML(dial)
	r.addEvent("twiml.dial", map[string]any{
		"number":     dial.Number,
		"client":     dial.Client,
		"queue":      dial.Queue,
		"conference": dial.Conference,
		"timeout":    dial.Timeout.Seconds(),
	})

	// Handle different dial targets
	if dial.Queue != "" {
		return r.executeDialQueue(ctx, dial, currentTwimlDocumentURL)
	}
	if dial.Conference != "" {
		return r.executeDialConference(ctx, dial)
	}
	if dial.Number != "" || dial.Client != "" {
		return r.executeDialNumber(ctx, dial)
	}

	return nil
}

func (r *CallRunner) executeDialQueue(ctx context.Context, dial *twiml.Dial, currentTwimlDocumentURL string) error {
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, dial.Queue)
	queueSID := queue.SID

	// Check if there are waiting members to connect to
	var targetCallSID model.SID
	if len(queue.Members) > 0 {
		// Get the first waiting caller (FIFO)
		targetCallSID = queue.Members[0]
	}

	// Two scenarios:
	// 1. If there's a waiting caller, bridge with them
	// 2. If no waiting caller, join queue and wait for one

	if targetCallSID != "" {
		// Scenario 1: Bridge with waiting caller
		return r.bridgeWithQueueMember(ctx, dial, queue, queueSID, targetCallSID, currentTwimlDocumentURL)
	}

	// Scenario 2: No waiting callers, join queue and wait
	return r.waitInDialQueue(ctx, dial, queue, queueSID, currentTwimlDocumentURL)
}

// bridgeWithQueueMember connects this call to a waiting queue member
func (r *CallRunner) bridgeWithQueueMember(ctx context.Context, dial *twiml.Dial, queue *model.Queue, queueSID model.SID, targetCallSID model.SID, currentTwimlDocumentURL string) error {
	startTime := r.clock.Now()

	r.addEvent("dial.queue.bridging", map[string]any{
		"queue":           dial.Queue,
		"target_call_sid": targetCallSID,
	})

	// Get the target call's runner to dequeue it
	r.state.mu.Lock()
	targetRunner := r.state.runners[targetCallSID]
	targetCall := r.state.calls[targetCallSID]

	// Calculate how long target was in queue
	var targetQueueStart time.Time
	for _, event := range targetCall.Timeline {
		if event.Type == "enqueued" || event.Type == "dial.queue.joined" {
			targetQueueStart = event.Time
			break
		}
	}
	r.state.mu.Unlock()

	// Signal the target call to dequeue with "bridged" result
	if targetRunner != nil {
		select {
		case targetRunner.dequeueCh <- "bridged":
		default:
		}
	}

	// Simulate bridge duration - wait until either call hangs up
	dialDuration := 0
	select {
	case <-ctx.Done():
	case <-r.hangupCh:
	case <-r.clock.After(dial.Timeout):
		// Bridge timeout
		dialDuration = int(dial.Timeout.Seconds())
	}

	endTime := r.clock.Now()
	dialDuration = int(endTime.Sub(startTime).Seconds())
	targetQueueTime := 0
	if !targetQueueStart.IsZero() {
		targetQueueTime = int(startTime.Sub(targetQueueStart).Seconds())
	}

	r.addEvent("dial.queue.bridged", map[string]any{
		"queue":             dial.Queue,
		"target_call_sid":   targetCallSID,
		"dial_duration":     dialDuration,
		"target_queue_time": targetQueueTime,
	})

	// Call action callback with bridge results
	form := url.Values{}
	form.Set("DialCallStatus", "completed")
	form.Set("DialCallDuration", fmt.Sprintf("%d", dialDuration))
	form.Set("QueueResult", "bridged")
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", targetQueueTime))

	return r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL)
}

// waitInDialQueue waits in queue when no members are available
func (r *CallRunner) waitInDialQueue(ctx context.Context, dial *twiml.Dial, queue *model.Queue, queueSID model.SID, currentTwimlDocumentURL string) error {
	startTime := r.clock.Now()

	r.state.mu.Lock()
	// Add this call to the queue
	queue.Members = append(queue.Members, r.call.SID)
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		startTime,
		"member.joined",
		map[string]any{"call_sid": r.call.SID},
	))

	r.call.CurrentEndpoint = "queue:" + dial.Queue
	r.state.mu.Unlock()

	r.addEvent("dial.queue.joined", map[string]any{
		"queue":     dial.Queue,
		"queue_sid": queueSID,
	})

	// Dial Queue has a timeout (unlike Enqueue which waits indefinitely)
	// Wait for dequeue, timeout, or hangup
	dialStatus := ""
	queueResult := ""
	select {
	case <-ctx.Done():
		dialStatus = "canceled"
		queueResult = "system-shutdown"
	case <-r.hangupCh:
		dialStatus = "canceled"
		queueResult = "hangup"
	case result := <-r.dequeueCh:
		dialStatus = "completed"
		queueResult = result
	case <-r.clock.After(dial.Timeout):
		dialStatus = "no-answer"
		queueResult = "timeout"
		r.addEvent("dial.queue.timeout", map[string]any{})
	}

	// Calculate time in queue
	r.state.mu.Lock()
	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())
	r.removeFromQueue(queue)
	r.call.CurrentEndpoint = ""
	r.state.mu.Unlock()

	r.addEvent("dial.queue.left", map[string]any{
		"queue":        dial.Queue,
		"dial_status":  dialStatus,
		"queue_result": queueResult,
		"queue_time":   queueTime,
	})

	// Call action callback with dial results
	form := url.Values{}
	form.Set("DialCallStatus", dialStatus)
	form.Set("DialCallDuration", "0") // Duration of the dialed leg (0 for queue timeout)
	form.Set("QueueResult", queueResult)
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	return r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL)
}

func (r *CallRunner) executeDialConference(ctx context.Context, dial *twiml.Dial) error {
	conf := r.engine.getOrCreateConference(r.call.AccountSID, dial.Conference)

	r.state.mu.Lock()
	// Add participant
	conf.Participants = append(conf.Participants, r.call.SID)
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		r.clock.Now(),
		"participant.joined",
		map[string]any{"call_sid": r.call.SID},
	))

	// Update conference status
	if len(conf.Participants) >= 2 && conf.Status == model.ConferenceCreated {
		conf.Status = model.ConferenceInProgress
		conf.Timeline = append(conf.Timeline, model.NewEvent(
			r.clock.Now(),
			"conference.started",
			map[string]any{},
		))
	}

	r.call.CurrentEndpoint = "conference:" + dial.Conference
	r.state.mu.Unlock()

	r.addEvent("joined.conference", map[string]any{
		"conference": dial.Conference,
		"sid":        conf.SID,
	})

	// Wait until hangup
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		r.state.mu.Lock()
		r.removeFromConference(conf)
		r.state.mu.Unlock()
		return nil
	}
}

func (r *CallRunner) executeDialNumber(ctx context.Context, dial *twiml.Dial) error {
	// Create a child call leg
	target := dial.Number
	if dial.Client != "" {
		target = "client:" + dial.Client
	}

	r.addEvent("dial.number", map[string]any{
		"target":  target,
		"timeout": dial.Timeout.Seconds(),
	})

	// For MVP, just simulate the dial
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		return nil
	case <-r.clock.After(dial.Timeout):
		r.addEvent("dial.no_answer", map[string]any{})
	}

	return nil
}

func (r *CallRunner) executeEnqueue(ctx context.Context, enqueue *twiml.Enqueue, currentTwimlDocumentURL string) error {
	r.trackTwiML(enqueue)
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, enqueue.Name)
	queueSID := queue.SID

	// Check if there are waiting agents (from Dial Queue) to connect to
	var targetCallSID model.SID
	if len(queue.Members) > 0 {
		// Get the first waiting agent (FIFO)
		targetCallSID = queue.Members[0]
	}

	// Two scenarios:
	// 1. If there's a waiting agent, bridge with them immediately
	// 2. If no waiting agent, enqueue and wait indefinitely

	if targetCallSID != "" {
		// Scenario 1: Bridge with waiting agent immediately
		return r.bridgeEnqueueWithAgent(ctx, enqueue, queueSID, targetCallSID, currentTwimlDocumentURL)
	}

	// Scenario 2: No waiting agents, enqueue and wait
	return r.waitInEnqueue(ctx, enqueue, queueSID, currentTwimlDocumentURL)
}

// bridgeEnqueueWithAgent connects this enqueued call to a waiting agent
func (r *CallRunner) bridgeEnqueueWithAgent(ctx context.Context, enqueue *twiml.Enqueue, queueSID model.SID, agentCallSID model.SID, currentTwimlDocumentURL string) error {
	startTime := r.clock.Now()

	r.addEvent("enqueue.bridging", map[string]any{
		"queue":          enqueue.Name,
		"agent_call_sid": agentCallSID,
	})

	// Get the agent call's runner to dequeue it
	r.state.mu.Lock()
	agentRunner := r.state.runners[agentCallSID]
	r.state.mu.Unlock()

	// Signal the agent call to dequeue with "bridged" result
	if agentRunner != nil {
		select {
		case agentRunner.dequeueCh <- "bridged":
		default:
		}
	}

	// Simulate bridge - wait for hangup (no timeout for enqueued callers)
	select {
	case <-ctx.Done():
	case <-r.hangupCh:
	}

	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())

	r.addEvent("enqueue.bridged", map[string]any{
		"queue":          enqueue.Name,
		"agent_call_sid": agentCallSID,
		"queue_time":     queueTime,
	})

	// Call action callback with bridge results
	form := url.Values{}
	form.Set("QueueResult", "bridged")
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	return r.executeActionCallback(ctx, enqueue.Action, form, currentTwimlDocumentURL)
}

// waitInEnqueue waits in queue when no agents are available
func (r *CallRunner) waitInEnqueue(ctx context.Context, enqueue *twiml.Enqueue, queueSID model.SID, currentTwimlDocumentURL string) error {
	startTime := r.clock.Now()

	queue := r.engine.getOrCreateQueue(r.call.AccountSID, enqueue.Name)

	r.state.mu.Lock()
	// Add this call to the queue
	queue.Members = append(queue.Members, r.call.SID)
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		startTime,
		"member.enqueued",
		map[string]any{"call_sid": r.call.SID},
	))

	r.call.CurrentEndpoint = "queue:" + enqueue.Name
	r.state.mu.Unlock()

	r.addEvent("enqueued", map[string]any{
		"queue":     enqueue.Name,
		"queue_sid": queueSID,
		"wait_url":  enqueue.WaitURL,
		"action":    enqueue.Action,
	})

	// Wait indefinitely - no timeout for Enqueue (unlike Dial Queue)
	// The call stays in queue until:
	// 1. Another call dials the queue and bridges
	// 2. Caller hangs up
	// 3. Context is cancelled
	queueResult := ""
	select {
	case <-ctx.Done():
		queueResult = "system-shutdown"
	case <-r.hangupCh:
		queueResult = "hangup"
	case result := <-r.dequeueCh:
		queueResult = result
	}

	// Calculate time in queue
	r.state.mu.Lock()
	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())
	r.removeFromQueue(queue)
	r.call.CurrentEndpoint = ""
	r.state.mu.Unlock()

	r.addEvent("dequeued", map[string]any{
		"queue":        enqueue.Name,
		"queue_result": queueResult,
		"queue_time":   queueTime,
	})

	// Call action callback with queue results
	form := url.Values{}
	form.Set("QueueResult", queueResult)
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	return r.executeActionCallback(ctx, enqueue.Action, form, currentTwimlDocumentURL)
}

func (r *CallRunner) executeRedirect(ctx context.Context, redirect *twiml.Redirect) error {
	r.trackTwiML(redirect)
	r.addEvent("twiml.redirect", map[string]any{
		"url":    redirect.URL,
		"method": redirect.Method,
	})

	// Fetch and execute new TwiML
	resp, err := r.fetchTwiML(ctx, redirect.URL, url.Values{})
	if err != nil {
		return err
	}

	return r.executeTwiML(ctx, resp, redirect.URL)
}

func (r *CallRunner) executeRecord(ctx context.Context, record *twiml.Record, currentTwimlDocumentURL string, terminated *bool) error {
	r.trackTwiML(record)
	startTime := r.clock.Now()

	r.addEvent("twiml.record", map[string]any{
		"max_length": record.MaxLength.Seconds(),
		"play_beep":  record.PlayBeep,
		"action":     record.Action,
		"transcribe": record.Transcribe,
		"timeout":    record.TimeoutInSeconds.Seconds(),
	})

	r.state.mu.Lock()
	r.call.CurrentEndpoint = "recording"
	r.state.mu.Unlock()

	// Simulate beep if enabled
	if record.PlayBeep {
		r.addEvent("record.beep", map[string]any{})
	}

	// Wait for timeout, maxLength, or hangup
	// In a real implementation, this would wait for silence detection or user hangup
	var recordingDuration time.Duration
	var recordingStatus string

	select {
	case <-ctx.Done():
		recordingStatus = "canceled"
		recordingDuration = r.clock.Now().Sub(startTime)
	case <-r.hangupCh:
		recordingStatus = "completed"
		recordingDuration = r.clock.Now().Sub(startTime)
	case <-r.clock.After(record.TimeoutInSeconds):
		// Timeout waiting for speech
		recordingStatus = "absent"
		recordingDuration = 0
		r.addEvent("record.timeout", map[string]any{})
	case <-r.clock.After(record.MaxLength):
		// Max length reached
		recordingStatus = "completed"
		recordingDuration = record.MaxLength
		r.addEvent("record.max_length", map[string]any{})
	}

	r.state.mu.Lock()
	r.call.CurrentEndpoint = ""
	r.state.mu.Unlock()

	// Generate a fake recording SID
	recordingSID := model.NewRecordingSID()

	r.addEvent("record.completed", map[string]any{
		"recording_sid":      recordingSID,
		"recording_duration": recordingDuration.Seconds(),
		"recording_status":   recordingStatus,
	})

	// Call action callback with recording results
	form := url.Values{}
	form.Set("RecordingSid", string(recordingSID))
	form.Set("RecordingUrl", fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Recordings/%s", r.call.AccountSID, recordingSID))
	form.Set("RecordingStatus", recordingStatus)
	form.Set("RecordingDuration", fmt.Sprintf("%.0f", recordingDuration.Seconds()))

	if err := r.executeActionCallback(ctx, record.Action, form, currentTwimlDocumentURL); err != nil {
		return err
	}
	*terminated = true
	return nil
}

func (r *CallRunner) executeHangup(implicit bool) error {
	if !implicit {
		r.trackTwiML(&twiml.Hangup{})
	}
	r.addEvent("twiml.hangup", map[string]any{})
	r.updateStatus(model.CallCompleted)
	now := r.clock.Now()
	r.state.mu.Lock()
	r.call.EndedAt = &now
	r.state.mu.Unlock()
	return ErrCallHungup // Signal to stop execution
}

// Hangup signals the runner to hang up
func (r *CallRunner) Hangup() {
	select {
	case r.hangupCh <- struct{}{}:
	default:
	}
}

// SendDigits sends digits to the gather
func (r *CallRunner) SendDigits(digits string) {
	select {
	case r.gatherCh <- digits:
	default:
	}
}

func (r *CallRunner) updateStatus(status model.CallStatus) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.engine.updateCallStatusLocked(r.state, r.call, status)
}

func (r *CallRunner) addEvent(eventType string, detail map[string]any) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.call.Timeline = append(r.call.Timeline, model.NewEvent(
		r.clock.Now(),
		eventType,
		detail,
	))
}

func (r *CallRunner) trackTwiML(verb any) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.call.ExecutedTwiML = append(r.call.ExecutedTwiML, verb)
}

func (r *CallRunner) buildCallForm() url.Values {
	r.state.mu.RLock()
	defer r.state.mu.RUnlock()

	form := url.Values{}
	form.Set("CallSid", string(r.call.SID))
	form.Set("AccountSid", string(r.call.AccountSID))
	form.Set("From", r.call.From)
	form.Set("To", r.call.To)
	form.Set("CallStatus", string(r.call.Status))
	form.Set("Direction", string(r.call.Direction))
	form.Set("ApiVersion", r.engine.apiVersion)

	// Add custom variables
	for k, v := range r.call.Variables {
		form.Set(k, v)
	}

	return form
}

// executeActionCallback calls an action URL with the provided form parameters
func (r *CallRunner) executeActionCallback(ctx context.Context, actionURL string, form url.Values, currentTwimlDocumentURL string) error {
	if actionURL == "" {
		return nil
	}

	resolvedURL, err := r.resolveActionURL(currentTwimlDocumentURL, actionURL)
	if err != nil {
		r.addEvent("action.url_error", map[string]any{
			"action": actionURL,
			"base":   currentTwimlDocumentURL,
			"error":  err.Error(),
		})
		return err
	}

	resp, err := r.fetchTwiML(ctx, resolvedURL, form)
	if err != nil {
		return err
	}

	// If the action callback returns no TwiML instructions, hangup the call
	if len(resp.Children) == 0 {
		r.addEvent("action.empty_response", map[string]any{
			"message": "Action callback returned no TwiML instructions, hanging up call",
			"url":     resolvedURL,
		})
		return r.executeHangup(true)
	}

	return r.executeTwiML(ctx, resp, resolvedURL)
}

// resolveActionURL resolves an action URL relative to the current TwiML document URL
func (r *CallRunner) resolveActionURL(currentDocURL, actionURL string) (string, error) {
	target, err := url.Parse(actionURL)
	if err != nil {
		return "", fmt.Errorf("invalid action URL %q: %w", actionURL, err)
	}

	if target.IsAbs() {
		return target.String(), nil
	}

	if currentDocURL == "" {
		return "", fmt.Errorf("cannot resolve relative action URL %q without base", actionURL)
	}

	base, err := url.Parse(currentDocURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", currentDocURL, err)
	}

	return base.ResolveReference(target).String(), nil
}

func (r *CallRunner) removeFromQueue(queue *model.Queue) {
	for i, sid := range queue.Members {
		if sid == r.call.SID {
			queue.Members = append(queue.Members[:i], queue.Members[i+1:]...)
			queue.Timeline = append(queue.Timeline, model.NewEvent(
				r.clock.Now(),
				"member.left",
				map[string]any{"call_sid": r.call.SID},
			))
			break
		}
	}
}

func (r *CallRunner) removeFromConference(conf *model.Conference) {
	for i, sid := range conf.Participants {
		if sid == r.call.SID {
			conf.Participants = append(conf.Participants[:i], conf.Participants[i+1:]...)
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				r.clock.Now(),
				"participant.left",
				map[string]any{"call_sid": r.call.SID},
			))

			// If last participant, mark conference completed
			if len(conf.Participants) == 0 {
				conf.Status = model.ConferenceCompleted
				now := r.clock.Now()
				conf.EndedAt = &now
				conf.Timeline = append(conf.Timeline, model.NewEvent(
					now,
					"conference.ended",
					map[string]any{},
				))
			}
			break
		}
	}

	r.call.CurrentEndpoint = ""
}
