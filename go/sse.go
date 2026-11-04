package sbbox

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// sseEvent is one parsed text/event-stream event.
type sseEvent struct {
	Event string
	Data  string
	ID    string
}

type sseReader struct {
	r  *bufio.Reader
	rc io.Closer
}

func newSSEReader(rc io.ReadCloser) *sseReader {
	return &sseReader{r: bufio.NewReaderSize(rc, 64*1024), rc: rc}
}

// next returns the next event, or io.EOF.
func (s *sseReader) next() (sseEvent, error) {
	var ev sseEvent
	var data []string
	got := false
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			if err == io.EOF && got {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			return ev, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if got {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		got = true
		switch field {
		case "event":
			ev.Event = value
		case "data":
			data = append(data, value)
		case "id":
			ev.ID = value
		}
	}
}

func (s *sseReader) close() error { return s.rc.Close() }

// ExecStream iterates the events of a streamed command.
type ExecStream struct {
	sse  *sseReader
	err  error
	done bool
}

// Next returns the next event; false when the stream ended (check Err).
func (s *ExecStream) Next() (ExecEvent, bool) {
	if s.done {
		return ExecEvent{}, false
	}
	for {
		raw, err := s.sse.next()
		if err != nil {
			if err != io.EOF {
				s.err = err
			}
			s.done = true
			_ = s.sse.close()
			return ExecEvent{}, false
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(raw.Data), &body)
		ev := ExecEvent{Kind: raw.Event}
		switch raw.Event {
		case "execId":
			ev.ExecID, _ = body["execId"].(string)
		case "stdout", "stderr", "data":
			ev.Data, _ = body["data"].(string)
			if raw.Event == "data" {
				ev.Kind = "stdout"
			}
		case "exit":
			if c, ok := body["exitCode"].(float64); ok {
				ev.ExitCode = int(c)
			}
			s.done = true
			_ = s.sse.close()
		case "error":
			ev.Data, _ = body["error"].(string)
			ev.Stage, _ = body["stage"].(string)
			s.done = true
			_ = s.sse.close()
		default:
			continue
		}
		return ev, true
	}
}

// Err returns the transport error that ended the stream, if any.
func (s *ExecStream) Err() error { return s.err }

// Close abandons the stream.
func (s *ExecStream) Close() error { s.done = true; return s.sse.close() }

// PtyStream iterates the events of a connected PTY (SSE).
type PtyStream struct {
	sse  *sseReader
	err  error
	done bool
}

// Next returns the next event; false when the stream ended (check Err).
func (p *PtyStream) Next() (PtyEvent, bool) {
	if p.done {
		return PtyEvent{}, false
	}
	for {
		raw, err := p.sse.next()
		if err != nil {
			if err != io.EOF {
				p.err = err
			}
			p.done = true
			_ = p.sse.close()
			return PtyEvent{}, false
		}
		var body map[string]any
		_ = json.Unmarshal([]byte(raw.Data), &body)
		switch raw.Event {
		case "data", "stdout":
			d, _ := body["data"].(string)
			return PtyEvent{Data: d}, true
		case "exit":
			ev := PtyEvent{Exit: true}
			ev.Reason, _ = body["reason"].(string)
			if c, ok := body["exitCode"].(float64); ok {
				ev.ExitCode = int(c)
			}
			p.done = true
			_ = p.sse.close()
			return ev, true
		case "error":
			ev := PtyEvent{Exit: true}
			ev.Error, _ = body["error"].(string)
			p.done = true
			_ = p.sse.close()
			return ev, true
		}
	}
}

func (p *PtyStream) Err() error   { return p.err }
func (p *PtyStream) Close() error { p.done = true; return p.sse.close() }
