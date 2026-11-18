package sprout

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wxy365/basal/errs"
)

// TestSSEEventEncode verifies the wire format of an SSE frame.
func TestSSEEventEncode(t *testing.T) {
	cases := []struct {
		name  string
		event sseEvent
		want  string
	}{
		{
			name:  "data only",
			event: sseEvent{Data: "hello"},
			want:  "data: hello\n\n",
		},
		{
			name:  "all the fields",
			event: sseEvent{ID: "42", Event: "tick", Data: "x", Retry: 3 * time.Second},
			want:  "id: 42\nevent: tick\nretry: 3000\ndata: x\n\n",
		},
		{
			name:  "an empty event still carries a data field",
			event: sseEvent{},
			want:  "data: null\n\n",
		},
		{
			name:  "a multi line payload is split into several data fields",
			event: sseEvent{Data: "a\nb"},
			want:  "data: a\ndata: b\n\n",
		},
		{
			name:  "the newlines of the field values are stripped",
			event: sseEvent{ID: "a\nb", Event: "c\r\nd", Data: "x"},
			want:  "id: ab\nevent: cd\ndata: x\n\n",
		},
		{
			name:  "a byte slice payload is written as is",
			event: sseEvent{Data: []byte("raw")},
			want:  "data: raw\n\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.event.encode()
			if err != nil {
				t.Fatalf("the event should be encoded, but got: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("the frame = %q, want %q", string(got), c.want)
			}
		})
	}
}

// TestSSEEventEncodeJSONPayload verifies that a structured payload is
// serialized as JSON.
func TestSSEEventEncodeJSONPayload(t *testing.T) {
	type payload struct {
		N int `json:"n"`
	}
	got, err := sseEvent{Data: payload{N: 7}}.encode()
	if err != nil {
		t.Fatalf("the event should be encoded, but got: %v", err)
	}
	want := "data: {\"n\":7}\n\n"
	if string(got) != want {
		t.Errorf("the frame = %q, want %q", string(got), want)
	}
}

// newTestSSEStream builds a stream that writes into the given recorder.
func newTestSSEStream(t *testing.T, rec *httptest.ResponseRecorder) *sseStream {
	t.Helper()
	req := newTestRequest(http.MethodGet, "/sse", "")
	ctx := &requestContext{Request: req, Writer: rec}
	return newSSEStream(ctx, 0)
}

// TestSSEStreamSend verifies that a sent event reaches the response and that
// the response is committed on the first write.
func TestSSEStreamSend(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Send(sseEvent{Data: "hello"}); err != nil {
		t.Fatalf("the event should be sent, but got: %v", err)
	}
	if rec.Body.String() != "data: hello\n\n" {
		t.Errorf("the body = %q, want %q", rec.Body.String(), "data: hello\n\n")
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("the Cache-Control header = %q, want %q", got, "no-cache")
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("the X-Accel-Buffering header = %q, want %q", got, "no")
	}
	if !rec.Flushed {
		t.Error("the response should be flushed after an event is written")
	}
	if !stream.isCommitted() {
		t.Error("the stream should be committed after an event is written")
	}
}

// TestSSEStreamCloseCommitsTheResponse verifies that closing a stream that
// has not written anything still commits an empty but well formed response.
func TestSSEStreamCloseCommitsTheResponse(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Close(); err != nil {
		t.Fatalf("the stream should be closed, but got: %v", err)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if len(rec.Body.Bytes()) != 0 {
		t.Errorf("the body should be empty, but got: %q", rec.Body.String())
	}
}

// TestSSEStreamSendAfterClose verifies that writing to a closed stream fails
// instead of silently dropping the event.
func TestSSEStreamSendAfterClose(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)
	stream.Close()

	if err := stream.Send(sseEvent{Data: "hello"}); err == nil {
		t.Error("sending to a closed stream should fail")
	}
}

// TestSSEStreamComment verifies that a comment is written as a comment line,
// which the browser ignores.
func TestSSEStreamComment(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	if err := stream.Comment("ping"); err != nil {
		t.Fatalf("the comment should be written, but got: %v", err)
	}
	if rec.Body.String() != ": ping\n\n" {
		t.Errorf("the body = %q, want %q", rec.Body.String(), ": ping\n\n")
	}
}

// TestSSEStreamDisconnect verifies that the stream reports the disconnection
// of the client.
func TestSSEStreamDisconnect(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/sse", "")
	cancellable, cancel := context.WithCancel(req.Context())
	req = req.WithContext(cancellable)
	rec := newResponseRecorder()
	stream := newSSEStream(&requestContext{Request: req, Writer: rec}, 0)

	cancel()

	select {
	case <-stream.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the stream should report the disconnection of the client")
	}
	if stream.Err() == nil {
		t.Error("the stream should report why it ended")
	}
}

// syncWriter is a response writer that the keep alive goroutine and the test
// can use at the same time. The recorder of the standard library is not safe
// for the concurrent use.
type syncWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
}

func (w *syncWriter) Header() http.Header {
	return w.header
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}

func (w *syncWriter) WriteHeader(code int) {}

func (w *syncWriter) Flush() {}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

// TestSSEStreamKeepAlive verifies that the keep alive comments are written
// while the stream is open.
func TestSSEStreamKeepAlive(t *testing.T) {
	req := newTestRequest(http.MethodGet, "/sse", "")
	w := &syncWriter{header: http.Header{}}
	stream := newSSEStream(&requestContext{Request: req, Writer: w}, 20*time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), ": ping") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	stream.Close()

	if !strings.Contains(w.String(), ": ping") {
		t.Errorf("the keep alive comment is missing, but got: %q", w.String())
	}
}

// TestSSEStreamSurvivesTheWriteTimeout verifies that the stream outlives the
// write timeout of the server, which would otherwise cut a long connection
// short.
func TestSSEStreamSurvivesTheWriteTimeout(t *testing.T) {
	svr := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := &requestContext{Request: r, Writer: w}
			stream := newSSEStream(ctx, 0)
			defer stream.Close()
			for i := 0; i < 4; i++ {
				if err := stream.Send(sseEvent{Data: "tick"}); err != nil {
					return
				}
				time.Sleep(300 * time.Millisecond)
			}
		}),
		WriteTimeout: 500 * time.Millisecond,
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("the listener should be created, but got: %v", err)
	}
	defer ln.Close()
	go svr.Serve(ln)
	defer svr.Close()

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("the request should be served, but got: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("the body should be read, but got: %v", err)
	}
	// The four ticks are 300 milliseconds apart, so the stream lives for about
	// 1.2 seconds, well beyond the write timeout of 500 milliseconds. Without
	// the write deadline being cleared, the third write would fail.
	if got := strings.Count(string(body), "data: tick\n\n"); got != 4 {
		t.Errorf("the number of the events = %d, want %d", got, 4)
	}
}

// TestSSEStreamConcurrentSend verifies that the concurrent writes of several
// goroutines do not interleave the frames.
func TestSSEStreamConcurrentSend(t *testing.T) {
	rec := newResponseRecorder()
	stream := newTestSSEStream(t, rec)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := stream.Send(sseEvent{Data: "0123456789"}); err != nil {
					t.Errorf("the event should be sent, but got: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	body := rec.Body.String()
	if got := strings.Count(body, "data: 0123456789\n\n"); got != 80 {
		t.Errorf("the number of the complete frames = %d, want %d", got, 80)
	}
}

// sseInput is the input entity used by the SSE endpoint tests.
type sseInput struct {
	Name  string `query:"name"`
	Count int    `query:"count" validate:"[1,10]"`
}

// serveSSE mounts the given SSE endpoint on a fresh server and serves one
// request against it. It returns the recorder holding the response.
func serveSSE[I any](t *testing.T, e *sseEndpoint[I], req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	svr := newDefaultServer("test")
	e.appendToServer(svr, nil)
	mx := svr.buildMux()
	rec := httptest.NewRecorder()
	mx.ServeHTTP(rec, req)
	return rec
}

// assertSSEBody verifies that every line of the body belongs to an SSE frame,
// so that a plain JSON error never leaks into the stream.
func assertSSEBody(t *testing.T, body string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if len(line) == 0 {
			continue
		}
		ok := strings.HasPrefix(line, "data: ") ||
			strings.HasPrefix(line, "event: ") ||
			strings.HasPrefix(line, "id: ") ||
			strings.HasPrefix(line, "retry: ") ||
			strings.HasPrefix(line, ": ")
		if !ok {
			t.Errorf("the line %q is not part of an SSE frame", line)
		}
	}
}

// TestSSEEndpointSuccess verifies the happy path of an SSE endpoint.
func TestSSEEndpointSuccess(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return stream.Send(sseEvent{Event: "tick", Data: "hello " + in.Name})
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse?name=foo", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	want := "event: tick\ndata: hello foo\n\n"
	if rec.Body.String() != want {
		t.Errorf("the body = %q, want %q", rec.Body.String(), want)
	}
}

// TestSSEEndpointLazyCommit verifies that an endpoint that writes nothing
// still answers with a well formed but empty SSE response.
func TestSSEEndpointLazyCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return nil
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != mimeEventStream {
		t.Errorf("the Content-Type header = %q, want %q", got, mimeEventStream)
	}
	if len(rec.Body.Bytes()) != 0 {
		t.Errorf("the body should be empty, but got: %q", rec.Body.String())
	}
}

// TestSSEEndpointErrorBeforeCommit verifies that an error that happens before
// the first event is reported with its own status code and a plain JSON body.
func TestSSEEndpointErrorBeforeCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			return errs.New("something went wrong").WithStatus(http.StatusConflict)
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusConflict {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusConflict)
	}
	if !strings.Contains(rec.Body.String(), "something went wrong") {
		t.Errorf("the body should contain the error message, but got: %s", rec.Body.String())
	}
}

// TestSSEEndpointErrorAfterCommit verifies that an error that happens after
// the first event is reported as an error event, because the status code can
// no longer be changed.
func TestSSEEndpointErrorAfterCommit(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			if err := stream.Send(sseEvent{Data: "before"}); err != nil {
				return err
			}
			return errs.New("something went wrong").WithStatus(http.StatusConflict)
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("the status code = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "data: before\n\n") {
		t.Errorf("the first event is missing, but got: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "event: error\n") {
		t.Errorf("the error event is missing, but got: %s", rec.Body.String())
	}
	assertSSEBody(t, rec.Body.String())
}

// TestSSEEndpointPanicRecovered verifies that a panic in the handler becomes
// an error event instead of a JSON body mixed into the stream.
func TestSSEEndpointPanicRecovered(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			if err := stream.Send(sseEvent{Data: "before"}); err != nil {
				return err
			}
			panic("boom")
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse", nil))

	if !strings.Contains(rec.Body.String(), "event: error\n") {
		t.Errorf("the error event is missing, but got: %s", rec.Body.String())
	}
	assertSSEBody(t, rec.Body.String())
}

// TestSSEEndpointValidationFailure verifies that an invalid input is rejected
// before the stream is opened.
func TestSSEEndpointValidationFailure(t *testing.T) {
	e := &sseEndpoint[sseInput]{
		Name:    "sse",
		Pattern: "/sse",
		Methods: []string{http.MethodGet},
		Handler: func(ctx *requestContext, in sseInput, stream *sseStream) error {
			t.Error("the handler should not be called for an invalid input")
			return nil
		},
	}

	rec := serveSSE(t, e, httptest.NewRequest(http.MethodGet, "/sse?count=99", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("the status code = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
