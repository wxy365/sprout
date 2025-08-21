package sprout

import (
	"net/http"
	"reflect"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/rflt"
)

func parseHttpRequest[T any](in *T, r *http.Request, decrypters map[string]func(cipher []byte) ([]byte, error)) error {
	iv := reflect.ValueOf(in)
	for iv.Type().Kind() == reflect.Pointer {
		iv = iv.Elem()
	}

	for i := 0; i < iv.NumField(); i++ {
		fv := iv.Field(i)
		tag := iv.Type().Field(i).Tag
		var valStr *string
		if def, ok := tag.Lookup("default"); ok && fv.IsZero() {
			valStr = &def
		}

		var queryMap map[string]string
		if key, ok := tag.Lookup("path"); ok {
			pathParams := r.Context().Value(ctxKeyPathParams)
			if pathParams != nil {
				if val, exists := pathParams.(map[string]string)[key]; exists {
					valStr = &val
				}
			}
		}
		if valStr == nil {
			if key, ok := tag.Lookup("query"); ok {
				if len(queryMap) == 0 {
					queryMap = make(map[string]string)
					for k, v := range r.URL.Query() {
						queryMap[k] = v[0]
					}
				}
				if val, exists := queryMap[key]; exists {
					valStr = &val
				}
			}
			if valStr == nil {
				if key, ok := tag.Lookup("header"); ok {
					val := r.Header.Get(key)
					if val != "" {
						valStr = &val
					}
				}
				if valStr == nil {
					if key, ok := tag.Lookup("cookie"); ok {
						cookie, err := r.Cookie(key)
						if err == nil {
							val := cookie.String()
							valStr = &val
						}
					}
				}
			}
		}
		if valStr != nil {
			str := *valStr
			if decryptAlg, ok := tag.Lookup("decrypt"); ok {
				if fn, exits := decrypters[decryptAlg]; exits {
					plain, err := fn([]byte(str))
					if err != nil {
						log.WarnErrF("Failed to decrypt [{0}.{1}]", err, iv.Type().Name(), iv.Type().Field(i).Name)
					} else {
						str = string(plain)
					}
				}
			}
			err := rflt.UnmarshalValue(fv, str)
			if err != nil {
				return errs.Wrap(err, "Failed to unmarshal value [{0}] to field [{1}] of type [{2}]", *valStr, iv.Type().Field(i).Name, fv.Type().Name())
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
