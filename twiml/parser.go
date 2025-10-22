package twiml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Parse parses TwiML XML and returns a Response AST
func Parse(data []byte) (*Response, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	var resp Response

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml parse error: %w", err)
		}

		if se, ok := token.(xml.StartElement); ok {
			if se.Name.Local == "Response" {
				if err := parseResponse(decoder, &se, &resp); err != nil {
					return nil, err
				}
				return &resp, nil
			}
		}
	}

	return nil, fmt.Errorf("no <Response> element found")
}

func parseResponse(decoder *xml.Decoder, start *xml.StartElement, resp *Response) error {
	// Check for unknown attributes on <Response>
	for _, attr := range start.Attr {
		return fmt.Errorf("unknown attribute '%s' on <Response>", attr.Name.Local)
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		switch t := token.(type) {
		case xml.StartElement:
			node, err := parseNode(decoder, &t)
			if err != nil {
				return err
			}
			if node != nil {
				resp.Children = append(resp.Children, node)
			}
		case xml.EndElement:
			if t.Name.Local == "Response" {
				return nil
			}
		}
	}
	return nil
}

func parseNode(decoder *xml.Decoder, start *xml.StartElement) (Node, error) {
	switch start.Name.Local {
	case "Say":
		return parseSay(decoder, start)
	case "Play":
		return parsePlay(decoder, start)
	case "Pause":
		return parsePause(decoder, start)
	case "Gather":
		return parseGather(decoder, start)
	case "Dial":
		return parseDial(decoder, start)
	case "Enqueue":
		return parseEnqueue(decoder, start)
	case "Redirect":
		return parseRedirect(decoder, start)
	case "Hangup":
		// Hangup is self-closing, consume the end tag
		decoder.Skip()
		return &Hangup{}, nil
	case "Record":
		return parseRecord(decoder, start)
	case "Number":
		return parseNumber(decoder, start)
	case "Client":
		return parseClient(decoder, start)
	case "Queue":
		return parseQueueDial(decoder, start)
	case "Conference":
		return parseConferenceDial(decoder, start)
	default:
		return nil, fmt.Errorf("unknown TwiML element: <%s>", start.Name.Local)
	}
}

func parseSay(decoder *xml.Decoder, start *xml.StartElement) (*Say, error) {
	say := &Say{}
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "voice":
			say.Voice = attr.Value
		case "language":
			say.Language = attr.Value
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Say>", attr.Name.Local)
		}
	}

	// Get text content
	if err := decoder.DecodeElement(&say.Text, start); err != nil {
		return nil, err
	}

	return say, nil
}

func parsePlay(decoder *xml.Decoder, start *xml.StartElement) (*Play, error) {
	play := &Play{}
	for _, attr := range start.Attr {
		return nil, fmt.Errorf("unknown attribute '%s' on <Play>", attr.Name.Local)
	}
	if err := decoder.DecodeElement(&play.URL, start); err != nil {
		return nil, err
	}
	return play, nil
}

func parsePause(decoder *xml.Decoder, start *xml.StartElement) (*Pause, error) {
	pause := &Pause{Length: 1 * time.Second} // default 1s
	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "length":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				pause.Length = time.Duration(n) * time.Second
			}
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Pause>", attr.Name.Local)
		}
	}
	decoder.Skip()
	return pause, nil
}

func parseGather(decoder *xml.Decoder, start *xml.StartElement) (*Gather, error) {
	gather := &Gather{
		Input:     "dtmf",
		Timeout:   5 * time.Second,
		NumDigits: 0,
		Method:    "POST",
	}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "input":
			gather.Input = attr.Value
		case "timeout":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				gather.Timeout = time.Duration(n) * time.Second
			}
		case "numDigits":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				gather.NumDigits = n
			}
		case "action":
			gather.Action = attr.Value
		case "method":
			gather.Method = strings.ToUpper(attr.Value)
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Gather>", attr.Name.Local)
		}
	}

	// Parse nested children
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.StartElement:
			node, err := parseNode(decoder, &t)
			if err != nil {
				return nil, err
			}
			if node != nil {
				gather.Children = append(gather.Children, node)
			}
		case xml.EndElement:
			if t.Name.Local == "Gather" {
				return gather, nil
			}
		}
	}

	return gather, nil
}

func parseDial(decoder *xml.Decoder, start *xml.StartElement) (*Dial, error) {
	dial := &Dial{
		Method:  "POST",
		Timeout: 30 * time.Second,
	}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "action":
			dial.Action = attr.Value
		case "method":
			dial.Method = strings.ToUpper(attr.Value)
		case "timeout":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				dial.Timeout = time.Duration(n) * time.Second
			}
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Dial>", attr.Name.Local)
		}
	}

	// Parse content which could be plain text (number) or nested elements
	var textContent string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		switch t := token.(type) {
		case xml.CharData:
			textContent += strings.TrimSpace(string(t))
		case xml.StartElement:
			node, err := parseNode(decoder, &t)
			if err != nil {
				return nil, err
			}
			if node != nil {
				dial.Children = append(dial.Children, node)
				// Extract specific fields from children
				switch n := node.(type) {
				case *Number:
					dial.Number = n.Number
				case *Client:
					dial.Client = n.Name
				case *QueueDial:
					dial.Queue = n.Name
				case *ConferenceDial:
					dial.Conference = n.Name
				}
			}
		case xml.EndElement:
			if t.Name.Local == "Dial" {
				// If no children but has text, it's a plain number
				if len(dial.Children) == 0 && textContent != "" {
					dial.Number = textContent
				}
				return dial, nil
			}
		}
	}

	return dial, nil
}

func parseEnqueue(decoder *xml.Decoder, start *xml.StartElement) (*Enqueue, error) {
	enqueue := &Enqueue{
		Method:     "POST",
		WaitMethod: "POST",
	}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "action":
			enqueue.Action = attr.Value
		case "method":
			enqueue.Method = strings.ToUpper(attr.Value)
		case "waitUrl":
			enqueue.WaitURL = attr.Value
		case "waitMethod":
			enqueue.WaitMethod = strings.ToUpper(attr.Value)
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Enqueue>", attr.Name.Local)
		}
	}

	if err := decoder.DecodeElement(&enqueue.Name, start); err != nil {
		return nil, err
	}

	return enqueue, nil
}

func parseRedirect(decoder *xml.Decoder, start *xml.StartElement) (*Redirect, error) {
	redirect := &Redirect{Method: "POST"}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "method":
			redirect.Method = strings.ToUpper(attr.Value)
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Redirect>", attr.Name.Local)
		}
	}

	if err := decoder.DecodeElement(&redirect.URL, start); err != nil {
		return nil, err
	}

	return redirect, nil
}

func parseNumber(decoder *xml.Decoder, start *xml.StartElement) (*Number, error) {
	num := &Number{}
	for _, attr := range start.Attr {
		return nil, fmt.Errorf("unknown attribute '%s' on <Number>", attr.Name.Local)
	}
	if err := decoder.DecodeElement(&num.Number, start); err != nil {
		return nil, err
	}
	return num, nil
}

func parseClient(decoder *xml.Decoder, start *xml.StartElement) (*Client, error) {
	client := &Client{}
	for _, attr := range start.Attr {
		return nil, fmt.Errorf("unknown attribute '%s' on <Client>", attr.Name.Local)
	}
	if err := decoder.DecodeElement(&client.Name, start); err != nil {
		return nil, err
	}
	return client, nil
}

func parseQueueDial(decoder *xml.Decoder, start *xml.StartElement) (*QueueDial, error) {
	queue := &QueueDial{}
	for _, attr := range start.Attr {
		return nil, fmt.Errorf("unknown attribute '%s' on <Queue>", attr.Name.Local)
	}
	if err := decoder.DecodeElement(&queue.Name, start); err != nil {
		return nil, err
	}
	return queue, nil
}

func parseConferenceDial(decoder *xml.Decoder, start *xml.StartElement) (*ConferenceDial, error) {
	conf := &ConferenceDial{
		StartConferenceOnEnter: true,
		EndConferenceOnExit:    false,
	}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "muted":
			conf.Muted = attr.Value == "true"
		case "startConferenceOnEnter":
			conf.StartConferenceOnEnter = attr.Value == "true"
		case "endConferenceOnExit":
			conf.EndConferenceOnExit = attr.Value == "true"
		case "waitUrl":
			conf.WaitURL = attr.Value
		case "statusCallback":
			conf.StatusCallback = attr.Value
		case "statusCallbackEvent":
			conf.StatusCallbackEvent = attr.Value
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Conference>", attr.Name.Local)
		}
	}

	if err := decoder.DecodeElement(&conf.Name, start); err != nil {
		return nil, err
	}

	return conf, nil
}

func parseRecord(decoder *xml.Decoder, start *xml.StartElement) (*Record, error) {
	record := &Record{
		MaxLength:        3600 * time.Second, // default 1 hour
		PlayBeep:         true,                // default true
		Method:           "POST",
		Transcribe:       false,
		TimeoutInSeconds: 5 * time.Second, // default 5 seconds
	}

	for _, attr := range start.Attr {
		switch attr.Name.Local {
		case "maxLength":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				record.MaxLength = time.Duration(n) * time.Second
			}
		case "playBeep":
			record.PlayBeep = attr.Value == "true"
		case "action":
			record.Action = attr.Value
		case "method":
			record.Method = strings.ToUpper(attr.Value)
		case "transcribe":
			record.Transcribe = attr.Value == "true"
		case "timeout":
			if n, err := strconv.Atoi(attr.Value); err == nil {
				record.TimeoutInSeconds = time.Duration(n) * time.Second
			}
		default:
			return nil, fmt.Errorf("unknown attribute '%s' on <Record>", attr.Name.Local)
		}
	}

	decoder.Skip()
	return record, nil
}
