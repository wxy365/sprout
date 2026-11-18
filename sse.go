package sprout

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	bjson "github.com/wxy365/basal/json"
)

// mimeEventStream is the MIME type for server-sent events.
const mimeEventStream = "text/event-stream"

// sseEvent represents a server-sent event.
//
// The fields map one to one onto the SSE protocol: ID is written as the "id"
// field, Event as the "event" field, Retry as the "retry" field and Data as
// the "data" field. The fields that carry no value are not written at all.
//
// The exported alias SSEEvent is declared in api.go, which collects the
// public API surface of the package.
type sseEvent struct {
	// ID is the identifier of the event. The client remembers the identifier
	// of the last event it received and sends it back in the Last-Event-ID
	// header when it reconnects.
	ID string
	// Event is the name of the event. The browser dispatches the event to the
	// listener registered for that name; an unnamed event is dispatched as a
	// plain message.
	Event string
	// Data is the payload of the event. A string or a byte slice is written as
	// is, any other value is serialized as JSON.
	Data any
	// Retry is the reconnection interval suggested to the client. It is
	// written in milliseconds.
	Retry time.Duration
}

// encode renders the event as an SSE frame. The fields are written in the
// order id, event, retry and data, and the frame ends with an empty line.
func (e sseEvent) encode() ([]byte, error) {
	data, err := sseData(e.Data)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if len(e.ID) > 0 {
		buf.WriteString("id: ")
		buf.WriteString(sanitizeSSEField(e.ID))
		buf.WriteByte('\n')
	}
	if len(e.Event) > 0 {
		buf.WriteString("event: ")
		buf.WriteString(sanitizeSSEField(e.Event))
		buf.WriteByte('\n')
	}
	if e.Retry > 0 {
		buf.WriteString("retry: ")
		buf.WriteString(strconv.FormatInt(e.Retry.Milliseconds(), 10))
		buf.WriteByte('\n')
	}
	for _, line := range strings.Split(data, "\n") {
		buf.WriteString("data: ")
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// sseData renders the payload of an event as text. A string and a byte slice
// are returned as they are, any other value is serialized as JSON. An empty
// payload is rendered as "null" rather than as an empty string, because the
// browser does not dispatch an event whose data buffer is empty.
func sseData(data any) (string, error) {
	switch v := data.(type) {
	case nil:
		return "null", nil
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	raw, err := bjson.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// sanitizeSSEField strips the newlines out of a field value. The SSE protocol
// does not allow a field value to contain a newline, because it would break
// the structure of the frame.
func sanitizeSSEField(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}
