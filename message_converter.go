package sprout

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"

	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/rflt"
	"github.com/wxy365/basal/text"
)

var (
	serializers   map[string]Serializer
	deserializers map[string]Deserializer
)

type Serializer func(model any, w http.ResponseWriter) error

type Deserializer func(r *http.Request, model any) error

const (
	MimeJson           = "application/json"
	MimeMultipartForm  = "multipart/form-data"
	MimeUrlencodedForm = "x-www-form-encoded"
	MimeText           = "text/plain"
	MimeHtml           = "text/html"
	MimePdf            = "application/pdf"
)

func SerializeJson(model any, w http.ResponseWriter) error {
	raw, err := json.Marshal(model)
	if err != nil {
		return err
	}
	_, err = w.Write(raw)
	if err != nil {
		return err
	}
	return nil
}

func DeserializeJson(r *http.Request, model any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, model)
}

func SerializeMultipartForm(model any, w http.ResponseWriter) error {
	writer := multipart.NewWriter(w)
	if e, ok := model.(*errs.Err); ok {
		if e.Code != "" {
			err := writer.WriteField("code", e.Code)
			if err != nil {
				return err
			}
		}
		if e.Message != "" {
			err := writer.WriteField("message", e.Message)
			if err != nil {
				return err
			}
		}
		if e.Cause != nil {
			err := writer.WriteField("cause", e.Cause.Error())
			if err != nil {
				return err
			}
		}
		if e.Status > 0 {
			err := writer.WriteField("status", strconv.Itoa(e.Status))
			if err != nil {
				return err
			}
			w.WriteHeader(e.Status)
		}
		return nil
	}
	mv := reflect.ValueOf(model).Elem()
	for i := 0; i < mv.NumField(); i++ {
		fv := mv.Field(i)
		field := mv.Type().Field(i)
		if filename, ok := field.Tag.Lookup("file"); ok {
			writer, err := writer.CreateFormFile(text.Pascal2Snake(field.Name), filename)
			if err != nil {
				return err
			}
			if fv.Type() == reflect.TypeOf([]byte{}) {
				_, err := writer.Write(fv.Bytes())
				if err != nil {
					return err
				}
			} else {
				var sample io.Reader
				if fv.Type().Implements(reflect.TypeOf(sample)) {
					reader := fv.Interface().(io.Reader)
					_, err := io.Copy(writer, reader)
					if err != nil {
						return err
					}
				}
			}
		} else {
			err := writer.WriteField(text.Pascal2Snake(field.Name), fv.String())
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func DeserializeMultipartForm(r *http.Request, model any) error {
	if p, ok := r.Context().Value(ctxKeyContentTypeParams).(map[string]string); ok {
		reader := multipart.NewReader(r.Body, p["boundary"])
		f, err := reader.ReadForm(25)
		if err != nil {
			return err
		}
		defer f.RemoveAll()
		mv := reflect.ValueOf(model).Elem()
		for k, v := range f.Value {
			fv := mv.FieldByName(k)
			err = rflt.UnmarshalValue(fv, v[0])
			if err != nil {
				return err
			}
		}
		for k, v := range f.File {
			fv := mv.FieldByName(k)
			if fv.Kind() == reflect.Pointer {
				fv = fv.Elem()
			}
			switch fv.Kind() {
			case reflect.Slice:
				if fv.Type().Elem().Kind() != reflect.Uint8 {
					return errs.New(`The field [{0}] of model [{1}] is supposed to be of []byte or io.Reader type`, k, mv.Type().Name())
				}
				file, err := v[0].Open()
				if err != nil {
					return err
				}
				fileContent, err := io.ReadAll(file)
				if err != nil {
					return err
				}
				fv.SetBytes(fileContent)
			case reflect.Interface:
				var sample io.Reader
				if !fv.Type().Implements(reflect.TypeOf(sample)) {
					return errs.New(`The field [{0}] of model [{1}] is supposed to be of []byte or io.Reader type`, k, mv.Type().Name())
				}
				file, err := v[0].Open()
				if err != nil {
					return err
				}
				fv.Set(reflect.ValueOf(file))
			}
		}
	} else {
		return errs.New("Form data boundary is not specified")
	}
	return nil
}

func RegisterSerializer(contentType string, serializer Serializer) {
	switch contentType {
	case MimeJson, MimeHtml, MimeText, MimeMultipartForm, MimeUrlencodedForm, MimePdf:
		serializers[contentType] = serializer
	default:
		log.Panic("Content type {0} not supported", contentType)
	}
}

func RegisterDeserializer(contentType string, deserializer Deserializer) {
	switch contentType {
	case MimeJson, MimeHtml, MimeText, MimeMultipartForm, MimeUrlencodedForm, MimePdf:
		deserializers[contentType] = deserializer
	default:
		log.Panic("Content type {0} not supported", contentType)
	}
}

func init() {
	if serializers == nil {
		serializers = make(map[string]Serializer)
	}
	if deserializers == nil {
		deserializers = make(map[string]Deserializer)
	}
	serializers[MimeJson] = SerializeJson
	deserializers[MimeJson] = DeserializeJson
	serializers[MimeMultipartForm] = SerializeMultipartForm
	deserializers[MimeMultipartForm] = DeserializeMultipartForm
}
