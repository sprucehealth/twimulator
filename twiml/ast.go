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
	Loop     int
}

func (Say) isNode() {}

// Play plays an audio file
type Play struct {
	URL  string
	Loop int
}

func (Play) isNode() {}

// Pause waits for a specified duration
type Pause struct {
	Length time.Duration
}

func (Pause) isNode() {}

// Gather collects DTMF input
type Gather struct {
	Input         string // "dtmf", "speech", "dtmf speech"
	Timeout       string // Can be "auto" or a positive integer (in seconds), default is 5
	NumDigits     int
	FinishOnKey   string // The digit to end input, default is "#"
	Action        string
	Method        string // "POST" or "GET"
	Hints         string
	SpeechTimeout string // Can be "auto" or a positive integer (in seconds)
	SpeechModel   string
	Children      []Node // Nested verbs to execute while gathering
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
	Name          string
	Action        string
	Method        string
	WaitURL       string
	WaitURLMethod string
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

// Record records the caller's voice
type Record struct {
	MaxLength        time.Duration
	PlayBeep         bool
	Action           string
	Method           string
	Transcribe       bool
	TimeoutInSeconds time.Duration
}

func (Record) isNode() {}

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
	Name                   string
	Muted                  bool
	StartConferenceOnEnter bool
	EndConferenceOnExit    bool
	WaitURL                string
	StatusCallback         string
	StatusCallbackEvent    string
}

func (ConferenceDial) isNode() {}
