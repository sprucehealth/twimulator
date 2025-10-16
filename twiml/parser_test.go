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
	if gather.Timeout != 5*time.Second {
		t.Errorf("Expected timeout 5s, got %v", gather.Timeout)
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
