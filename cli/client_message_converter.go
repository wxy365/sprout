package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/wxy365/basal/errs"
)

var (
	serializers   map[string]Serializer
	deserializers map[string]Deserializer
)

type Serializer func(model any, bodyKeys []string) (io.Reader, error)

type Deserializer func(r io.Reader, params map[string]string, model any) error

func SerializeJson(model any, bodyKeys []string) (io.Reader, error) {
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(raw), nil
}

func DeserializeJson(r io.Reader, params map[string]string, model any) error {
	raw, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if e, ok := model.(*errs.Err); ok {
		return deserializeError(raw, e)
	}
	return json.Unmarshal(raw, model)
}

func deserializeError(raw []byte, e *errs.Err) error {
	dto := make(map[string]any)
	err := json.Unmarshal(raw, &dto)
	if err != nil {
		return err
	}
	map2Err(dto, e)
	return e
}

func map2Err(m map[string]any, err *errs.Err) {
	if code, exists := m["code"]; exists {
		if s, ok := code.(string); ok {
			err.Code = s
		} else {
			err.Code = fmt.Sprint(code)
		}
	}
	if msg, exists := m["message"]; exists {
		if s, ok := msg.(string); ok {
			err.Message = s
		} else {
			err.Message = fmt.Sprint(msg)
		}
	}
	if status, exists := m["status"]; exists {
		if f, ok := status.(float64); ok {
			err.Status = int(f)
		}
	}
	if cause, exists := m["cause"]; exists {
		if m1, ok := cause.(map[string]any); ok {
			err1 := new(errs.Err)
			map2Err(m1, err1)
			err.Cause = err1
		} else {
			err.Cause = errs.New(fmt.Sprint(cause))
		}
	}
}

func init() {
	serializers = make(map[string]Serializer)
	deserializers = make(map[string]Deserializer)
	serializers["application/json"] = SerializeJson
	deserializers["application/json"] = DeserializeJson
}
