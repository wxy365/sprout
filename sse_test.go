package sprout

import (
	"testing"
	"time"
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
