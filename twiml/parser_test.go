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

	if dial.Number != "+15551234567" {
		t.Errorf("Expected number, got %q", dial.Number)
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

	if dial.Queue != "support" {
		t.Errorf("Expected queue 'support', got %q", dial.Queue)
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

	if dial.Conference != "my-room" {
		t.Errorf("Expected conference 'my-room', got %q", dial.Conference)
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
