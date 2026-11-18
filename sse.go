package sprout

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wxy365/basal/errs"
	bjson "github.com/wxy365/basal/json"
	"github.com/wxy365/basal/log"
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

// errSSEClosed is returned when an event is written to a stream that has
// already ended.
var errSSEClosed = errors.New("The SSE stream has been closed")

// sseStream writes server-sent events to an HTTP response.
//
// The framework creates the stream before it calls the business logic and
// hands it over as the third argument of the handler. The writes are
// serialized by a mutex, so the keep alive goroutine and the handler may call
// Send at the same time.
//
// The exported alias SSEStream is declared in api.go, which collects the
// public API surface of the package.
type sseStream struct {
	writer     http.ResponseWriter
	controller *http.ResponseController
	// requestCtx is the context of the request that opened the stream. It is
	// cancelled when the client disconnects.
	requestCtx context.Context
	// protoMajor is the major version of the HTTP protocol of the request.
	// Only HTTP/1.1 may carry the Connection header.
	protoMajor int

	mu        sync.Mutex
	committed bool
	closed    bool
	err       error
	done      chan struct{}
	closeOnce sync.Once
}

// newSSEStream builds a stream that writes into the response of the given
// request. A keep alive interval greater than zero starts the goroutine that
// keeps the connection alive.
func newSSEStream(ctx *requestContext, keepAlive time.Duration) *sseStream {
	s := &sseStream{
		writer:     ctx.Writer,
		controller: http.NewResponseController(ctx.Writer),
		requestCtx: ctx.Request.Context(),
		protoMajor: ctx.Request.ProtoMajor,
		done:       make(chan struct{}),
	}
	// The write timeout of the server cuts a long connection short, so it is
	// cleared here. The writers of HTTP/1.1, HTTP/2 and HTTP/3 all support
	// it.
	if err := s.controller.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		log.WarnErrF("Failed to clear the write deadline of the SSE stream", err)
	}
	go s.watchDisconnect()
	if keepAlive > 0 {
		go s.keepAlive(keepAlive)
	}
	return s
}

// Send writes an event to the stream. The response is committed on the first
// write.
func (s *sseStream) Send(ev sseEvent) error {
	frame, err := ev.encode()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSSEClosed
	}
	s.commitLocked()
	if _, err := s.writer.Write(frame); err != nil {
		s.failLocked(err)
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failLocked(err)
		return err
	}
	return nil
}

// SendData writes an event that carries nothing but a payload.
func (s *sseStream) SendData(data any) error {
	return s.Send(sseEvent{Data: data})
}

// SendEvent writes a named event.
func (s *sseStream) SendEvent(name string, data any) error {
	return s.Send(sseEvent{Event: name, Data: data})
}

// Comment writes a comment line. A comment is invisible to the browser, so it
// only keeps the connection alive.
func (s *sseStream) Comment(text string) error {
	line := ": " + sanitizeSSEField(text) + "\n\n"
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errSSEClosed
	}
	s.commitLocked()
	if _, err := s.writer.Write([]byte(line)); err != nil {
		s.failLocked(err)
		return err
	}
	if err := s.controller.Flush(); err != nil {
		s.failLocked(err)
		return err
	}
	return nil
}

// Close ends the stream. A stream that has not written anything yet is
// committed on the way out, so that the client receives a well formed but
// empty response.
func (s *sseStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.commitLocked()
	s.closed = true
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}

// Done is closed when the client disconnects, when a write fails or when the
// stream is closed. The handler should watch it to leave its loop in time.
func (s *sseStream) Done() <-chan struct{} {
	return s.done
}

// Err reports why the stream ended: the error of the request context when the
// client disconnected, the underlying I/O error when a write failed, and nil
// when the stream was closed on purpose.
func (s *sseStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// isCommitted reports whether the response has been committed. Once it is,
// the status code can no longer be changed, so an error can only be reported
// as an event.
func (s *sseStream) isCommitted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.committed
}

// sendError reports an error as an error event. It is only useful once the
// response has been committed.
func (s *sseStream) sendError(err error) error {
	payload := map[string]any{"message": err.Error()}
	var e *errs.Err
	if errors.As(err, &e) {
		if len(e.Code) > 0 {
			payload["code"] = e.Code
		}
		if len(e.Message) > 0 {
			payload["message"] = e.Message
		}
		if e.Status > 0 {
			payload["status"] = e.Status
		}
	}
	return s.Send(sseEvent{Event: "error", Data: payload})
}

// commitLocked writes the headers of the SSE response. The response is
// committed lazily: the headers go out only when a frame is really written, so
// that an error that happens before that still gets the right status code.
// The caller must hold s.mu.
func (s *sseStream) commitLocked() {
	if s.committed {
		return
	}
	header := s.writer.Header()
	header.Set("Content-Type", mimeEventStream)
	header.Set("Cache-Control", "no-cache")
	header.Set("X-Accel-Buffering", "no")
	if s.protoMajor == 1 {
		// HTTP/2 and HTTP/3 do not allow the connection specific headers.
		header.Set("Connection", "keep-alive")
	}
	s.writer.WriteHeader(http.StatusOK)
	s.committed = true
}

// failLocked ends the stream with the given error. The caller must hold s.mu.
func (s *sseStream) failLocked(err error) {
	if s.closed {
		return
	}
	s.closed = true
	if err != nil && s.err == nil {
		s.err = err
	}
	s.closeOnce.Do(func() { close(s.done) })
}

// watchDisconnect waits for the client to disconnect. The request context is
// cancelled when the client goes away and when the handler returns, so this
// goroutine always ends.
func (s *sseStream) watchDisconnect() {
	<-s.requestCtx.Done()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failLocked(s.requestCtx.Err())
}

// keepAlive writes a comment line at the given interval, so that the proxies
// in between do not drop an idle connection.
func (s *sseStream) keepAlive(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := s.Comment("ping"); err != nil {
				return
			}
		case <-s.done:
			return
		}
	}
}
