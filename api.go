// Package sprout is a generic REST/HTTP microframework.
//
// This file collects the public API surface of the package, so that consumers
// have a single, stable entry point to discover what the framework offers.
// The implementations stay in the files where they belong: the types below
// are aliases of the unexported types that are implemented there, and the
// functions and variables below are thin re-exports of them. The aliases keep
// the exported names, their signatures and their behaviour unchanged, so the
// existing code keeps compiling as it is.
package sprout

import (
	"net/http"
)

type (
	// App is a generic application container that bootstraps and manages the
	// lifecycle of HTTP servers, endpoints, interceptors, and shared
	// application context.
	//
	// See app.go for the implementation.
	App[C any] = app[C]
	// AppInitializer is a function type for application initialization
	// callbacks. It is called during App.Run() after all configuration has
	// been loaded. If an initializer returns an error, the application panics.
	//
	// See app.go for the implementation.
	AppInitializer[C any] = appInitializer[C]
	// Server represents an HTTP server instance managed by the application.
	// It holds network configuration, validator chains, error handlers, and
	// the collection of registered endpoints.
	//
	// See server.go for the implementation.
	Server = httpServer
	// Endpoint defines a typed API endpoint with its pattern, HTTP methods,
	// business logic handler, interceptors, and error handler.
	//
	// See endpoint.go for the implementation.
	Endpoint[I any, O any] = endpoint[I, O]
	// Handler is the function signature for business logic handlers.
	// It receives a request context and a deserialized input struct, and
	// returns an output struct or an error.
	//
	// See endpoint.go for the implementation.
	Handler[I any, O any] = handlerFunc[I, O]
	// ErrorHandler is the function signature for handling errors that occur
	// during request processing. It is invoked by the endpoint or server when
	// a handler returns an error.
	//
	// See endpoint.go for the implementation.
	ErrorHandler = errorHandlerFunc
	// Interceptor is a middleware function that wraps an endpoint handler.
	// Interceptors are chained so that each one can perform pre-processing,
	// post-processing, or short-circuit the request before it reaches the
	// handler.
	//
	// See interceptor.go for the implementation.
	Interceptor = interceptorFunc
	// ClientIdentifier is a function that extracts a client identity string
	// from an HTTP request. It is used by the rate limiter to apply per-client
	// limits. The default implementation returns the client IP address.
	//
	// See interceptor.go for the implementation.
	ClientIdentifier = clientIdentifierFunc
	// Validator inspects a struct field's validation tag and, if the tag
	// matches, returns a ValidateFunc that performs the actual validation at
	// request time.
	//
	// See validator.go for the implementation.
	Validator = validator
	// ValidateFunc validates a single struct field value. It returns an error
	// if the field value does not satisfy the validation rule.
	//
	// See validator.go for the implementation.
	ValidateFunc = validateFunc
	// ObjectValidateFunc validates an entire struct value. It is typically
	// built by composing per-field ValidateFunc instances into a single
	// function.
	//
	// See validator.go for the implementation.
	ObjectValidateFunc = objectValidateFunc
	// RequiredValidator validates that a field is not the zero value when its
	// validation tag contains "required".
	//
	// See validator.go for the implementation.
	RequiredValidator = requiredValidator
	// RequiredByValidator validates that a field is required when at least one
	// of the named sibling fields is non-zero. The tag format is
	// "required_by=FieldA,FieldB".
	//
	// See validator.go for the implementation.
	RequiredByValidator = requiredByValidator
	// EitherValidator validates that, among a set of fields, at least one is
	// non-zero. The tag format is "either=FieldA,FieldB".
	//
	// See validator.go for the implementation.
	EitherValidator = eitherValidator
	// NumberRangeValidator validates that a numeric field falls within a
	// specified range. The tag format uses interval notation: "[min,max]",
	// "(min,max]", "[min,max)", or "(min,max)".
	//
	// See validator.go for the implementation.
	NumberRangeValidator = numberRangeValidator
	// StringLengthValidator validates that a string field's length falls within
	// a specified range. The tag format uses interval notation: "[min,max]",
	// "(min,max]", "[min,max)", or "(min,max)".
	//
	// See validator.go for the implementation.
	StringLengthValidator = stringLengthValidator
	// EmailValidator validates that a string field is a valid email address
	// when the validation tag contains "email".
	//
	// See validator.go for the implementation.
	EmailValidator = emailValidator
	// Serializer serializes a model into the HTTP response body.
	// Implementations write the serialized representation to the response
	// writer.
	//
	// See message_converter.go for the implementation.
	Serializer = serializerFunc
	// Deserializer deserializes an HTTP request body into the provided model.
	// Implementations read the request body and populate the model fields.
	//
	// See message_converter.go for the implementation.
	Deserializer = deserializerFunc
	// Context wraps an HTTP request and response writer, providing convenience
	// methods that delegate to the underlying request context. It implements
	// a subset of the context.Context interface.
	//
	// See context.go for the implementation.
	Context = requestContext
	// SSEEvent represents a server-sent event. Its fields map one to one onto
	// the SSE protocol: the identifier, the name, the payload and the
	// reconnection interval.
	//
	// See sse.go for the implementation.
	SSEEvent = sseEvent
	// SSEStream writes server-sent events to an HTTP response. The framework
	// creates it and hands it over to the business logic of an SSE endpoint.
	//
	// See sse.go for the implementation.
	SSEStream = sseStream
	// SSEHandler is the function signature for the business logic of an SSE
	// endpoint. It receives a request context, a deserialized input struct and
	// the stream the events are written to.
	//
	// See sse.go for the implementation.
	SSEHandler[I any] = sseHandlerFunc[I]
	// SSEEndpoint defines an SSE endpoint with its pattern, HTTP methods,
	// business logic handler, interceptors and error handler.
	//
	// See sse.go for the implementation.
	SSEEndpoint[I any] = sseEndpoint[I]
)

const (
	// MimeJson is the MIME type for JSON content.
	MimeJson = mimeJson
	// MimeMultipartForm is the MIME type for multipart form data.
	MimeMultipartForm = mimeMultipartForm
	// MimeUrlencodedForm is the MIME type for URL-encoded form data.
	MimeUrlencodedForm = mimeUrlencodedForm
	// MimeText is the MIME type for plain text content.
	MimeText = mimeText
	// MimeHtml is the MIME type for HTML content.
	MimeHtml = mimeHtml
	// MimePdf is the MIME type for PDF content.
	MimePdf = mimePdf
	// MimeEventStream is the MIME type for server-sent events.
	MimeEventStream = mimeEventStream
)

var (
	// ErrRateLimited is returned when a request is rejected due to rate limiting.
	ErrRateLimited = errRateLimited
	// ErrCircuitBroken is returned when a request is rejected because the circuit
	// breaker is open and the endpoint is not accepting requests.
	ErrCircuitBroken = errCircuitBroken
)

// Mount registers an endpoint onto a server within the application.
// The endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
//
// See app.go for the implementation.
func Mount[C any, I any, O any](e *Endpoint[I, O], a *App[C], svrName ...string) {
	mount(e, a, svrName...)
}

// MountSSE registers an SSE endpoint onto a server within the application.
// The endpoint is wired up via an AppInitializer, ensuring it is registered before
// the server starts. If no server name is provided, the endpoint is mounted on
// the first server in the App.Servers slice.
//
// See app.go for the implementation.
func MountSSE[C any, I any](e *SSEEndpoint[I], a *App[C], svrName ...string) {
	mountSSE(e, a, svrName...)
}

// RegisterClientIdentifier registers a named client identifier function that
// can be referenced by the rate limiter configuration.
//
// See interceptor.go for the implementation.
func RegisterClientIdentifier(name string, identifier ClientIdentifier) {
	registerClientIdentifier(name, identifier)
}

// RegisterSerializer registers a serializer function for the given content type.
// Supported content types are: application/json, text/html, text/plain,
// multipart/form-data, x-www-form-encoded, and application/pdf.
//
// See message_converter.go for the implementation.
func RegisterSerializer(contentType string, serializer Serializer) {
	registerSerializer(contentType, serializer)
}

// RegisterDeserializer registers a deserializer function for the given content type.
// Supported content types are: application/json, text/html, text/plain,
// multipart/form-data, x-www-form-encoded, and application/pdf.
//
// See message_converter.go for the implementation.
func RegisterDeserializer(contentType string, deserializer Deserializer) {
	registerDeserializer(contentType, deserializer)
}

// SerializeJson serializes a model as JSON into the response writer.
//
// See message_converter.go for the implementation.
func SerializeJson(model any, w http.ResponseWriter) error {
	return serializeJson(model, w)
}

// DeserializeJson deserializes JSON from the request body into the provided model.
//
// See message_converter.go for the implementation.
func DeserializeJson(r *http.Request, model any) error {
	return deserializeJson(r, model)
}

// SerializeMultipartForm serializes a model as multipart form data into the
// response writer. Struct fields tagged with "file" are written as file parts;
// all other fields are written as form fields.
//
// See message_converter.go for the implementation.
func SerializeMultipartForm(model any, w http.ResponseWriter) error {
	return serializeMultipartForm(model, w)
}

// DeserializeMultipartForm deserializes multipart form data from the request
// body into the provided model. Form fields are mapped to struct fields by name;
// file uploads are mapped to []byte or io.Reader fields.
//
// See message_converter.go for the implementation.
func DeserializeMultipartForm(r *http.Request, model any) error {
	return deserializeMultipartForm(r, model)
}
