package twiml

import "time"

// Node is the interface for all TwiML AST nodes
type Node interface {
	isNode()
}

// Response is the root TwiML element
type Response struct {
	Children []Node
}

func (Response) isNode() {}

// Say outputs text-to-speech
type Say struct {
	Text     string
	Voice    string
	Language string
}

func (Say) isNode() {}

// Play plays an audio file
type Play struct {
	URL string
}

func (Play) isNode() {}

// Pause waits for a specified duration
type Pause struct {
	Length time.Duration
}

func (Pause) isNode() {}

// Gather collects DTMF input
type Gather struct {
	Input     string // "dtmf", "speech", "dtmf speech"
	Timeout   time.Duration
	NumDigits int
	Action    string
	Method    string // "POST" or "GET"
	Children  []Node // Nested verbs to execute while gathering
}

func (Gather) isNode() {}

// Dial connects to another party
type Dial struct {
	Number     string
	Client     string
	Queue      string
	Conference string
	Action     string
	Method     string
	Timeout    time.Duration
	Children   []Node // For nested <Number>, <Client>, <Queue>, <Conference>
}

func (Dial) isNode() {}

// Enqueue adds caller to a queue
type Enqueue struct {
	Name       string
	Action     string
	Method     string
	WaitURL    string
	WaitMethod string
}

func (Enqueue) isNode() {}

// Redirect fetches new TwiML from a URL
type Redirect struct {
	URL    string
	Method string
}

func (Redirect) isNode() {}

// Hangup ends the call
type Hangup struct{}

func (Hangup) isNode() {}

// Number is used inside <Dial> to specify a phone number
type Number struct {
	Number string
}

func (Number) isNode() {}

// Client is used inside <Dial> to dial a Twilio Client
type Client struct {
	Name string
}

func (Client) isNode() {}

// QueueDial is used inside <Dial> to dial a queue member
type QueueDial struct {
	Name string
}

func (QueueDial) isNode() {}

// ConferenceDial is used inside <Dial> to join a conference
type ConferenceDial struct {
	Name                 string
	Muted                bool
	StartConferenceOnEnter bool
	EndConferenceOnExit  bool
	WaitURL              string
	StatusCallback       string
	StatusCallbackEvent  string
}

func (ConferenceDial) isNode() {}
