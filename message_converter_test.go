package sprout

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// formModel is the model used by the multipart form tests.
type formModel struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestSerializeJson(t *testing.T) {
	rec := newResponseRecorder()
	model := formModel{Name: "foo", Count: 3}

	if err := SerializeJson(model, rec); err != nil {
		t.Fatalf("the model should be serialized, but got: %v", err)
	}

	var got formModel
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("the serialized body is not valid JSON: %v", err)
	}
	if got.Name != "foo" || got.Count != 3 {
		t.Errorf("the serialized model = %+v, want {foo 3}", got)
	}
}

func TestDeserializeJson(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", `{"name":"foo","count":3}`)
	req.Header.Set("Content-Type", MimeJson)

	var model formModel
	if err := DeserializeJson(req, &model); err != nil {
		t.Fatalf("the body should be deserialized, but got: %v", err)
	}
	if model.Name != "foo" || model.Count != 3 {
		t.Errorf("the deserialized model = %+v, want {foo 3}", model)
	}
}

// TestDeserializeJsonEmptyBody verifies that an empty body is not an error.
func TestDeserializeJsonEmptyBody(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", "")

	var model formModel
	if err := DeserializeJson(req, &model); err != nil {
		t.Fatalf("an empty body should be accepted, but got: %v", err)
	}
	if model.Name != "" || model.Count != 0 {
		t.Errorf("the model should stay empty, but got: %+v", model)
	}
}

// TestDeserializeJsonInvalidBody verifies that a malformed body is reported.
func TestDeserializeJsonInvalidBody(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", `{"name":`)

	var model formModel
	if err := DeserializeJson(req, &model); err == nil {
		t.Error("the malformed body should be rejected")
	}
}

// TestSerializeMultipartForm verifies that the multipart serializer writes the
// regular fields as form fields.
func TestSerializeMultipartForm(t *testing.T) {
	rec := newResponseRecorder()
	model := formModel{Name: "foo", Count: 3}

	if err := SerializeMultipartForm(&model, rec); err != nil {
		t.Fatalf("the model should be serialized, but got: %v", err)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `name="name"`) || !strings.Contains(body, "foo") {
		t.Errorf("the body should contain the name field, but got: %s", body)
	}
	if !strings.Contains(body, `name="count"`) {
		t.Errorf("the body should contain the count field, but got: %s", body)
	}
}

// TestDeserializeMultipartForm verifies that the multipart deserializer reads
// the form fields into the model.
func TestDeserializeMultipartForm(t *testing.T) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("Name", "foo"); err != nil {
		t.Fatalf("the form field should be written, but got: %v", err)
	}
	if err := writer.WriteField("Count", "3"); err != nil {
		t.Fatalf("the form field should be written, but got: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("the multipart writer should be closed, but got: %v", err)
	}

	req := newTestRequest(http.MethodPost, "/x", buf.String())
	ctx := context.WithValue(req.Context(), ctxKeyContentTypeParams, map[string]string{"boundary": writer.Boundary()})
	req = req.WithContext(ctx)

	var model formModel
	if err := DeserializeMultipartForm(req, &model); err != nil {
		t.Fatalf("the form should be deserialized, but got: %v", err)
	}
	if model.Name != "foo" {
		t.Errorf("the deserialized Name = %q, want %q", model.Name, "foo")
	}
	if model.Count != 3 {
		t.Errorf("the deserialized Count = %d, want %d", model.Count, 3)
	}
}

// TestDeserializeMultipartFormWithoutBoundary verifies that a missing boundary
// is reported as an error.
func TestDeserializeMultipartFormWithoutBoundary(t *testing.T) {
	req := newTestRequest(http.MethodPost, "/x", "")

	var model formModel
	if err := DeserializeMultipartForm(req, &model); err == nil {
		t.Error("the missing boundary should be reported")
	}
}

// TestRegisterSerializerUnsupportedType verifies that an unsupported content
// type is rejected.
func TestRegisterSerializerUnsupportedType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("registering an unsupported content type should panic")
		}
	}()
	RegisterSerializer("application/xml", SerializeJson)
}

// TestRegisterDeserializerUnsupportedType verifies that an unsupported content
// type is rejected.
func TestRegisterDeserializerUnsupportedType(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("registering an unsupported content type should panic")
		}
	}()
	RegisterDeserializer("application/xml", DeserializeJson)
}

// TestRegisterSerializerSupportedType verifies that a supported content type
// is registered.
func TestRegisterSerializerSupportedType(t *testing.T) {
	RegisterSerializer(MimeText, SerializeJson)
	if serializers[MimeText] == nil {
		t.Error("the serializer should be registered")
	}
}
