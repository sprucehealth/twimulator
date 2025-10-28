package twiml

import (
	"testing"
	"time"
)

func TestParseSay(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="alice" language="en-US">Hello World</Say>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(resp.Children) != 1 {
		t.Fatalf("Expected 1 child, got %d", len(resp.Children))
	}

	say, ok := resp.Children[0].(*Say)
	if !ok {
		t.Fatalf("Expected *Say, got %T", resp.Children[0])
	}

	if say.Text != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", say.Text)
	}
	if say.Voice != "alice" {
		t.Errorf("Expected voice 'alice', got %q", say.Voice)
	}
	if say.Language != "en-US" {
		t.Errorf("Expected language 'en-US', got %q", say.Language)
	}
}

func TestParseGather(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="dtmf" timeout="5" numDigits="1" action="http://example.com/gather">
    <Say>Press 1</Say>
  </Gather>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	gather, ok := resp.Children[0].(*Gather)
	if !ok {
		t.Fatalf("Expected *Gather, got %T", resp.Children[0])
	}

	if gather.Input != "dtmf" {
		t.Errorf("Expected input 'dtmf', got %q", gather.Input)
	}
	if gather.Timeout != "5" {
		t.Errorf("Expected timeout '5', got %v", gather.Timeout)
	}
	if gather.NumDigits != 1 {
		t.Errorf("Expected numDigits 1, got %d", gather.NumDigits)
	}
	if gather.Action != "http://example.com/gather" {
		t.Errorf("Expected action URL, got %q", gather.Action)
	}

	if len(gather.Children) != 1 {
		t.Fatalf("Expected 1 child in Gather, got %d", len(gather.Children))
	}

	say, ok := gather.Children[0].(*Say)
	if !ok {
		t.Fatalf("Expected *Say in Gather, got %T", gather.Children[0])
	}
	if say.Text != "Press 1" {
		t.Errorf("Expected 'Press 1', got %q", say.Text)
	}
}

func TestParseDialNumber(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial>+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if dial.Children == nil || len(dial.Children) != 1 {
		t.Fatal("Expected Number to be set")
	}
	number, ok := dial.Children[0].(*Number)
	if !ok {
		t.Fatalf("Expected *Number, got %T", dial.Children[0])
	}
	if number.Number != "+15551234567" {
		t.Errorf("Expected number, got %q", number.Number)
	}
}

func TestParseDialQueue(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Queue>support</Queue></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}
	if dial.Children == nil || len(dial.Children) != 1 {
		t.Fatal("Expected QueueDial to be set")
	}
	queue, ok := dial.Children[0].(*QueueDial)
	if !ok {
		t.Fatalf("Expected *QueueDial, got %T", dial.Children[0])
	}
	if queue.Name != "support" {
		t.Errorf("Expected queue 'support', got %q", queue.Name)
	}
}

func TestParseDialConference(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference startConferenceOnEnter="true" endConferenceOnExit="false">my-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}
	if dial.Children == nil || len(dial.Children) != 1 {
		t.Fatal("Expected Conference to be set")
	}
	dialCnf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if dialCnf.Name != "my-room" {
		t.Errorf("Expected conference 'my-room', got %q", dialCnf.Name)
	}

	if len(dial.Children) != 1 {
		t.Fatalf("Expected 1 child, got %d", len(dial.Children))
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if !conf.StartConferenceOnEnter {
		t.Error("Expected StartConferenceOnEnter true")
	}
	if conf.EndConferenceOnExit {
		t.Error("Expected EndConferenceOnExit false")
	}

	// Verify dial.ConferenceDial points to the same object
	if dialCnf != conf {
		t.Error("Expected dial.ConferenceDial to point to same object as in Children")
	}

	if !dialCnf.StartConferenceOnEnter {
		t.Error("Expected ConferenceDial.StartConferenceOnEnter true")
	}
}

func TestParseConferenceDialBeep(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference beep="true">my-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if !conf.Beep {
		t.Error("Expected Beep to be true")
	}
}

func TestParseConferenceDialWaitMethod(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference waitUrl="http://example.com/wait" waitMethod="GET">my-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if conf.WaitURL != "http://example.com/wait" {
		t.Errorf("Expected WaitURL 'http://example.com/wait', got %q", conf.WaitURL)
	}

	if conf.WaitMethod != "GET" {
		t.Errorf("Expected WaitMethod 'GET', got %q", conf.WaitMethod)
	}
}

func TestParseConferenceDialWaitMethodDefault(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference>my-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if conf.WaitMethod != "POST" {
		t.Errorf("Expected default WaitMethod 'POST', got %q", conf.WaitMethod)
	}
}

func TestParseConferenceDialRecord(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference record="record-from-start" recordingStatusCallback="http://example.com/recording">my-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if conf.Record != "record-from-start" {
		t.Errorf("Expected Record 'record-from-start', got %q", conf.Record)
	}

	if conf.RecordingStatusCallback != "http://example.com/recording" {
		t.Errorf("Expected RecordingStatusCallback URL, got %q", conf.RecordingStatusCallback)
	}
}

func TestParseConferenceDialAllAttributes(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial><Conference
    muted="true"
    beep="true"
    startConferenceOnEnter="false"
    endConferenceOnExit="true"
    waitUrl="http://example.com/wait"
    waitMethod="GET"
    statusCallback="http://example.com/status"
    statusCallbackEvent="start end"
    record="record-from-start"
    recordingStatusCallback="http://example.com/recording">test-room</Conference></Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	conf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}

	if !conf.Muted {
		t.Error("Expected Muted to be true")
	}
	if !conf.Beep {
		t.Error("Expected Beep to be true")
	}
	if conf.StartConferenceOnEnter {
		t.Error("Expected StartConferenceOnEnter to be false")
	}
	if !conf.EndConferenceOnExit {
		t.Error("Expected EndConferenceOnExit to be true")
	}
	if conf.WaitURL != "http://example.com/wait" {
		t.Errorf("Expected WaitURL, got %q", conf.WaitURL)
	}
	if conf.WaitMethod != "GET" {
		t.Errorf("Expected WaitMethod 'GET', got %q", conf.WaitMethod)
	}
	if conf.StatusCallback != "http://example.com/status" {
		t.Errorf("Expected StatusCallback, got %q", conf.StatusCallback)
	}
	if conf.StatusCallbackEvent != "start end" {
		t.Errorf("Expected StatusCallbackEvent, got %q", conf.StatusCallbackEvent)
	}
	if conf.Record != "record-from-start" {
		t.Errorf("Expected Record, got %q", conf.Record)
	}
	if conf.RecordingStatusCallback != "http://example.com/recording" {
		t.Errorf("Expected RecordingStatusCallback, got %q", conf.RecordingStatusCallback)
	}
	if dial.Children == nil || len(dial.Children) != 1 {
		t.Fatal("Expected Conference to be set")
	}
	dialCnf, ok := dial.Children[0].(*ConferenceDial)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}
	// Test that full object is accessible via ConferenceDial with all attributes
	if !dialCnf.Beep {
		t.Error("Expected ConferenceDial.Beep to be true")
	}
	if dialCnf.WaitMethod != "GET" {
		t.Errorf("Expected ConferenceDial.WaitMethod 'GET', got %q", dialCnf.WaitMethod)
	}
	if dialCnf.Record != "record-from-start" {
		t.Errorf("Expected ConferenceDial.Record, got %q", dialCnf.Record)
	}
}

func TestParseDialHangupOnStar(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial hangupOnStar="true">+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if !dial.HangupOnStar {
		t.Error("Expected HangupOnStar to be true")
	}
	if dial.Children == nil || len(dial.Children) != 1 {
		t.Fatal("Expected Conference to be set")
	}
	num, ok := dial.Children[0].(*Number)
	if !ok {
		t.Fatalf("Expected *ConferenceDial, got %T", dial.Children[0])
	}
	if num.Number != "+15551234567" {
		t.Errorf("Expected number, got %q", num.Number)
	}
}

func TestParseDialHangupOnStarDefault(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial>+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if dial.HangupOnStar {
		t.Error("Expected HangupOnStar to be false by default")
	}
}

func TestParseDialRecord(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial record="record-from-answer">+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if dial.Record != "record-from-answer" {
		t.Errorf("Expected record 'record-from-answer', got %q", dial.Record)
	}
}

func TestParseDialRecordAllValues(t *testing.T) {
	validValues := []string{
		"do-not-record",
		"record-from-answer",
		"record-from-ringing",
		"record-from-answer-dual",
		"record-from-ringing-dual",
	}

	for _, value := range validValues {
		xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial record="` + value + `">+15551234567</Dial>
</Response>`

		resp, err := Parse([]byte(xml))
		if err != nil {
			t.Fatalf("Parse error for record value '%s': %v", value, err)
		}

		dial, ok := resp.Children[0].(*Dial)
		if !ok {
			t.Fatalf("Expected *Dial, got %T", resp.Children[0])
		}

		if dial.Record != value {
			t.Errorf("Expected record '%s', got %q", value, dial.Record)
		}
	}
}

func TestParseDialRecordBackwardCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
	}{
		{"true maps to record-from-answer", "true", "record-from-answer"},
		{"false maps to do-not-record", "false", "do-not-record"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial record="` + tt.value + `">+15551234567</Dial>
</Response>`

			resp, err := Parse([]byte(xml))
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}

			dial, ok := resp.Children[0].(*Dial)
			if !ok {
				t.Fatalf("Expected *Dial, got %T", resp.Children[0])
			}

			if dial.Record != tt.expected {
				t.Errorf("Expected record '%s', got %q", tt.expected, dial.Record)
			}
		})
	}
}

func TestParseDialRecordInvalid(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial record="invalid-value">+15551234567</Dial>
</Response>`

	_, err := Parse([]byte(xml))
	if err == nil {
		t.Error("Expected error for invalid record value, got none")
	}
}

func TestParseDialRecordingStatusCallback(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial record="record-from-answer" recordingStatusCallback="http://example.com/recording">+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if dial.Record != "record-from-answer" {
		t.Errorf("Expected record 'record-from-answer', got %q", dial.Record)
	}

	if dial.RecordingStatusCallback != "http://example.com/recording" {
		t.Errorf("Expected recordingStatusCallback URL, got %q", dial.RecordingStatusCallback)
	}
}

func TestParseDialRecordDefault(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Dial>+15551234567</Dial>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	dial, ok := resp.Children[0].(*Dial)
	if !ok {
		t.Fatalf("Expected *Dial, got %T", resp.Children[0])
	}

	if dial.Record != "" {
		t.Errorf("Expected record to be empty by default, got %q", dial.Record)
	}
}

func TestParseEnqueue(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Enqueue>support</Enqueue>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	enqueue, ok := resp.Children[0].(*Enqueue)
	if !ok {
		t.Fatalf("Expected *Enqueue, got %T", resp.Children[0])
	}

	if enqueue.Name != "support" {
		t.Errorf("Expected name 'support', got %q", enqueue.Name)
	}
}

func TestParseRedirect(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Redirect method="GET">http://example.com/new</Redirect>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	redirect, ok := resp.Children[0].(*Redirect)
	if !ok {
		t.Fatalf("Expected *Redirect, got %T", resp.Children[0])
	}

	if redirect.URL != "http://example.com/new" {
		t.Errorf("Expected URL, got %q", redirect.URL)
	}
	if redirect.Method != "GET" {
		t.Errorf("Expected method GET, got %q", redirect.Method)
	}
}

func TestParsePause(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Pause length="3"/>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	pause, ok := resp.Children[0].(*Pause)
	if !ok {
		t.Fatalf("Expected *Pause, got %T", resp.Children[0])
	}

	if pause.Length != 3*time.Second {
		t.Errorf("Expected 3s, got %v", pause.Length)
	}
}

func TestParseHangup(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Hangup/>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	_, ok := resp.Children[0].(*Hangup)
	if !ok {
		t.Fatalf("Expected *Hangup, got %T", resp.Children[0])
	}
}

func TestParseMultipleVerbs(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say>Welcome</Say>
  <Pause length="1"/>
  <Gather numDigits="1">
    <Say>Press 1</Say>
  </Gather>
  <Hangup/>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if len(resp.Children) != 4 {
		t.Fatalf("Expected 4 children, got %d", len(resp.Children))
	}

	// Verify types
	if _, ok := resp.Children[0].(*Say); !ok {
		t.Errorf("Child 0 should be Say")
	}
	if _, ok := resp.Children[1].(*Pause); !ok {
		t.Errorf("Child 1 should be Pause")
	}
	if _, ok := resp.Children[2].(*Gather); !ok {
		t.Errorf("Child 2 should be Gather")
	}
	if _, ok := resp.Children[3].(*Hangup); !ok {
		t.Errorf("Child 3 should be Hangup")
	}
}

func TestParseRecord(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Record maxLength="30" playBeep="true" action="http://example.com/recording" transcribe="true" timeout="5"/>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	record, ok := resp.Children[0].(*Record)
	if !ok {
		t.Fatalf("Expected *Record, got %T", resp.Children[0])
	}

	if record.MaxLength != 30*time.Second {
		t.Errorf("Expected maxLength 30s, got %v", record.MaxLength)
	}
	if !record.PlayBeep {
		t.Error("Expected playBeep true")
	}
	if record.Action != "http://example.com/recording" {
		t.Errorf("Expected action URL, got %q", record.Action)
	}
	if !record.Transcribe {
		t.Error("Expected transcribe true")
	}
	if record.TimeoutInSeconds != 5*time.Second {
		t.Errorf("Expected timeout 5s, got %v", record.TimeoutInSeconds)
	}
}

func TestParseRecordDefaults(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Record/>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	record, ok := resp.Children[0].(*Record)
	if !ok {
		t.Fatalf("Expected *Record, got %T", resp.Children[0])
	}

	// Check defaults
	if record.MaxLength != 3600*time.Second {
		t.Errorf("Expected default maxLength 3600s, got %v", record.MaxLength)
	}
	if !record.PlayBeep {
		t.Error("Expected default playBeep true")
	}
	if record.Method != "POST" {
		t.Errorf("Expected default method POST, got %q", record.Method)
	}
	if record.Transcribe {
		t.Error("Expected default transcribe false")
	}
	if record.TimeoutInSeconds != 5*time.Second {
		t.Errorf("Expected default timeout 5s, got %v", record.TimeoutInSeconds)
	}
}

func TestParseSayLoop(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say voice="alice" loop="3">Hello World</Say>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	say, ok := resp.Children[0].(*Say)
	if !ok {
		t.Fatalf("Expected *Say, got %T", resp.Children[0])
	}

	if say.Loop != 3 {
		t.Errorf("Expected loop 3, got %d", say.Loop)
	}
}

func TestParsePlayLoop(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Play loop="5">http://example.com/music.mp3</Play>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	play, ok := resp.Children[0].(*Play)
	if !ok {
		t.Fatalf("Expected *Play, got %T", resp.Children[0])
	}

	if play.Loop != 5 {
		t.Errorf("Expected loop 5, got %d", play.Loop)
	}
	if play.URL != "http://example.com/music.mp3" {
		t.Errorf("Expected URL, got %q", play.URL)
	}
}

func TestParseGatherTimeoutAuto(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather timeout="auto">
    <Say>Press a digit</Say>
  </Gather>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	gather, ok := resp.Children[0].(*Gather)
	if !ok {
		t.Fatalf("Expected *Gather, got %T", resp.Children[0])
	}

	if gather.Timeout != "auto" {
		t.Errorf("Expected timeout 'auto', got %q", gather.Timeout)
	}
}

func TestParseGatherSpeechTimeout(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather input="speech" speechTimeout="auto" speechModel="default" hints="yes,no">
    <Say>Say yes or no</Say>
  </Gather>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	gather, ok := resp.Children[0].(*Gather)
	if !ok {
		t.Fatalf("Expected *Gather, got %T", resp.Children[0])
	}

	if gather.SpeechTimeout != "auto" {
		t.Errorf("Expected speechTimeout 'auto', got %q", gather.SpeechTimeout)
	}
	if gather.SpeechModel != "default" {
		t.Errorf("Expected speechModel 'default', got %q", gather.SpeechModel)
	}
	if gather.Hints != "yes,no" {
		t.Errorf("Expected hints 'yes,no', got %q", gather.Hints)
	}
}

func TestParseGatherFinishOnKey(t *testing.T) {
	tests := []struct {
		name        string
		finishOnKey string
		shouldError bool
	}{
		{"default hash", "#", false},
		{"asterisk", "*", false},
		{"digit 0", "0", false},
		{"digit 5", "5", false},
		{"digit 9", "9", false},
		{"empty string", "", false},
		{"invalid letter", "a", true},
		{"invalid multiple", "12", true},
		{"invalid double", "##", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather finishOnKey="` + tt.finishOnKey + `">
    <Say>Press digits</Say>
  </Gather>
</Response>`

			resp, err := Parse([]byte(xml))
			if tt.shouldError {
				if err == nil {
					t.Errorf("Expected error for finishOnKey %q, but got none", tt.finishOnKey)
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected parse error: %v", err)
			}

			gather, ok := resp.Children[0].(*Gather)
			if !ok {
				t.Fatalf("Expected *Gather, got %T", resp.Children[0])
			}

			if gather.FinishOnKey != tt.finishOnKey {
				t.Errorf("Expected finishOnKey %q, got %q", tt.finishOnKey, gather.FinishOnKey)
			}
		})
	}
}

func TestParseGatherDefaults(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Gather>
    <Say>Press a digit</Say>
  </Gather>
</Response>`

	resp, err := Parse([]byte(xml))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	gather, ok := resp.Children[0].(*Gather)
	if !ok {
		t.Fatalf("Expected *Gather, got %T", resp.Children[0])
	}

	if gather.Input != "dtmf" {
		t.Errorf("Expected default input 'dtmf', got %q", gather.Input)
	}
	if gather.Timeout != "5" {
		t.Errorf("Expected default timeout '5', got %q", gather.Timeout)
	}
	if gather.Method != "POST" {
		t.Errorf("Expected default method 'POST', got %q", gather.Method)
	}
}

func TestParseUnknownAttribute(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Say unknownAttr="value">Hello</Say>
</Response>`

	_, err := Parse([]byte(xml))
	if err == nil {
		t.Fatal("Expected error for unknown attribute, got none")
	}
}
