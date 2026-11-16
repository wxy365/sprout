package sprout

import (
	"net/http"
	"reflect"
	"sync"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/rflt"
)

// fieldBinding describes how one field of an input entity is bound to the
// request. The struct tags of an input entity never change at runtime, so
// they are read once per input type instead of once per request.
type fieldBinding struct {
	// pathKey is the name of the path parameter the field is bound to.
	pathKey string
	// queryKey is the name of the query parameter the field is bound to.
	queryKey string
	// headerKey is the name of the header the field is bound to.
	headerKey string
	// cookieKey is the name of the cookie the field is bound to.
	cookieKey string
	// decryptAlg is the name of the algorithm that decrypts the value of the
	// field.
	decryptAlg string
	// defValue is the default value of the field.
	defValue string
	// hasDef tells whether the field carries a default value.
	hasDef bool
	// name is the name of the field, used by the error messages.
	name string
}

// inputBinding holds the binding metadata of the fields of an input entity.
type inputBinding struct {
	fields []fieldBinding
}

// inputBindings caches the binding metadata of the input entity types that
// have already been seen.
var inputBindings sync.Map // map[reflect.Type]*inputBinding

// bindingOf returns the binding metadata of the given input entity type,
// building it when the type has not been seen yet.
func bindingOf(inputType reflect.Type) *inputBinding {
	if cached, ok := inputBindings.Load(inputType); ok {
		return cached.(*inputBinding)
	}
	binding := &inputBinding{fields: make([]fieldBinding, inputType.NumField())}
	for i := 0; i < inputType.NumField(); i++ {
		field := inputType.Field(i)
		tag := field.Tag
		binding.fields[i].pathKey, _ = tag.Lookup("path")
		binding.fields[i].queryKey, _ = tag.Lookup("query")
		binding.fields[i].headerKey, _ = tag.Lookup("header")
		binding.fields[i].cookieKey, _ = tag.Lookup("cookie")
		binding.fields[i].decryptAlg, _ = tag.Lookup("decrypt")
		binding.fields[i].defValue, binding.fields[i].hasDef = tag.Lookup("default")
		binding.fields[i].name = field.Name
	}
	inputBindings.Store(inputType, binding)
	return binding
}

func parseHttpRequest[T any](in *T, r *http.Request, decrypters map[string]func(cipher []byte) ([]byte, error)) error {
	iv := reflect.ValueOf(in)
	inputType := iv.Type()
	for inputType.Kind() == reflect.Pointer {
		iv = iv.Elem()
		inputType = inputType.Elem()
	}
	binding := bindingOf(inputType)

	var queryMap map[string]string
	for i := 0; i < iv.NumField(); i++ {
		fv := iv.Field(i)
		field := &binding.fields[i]
		var valStr *string
		if field.hasDef && fv.IsZero() {
			valStr = &field.defValue
		}

		if field.pathKey != "" {
			pathParams := r.Context().Value(ctxKeyPathParams)
			if pathParams != nil {
				if val, exists := pathParams.(map[string]string)[field.pathKey]; exists {
					valStr = &val
				}
			}
		}
		if valStr == nil {
			if field.queryKey != "" {
				if len(queryMap) == 0 {
					queryMap = make(map[string]string)
					for k, v := range r.URL.Query() {
						queryMap[k] = v[0]
					}
				}
				if val, exists := queryMap[field.queryKey]; exists {
					valStr = &val
				}
			}
			if valStr == nil {
				if field.headerKey != "" {
					val := r.Header.Get(field.headerKey)
					if val != "" {
						valStr = &val
					}
				}
				if valStr == nil {
					if field.cookieKey != "" {
						cookie, err := r.Cookie(field.cookieKey)
						if err == nil {
							val := cookie.Value
							valStr = &val
						}
					}
				}
			}
		}
		if valStr != nil {
			str := *valStr
			if field.decryptAlg != "" {
				if fn, exits := decrypters[field.decryptAlg]; exits {
					plain, err := fn([]byte(str))
					if err != nil {
						log.WarnErrF("Failed to decrypt [{0}.{1}]", err, inputType.Name(), field.name)
					} else {
						str = string(plain)
					}
				}
			}
			err := rflt.UnmarshalValue(fv, str)
			if err != nil {
				return errs.Wrap(err, "Failed to unmarshal value [{0}] to field [{1}] of type [{2}]", *valStr, field.name, fv.Type().Name())
			}
		}
	}

	return parseHttpRequestBody(in, r)
}

func parseHttpRequestBody[T any](t *T, r *http.Request) error {
	deserializer := r.Context().Value(ctxKeyDeserializer).(Deserializer)
	if deserializer != nil {
		err := deserializer(r, t)
		if err != nil {
			return err
		}
	}
	return nil
}
