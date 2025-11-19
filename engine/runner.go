// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sprucehealth/twimulator/model"
	"github.com/sprucehealth/twimulator/twiml"
)

// ErrCallHungup is returned when a Hangup verb is executed
var ErrCallHungup = errors.New("call hungup via Hangup verb")

// ErrURLUpdated is returned when the call URL is updated during execution
var ErrURLUpdated = errors.New("call URL updated")

// dequeueResult contains information about a dequeue event
type dequeueResult struct {
	result     string    // "bridged", "hangup", etc.
	partnerSID model.SID // SID of the bridged partner (if bridged)
}

// CallRunner executes TwiML for a call
type CallRunner struct {
	call    *model.Call
	clock   Clock
	state   *subAccountState
	engine  *EngineImpl
	timeout time.Duration

	// State for gather
	gatherCh             chan string
	hangupCh             chan struct{}
	hangupOnce           sync.Once // Ensures hangupCh is closed only once
	answerCh             chan struct{}
	busyCh               chan struct{}
	failedCh             chan struct{}
	dequeueCh            chan dequeueResult // for explicit dequeue with result and partner info
	urlUpdateCh          chan string        // signals URL update with new URL
	conferenceCompleteCh chan struct{}      // signals conference completion via API
	bridgeEndCh          chan struct{}      // signals bridge partner has hung up
	done                 chan struct{}
}

// NewCallRunner creates a new call runner
func NewCallRunner(call *model.Call, state *subAccountState, engine *EngineImpl, timeout time.Duration) *CallRunner {
	return &CallRunner{
		call:                 call,
		clock:                state.clock,
		state:                state,
		engine:               engine,
		timeout:              timeout,
		gatherCh:             make(chan string, 1),
		hangupCh:             make(chan struct{}), // No buffer - will be closed to broadcast
		answerCh:             make(chan struct{}, 1),
		busyCh:               make(chan struct{}, 1),
		failedCh:             make(chan struct{}, 1),
		dequeueCh:            make(chan dequeueResult, 1),
		urlUpdateCh:          make(chan string, 1),
		conferenceCompleteCh: make(chan struct{}, 1),
		bridgeEndCh:          make(chan struct{}, 1),
		done:                 make(chan struct{}),
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
	if r.call.Direction != model.Inbound && r.call.Status != model.CallInProgress {
		// an outbound call is answered first, and then it's url is fetched
		answerNow()
	}

	// Main execution loop - allows for URL updates during execution
	for {
		// Get current URL from call state
		r.state.mu.Lock()
		currentURL := r.call.Url
		currentMethod := r.call.Method
		r.state.mu.Unlock()

		// Fetch TwiML
		values := url.Values{}
		for k, v := range r.call.InitialParams {
			values.Set(k, v)
		}
		// clear initial params
		r.call.InitialParams = nil
		twimlResp, err := r.fetchTwiML(ctx, currentMethod, currentURL, values)
		if err != nil {
			log.Printf("Failed to fetch Url for call %s: %v", r.call.SID, err)
			r.recordError(err)
			r.updateStatus(model.CallFailed)
			return
		}
		if r.call.Direction == model.Inbound && r.call.Status != model.CallInProgress {
			// backend answers the inbound call when twiml is fetched
			answerNow()
		}

		// Execute TwiML
		err = r.executeTwiML(ctx, twimlResp, currentURL, false)
		if err != nil {
			// Check if this is a normal hangup or an actual error
			if errors.Is(err, ErrCallHungup) {
				// Normal hangup via <Hangup/> verb - call already completed by executeHangup
				return
			}
			// Check if URL was updated - if so, loop to fetch new TwiML
			if errors.Is(err, ErrURLUpdated) {
				r.addCallEvent("call.url_updated", map[string]any{"message": "Fetching new TwiML from updated URL"})
				continue
			}
			// Actual error - mark call as failed
			log.Printf("TwiML execution error for call %s: %v", r.call.SID, err)
			r.recordError(err)
			r.updateStatus(model.CallFailed)
			return
		}
		// if we reach here, the call is completed
		r.Hangup()
		r.addCallEvent("call.completed.no_more_twiml", map[string]any{})

		// TwiML execution completed, wait for hangup or URL update
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
		case <-r.urlUpdateCh:
			// URL updated after TwiML completion, fetch new TwiML
			r.addCallEvent("call.url_updated", map[string]any{"message": "Fetching new TwiML from updated URL"})
			continue
		}
	}
}

func (r *CallRunner) fetchTwiML(ctx context.Context, method, targetURL string, form url.Values) (*twiml.Response, error) {
	// Build form with call parameters
	callForm := r.buildCallForm()
	for k, v := range form {
		callForm[k] = v
	}

	// Log webhook request
	r.addCallEvent("webhook.request", map[string]any{
		"url":  targetURL,
		"form": callForm,
	})

	// Make request
	reqCtx, cancel := context.WithTimeout(ctx, r.engine.timeout)
	defer cancel()

	var status int
	var body []byte
	var headers http.Header
	var err error
	if method == "GET" {
		status, body, headers, err = r.engine.webhook.GET(reqCtx, targetURL)
	} else {
		status, body, headers, err = r.engine.webhook.POST(reqCtx, targetURL, callForm)
	}
	if err != nil {
		r.addCallEvent("webhook.error", map[string]any{
			"url":   targetURL,
			"error": err.Error(),
		})
		return nil, fmt.Errorf("webhook request failed: %w", err)
	}

	// Log response
	r.addCallEvent("webhook.response", map[string]any{
		"url":     targetURL,
		"status":  status,
		"headers": headers,
		"body":    string(body),
	})

	// Parse TwiML
	resp, err := twiml.Parse(body)
	if err != nil {
		r.addCallEvent("twiml.parse_error", map[string]any{
			"error": err.Error(),
			"body":  string(body),
		})
		return nil, fmt.Errorf("failed to parse TwiML: %w", err)
	}

	return resp, nil
}

func (r *CallRunner) executeTwiML(ctx context.Context, resp *twiml.Response, currentTwimlDocumentURL string, executingWaitTwiml bool) error {
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
		if err := r.executeNode(ctx, node, currentTwimlDocumentURL, &terminated, executingWaitTwiml); err != nil {
			return err
		}
	}
	return nil
}

func (r *CallRunner) executeNode(ctx context.Context, node twiml.Node, currentTwimlDocumentURL string, terminated *bool, executingWaitTwiml bool) error {
	switch n := node.(type) {
	case *twiml.Say:
		return r.executeSay(ctx, n, false, executingWaitTwiml)
	case *twiml.Play:
		return r.executePlay(ctx, n, false, executingWaitTwiml)
	case *twiml.Pause:
		return r.executePause(ctx, n, false)
	case *twiml.Gather:
		return r.executeGather(ctx, n, currentTwimlDocumentURL, terminated)
	case *twiml.Dial:
		return r.executeDial(ctx, n, currentTwimlDocumentURL)
	case *twiml.Enqueue:
		return r.executeEnqueue(ctx, n, currentTwimlDocumentURL)
	case *twiml.Redirect:
		return r.executeRedirect(ctx, n, currentTwimlDocumentURL, executingWaitTwiml)
	case *twiml.Record:
		return r.executeRecord(ctx, n, currentTwimlDocumentURL, terminated)
	case *twiml.Hangup:
		return r.executeHangup(false)
	default:
		msg := fmt.Sprintf("Unknown TwiML node type: %T", node)
		err := errors.New(msg)
		r.recordError(err)
		log.Printf("ERROR: %s", err)
		r.addCallEvent("twiml.invalid_node", map[string]any{"node": msg})
	}
	return nil
}

func (r *CallRunner) executeSay(ctx context.Context, say *twiml.Say, skipTracking bool, executingWaitTwiml bool) error {
	if !skipTracking {
		r.trackCallTwiML(say)
	}
	r.addCallEvent("twiml.say", map[string]any{
		"text":     say.Text,
		"voice":    say.Voice,
		"language": say.Language,
	})
	if say.Loop == 0 && !executingWaitTwiml {
		return r.busyLoop(ctx, "say")
	}
	return nil
}

func (r *CallRunner) executePlay(ctx context.Context, play *twiml.Play, skipTracking bool, executingWaitTwiml bool) error {
	// Trim whitespace and newlines from URL
	playURL := strings.TrimSpace(play.URL)
	play.URL = playURL
	if !skipTracking {
		r.trackCallTwiML(play)
	}

	// Log the play attempt
	r.addCallEvent("twiml.play", map[string]any{
		"url": playURL,
	})

	// Check the media URL to ensure it's accessible (using HEAD to avoid downloading the entire file)
	reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	status, _, err := r.engine.webhook.HEAD(reqCtx, playURL)
	if err != nil {
		r.addCallEvent("play.error", map[string]any{
			"url":   playURL,
			"error": err.Error(),
		})
		err := fmt.Errorf("failed to check play URL %s: %w", playURL, err)
		r.recordError(err)
		return err
	}

	// Check for non-2xx status codes
	if status < 200 || status >= 300 {
		r.addCallEvent("play.error", map[string]any{
			"url":    playURL,
			"status": status,
		})
		err := fmt.Errorf("play URL %s returned status %d", playURL, status)
		r.recordError(err)
		return err
	}

	r.addCallEvent("play.success", map[string]any{
		"url":    playURL,
		"status": status,
	})
	if play.Loop == 0 && !executingWaitTwiml {
		return r.busyLoop(ctx, "play")
	}
	return nil
}

func (r *CallRunner) busyLoop(ctx context.Context, verbName string) error {
	//return nil
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.hangupCh:
			return nil
		case <-r.urlUpdateCh:
			// URL updated, skip through gather
			r.addCallEvent(fmt.Sprintf("%s.interrupted", verbName), map[string]any{"reason": "url_updated"})
			return ErrURLUpdated
		}
	}
}

func (r *CallRunner) executePause(ctx context.Context, pause *twiml.Pause, skipTracking bool) error {
	if !skipTracking {
		r.trackCallTwiML(pause)
	}
	r.addCallEvent("twiml.pause", map[string]any{
		"length": pause.Length.Seconds(),
	})

	//pauseNow := func() error {
	//	// Use the clock-aware After which respects Advance() calls
	//	timer := r.clock.After(pause.Length)
	//
	//	// Wait for either the timer to expire, or an interrupt
	//	select {
	//	case <-ctx.Done():
	//		return ctx.Err()
	//	case <-r.hangupCh:
	//		return nil
	//	case <-r.urlUpdateCh:
	//		r.addCallEvent("pause.interrupted", map[string]any{"reason": "url_updated"})
	//		return ErrURLUpdated
	//	case <-timer:
	//		// Pause completed normally
	//		return nil
	//	}
	//}
	////for now we don't perform real pause to keep things predictable
	//return pauseNow()
	return nil
}

func (r *CallRunner) executeGather(ctx context.Context, gather *twiml.Gather, currentTwimlDocumentURL string, terminated *bool) error {
	r.trackCallTwiML(gather)

	// Parse timeout: can be "auto" or a numeric value
	// When "auto", default is 5 seconds
	timeout := 5 * time.Second // default
	if gather.Timeout == "auto" {
		timeout = 5 * time.Second
	} else if gather.Timeout != "" {
		if n, err := strconv.Atoi(gather.Timeout); err == nil {
			timeout = time.Duration(n) * time.Second
		}
	}

	r.addCallEvent("twiml.gather", map[string]any{
		"input":         gather.Input,
		"timeout":       timeout.Seconds(),
		"num_digits":    gather.NumDigits,
		"finish_on_key": gather.FinishOnKey,
		"action":        gather.Action,
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
			if err := r.executeSay(ctx, n, true, false); err != nil {
				return err
			}
		case *twiml.Play:
			if err := r.executePlay(ctx, n, true, false); err != nil {
				return err
			}
		case *twiml.Pause:
			if err := r.executePause(ctx, n, true); err != nil {
				return err
			}
		default:
			nodeType := fmt.Sprintf("%T", child)
			r.addCallEvent("gather.invalid_child", map[string]any{"node": nodeType})
			return fmt.Errorf("gather cannot contain %s", nodeType)
		}
	}

	if *terminated {
		return nil
	}

	// Collect digits one by one until:
	// - finishOnKey is pressed (if set)
	// - numDigits is reached (if > 0)
	// - timeout occurs
	// - hangup or context cancellation
	var collectedDigits string
	var urlUpdated bool
	timeoutTimer := r.clock.After(timeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.hangupCh:
			return nil
		case <-r.urlUpdateCh:
			// URL updated, skip through gather
			r.addCallEvent("gather.interrupted", map[string]any{"reason": "url_updated"})
			urlUpdated = true
			goto gatherComplete
		case digits := <-r.gatherCh:
			// Got one or more digits - process each character individually
			for _, char := range digits {
				digit := string(char)

				// Check if it's the finish key
				if gather.FinishOnKey != "" && digit == gather.FinishOnKey {
					r.addCallEvent("gather.finish_key_pressed", map[string]any{
						"collected_digits": collectedDigits,
						"finish_key":       digit,
						"all_digits":       digits,
					})
					goto gatherComplete
				}

				// Not a finish key, add to collected digits
				collectedDigits += digit
				r.addCallEvent("gather.digit_received", map[string]any{
					"digit":            digit,
					"collected_digits": collectedDigits,
					"target_digits":    gather.NumDigits,
					"all_digits":       digits,
				})

				// Check if we've reached numDigits
				if gather.NumDigits > 0 && len(collectedDigits) >= gather.NumDigits {
					r.addCallEvent("gather.num_digits_reached", map[string]any{
						"collected_digits": collectedDigits,
						"num_digits":       gather.NumDigits,
						"all_digits":       digits,
					})
					goto gatherComplete
				}
			}

			// If finishOnKey is empty and numDigits is 0, continue collecting
			// (will only stop on timeout or interruption)

		case <-timeoutTimer:
			// Timeout
			r.addCallEvent("gather.timeout", map[string]any{
				"collected_digits": collectedDigits,
			})
			goto gatherComplete
		}
	}

gatherComplete:
	// If URL was updated, skip action callback entirely
	if urlUpdated {
		return ErrURLUpdated
	}

	if collectedDigits == "" {
		return nil
	}

	r.state.mu.Lock()
	r.call.CurrentEndpoint = ""
	r.call.Variables["Digits"] = collectedDigits
	r.state.mu.Unlock()

	// Call action callback with gathered digits
	form := url.Values{}
	form.Set("Digits", collectedDigits)

	return r.executeActionCallback(ctx, gather.Method, gather.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDial(ctx context.Context, dial *twiml.Dial, currentTwimlDocumentURL string) error {
	r.trackCallTwiML(dial)

	var queueDial *twiml.Queue
	var conferenceDial *twiml.Conference
	var numbers []*twiml.Number
	var clients []*twiml.Client
	var sips []*twiml.Sip
	for _, child := range dial.Children {
		switch n := child.(type) {
		case *twiml.Number:
			numbers = append(numbers, n)
		case *twiml.Client:
			clients = append(clients, n)
		case *twiml.Sip:
			sips = append(sips, n)
		case *twiml.Queue:
			if queueDial != nil {
				return fmt.Errorf("dial cannot contain more than one queue")
			}
			queueDial = n
		case *twiml.Conference:
			if conferenceDial != nil {
				return fmt.Errorf("dial cannot contain more than one conference")
			}
			conferenceDial = n
		}
	}
	if queueDial != nil && conferenceDial != nil {
		return fmt.Errorf("dial cannot contain both queue and conference")
	}

	r.addCallEvent("twiml.dial", map[string]any{
		"number":     numbers,
		"client":     clients,
		"queue":      queueDial,
		"conference": conferenceDial,
		"timeout":    dial.Timeout.Seconds(),
	})

	// Handle different dial targets
	if queueDial != nil {
		return r.executeDialQueue(ctx, dial, queueDial, currentTwimlDocumentURL)
	}
	if conferenceDial != nil {
		return r.executeDialConference(ctx, dial, conferenceDial, currentTwimlDocumentURL)
	}
	if len(numbers) > 0 || len(clients) > 0 || len(sips) > 0 {
		return r.executeDialNumber(ctx, dial, numbers, clients, sips)
	}

	return nil
}

func (r *CallRunner) executeDialQueue(ctx context.Context, dial *twiml.Dial, queueDial *twiml.Queue, currentTwimlDocumentURL string) error {
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, queueDial.Name)
	queueSID := queue.SID

	r.state.mu.RLock()
	// Check if there are waiting members to connect to
	var targetCallSID model.SID
	if len(queue.Members) > 0 {
		// Get the first waiting caller (FIFO)
		targetCallSID = queue.Members[0]
	}
	r.state.mu.RUnlock()
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

	r.addCallEvent("dial.queue.bridging", map[string]any{
		"queue":           queue.Name,
		"target_call_sid": targetCallSID,
		"hangupOnStar":    dial.HangupOnStar,
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

	// Signal the target call to dequeue with "bridged" result and partner SID
	if targetRunner != nil {
		select {
		case targetRunner.dequeueCh <- dequeueResult{result: "bridged", partnerSID: r.call.SID}:
		default:
		}
	}

	// Bridge is established - wait until either call hangs up (no timeout during bridge)
	var dialDuration int
	urlUpdated := false
	if dial.HangupOnStar {
		// Listen for star key to hangup during bridge
		for {
			select {
			case <-ctx.Done():
				goto bridgeEnded
			case <-r.hangupCh:
				// This call hung up, notify the target
				if targetRunner != nil {
					select {
					case targetRunner.bridgeEndCh <- struct{}{}:
					default:
					}
				}
				goto bridgeEnded
			case <-r.bridgeEndCh:
				// Target call hung up, end this bridge
				r.addCallEvent("dial.queue.partner_hangup", map[string]any{})
				goto bridgeEnded
			case <-r.urlUpdateCh:
				urlUpdated = true
				r.addCallEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
				// Notify the target before leaving
				if targetRunner != nil {
					select {
					case targetRunner.bridgeEndCh <- struct{}{}:
					default:
					}
				}
				goto bridgeEnded
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addCallEvent("dial.hangup_on_star", map[string]any{
						"digits": digits,
					})
					// Notify the target
					if targetRunner != nil {
						select {
						case targetRunner.bridgeEndCh <- struct{}{}:
						default:
						}
					}
					goto bridgeEnded
				}
			}
		}
	} else {
		// Wait for bridge to end (no timeout)
		select {
		case <-ctx.Done():
		case <-r.hangupCh:
			// This call hung up, notify the target
			if targetRunner != nil {
				select {
				case targetRunner.bridgeEndCh <- struct{}{}:
				default:
				}
			}
		case <-r.bridgeEndCh:
			// Target call hung up, end this bridge
			r.addCallEvent("dial.queue.partner_hangup", map[string]any{})
		case <-r.urlUpdateCh:
			urlUpdated = true
			r.addCallEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
			// Notify the target before leaving
			if targetRunner != nil {
				select {
				case targetRunner.bridgeEndCh <- struct{}{}:
				default:
				}
			}
		}
	}

bridgeEnded:
	endTime := r.clock.Now()
	dialDuration = int(endTime.Sub(startTime).Seconds())
	targetQueueTime := 0
	if !targetQueueStart.IsZero() {
		targetQueueTime = int(startTime.Sub(targetQueueStart).Seconds())
	}

	r.addCallEvent("dial.queue.left", map[string]any{
		"queue":             queue.Name,
		"target_call_sid":   targetCallSID,
		"dial_duration":     dialDuration,
		"target_queue_time": targetQueueTime,
	})

	// For queued calls, recording is always on the enqueued call
	r.invokeRecordingCallback(ctx, dial, nil, targetCallSID, currentTwimlDocumentURL)

	// Call action callback with bridge results
	form := url.Values{}
	form.Set("DialCallStatus", "completed")
	form.Set("DialCallDuration", fmt.Sprintf("%d", dialDuration))
	form.Set("QueueResult", "bridged")
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", targetQueueTime))

	// If URL was updated, skip TwiML execution
	if urlUpdated {
		if err := r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, true); err != nil {
			r.recordError(err)
		}
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, false)
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

	r.call.CurrentEndpoint = "queue:" + queue.Name
	r.state.mu.Unlock()

	r.addCallEvent("dial.queue.joined", map[string]any{
		"queue":        queue.Name,
		"queue_sid":    queueSID,
		"hangupOnStar": dial.HangupOnStar,
	})

	// Dial Queue has a timeout (unlike Enqueue which waits indefinitely)
	// Wait for dequeue, timeout, or hangup
	dialStatus := ""
	queueResult := ""
	var bridgePartnerSID model.SID
	urlUpdated := false
	if dial.HangupOnStar {
		// Listen for star key to hangup
		for {
			select {
			case <-ctx.Done():
				dialStatus = "canceled"
				queueResult = "system-shutdown"
				goto queueLeft
			case <-r.hangupCh:
				dialStatus = "canceled"
				queueResult = "hangup"
				goto queueLeft
			case <-r.urlUpdateCh:
				dialStatus = "canceled"
				queueResult = "url-updated"
				urlUpdated = true
				r.addCallEvent("dial.queue.interrupted", map[string]any{"reason": "url_updated"})
				goto queueLeft
			case dqResult := <-r.dequeueCh:
				dialStatus = "completed"
				queueResult = dqResult.result
				bridgePartnerSID = dqResult.partnerSID
				goto queueLeft
			case <-r.clock.After(dial.Timeout):
				dialStatus = "no-answer"
				queueResult = "timeout"
				r.addCallEvent("dial.queue.timeout", map[string]any{})
				goto queueLeft
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addCallEvent("dial.hangup_on_star", map[string]any{
						"digits": digits,
					})
					queueResult = "hangup-on-star"
					goto queueLeft
				}
			}
		}
	} else {
		select {
		case <-ctx.Done():
			dialStatus = "canceled"
			queueResult = "system-shutdown"
		case <-r.hangupCh:
			dialStatus = "canceled"
			queueResult = "hangup"
		case <-r.urlUpdateCh:
			dialStatus = "canceled"
			queueResult = "url-updated"
			urlUpdated = true
			r.addCallEvent("dial.queue.interrupted", map[string]any{"reason": "url_updated"})
		case dqResult := <-r.dequeueCh:
			dialStatus = "completed"
			queueResult = dqResult.result
			bridgePartnerSID = dqResult.partnerSID
		case <-r.clock.After(dial.Timeout):
			dialStatus = "no-answer"
			queueResult = "timeout"
			r.addCallEvent("dial.queue.timeout", map[string]any{})
		}
	}

queueLeft:

	// Calculate time in queue (time waiting before bridge)
	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())

	r.state.mu.Lock()
	r.removeFromQueue(queue)
	r.call.CurrentEndpoint = ""
	r.state.mu.Unlock()

	r.addCallEvent("dial.queue.left", map[string]any{
		"queue":        queue.Name,
		"dial_status":  dialStatus,
		"queue_result": queueResult,
		"queue_time":   queueTime,
	})

	// If bridged, wait for bridge to complete before calling action
	// Per Twilio behavior: Dial action is called when the call ends, not when connection starts
	dialDuration := 0
	if queueResult == "bridged" {
		bridgeStartTime := r.clock.Now()
		r.addCallEvent("dial.queue.bridge_started", map[string]any{
			"queue":       queue.Name,
			"partner_sid": bridgePartnerSID,
		})

		// Get the bridge partner runner to notify them when we hang up
		r.state.mu.Lock()
		partnerRunner := r.state.runners[bridgePartnerSID]
		r.state.mu.Unlock()

		// Wait for bridge to complete (no timeout during bridge)
		if dial.HangupOnStar {
			// Listen for star key to hangup during bridge
			for {
				select {
				case <-ctx.Done():
					goto bridgeEnded
				case <-r.hangupCh:
					// This call hung up, notify the partner
					if partnerRunner != nil {
						select {
						case partnerRunner.bridgeEndCh <- struct{}{}:
						default:
						}
					}
					goto bridgeEnded
				case <-r.bridgeEndCh:
					// Partner call hung up, end this bridge
					r.addCallEvent("dial.queue.partner_hangup", map[string]any{})
					goto bridgeEnded
				case <-r.urlUpdateCh:
					urlUpdated = true
					r.addCallEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
					// Notify partner before leaving
					if partnerRunner != nil {
						select {
						case partnerRunner.bridgeEndCh <- struct{}{}:
						default:
						}
					}
					goto bridgeEnded
				case digits := <-r.gatherCh:
					// Check if star is pressed
					if strings.Contains(digits, "*") {
						r.addCallEvent("dial.hangup_on_star", map[string]any{
							"digits": digits,
						})
						// Notify partner before leaving
						if partnerRunner != nil {
							select {
							case partnerRunner.bridgeEndCh <- struct{}{}:
							default:
							}
						}
						goto bridgeEnded
					}
				}
			}
		} else {
			select {
			case <-ctx.Done():
			case <-r.hangupCh:
				// This call hung up, notify the partner
				if partnerRunner != nil {
					select {
					case partnerRunner.bridgeEndCh <- struct{}{}:
					default:
					}
				}
			case <-r.bridgeEndCh:
				// Partner call hung up, end this bridge
				r.addCallEvent("dial.queue.partner_hangup", map[string]any{})
			case <-r.urlUpdateCh:
				urlUpdated = true
				r.addCallEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
				// Notify partner before leaving
				if partnerRunner != nil {
					select {
					case partnerRunner.bridgeEndCh <- struct{}{}:
					default:
					}
				}
			}
		}

	bridgeEnded:
		bridgeEndTime := r.clock.Now()
		dialDuration = int(bridgeEndTime.Sub(bridgeStartTime).Seconds())

		r.addCallEvent("dial.queue.bridge_completed", map[string]any{
			"queue":         queue.Name,
			"dial_duration": dialDuration,
		})
	}

	// For queued calls, recording is always on the enqueued call
	r.invokeRecordingCallback(ctx, dial, nil, bridgePartnerSID, currentTwimlDocumentURL)

	// Call action callback with dial results
	form := url.Values{}
	form.Set("DialCallStatus", dialStatus)
	form.Set("DialCallDuration", fmt.Sprintf("%d", dialDuration))
	form.Set("QueueResult", queueResult)
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	// If URL was updated, skip action callback entirely
	if urlUpdated {
		if err := r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, true); err != nil {
			r.recordError(err)
		}
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDialConference(ctx context.Context, dial *twiml.Dial, conference *twiml.Conference, currentTwimlDocumentURL string) error {
	conf := r.engine.getOrCreateConference(r.call.AccountSID, conference)
	if err := r.executeWait(ctx, "conference", conference.WaitURL, conference.WaitMethod, currentTwimlDocumentURL); err != nil {
		return err
	}
	r.state.mu.Lock()
	// Add participant
	conf.Participants = append(conf.Participants, r.call.SID)
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		r.clock.Now(),
		"participant.joined",
		map[string]any{"call_sid": r.call.SID},
	))

	// Store participant attributes
	if r.state.participantStates[conf.SID] == nil {
		r.state.participantStates[conf.SID] = make(map[model.SID]*model.ParticipantState)
	}
	partState := r.state.participantStates[conf.SID][r.call.SID]
	if partState == nil {
		partState = &model.ParticipantState{}
		r.state.participantStates[conf.SID][r.call.SID] = partState
	}
	partState.StartConferenceOnEnter = conference.StartConferenceOnEnter
	partState.EndConferenceOnExit = conference.EndConferenceOnExit

	// Check if conference should start
	// Conference starts if it has at least 2 participants and at least one has StartConferenceOnEnter=true
	shouldStart := false
	if len(conf.Participants) >= 2 && conf.Status == model.ConferenceCreated {
		// Check if at least one participant has StartConferenceOnEnter=true
		for _, participantSID := range conf.Participants {
			if ps := r.state.participantStates[conf.SID][participantSID]; ps != nil && ps.StartConferenceOnEnter {
				shouldStart = true
				break
			}
		}
		if shouldStart {
			conf.Status = model.ConferenceInProgress
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				r.clock.Now(),
				"conference.started",
				map[string]any{},
			))
		}
	}

	r.call.CurrentEndpoint = "conference:" + conference.Name

	// Send status callbacks if configured
	if conf.StatusCallback != "" {
		// Send participant-join callback
		if r.engine.shouldSendConferenceStatusCallback(conf, "participant-join") {
			// Queue the callback for serial execution
			callSID := r.call.SID
			conf.CallbackQueue <- func() {
				r.engine.sendConferenceStatusCallback(r.state, conf, "participant-join", &callSID, currentTwimlDocumentURL)
			}
		} else {
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				r.clock.Now(),
				"webhook.conference_status_callback_skipped",
				map[string]any{
					"event":    "participant-join",
					"call_sid": r.call.SID,
				},
			))
		}

		// Send conference-start callback if conference just started
		if shouldStart {
			if r.engine.shouldSendConferenceStatusCallback(conf, "conference-start") {
				// Queue the callback for serial execution
				conf.CallbackQueue <- func() {
					r.engine.sendConferenceStatusCallback(r.state, conf, "conference-start", nil, currentTwimlDocumentURL)
				}
			} else {
				conf.Timeline = append(conf.Timeline, model.NewEvent(
					r.clock.Now(),
					"webhook.conference_status_callback_skipped",
					map[string]any{
						"event": "conference-start",
					},
				))
			}
		}
	}

	r.state.mu.Unlock()

	r.addCallEvent("joined.conference", map[string]any{
		"conference":   conference.Name,
		"sid":          conf.SID,
		"hangupOnStar": dial.HangupOnStar,
	})

	// Wait until hangup or leave conference
	urlUpdated := false
	if dial.HangupOnStar {
		// Listen for star key to leave conference
		for {
			select {
			case <-ctx.Done():
				goto conferenceEnded
			case <-r.hangupCh:
				goto conferenceEnded
			case <-r.urlUpdateCh:
				urlUpdated = true
				r.addCallEvent("dial.conference.interrupted", map[string]any{"reason": "url_updated"})
				goto conferenceEnded
			case <-r.conferenceCompleteCh:
				r.addCallEvent("dial.conference.completed", map[string]any{"reason": "completed_via_api"})
				goto conferenceEnded
			case digits := <-r.gatherCh:
				// Check if star is pressed by caller to leave conference
				if strings.Contains(digits, "*") {
					r.addCallEvent("dial.hangup_on_star", map[string]any{
						"digits": digits,
					})
					goto conferenceEnded
				}
			}
		}
	} else {
		select {
		case <-ctx.Done():
			goto conferenceEnded
		case <-r.hangupCh:
			goto conferenceEnded
		case <-r.urlUpdateCh:
			urlUpdated = true
			r.addCallEvent("dial.conference.interrupted", map[string]any{"reason": "url_updated"})
			goto conferenceEnded
		case <-r.conferenceCompleteCh:
			r.addCallEvent("dial.conference.completed", map[string]any{"reason": "completed_via_api"})
			goto conferenceEnded
		}
	}
conferenceEnded:
	r.state.mu.Lock()
	r.removeFromConference(conf, currentTwimlDocumentURL)
	r.state.mu.Unlock()

	// If recording was enabled and a recording was set, invoke RecordingStatusCallback
	r.invokeRecordingCallback(ctx, dial, conference, r.call.SID, currentTwimlDocumentURL)

	// Call action callback
	r.state.mu.RLock()
	form := url.Values{}
	form.Set("DialCallStatus", "answered")
	form.Set("DialBridged", "true")
	form.Set("ConferenceSid", conf.SID.String())
	form.Set("FriendlyName", conf.Name)
	form.Set("ConferenceStatus", string(conf.Status))
	r.state.mu.RUnlock()
	if urlUpdated {
		if err := r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, true); err != nil {
			r.recordError(err)
		}
		return ErrURLUpdated
	}
	return r.executeActionCallback(ctx, dial.Method, dial.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDialNumber(ctx context.Context, dial *twiml.Dial, numbers []*twiml.Number, clients []*twiml.Client, sips []*twiml.Sip) error {
	// TODO finish this implementation
	// Create child call leg
	r.addCallEvent("dial.number", map[string]any{
		"numbers":      numbers,
		"clients":      clients,
		"sips":         sips,
		"timeout":      dial.Timeout.Seconds(),
		"hangupOnStar": dial.HangupOnStar,
	})

	// For MVP, just simulate the dial
	if dial.HangupOnStar {
		// Listen for star key to hangup
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-r.hangupCh:
				return nil
			case <-r.clock.After(dial.Timeout):
				r.addCallEvent("dial.no_answer", map[string]any{})
				return nil
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addCallEvent("dial.hangup_on_star", map[string]any{
						"digits": digits,
					})
					return r.executeHangup(false)
				}
			}
		}
	} else {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.hangupCh:
			return nil
		case <-r.clock.After(dial.Timeout):
			r.addCallEvent("dial.no_answer", map[string]any{})
		}
	}

	return nil
}

func (r *CallRunner) executeEnqueue(ctx context.Context, enqueue *twiml.Enqueue, currentTwimlDocumentURL string) error {
	r.trackCallTwiML(enqueue)
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, enqueue.Name)
	queueSID := queue.SID

	if err := r.executeWait(ctx, "enqueue", enqueue.WaitURL, enqueue.WaitURLMethod, currentTwimlDocumentURL); err != nil {
		return err
	}
	// Two scenarios:
	// 1. If there's a waiting agent, bridge with them immediately
	// 2. If no waiting agent, enqueue and wait indefinitely

	r.state.mu.RLock()
	// Check if there are waiting agents (from Dial Queue) to connect to
	var targetCallSID model.SID
	if len(queue.Members) > 0 {
		// Get the first waiting agent (FIFO)
		targetCallSID = queue.Members[0]
	}
	r.state.mu.RUnlock()
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

	r.addCallEvent("enqueue.bridging", map[string]any{
		"queue":          enqueue.Name,
		"agent_call_sid": agentCallSID,
	})

	// Get the agent call's runner to dequeue it
	r.state.mu.Lock()
	agentRunner := r.state.runners[agentCallSID]
	r.state.mu.Unlock()

	// Signal the agent call to dequeue with "bridged" result and partner SID
	if agentRunner != nil {
		select {
		case agentRunner.dequeueCh <- dequeueResult{result: "bridged", partnerSID: r.call.SID}:
		default:
		}
	}

	// Bridge - wait for hangup (no timeout for enqueued callers)
	urlUpdated := false
	select {
	case <-ctx.Done():
	case <-r.hangupCh:
		// This call hung up, notify the agent
		if agentRunner != nil {
			select {
			case agentRunner.bridgeEndCh <- struct{}{}:
			default:
			}
		}
	case <-r.bridgeEndCh:
		// Agent call hung up, end this bridge
		r.addCallEvent("enqueue.partner_hangup", map[string]any{})
	case <-r.urlUpdateCh:
		urlUpdated = true
		r.addCallEvent("enqueue.bridge_interrupted", map[string]any{"reason": "url_updated"})
		// Notify the agent before leaving
		if agentRunner != nil {
			select {
			case agentRunner.bridgeEndCh <- struct{}{}:
			default:
			}
		}
	}

	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())

	r.addCallEvent("enqueue.left", map[string]any{
		"queue":          enqueue.Name,
		"agent_call_sid": agentCallSID,
		"queue_time":     queueTime,
	})

	// Call action callback with bridge results
	form := url.Values{}
	form.Set("QueueResult", "bridged")
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	// If URL was updated, skip TwiML execution
	if urlUpdated {
		if err := r.executeActionCallback(ctx, enqueue.Method, enqueue.Action, form, currentTwimlDocumentURL, true); err != nil {
			r.recordError(err)
		}
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, enqueue.Method, enqueue.Action, form, currentTwimlDocumentURL, false)
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

	r.addCallEvent("enqueued", map[string]any{
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
	// 4. URL is updated
	queueResult := ""
	var bridgePartnerSID model.SID
	urlUpdated := false
	select {
	case <-ctx.Done():
		queueResult = "system-shutdown"
	case <-r.hangupCh:
		queueResult = "hangup"
	case <-r.urlUpdateCh:
		queueResult = "url-updated"
		urlUpdated = true
		r.addCallEvent("enqueue.interrupted", map[string]any{"reason": "url_updated"})
	case dqResult := <-r.dequeueCh:
		queueResult = dqResult.result
		bridgePartnerSID = dqResult.partnerSID
	}

	// Calculate time in queue (time waiting before bridge)
	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())

	r.state.mu.Lock()
	r.removeFromQueue(queue)
	r.call.CurrentEndpoint = ""
	r.state.mu.Unlock()

	r.addCallEvent("dequeued", map[string]any{
		"queue":        enqueue.Name,
		"queue_result": queueResult,
		"queue_time":   queueTime,
	})

	// If dequeued via Dial (bridged), wait for bridge to complete before calling action
	// Per Twilio docs: "the action URL is hit once when the bridged parties disconnect"
	if queueResult == "bridged" {
		bridgeStartTime := r.clock.Now()
		r.addCallEvent("enqueue.bridge_started", map[string]any{
			"queue":       enqueue.Name,
			"partner_sid": bridgePartnerSID,
		})

		// Get the bridge partner runner to notify them when we hang up
		r.state.mu.Lock()
		partnerRunner := r.state.runners[bridgePartnerSID]
		r.state.mu.Unlock()

		// Wait for bridge to complete
		select {
		case <-ctx.Done():
		case <-r.hangupCh:
			// This call hung up, notify the partner
			if partnerRunner != nil {
				select {
				case partnerRunner.bridgeEndCh <- struct{}{}:
				default:
				}
			}
		case <-r.bridgeEndCh:
			// Partner call hung up, end this bridge
			r.addCallEvent("enqueue.partner_hangup", map[string]any{})
		case <-r.urlUpdateCh:
			urlUpdated = true
			r.addCallEvent("enqueue.bridge_interrupted", map[string]any{"reason": "url_updated"})
			// Notify partner before leaving
			if partnerRunner != nil {
				select {
				case partnerRunner.bridgeEndCh <- struct{}{}:
				default:
				}
			}
		}

		bridgeEndTime := r.clock.Now()
		bridgeDuration := int(bridgeEndTime.Sub(bridgeStartTime).Seconds())

		r.addCallEvent("enqueue.bridge_completed", map[string]any{
			"queue":           enqueue.Name,
			"bridge_duration": bridgeDuration,
		})
	}

	// Call action callback with queue results
	form := url.Values{}
	form.Set("QueueResult", queueResult)
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	// If URL was updated, skip action callback entirely
	if urlUpdated {
		if err := r.executeActionCallback(ctx, enqueue.Method, enqueue.Action, form, currentTwimlDocumentURL, true); err != nil {
			r.recordError(err)
		}
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, enqueue.Method, enqueue.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeWait(ctx context.Context, eventPrefix, waitURL, waitURLMethod, currentTwimlDocumentURL string) error {
	// Fetch and parse WaitURL if provided
	// WaitURL can return either TwiML or an audio file
	var waitTwiML *twiml.Response
	var waitTwiMLDocumentURL string
	var waitAudioURL string
	if waitURL != "" {
		resolvedWaitURL, urlErr := resolveURL(currentTwimlDocumentURL, waitURL)
		if urlErr != nil {
			r.addCallEvent(eventPrefix+".wait_url_error", map[string]any{
				"wait_url": waitURL,
				"error":    urlErr.Error(),
			})
			err := fmt.Errorf("failed to resolve wait URL %s: %w", waitURL, urlErr)
			r.recordError(err)
			return err
		}

		reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()

		var status int
		var body []byte
		var headers http.Header
		var fetchErr error
		if waitURLMethod == "GET" {
			status, body, headers, fetchErr = r.engine.webhook.GET(reqCtx, resolvedWaitURL)
		} else {
			status, body, headers, fetchErr = r.engine.webhook.POST(reqCtx, resolvedWaitURL, nil)
		}

		if fetchErr != nil {
			r.addCallEvent(eventPrefix+".wait_url_error", map[string]any{
				"wait_url": resolvedWaitURL,
				"error":    fetchErr.Error(),
			})
			err := fmt.Errorf("failed to fetch wait URL %s: %w", resolvedWaitURL, fetchErr)
			r.recordError(err)
			return err
		}

		if status < 200 || status >= 300 {
			r.addCallEvent(eventPrefix+".wait_url_error", map[string]any{
				"wait_url": resolvedWaitURL,
				"status":   status,
			})
			err := fmt.Errorf("wait URL %s returned status %d", resolvedWaitURL, status)
			r.recordError(err)
			return err
		}

		// Check Content-Type to determine if it's TwiML or audio
		contentType := headers.Get("Content-Type")

		// Try to parse as TwiML first (text/xml or application/xml)
		if strings.Contains(contentType, "xml") || contentType == "" {
			parsed, parseErr := twiml.Parse(body)
			if parseErr == nil {
				waitTwiML = parsed
				waitTwiMLDocumentURL = resolvedWaitURL
				r.addCallEvent(eventPrefix+".wait_url_fetched", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "twiml",
					"status":   status,
				})
			} else if strings.Contains(contentType, "xml") {
				r.recordError(parseErr)
				r.addCallEvent(eventPrefix+".wait_url_error", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "twiml",
					"status":   status,
					"error":    parseErr.Error(),
				})
			} else {
				// If parsing as TwiML failed, treat it as audio URL
				waitAudioURL = resolvedWaitURL
				r.addCallEvent(eventPrefix+".wait_url_fetched", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "audio",
					"status":   status,
				})
			}
		} else {
			// Content type indicates audio (audio/*, etc.)
			waitAudioURL = resolvedWaitURL
			r.addCallEvent(eventPrefix+".wait_url_fetched", map[string]any{
				"wait_url": resolvedWaitURL,
				"type":     "audio",
				"status":   status,
			})
		}
	}

	// Execute wait TwiML once to validate it (e.g., check Play URLs are reachable)
	if waitTwiML != nil {
		// skip redirect for wait twiml otherwise we will keep looping on audio files
		if err := r.executeTwiML(ctx, waitTwiML, waitTwiMLDocumentURL, true); err != nil {
			if errors.Is(err, ErrURLUpdated) {
				return err
			} else {
				r.addCallEvent(eventPrefix+".wait_twiml_error", map[string]any{
					"error": err.Error(),
				})
				// Log error but don't fail theeventPrefix+
				r.recordError(err)
			}
		}
	} else if waitAudioURL != "" {
		// Audio URL was validated above, just log that it would be played
		r.addCallEvent(eventPrefix+".wait_audio_validated", map[string]any{
			"audio_url": waitAudioURL,
		})
	}

	return nil
}

func (r *CallRunner) executeRedirect(ctx context.Context, redirect *twiml.Redirect, currentTwimlDocumentURL string, skipRedirect bool) error {
	r.trackCallTwiML(redirect)
	r.addCallEvent("twiml.redirect", map[string]any{
		"url":    redirect.URL,
		"method": redirect.Method,
	})

	resolvedURL, err := resolveURL(currentTwimlDocumentURL, redirect.URL)
	if err != nil {
		r.addCallEvent("action.url_error", map[string]any{
			"action": redirect.URL,
			"base":   currentTwimlDocumentURL,
			"error":  err.Error(),
		})
		return err
	}

	// Fetch and execute new TwiML
	resp, err := r.fetchTwiML(ctx, redirect.Method, resolvedURL, url.Values{})
	if err != nil {
		return err
	}
	if skipRedirect {
		r.addCallEvent("twiml.redirect.execution_skipped", map[string]any{
			"url":    resolvedURL,
			"method": redirect.Method,
		})
		return nil
	}
	return r.executeTwiML(ctx, resp, resolvedURL, false)
}

func (r *CallRunner) executeRecord(ctx context.Context, record *twiml.Record, currentTwimlDocumentURL string, terminated *bool) error {
	r.trackCallTwiML(record)

	r.addCallEvent("twiml.record", map[string]any{
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
		r.addCallEvent("record.beep", map[string]any{})
	}

	// Wait for timeout, maxLength, or hangup
	// In a real implementation, this would wait for silence detection or user hangup
	var recordingStatus string

	select {
	case <-ctx.Done():
		recordingStatus = "canceled"
	case <-r.hangupCh:
		recordingStatus = "completed"
	case <-r.clock.After(record.MaxLength):
		// Max length reached
		recordingStatus = "completed"
		r.addCallEvent("record.max_length", map[string]any{})
	}

	r.state.mu.Lock()
	r.call.CurrentEndpoint = ""

	// Check if a voicemail recording was set for this call
	var recordingSID model.SID
	var recordingDuration int
	if voicemailSID, exists := r.state.callVoicemails[r.call.SID]; exists {
		recordingSID = voicemailSID
		if recording, exists := r.state.recordings[recordingSID]; exists {
			recording.Status = recordingStatus
			recordingDuration = recording.Duration
		}
	} else {
		// Generate a fake recording SID if no voicemail was set
		recordingSID = model.NewRecordingSID()
	}
	r.state.mu.Unlock()

	r.addCallEvent("record.completed", map[string]any{
		"recording_sid":      recordingSID,
		"recording_duration": recordingDuration,
		"recording_status":   recordingStatus,
	})

	// Call action callback with recording results
	form := url.Values{}
	form.Set("RecordingSid", string(recordingSID))
	// Use baseURL if set, otherwise use the default Twilio URL
	recordingURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Recordings/%s", r.call.AccountSID, recordingSID)
	if r.engine.baseURL != "" {
		recordingURL = fmt.Sprintf("%s/Accounts/%s/Recordings/%s", r.engine.baseURL, r.call.AccountSID, recordingSID)
	}
	form.Set("RecordingUrl", recordingURL)
	form.Set("RecordingStatus", recordingStatus)
	form.Set("RecordingDuration", fmt.Sprintf("%d", recordingDuration))

	if err := r.executeActionCallback(ctx, record.Method, record.Action, form, currentTwimlDocumentURL, false); err != nil {
		return err
	}
	*terminated = true
	return nil
}

func (r *CallRunner) executeHangup(implicit bool) error {
	if !implicit {
		r.trackCallTwiML(&twiml.Hangup{})
	}
	r.addCallEvent("twiml.hangup", map[string]any{})
	r.updateStatus(model.CallCompleted)
	now := r.clock.Now()
	r.state.mu.Lock()
	r.call.EndedAt = &now
	r.state.mu.Unlock()
	return ErrCallHungup // Signal to stop execution
}

// Hangup signals the runner to hang up
func (r *CallRunner) Hangup() {
	r.hangupOnce.Do(func() {
		close(r.hangupCh)
	})
}

// SendDigits sends digits to the gather
func (r *CallRunner) SendDigits(digits string) {
	select {
	case r.gatherCh <- digits:
	}
}

// UpdateURL signals the runner to interrupt current execution and fetch new TwiML from the updated URL
func (r *CallRunner) UpdateURL(newURL string) {
	select {
	case r.urlUpdateCh <- newURL:
	default:
	}
}

func (r *CallRunner) updateStatus(status model.CallStatus) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.engine.updateCallStatusLocked(r.state, r.call, status)
}

func (r *CallRunner) recordError(err error) {
	if errors.Is(err, ErrCallHungup) || errors.Is(err, ErrURLUpdated) {
		return
	}
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.errors = append(r.state.errors, err)
}

func (r *CallRunner) addCallEvent(eventType string, detail map[string]any) {
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.call.Timeline = append(r.call.Timeline, model.NewEvent(
		r.clock.Now(),
		eventType,
		detail,
	))
}

func (r *CallRunner) trackCallTwiML(verb any) {
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
func (r *CallRunner) executeActionCallback(ctx context.Context, actionMethod, actionURL string, form url.Values, currentTwimlDocumentURL string, skipTwimlExecution bool) error {
	if actionURL == "" {
		return nil
	}

	resolvedURL, err := resolveURL(currentTwimlDocumentURL, actionURL)
	if err != nil {
		r.addCallEvent("action.url_error", map[string]any{
			"action": actionURL,
			"base":   currentTwimlDocumentURL,
			"error":  err.Error(),
		})
		return err
	}

	resp, err := r.fetchTwiML(ctx, actionMethod, resolvedURL, form)
	if err != nil {
		return err
	}
	// clear collected digits to avoid sending them again
	r.state.mu.RLock()
	digits := r.call.Variables["Digits"]
	r.state.mu.RUnlock()
	if digits != "" {
		r.state.mu.Lock()
		r.call.Variables["Digits"] = ""
		r.state.mu.Unlock()
		r.addCallEvent("digits.cleared", map[string]any{
			"digits": digits,
		})
	}

	if skipTwimlExecution {
		return nil
	}
	// If the action callback returns no TwiML instructions, hangup the call
	if len(resp.Children) == 0 {
		r.addCallEvent("action.empty_response", map[string]any{
			"message": "Action callback returned no TwiML instructions, hanging up call",
			"url":     resolvedURL,
		})
		return r.executeHangup(true)
	}

	return r.executeTwiML(ctx, resp, resolvedURL, false)
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

func (r *CallRunner) removeFromConference(conf *model.Conference, currentTwimlDocumentURL string) {
	// Check if this participant has EndConferenceOnExit=true
	endConferenceOnExit := false
	if r.state.participantStates[conf.SID] != nil {
		if ps := r.state.participantStates[conf.SID][r.call.SID]; ps != nil && ps.EndConferenceOnExit {
			endConferenceOnExit = true
		}
	}

	conferenceEnded := false
	for i, sid := range conf.Participants {
		if sid == r.call.SID {
			conf.Participants = append(conf.Participants[:i], conf.Participants[i+1:]...)
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				r.clock.Now(),
				"participant.left",
				map[string]any{
					"call_sid":               r.call.SID,
					"end_conference_on_exit": endConferenceOnExit,
				},
			))

			// Send participant-leave callback if configured
			if conf.StatusCallback != "" {
				if r.engine.shouldSendConferenceStatusCallback(conf, "participant-leave") {
					// Queue the callback for serial execution
					callSID := r.call.SID
					conf.CallbackQueue <- func() {
						r.engine.sendConferenceStatusCallback(r.state, conf, "participant-leave", &callSID, currentTwimlDocumentURL)
					}
				} else {
					conf.Timeline = append(conf.Timeline, model.NewEvent(
						r.clock.Now(),
						"webhook.conference_status_callback_skipped",
						map[string]any{
							"event":    "participant-leave",
							"call_sid": r.call.SID,
						},
					))
				}
			}

			// If this participant has EndConferenceOnExit=true, end the conference
			// Or if last participant, mark conference completed
			// Only mark as ended if not already completed
			if (endConferenceOnExit || len(conf.Participants) == 0) && conf.Status != model.ConferenceCompleted {
				conf.Status = model.ConferenceCompleted
				now := r.clock.Now()
				conf.EndedAt = &now
				reason := "last_participant_left"
				if endConferenceOnExit && len(conf.Participants) > 0 {
					reason = "end_conference_on_exit"
				}
				conf.Timeline = append(conf.Timeline, model.NewEvent(
					now,
					"conference.ended",
					map[string]any{"reason": reason},
				))
				conferenceEnded = true
			}
			break
		}
	}

	// Send conference-end callback if conference ended
	if conferenceEnded && conf.StatusCallback != "" {
		if r.engine.shouldSendConferenceStatusCallback(conf, "conference-end") {
			// Queue the callback for serial execution
			conf.CallbackQueue <- func() {
				r.engine.sendConferenceStatusCallback(r.state, conf, "conference-end", nil, currentTwimlDocumentURL)
			}
		} else {
			conf.Timeline = append(conf.Timeline, model.NewEvent(
				r.clock.Now(),
				"webhook.conference_status_callback_skipped",
				map[string]any{
					"event": "conference-end",
				},
			))
		}
	}
	if conferenceEnded {
		// Close the callback queue after the conference ends
		// This allows the worker goroutine to exit cleanly
		close(conf.CallbackQueue)
	}

	r.call.CurrentEndpoint = ""
}

// invokeRecordingCallback invokes the RecordingStatusCallback if recording is enabled and a recording was set
func (r *CallRunner) invokeRecordingCallback(ctx context.Context, dial *twiml.Dial, conference *twiml.Conference, recordedCallSID model.SID, currentTwimlDocumentURL string) {
	// Check if recording is enabled
	recordEnabled := false
	if conference != nil && conference.Record != "" && conference.Record != "do-not-record" {
		recordEnabled = true
	}
	if dial.Record != "" && dial.Record != "do-not-record" {
		recordEnabled = true
	}

	if !recordEnabled {
		r.addCallEvent("recording.status_callback_skipped", map[string]any{
			"reason": "recording_not_enabled",
		})
		return
	}
	var cnf *model.Conference
	// Check if a recording was set for this call
	r.state.mu.RLock()
	recordingSID, hasRecording := r.state.callRecordings[recordedCallSID]
	var recording *model.Recording
	if hasRecording {
		recording = r.state.recordings[recordingSID]
	}
	if conference != nil {
		cnf = r.state.conferences[conference.Name]
	}
	r.state.mu.RUnlock()

	if !hasRecording || recording == nil {
		r.addCallEvent("recording.status_callback_skipped", map[string]any{
			"reason": "recording_not_set",
		})
		return
	}

	// Use RecordingStatusCallback from Conference or Dial (Conference takes precedence)
	recordingCallback := ""
	if conference != nil && conference.RecordingStatusCallback != "" {
		recordingCallback = conference.RecordingStatusCallback
	}
	if recordingCallback == "" && dial.RecordingStatusCallback != "" {
		recordingCallback = dial.RecordingStatusCallback
	}

	if recordingCallback == "" {
		r.addCallEvent("recording.status_callback_skipped", map[string]any{
			"reason": "recording_status_callback_not_set",
		})
		return
	}

	// Invoke RecordingStatusCallback
	recordingForm := url.Values{}
	recordingForm.Set("RecordingSid", string(recordingSID))
	if cnf != nil {
		recordingForm.Set("ConferenceSid", string(cnf.SID))
	}
	// Use baseURL if set, otherwise use the default Twilio URL
	recordingURL := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Recordings/%s", r.call.AccountSID, recordingSID)
	if r.engine.baseURL != "" {
		recordingURL = fmt.Sprintf("%s/Accounts/%s/Recordings/%s", r.engine.baseURL, r.call.AccountSID, recordingSID)
	}
	recordingForm.Set("RecordingUrl", recordingURL)
	recordingForm.Set("RecordingStatus", recording.Status)
	recordingForm.Set("RecordingDuration", fmt.Sprintf("%d", recording.Duration))
	recordingForm.Set("CallSid", recording.CallSID.String())
	recordingForm.Set("AccountSid", string(r.call.AccountSID))

	r.addCallEvent("recording.status_callback", map[string]any{
		"recording_sid": recordingSID,
		"callback_url":  recordingCallback,
	})

	if err := r.executeActionCallback(ctx, "POST", recordingCallback, recordingForm, currentTwimlDocumentURL, true); err != nil {
		r.addCallEvent("recording.status_callback_error", map[string]any{
			"error": err,
		})
		r.recordError(err)
	}
}
