package sprout

import (
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"

	"github.com/wxy365/basal/errs"
	bjson "github.com/wxy365/basal/json"
	"github.com/wxy365/basal/log"
	"github.com/wxy365/basal/rflt"
	"github.com/wxy365/basal/text"
)

var (
	serializers   map[string]serializerFunc
	deserializers map[string]deserializerFunc
)

// serializerFunc serializes a model into the HTTP response body.
// Implementations write the serialized representation to the response writer.
type serializerFunc func(model any, w http.ResponseWriter) error

// deserializerFunc deserializes an HTTP request body into the provided model.
// Implementations read the request body and populate the model fields.
type deserializerFunc func(r *http.Request, model any) error

const (
	// mimeJson is the MIME type for JSON content.
	mimeJson = "application/json"
	// mimeMultipartForm is the MIME type for multipart form data.
	mimeMultipartForm = "multipart/form-data"
	// mimeUrlencodedForm is the MIME type for URL-encoded form data.
	mimeUrlencodedForm = "x-www-form-encoded"
	// mimeText is the MIME type for plain text content.
	mimeText = "text/plain"
	// mimeHtml is the MIME type for HTML content.
	mimeHtml = "text/html"
	// mimePdf is the MIME type for PDF content.
	mimePdf = "application/pdf"
)

// serializeJson serializes a model as JSON into the response writer.
func serializeJson(model any, w http.ResponseWriter) error {
	return bjson.MarshalWrite(w, model)
}

// deserializeJson deserializes JSON from the request body into the provided model.
func deserializeJson(r *http.Request, model any) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	return bjson.Unmarshal(raw, model)
}

// serializeMultipartForm serializes a model as multipart form data into the
// response writer. Struct fields tagged with "file" are written as file parts;
// all other fields are written as form fields.
func serializeMultipartForm(model any, w http.ResponseWriter) error {
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
			valStr, valErr := rflt.ValueToString(fv)
			if valErr != nil {
				return valErr
			}
			err := writer.WriteField(text.Pascal2Snake(field.Name), valStr)
			if err != nil {
				return err
			}
		}
	}
	return writer.Close()
}


// deserializeMultipartForm deserializes multipart form data from the request
// body into the provided model. Form fields are mapped to struct fields by name;
// file uploads are mapped to []byte or io.Reader fields.
func deserializeMultipartForm(r *http.Request, model any) error {
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

// registerSerializer registers a serializer function for the given content type.
// Supported content types are: application/json, text/html, text/plain,
// multipart/form-data, x-www-form-encoded, and application/pdf.
func registerSerializer(contentType string, serializer serializerFunc) {
	switch contentType {
	case mimeJson, mimeHtml, mimeText, mimeMultipartForm, mimeUrlencodedForm, mimePdf:
		serializers[contentType] = serializer
	default:
		log.Panic("Content type {0} not supported", contentType)
	}
}

// registerDeserializer registers a deserializer function for the given content type.
// Supported content types are: application/json, text/html, text/plain,
// multipart/form-data, x-www-form-encoded, and application/pdf.
func registerDeserializer(contentType string, deserializer deserializerFunc) {
	switch contentType {
	case mimeJson, mimeHtml, mimeText, mimeMultipartForm, mimeUrlencodedForm, mimePdf:
		deserializers[contentType] = deserializer
	default:
		log.Panic("Content type {0} not supported", contentType)
	}
}

func init() {
	if serializers == nil {
		serializers = make(map[string]serializerFunc)
	}
	if deserializers == nil {
		deserializers = make(map[string]deserializerFunc)
	}
	serializers[mimeJson] = serializeJson
	deserializers[mimeJson] = deserializeJson
	serializers[mimeMultipartForm] = serializeMultipartForm
	deserializers[mimeMultipartForm] = deserializeMultipartForm
}
