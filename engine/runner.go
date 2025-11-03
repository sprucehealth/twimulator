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
	result    string    // "bridged", "hangup", etc.
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
		hangupCh:             make(chan struct{}, 1),
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
		r.state.mu.Unlock()

		// Fetch TwiML
		twimlResp, err := r.fetchTwiML(ctx, currentURL, url.Values{})
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
		err = r.executeTwiML(ctx, twimlResp, currentURL)
		if err != nil {
			// Check if this is a normal hangup or an actual error
			if errors.Is(err, ErrCallHungup) {
				// Normal hangup via <Hangup/> verb - call already completed by executeHangup
				return
			}
			// Check if URL was updated - if so, loop to fetch new TwiML
			if errors.Is(err, ErrURLUpdated) {
				r.addEvent("call.url_updated", map[string]any{"message": "Fetching new TwiML from updated URL"})
				continue
			}
			// Actual error - mark call as failed
			log.Printf("TwiML execution error for call %s: %v", r.call.SID, err)
			r.recordError(err)
			r.updateStatus(model.CallFailed)
			return
		}

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
			r.addEvent("call.url_updated", map[string]any{"message": "Fetching new TwiML from updated URL"})
			continue
		}
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
	reqCtx, cancel := context.WithTimeout(ctx, r.engine.timeout)
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
		return r.executeRedirect(ctx, n, currentTwimlDocumentURL)
	case *twiml.Record:
		return r.executeRecord(ctx, n, currentTwimlDocumentURL, terminated)
	case *twiml.Hangup:
		return r.executeHangup(false)
	default:
		msg := fmt.Sprintf("Unknown TwiML node type: %T", node)
		err := errors.New(msg)
		r.recordError(err)
		log.Printf("ERROR: %s", err)
		r.addEvent("twiml.invalid_node", map[string]any{"node": msg})
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

	// Trim whitespace and newlines from URL
	playURL := strings.TrimSpace(play.URL)

	// Log the play attempt
	r.addEvent("twiml.play", map[string]any{
		"url": playURL,
	})

	// Fetch the media URL to ensure it's accessible
	reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	status, _, _, err := r.engine.webhook.GET(reqCtx, playURL)
	if err != nil {
		r.addEvent("play.error", map[string]any{
			"url":   playURL,
			"error": err.Error(),
		})
		err := fmt.Errorf("failed to fetch play URL %s: %w", playURL, err)
		r.recordError(err)
		return err
	}

	// Check for non-2xx status codes
	if status < 200 || status >= 300 {
		r.addEvent("play.error", map[string]any{
			"url":    playURL,
			"status": status,
		})
		err := fmt.Errorf("play URL %s returned status %d", playURL, status)
		r.recordError(err)
		return err
	}

	r.addEvent("play.success", map[string]any{
		"url":    playURL,
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
	case <-r.urlUpdateCh:
		// URL updated, skip through pause
		r.addEvent("pause.interrupted", map[string]any{"reason": "url_updated"})
		return ErrURLUpdated
	case <-r.clock.After(pause.Length):
		return nil
	}
}

func (r *CallRunner) executeGather(ctx context.Context, gather *twiml.Gather, currentTwimlDocumentURL string, terminated *bool) error {
	r.trackTwiML(gather)

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

	r.addEvent("twiml.gather", map[string]any{
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
			r.addEvent("gather.interrupted", map[string]any{"reason": "url_updated"})
			urlUpdated = true
			goto gatherComplete
		case digits := <-r.gatherCh:
			// Got one or more digits - process each character individually
			for _, char := range digits {
				digit := string(char)

				// Check if it's the finish key
				if gather.FinishOnKey != "" && digit == gather.FinishOnKey {
					r.addEvent("gather.finish_key_pressed", map[string]any{
						"collected_digits": collectedDigits,
						"finish_key":       digit,
					})
					goto gatherComplete
				}

				// Not a finish key, add to collected digits
				collectedDigits += digit
				r.addEvent("gather.digit_received", map[string]any{
					"digit":            digit,
					"collected_digits": collectedDigits,
					"target_digits":    gather.NumDigits,
				})

				// Check if we've reached numDigits
				if gather.NumDigits > 0 && len(collectedDigits) >= gather.NumDigits {
					r.addEvent("gather.num_digits_reached", map[string]any{
						"collected_digits": collectedDigits,
						"num_digits":       gather.NumDigits,
					})
					goto gatherComplete
				}
			}

			// If finishOnKey is empty and numDigits is 0, continue collecting
			// (will only stop on timeout or interruption)

		case <-timeoutTimer:
			// Timeout
			r.addEvent("gather.timeout", map[string]any{
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

	return r.executeActionCallback(ctx, gather.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDial(ctx context.Context, dial *twiml.Dial, currentTwimlDocumentURL string) error {
	r.trackTwiML(dial)

	var queueDial *twiml.QueueDial
	var conferenceDial *twiml.ConferenceDial
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
		case *twiml.QueueDial:
			if queueDial != nil {
				return fmt.Errorf("dial cannot contain more than one queue")
			}
			queueDial = n
		case *twiml.ConferenceDial:
			if conferenceDial != nil {
				return fmt.Errorf("dial cannot contain more than one conference")
			}
			conferenceDial = n
		}
	}
	if queueDial != nil && conferenceDial != nil {
		return fmt.Errorf("dial cannot contain both queue and conference")
	}

	r.addEvent("twiml.dial", map[string]any{
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

func (r *CallRunner) executeDialQueue(ctx context.Context, dial *twiml.Dial, queueDial *twiml.QueueDial, currentTwimlDocumentURL string) error {
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, queueDial.Name)
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
				r.addEvent("dial.queue.partner_hangup", map[string]any{})
				goto bridgeEnded
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addEvent("dial.hangup_on_star", map[string]any{
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
			r.addEvent("dial.queue.partner_hangup", map[string]any{})
		}
	}

bridgeEnded:
	endTime := r.clock.Now()
	dialDuration = int(endTime.Sub(startTime).Seconds())
	targetQueueTime := 0
	if !targetQueueStart.IsZero() {
		targetQueueTime = int(startTime.Sub(targetQueueStart).Seconds())
	}

	r.addEvent("dial.queue.left", map[string]any{
		"queue":             queue.Name,
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

	return r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL, false)
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

	r.addEvent("dial.queue.joined", map[string]any{
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
				r.addEvent("dial.queue.interrupted", map[string]any{"reason": "url_updated"})
				goto queueLeft
			case dqResult := <-r.dequeueCh:
				dialStatus = "completed"
				queueResult = dqResult.result
				bridgePartnerSID = dqResult.partnerSID
				goto queueLeft
			case <-r.clock.After(dial.Timeout):
				dialStatus = "no-answer"
				queueResult = "timeout"
				r.addEvent("dial.queue.timeout", map[string]any{})
				goto queueLeft
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addEvent("dial.hangup_on_star", map[string]any{
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
			r.addEvent("dial.queue.interrupted", map[string]any{"reason": "url_updated"})
		case dqResult := <-r.dequeueCh:
			dialStatus = "completed"
			queueResult = dqResult.result
			bridgePartnerSID = dqResult.partnerSID
		case <-r.clock.After(dial.Timeout):
			dialStatus = "no-answer"
			queueResult = "timeout"
			r.addEvent("dial.queue.timeout", map[string]any{})
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

	r.addEvent("dial.queue.left", map[string]any{
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
		r.addEvent("dial.queue.bridge_started", map[string]any{
			"queue":      queue.Name,
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
					r.addEvent("dial.queue.partner_hangup", map[string]any{})
					goto bridgeEnded
				case <-r.urlUpdateCh:
					urlUpdated = true
					r.addEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
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
						r.addEvent("dial.hangup_on_star", map[string]any{
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
				r.addEvent("dial.queue.partner_hangup", map[string]any{})
			case <-r.urlUpdateCh:
				urlUpdated = true
				r.addEvent("dial.queue.bridge_interrupted", map[string]any{"reason": "url_updated"})
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

		r.addEvent("dial.queue.bridge_completed", map[string]any{
			"queue":         queue.Name,
			"dial_duration": dialDuration,
		})
	}
	// Call action callback with dial results
	form := url.Values{}
	form.Set("DialCallStatus", dialStatus)
	form.Set("DialCallDuration", fmt.Sprintf("%d", dialDuration))
	form.Set("QueueResult", queueResult)
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	// If URL was updated, skip action callback entirely
	if urlUpdated {
		_ = r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL, true)
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDialConference(ctx context.Context, dial *twiml.Dial, conferenceDial *twiml.ConferenceDial, currentTwimlDocumentURL string) error {
	conf := r.engine.getOrCreateConference(r.call.AccountSID, conferenceDial)

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
	partState.StartConferenceOnEnter = conferenceDial.StartConferenceOnEnter
	partState.EndConferenceOnExit = conferenceDial.EndConferenceOnExit

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

	r.call.CurrentEndpoint = "conference:" + conferenceDial.Name

	// Send status callbacks if configured
	if conf.StatusCallback != "" {
		// Send participant-join callback
		if r.engine.shouldSendConferenceStatusCallback(conf, "participant-join") {
			go r.engine.sendConferenceStatusCallback(r.state, conf, "participant-join", &r.call.SID, currentTwimlDocumentURL)
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
				go r.engine.sendConferenceStatusCallback(r.state, conf, "conference-start", nil, currentTwimlDocumentURL)
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

	r.addEvent("joined.conference", map[string]any{
		"conference":   conferenceDial.Name,
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
				r.state.mu.Lock()
				r.removeFromConference(conf, currentTwimlDocumentURL)
				r.state.mu.Unlock()
				goto conferenceEnded
			case <-r.hangupCh:
				r.state.mu.Lock()
				r.removeFromConference(conf, currentTwimlDocumentURL)
				r.state.mu.Unlock()
				goto conferenceEnded
			case <-r.urlUpdateCh:
				urlUpdated = true
				r.addEvent("dial.conference.interrupted", map[string]any{"reason": "url_updated"})
				goto conferenceEnded
			case <-r.conferenceCompleteCh:
				r.addEvent("dial.conference.completed", map[string]any{"reason": "completed_via_api"})
				r.state.mu.Lock()
				r.removeFromConference(conf, currentTwimlDocumentURL)
				r.state.mu.Unlock()
				goto conferenceEnded
			case digits := <-r.gatherCh:
				// Check if star is pressed by caller to leave conference
				if strings.Contains(digits, "*") {
					r.addEvent("dial.hangup_on_star", map[string]any{
						"digits": digits,
					})
					r.state.mu.Lock()
					r.removeFromConference(conf, currentTwimlDocumentURL)
					r.state.mu.Unlock()
					goto conferenceEnded
				}
			}
		}
	} else {
		select {
		case <-ctx.Done():
			r.state.mu.Lock()
			r.removeFromConference(conf, currentTwimlDocumentURL)
			r.state.mu.Unlock()
			goto conferenceEnded
		case <-r.hangupCh:
			r.state.mu.Lock()
			r.removeFromConference(conf, currentTwimlDocumentURL)
			r.state.mu.Unlock()
			goto conferenceEnded
		case <-r.urlUpdateCh:
			urlUpdated = true
			r.addEvent("dial.conference.interrupted", map[string]any{"reason": "url_updated"})
			goto conferenceEnded
		case <-r.conferenceCompleteCh:
			r.addEvent("dial.conference.completed", map[string]any{"reason": "completed_via_api"})
			r.state.mu.Lock()
			r.removeFromConference(conf, currentTwimlDocumentURL)
			r.state.mu.Unlock()
			goto conferenceEnded
		}
	}
conferenceEnded:
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
		_ = r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL, true)
		return ErrURLUpdated
	}
	return r.executeActionCallback(ctx, dial.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeDialNumber(ctx context.Context, dial *twiml.Dial, numbers []*twiml.Number, clients []*twiml.Client, sips []*twiml.Sip) error {
	// Create child call leg
	r.addEvent("dial.number", map[string]any{
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
				r.addEvent("dial.no_answer", map[string]any{})
				return nil
			case digits := <-r.gatherCh:
				// Check if star is pressed
				if strings.Contains(digits, "*") {
					r.addEvent("dial.hangup_on_star", map[string]any{
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
			r.addEvent("dial.no_answer", map[string]any{})
		}
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

	// Signal the agent call to dequeue with "bridged" result and partner SID
	if agentRunner != nil {
		select {
		case agentRunner.dequeueCh <- dequeueResult{result: "bridged", partnerSID: r.call.SID}:
		default:
		}
	}

	// Bridge - wait for hangup (no timeout for enqueued callers)
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
		r.addEvent("enqueue.partner_hangup", map[string]any{})
	}

	endTime := r.clock.Now()
	queueTime := int(endTime.Sub(startTime).Seconds())

	r.addEvent("enqueue.left", map[string]any{
		"queue":          enqueue.Name,
		"agent_call_sid": agentCallSID,
		"queue_time":     queueTime,
	})

	// Call action callback with bridge results
	form := url.Values{}
	form.Set("QueueResult", "bridged")
	form.Set("QueueSid", string(queueSID))
	form.Set("QueueTime", fmt.Sprintf("%d", queueTime))

	return r.executeActionCallback(ctx, enqueue.Action, form, currentTwimlDocumentURL, false)
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

	// Fetch and parse WaitURL if provided
	// WaitURL can return either TwiML or an audio file
	var waitTwiML *twiml.Response
	var waitAudioURL string
	if enqueue.WaitURL != "" {
		resolvedWaitURL, urlErr := resolveURL(currentTwimlDocumentURL, enqueue.WaitURL)
		if urlErr != nil {
			r.addEvent("enqueue.wait_url_error", map[string]any{
				"wait_url": enqueue.WaitURL,
				"error":    urlErr.Error(),
			})
			err := fmt.Errorf("failed to resolve wait URL %s: %w", enqueue.WaitURL, urlErr)
			r.recordError(err)
			return err
		}

		reqCtx, cancel := context.WithTimeout(ctx, r.timeout)
		defer cancel()

		var status int
		var body []byte
		var headers http.Header
		var fetchErr error
		if enqueue.WaitURLMethod == "GET" {
			status, body, headers, fetchErr = r.engine.webhook.GET(reqCtx, resolvedWaitURL)
		} else {
			status, body, headers, fetchErr = r.engine.webhook.POST(reqCtx, resolvedWaitURL, nil)
		}

		if fetchErr != nil {
			r.addEvent("enqueue.wait_url_error", map[string]any{
				"wait_url": resolvedWaitURL,
				"error":    fetchErr.Error(),
			})
			err := fmt.Errorf("failed to fetch wait URL %s: %w", resolvedWaitURL, fetchErr)
			r.recordError(err)
			return err
		}

		if status < 200 || status >= 300 {
			r.addEvent("enqueue.wait_url_error", map[string]any{
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
				r.addEvent("enqueue.wait_url_fetched", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "twiml",
					"status":   status,
				})
			} else if strings.Contains(contentType, "xml") {
				r.recordError(parseErr)
				r.addEvent("enqueue.wait_url_error", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "twiml",
					"status":   status,
					"error":    parseErr.Error(),
				})
			} else {
				// If parsing as TwiML failed, treat it as audio URL
				waitAudioURL = resolvedWaitURL
				r.addEvent("enqueue.wait_url_fetched", map[string]any{
					"wait_url": resolvedWaitURL,
					"type":     "audio",
					"status":   status,
				})
			}
		} else {
			// Content type indicates audio (audio/*, etc.)
			waitAudioURL = resolvedWaitURL
			r.addEvent("enqueue.wait_url_fetched", map[string]any{
				"wait_url": resolvedWaitURL,
				"type":     "audio",
				"status":   status,
			})
		}
	}

	// Execute wait TwiML once to validate it (e.g., check Play URLs are reachable)
	if waitTwiML != nil {
		if err := r.executeTwiML(ctx, waitTwiML, currentTwimlDocumentURL); err != nil {
			r.addEvent("enqueue.wait_twiml_error", map[string]any{
				"error": err.Error(),
			})
			// Log error but don't fail the enqueue
			r.recordError(err)
		}
	} else if waitAudioURL != "" {
		// Audio URL was validated above, just log that it would be played
		r.addEvent("enqueue.wait_audio_validated", map[string]any{
			"audio_url": waitAudioURL,
		})
	}

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
		r.addEvent("enqueue.interrupted", map[string]any{"reason": "url_updated"})
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

	r.addEvent("dequeued", map[string]any{
		"queue":        enqueue.Name,
		"queue_result": queueResult,
		"queue_time":   queueTime,
	})

	// If dequeued via Dial (bridged), wait for bridge to complete before calling action
	// Per Twilio docs: "the action URL is hit once when the bridged parties disconnect"
	if queueResult == "bridged" {
		bridgeStartTime := r.clock.Now()
		r.addEvent("enqueue.bridge_started", map[string]any{
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
			r.addEvent("enqueue.partner_hangup", map[string]any{})
		case <-r.urlUpdateCh:
			urlUpdated = true
			r.addEvent("enqueue.bridge_interrupted", map[string]any{"reason": "url_updated"})
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

		r.addEvent("enqueue.bridge_completed", map[string]any{
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
		_ = r.executeActionCallback(ctx, enqueue.Action, form, currentTwimlDocumentURL, true)
		return ErrURLUpdated
	}

	return r.executeActionCallback(ctx, enqueue.Action, form, currentTwimlDocumentURL, false)
}

func (r *CallRunner) executeRedirect(ctx context.Context, redirect *twiml.Redirect, currentTwimlDocumentURL string) error {
	r.trackTwiML(redirect)
	r.addEvent("twiml.redirect", map[string]any{
		"url":    redirect.URL,
		"method": redirect.Method,
	})

	resolvedURL, err := resolveURL(currentTwimlDocumentURL, redirect.URL)
	if err != nil {
		r.addEvent("action.url_error", map[string]any{
			"action": redirect.URL,
			"base":   currentTwimlDocumentURL,
			"error":  err.Error(),
		})
		return err
	}

	// Fetch and execute new TwiML
	resp, err := r.fetchTwiML(ctx, resolvedURL, url.Values{})
	if err != nil {
		return err
	}

	return r.executeTwiML(ctx, resp, resolvedURL)
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

	if err := r.executeActionCallback(ctx, record.Action, form, currentTwimlDocumentURL, false); err != nil {
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
	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	r.state.errors = append(r.state.errors, err)
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
func (r *CallRunner) executeActionCallback(ctx context.Context, actionURL string, form url.Values, currentTwimlDocumentURL string, skipTwimlExecution bool) error {
	if actionURL == "" {
		return nil
	}

	resolvedURL, err := resolveURL(currentTwimlDocumentURL, actionURL)
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
	// clear collected digits to avoid sending them again
	r.state.mu.RLock()
	digits := r.call.Variables["Digits"]
	r.state.mu.RUnlock()
	if digits != "" {
		r.state.mu.Lock()
		r.call.Variables["Digits"] = ""
		r.state.mu.Unlock()
	}

	if skipTwimlExecution {
		return nil
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
					go r.engine.sendConferenceStatusCallback(r.state, conf, "participant-leave", &r.call.SID, currentTwimlDocumentURL)
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
			go r.engine.sendConferenceStatusCallback(r.state, conf, "conference-end", nil, currentTwimlDocumentURL)
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

	r.call.CurrentEndpoint = ""
}
