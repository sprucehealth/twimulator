package engine

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"time"

	"twimulator/model"
	"twimulator/twiml"
)

// CallRunner executes TwiML for a call
type CallRunner struct {
	call    *model.Call
	engine  *EngineImpl
	timeout time.Duration

	// State for gather
	gatherCh chan string
	hangupCh chan struct{}
	done     chan struct{}
}

// NewCallRunner creates a new call runner
func NewCallRunner(call *model.Call, engine *EngineImpl, timeout time.Duration) *CallRunner {
	return &CallRunner{
		call:     call,
		engine:   engine,
		timeout:  timeout,
		gatherCh: make(chan string, 1),
		hangupCh: make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
}

// Run executes the call lifecycle
func (r *CallRunner) Run(ctx context.Context) {
	defer close(r.done)

	// Transition to ringing
	r.updateStatus(model.CallRinging)

	// Wait for answer or timeout
	select {
	case <-ctx.Done():
		return
	case <-r.hangupCh:
		r.updateStatus(model.CallCompleted)
		return
	case <-r.engine.clock.After(r.timeout):
		r.updateStatus(model.CallNoAnswer)
		return
	case <-r.engine.clock.After(50 * time.Millisecond): // Simulate ring then answer
		// Answer the call
		r.answer(ctx)
	}
}

func (r *CallRunner) answer(ctx context.Context) {
	r.updateStatus(model.CallInProgress)
	now := r.engine.clock.Now()
	r.engine.mu.Lock()
	r.call.AnsweredAt = &now
	r.engine.mu.Unlock()

	// Fetch initial TwiML
	twimlResp, err := r.fetchTwiML(ctx, r.call.Url, url.Values{})
	if err != nil {
		log.Printf("Failed to fetch Url for call %s: %v", r.call.SID, err)
		r.updateStatus(model.CallFailed)
		return
	}

	// Execute TwiML
	if err := r.executeTwiML(ctx, twimlResp); err != nil {
		log.Printf("TwiML execution error for call %s: %v", r.call.SID, err)
		r.updateStatus(model.CallFailed)
		return
	}

	// Call completed normally
	r.updateStatus(model.CallCompleted)
	now = r.engine.clock.Now()
	r.engine.mu.Lock()
	r.call.EndedAt = &now
	r.engine.mu.Unlock()
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

func (r *CallRunner) executeTwiML(ctx context.Context, resp *twiml.Response) error {
	for _, node := range resp.Children {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.hangupCh:
			return nil
		default:
		}

		if err := r.executeNode(ctx, node); err != nil {
			return err
		}
	}
	return nil
}

func (r *CallRunner) executeNode(ctx context.Context, node twiml.Node) error {
	switch n := node.(type) {
	case *twiml.Say:
		return r.executeSay(n)
	case *twiml.Play:
		return r.executePlay(n)
	case *twiml.Pause:
		return r.executePause(ctx, n)
	case *twiml.Gather:
		return r.executeGather(ctx, n)
	case *twiml.Dial:
		return r.executeDial(ctx, n)
	case *twiml.Enqueue:
		return r.executeEnqueue(ctx, n)
	case *twiml.Redirect:
		return r.executeRedirect(ctx, n)
	case *twiml.Hangup:
		return r.executeHangup()
	default:
		log.Printf("Unknown TwiML node type: %T", node)
	}
	return nil
}

func (r *CallRunner) executeSay(say *twiml.Say) error {
	r.addEvent("twiml.say", map[string]any{
		"text":     say.Text,
		"voice":    say.Voice,
		"language": say.Language,
	})
	return nil
}

func (r *CallRunner) executePlay(play *twiml.Play) error {
	r.addEvent("twiml.play", map[string]any{
		"url": play.URL,
	})
	return nil
}

func (r *CallRunner) executePause(ctx context.Context, pause *twiml.Pause) error {
	r.addEvent("twiml.pause", map[string]any{
		"length": pause.Length.Seconds(),
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		return nil
	case <-r.engine.clock.After(pause.Length):
		return nil
	}
}

func (r *CallRunner) executeGather(ctx context.Context, gather *twiml.Gather) error {
	r.addEvent("twiml.gather", map[string]any{
		"input":      gather.Input,
		"timeout":    gather.Timeout.Seconds(),
		"num_digits": gather.NumDigits,
		"action":     gather.Action,
	})

	r.engine.mu.Lock()
	r.call.CurrentEndpoint = "gather"
	r.engine.mu.Unlock()

	// Execute nested children while gathering
	for _, child := range gather.Children {
		if err := r.executeNode(ctx, child); err != nil {
			return err
		}
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
	case <-r.engine.clock.After(gather.Timeout):
		// Timeout
		digits = ""
		r.addEvent("gather.timeout", map[string]any{})
	}

	r.engine.mu.Lock()
	r.call.CurrentEndpoint = ""
	r.call.Variables["Digits"] = digits
	r.engine.mu.Unlock()

	// If action URL specified, fetch and execute
	if gather.Action != "" {
		form := url.Values{}
		form.Set("Digits", digits)
		resp, err := r.fetchTwiML(ctx, gather.Action, form)
		if err != nil {
			return err
		}
		return r.executeTwiML(ctx, resp)
	}

	return nil
}

func (r *CallRunner) executeDial(ctx context.Context, dial *twiml.Dial) error {
	r.addEvent("twiml.dial", map[string]any{
		"number":     dial.Number,
		"client":     dial.Client,
		"queue":      dial.Queue,
		"conference": dial.Conference,
		"timeout":    dial.Timeout.Seconds(),
	})

	// Handle different dial targets
	if dial.Queue != "" {
		return r.executeDialQueue(ctx, dial)
	}
	if dial.Conference != "" {
		return r.executeDialConference(ctx, dial)
	}
	if dial.Number != "" || dial.Client != "" {
		return r.executeDialNumber(ctx, dial)
	}

	return nil
}

func (r *CallRunner) executeDialQueue(ctx context.Context, dial *twiml.Dial) error {
	r.engine.mu.Lock()
	queue := r.engine.getOrCreateQueue(r.call.AccountSID, dial.Queue)

	// Add this call to the queue
	queue.Members = append(queue.Members, r.call.SID)
	queue.Timeline = append(queue.Timeline, model.NewEvent(
		r.engine.clock.Now(),
		"member.joined",
		map[string]any{"call_sid": r.call.SID},
	))

	r.call.CurrentEndpoint = "queue:" + dial.Queue
	r.engine.mu.Unlock()

	r.addEvent("joined.queue", map[string]any{"queue": dial.Queue})

	// In a real scenario, we'd wait for another call to dial this queue
	// For now, simulate waiting
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		return nil
	case <-r.engine.clock.After(dial.Timeout):
		// Timeout waiting in queue
		r.addEvent("queue.timeout", map[string]any{})

		r.engine.mu.Lock()
		r.removeFromQueue(queue)
		r.call.CurrentEndpoint = ""
		r.engine.mu.Unlock()
	}

	return nil
}

func (r *CallRunner) executeDialConference(ctx context.Context, dial *twiml.Dial) error {
	r.engine.mu.Lock()
	conf := r.engine.getOrCreateConference(r.call.AccountSID, dial.Conference)

	// Add participant
	conf.Participants = append(conf.Participants, r.call.SID)
	conf.Timeline = append(conf.Timeline, model.NewEvent(
		r.engine.clock.Now(),
		"participant.joined",
		map[string]any{"call_sid": r.call.SID},
	))

	// Update conference status
	if len(conf.Participants) >= 2 && conf.Status == model.ConferenceCreated {
		conf.Status = model.ConferenceInProgress
		conf.Timeline = append(conf.Timeline, model.NewEvent(
			r.engine.clock.Now(),
			"conference.started",
			map[string]any{},
		))
	}

	r.call.CurrentEndpoint = "conference:" + dial.Conference
	r.engine.mu.Unlock()

	r.addEvent("joined.conference", map[string]any{
		"conference": dial.Conference,
		"sid":        conf.SID,
	})

	// Wait until hangup
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.hangupCh:
		r.engine.mu.Lock()
		r.removeFromConference(conf)
		r.engine.mu.Unlock()
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
	case <-r.engine.clock.After(dial.Timeout):
		r.addEvent("dial.no_answer", map[string]any{})
	}

	return nil
}

func (r *CallRunner) executeEnqueue(ctx context.Context, enqueue *twiml.Enqueue) error {
	// Enqueue is essentially Dial Queue
	dial := &twiml.Dial{
		Queue:  enqueue.Name,
		Action: enqueue.Action,
		Method: enqueue.Method,
	}
	return r.executeDialQueue(ctx, dial)
}

func (r *CallRunner) executeRedirect(ctx context.Context, redirect *twiml.Redirect) error {
	r.addEvent("twiml.redirect", map[string]any{
		"url":    redirect.URL,
		"method": redirect.Method,
	})

	// Fetch and execute new TwiML
	resp, err := r.fetchTwiML(ctx, redirect.URL, url.Values{})
	if err != nil {
		return err
	}

	return r.executeTwiML(ctx, resp)
}

func (r *CallRunner) executeHangup() error {
	r.addEvent("twiml.hangup", map[string]any{})
	r.updateStatus(model.CallCompleted)
	return fmt.Errorf("call hungup") // Signal to stop execution
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
	r.engine.mu.Lock()
	defer r.engine.mu.Unlock()
	r.engine.updateCallStatus(r.call, status)
}

func (r *CallRunner) addEvent(eventType string, detail map[string]any) {
	r.engine.mu.Lock()
	defer r.engine.mu.Unlock()
	r.call.Timeline = append(r.call.Timeline, model.NewEvent(
		r.engine.clock.Now(),
		eventType,
		detail,
	))
}

func (r *CallRunner) buildCallForm() url.Values {
	r.engine.mu.RLock()
	defer r.engine.mu.RUnlock()

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

func (r *CallRunner) removeFromQueue(queue *model.Queue) {
	for i, sid := range queue.Members {
		if sid == r.call.SID {
			queue.Members = append(queue.Members[:i], queue.Members[i+1:]...)
			queue.Timeline = append(queue.Timeline, model.NewEvent(
				r.engine.clock.Now(),
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
				r.engine.clock.Now(),
				"participant.left",
				map[string]any{"call_sid": r.call.SID},
			))

			// If last participant, mark conference completed
			if len(conf.Participants) == 0 {
				conf.Status = model.ConferenceCompleted
				now := r.engine.clock.Now()
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
