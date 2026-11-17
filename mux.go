package sprout

import (
	"errors"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/errs"
	"github.com/wxy365/basal/log"
)

var (
	allowedMethods = []string{
		http.MethodConnect, http.MethodGet, http.MethodHead,
		http.MethodPut, http.MethodPost, http.MethodPatch,
		http.MethodOptions, http.MethodTrace, http.MethodDelete,
	}
	expNamed       = regexp.MustCompile(`\{\w+}`)
	expNamedRegexp = regexp.MustCompile(`\{\w+:~[\s\S]+}`)
	expStatic      = regexp.MustCompile(`[\w.-]+`)
)

type mux struct {
	root *rootSection
}

func newMux(handlers map[epSig]func(*requestContext)) *mux {
	m := &mux{}
	if len(handlers) == 0 {
		log.Panic("No handler specified when creating new http route mux")
	}
	m.root = &rootSection{
		baseSection{
			lvl:     0,
			patn:    "/",
			hdlrMap: make(map[string]func(*requestContext)),
		},
	}
	// scan for endpoints with pattern "/"
	for e, h := range handlers {
		if e.pattern == "/" {
			if _, exists := m.root.hdlrMap[e.method]; exists {
				log.Panic("Duplicate endpoint definitions with the same uri pattern(/) and methods({0})", e.method)
			}
			m.root.hdlrMap[e.method] = h
			delete(handlers, e)
		}
	}
	for e, h := range handlers {
		pattern := strings.TrimSpace(e.pattern)
		for strings.Contains(pattern, "//") {
		pattern = strings.ReplaceAll(pattern, "//", "/")
	}
		parts := strings.Split(pattern, "/")
		if len(parts) > 0 {
			if parts[0] == "" {
				parts = parts[1:]
			}
			addSection(m.root, 0, parts, e.method, h)
		}
	}
	return m
}

func addSection(parent section, i int, parts []string, method string, h func(*requestContext)) {
	if i >= len(parts) {
		return
	}
	part := strings.TrimSpace(parts[i])
	done := false
	for _, chdn := range parent.children() {
		if chdn.pattern() == part {
			if i == len(parts)-1 {
				if len(chdn.handlerMap()) > 0 {
					if _, exists := chdn.handlerMap()[method]; exists {
						log.Panic("Duplicate endpoint definitions with the same uri pattern(/{0}) and method({1}})", strings.Join(parts, "/"), method)
					}
				}
				chdn.addHandler(method, h)
				return
			} else {
				addSection(chdn, i+1, parts, method, h)
				done = true
				break
			}
		}
	}
	if !done {
		s := newSection(parent, i, parts, method, h)
		addSection(s, i+1, parts, method, h)
	}
}

func newSection(parent section, i int, parts []string, method string, h func(ctx *requestContext)) section {
	var s section
	isEndPart := i == len(parts)-1
	pattern := parts[i]
	base := baseSection{
		lvl:  parent.level() + 1,
		prnt: parent,
		patn: pattern,
	}
	if pattern == "*" {
		s = &matchAllSection{base}
	} else if strings.HasPrefix(pattern, "~") {
		reg, err := regexp.Compile(pattern[1:])
		if err != nil {
			log.PanicErr(err)
		}
		s = &regexpSection{
			baseSection: base,
			exp:         reg,
		}
	} else if pattern == "%s" {
		s = &formatSection{
			baseSection: base,
			validator: func(s string) bool {
				return s != ""
			},
		}
	} else if pattern == "%d" {
		s = &formatSection{
			baseSection: base,
			validator: func(s string) bool {
				_, e := strconv.ParseInt(s, 10, 64)
				return e == nil
			},
		}
	} else if pattern == "" {
		s = &emptyFinalSection{base}
	} else if expNamedRegexp.MatchString(pattern) {
		expr := pattern[1 : len(pattern)-1]
		t := strings.Split(expr, ":")
		rexp, err := regexp.Compile(t[1][1:])
		if err != nil {
			log.PanicErr(err)
		}
		s = &namedRegexpSection{
			baseSection: base,
			name:        t[0],
			exp:         rexp,
		}
	} else if expNamed.MatchString(pattern) {
		s = &namedSection{
			baseSection: base,
			name:        pattern[1 : len(pattern)-1],
		}
	} else if expStatic.MatchString(pattern) {
		s = &staticSection{
			base,
		}
	}

	if isEndPart {
		s.addHandler(method, h)
	}
	parent.addChild(s)
	return s
}

// parseMediaType returns the media type of the given header value, along with
// its parameters.
//
// The media types that carry no parameter are recognised without the full
// parsing, because mime.ParseMediaType is expensive and the headers are read
// on every request. The parameters are parsed only when they are asked for:
// they are needed by the multipart deserializer alone. The wildcard media type
// is reported as the JSON media type, which is the format the framework falls
// back to.
//
// A media type that the framework does not know is parsed in full, so that a
// header that the mime package rejects keeps falling back to the JSON media
// type, while a media type that it accepts is reported as it is.
func parseMediaType(header string, wantParams bool) (string, map[string]string) {
	i := strings.IndexByte(header, ';')
	if i < 0 {
		mediaType := strings.ToLower(strings.TrimSpace(header))
		if mediaType == "" || mediaType == "*/*" {
			return MimeJson, nil
		}
		return mediaType, nil
	}
	base := strings.ToLower(strings.TrimSpace(header[:i]))
	if base != "" && isKnownMediaType(base) && !wantParams && simpleParamsOK(header[i+1:]) {
		return base, nil
	}
	mediaType, params, err := mime.ParseMediaType(header)
	if err != nil {
		return MimeJson, nil
	}
	if mediaType == "*/*" {
		return MimeJson, params
	}
	if !wantParams {
		params = nil
	}
	return mediaType, params
}

// isKnownMediaType reports whether the media type is one of the types that
// the framework supports.
func isKnownMediaType(mediaType string) bool {
	switch mediaType {
	case mimeJson, mimeHtml, mimeText, mimeMultipartForm, mimeUrlencodedForm, mimePdf:
		return true
	}
	return false
}

// simpleParamsOK reports whether the parameters of a media type header are
// simple enough for the media type to be used without the full parsing: every
// parameter is a plain token, without a quoted string.
//
// The parameters are the ones that follow the first semicolon of the header.
//
// A header whose parameters are not simple is parsed in full, so that a
// malformed header keeps falling back to the JSON media type. A parameter that
// this function rejects is therefore never wrong, it is only slower.
func simpleParamsOK(params string) bool {
	for {
		// the spaces that separate the parameters
		i := 0
		for i < len(params) && isMediaSpace(params[i]) {
			i++
		}
		params = params[i:]
		if len(params) == 0 {
			return true
		}
		if params[0] == ';' {
			// a trailing semicolon is accepted by the mime package
			params = params[1:]
			for len(params) > 0 && isMediaSpace(params[0]) {
				params = params[1:]
			}
			return len(params) == 0
		}
		// the name of the parameter
		i = 0
		for i < len(params) && isMediaTokenChar(params[i]) {
			i++
		}
		if i == 0 {
			return false
		}
		params = params[i:]
		for len(params) > 0 && isMediaSpace(params[0]) {
			params = params[1:]
		}
		if len(params) == 0 || params[0] != '=' {
			return false
		}
		params = params[1:]
		for len(params) > 0 && isMediaSpace(params[0]) {
			params = params[1:]
		}
		if len(params) == 0 {
			return false
		}
		if params[0] == '"' {
			// a quoted string needs the full parsing
			return false
		}
		// the value of the parameter
		i = 0
		for i < len(params) && isMediaTokenChar(params[i]) {
			i++
		}
		if i == 0 {
			return false
		}
		params = params[i:]
		// the separator of the next parameter
		i = 0
		for i < len(params) && isMediaSpace(params[i]) {
			i++
		}
		params = params[i:]
		if len(params) == 0 {
			return true
		}
		if params[0] != ';' {
			return false
		}
		params = params[1:]
	}
}

// isMediaSpace reports whether the byte is one of the spaces that separate
// the parts of a media type header.
func isMediaSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// isMediaTokenChar reports whether the byte may appear in a token of a media
// type header. The set is a subset of the tokens that the mime package
// accepts, so a parameter that this function accepts is always a valid one.
func isMediaTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

func (m *mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	acceptType, _ := parseMediaType(r.Header.Get("Accept"), false)
	serializer := serializers[acceptType]
	if serializer == nil {
		serializer = serializers[MimeJson]
	}
	serialize := func(er error) {
		var e *errs.Err
		if errors.As(er, &e) && e.Status > 0 {
			w.WriteHeader(e.Status)
		}
		if err := serializer(er, w); err != nil {
			// this should never happen
			log.ErrorErrF("Error in serializing error message", err)
		}
	}

	contentTypeHeader := r.Header.Get("Content-Type")
	contentType, params := parseMediaType(contentTypeHeader, false)
	deserializer := deserializers[contentType]
	if contentType == mimeMultipartForm {
		// The multipart deserializer is the only one that needs the
		// parameters of the content type, so they are parsed for it alone.
		contentType, params = parseMediaType(contentTypeHeader, true)
		deserializer = deserializers[contentType]
	}

	if m.root == nil {
		serialize(errs.New("No endpoint defined").WithStatus(http.StatusNotFound))
		return
	}
	path := strings.ReplaceAll(r.URL.Path, "//", "/")
	rootFm, _, _ := m.root.finalMatch(r.Method, "")

	var handler func(*requestContext)
	var pathParams map[string]string
	if path == "" || path == "/" {
		if !rootFm {
			serialize(errs.New("Resource not found").WithStatus(http.StatusNotFound))
			return
		}
		handler = m.root.handler(r.Method)
	} else {
		var theOne section
		if rootFm {
			theOne = m.root
		}
		if len(m.root.chdn) > 0 {
			offset := 0
			if path[0] == '/' {
				offset = 1
			}
			for _, s := range m.root.children() {
				theOne = match(s, path, offset, r.Method, theOne)
			}
		}
		if theOne == nil {
			serialize(errs.New("Resource not found").WithStatus(http.StatusNotFound))
			return
		}
		pathParams = collectPathParams(theOne, path)
		handler = theOne.handler(r.Method)
	}

	if handler == nil {
		serialize(errs.New("Resource not found").WithStatus(http.StatusNotFound))
		return
	}

	// The context is built at the dispatch point, so that the handler always
	// sees a request carrying every context value: the serializer, the
	// deserializer, the accept type, the content type parameters and the path
	// parameters. The values travel in a single context, instead of a chain
	// of context.WithValue calls, each of which allocated a value context
	// and a copy of the request.
	ctx := &requestContext{
		Request: r,
		Writer:  w,
	}
	ctx.values.parent = r.Context()
	ctx.values.serializer = serializer
	ctx.values.acceptType = acceptType
	ctx.values.deserializer = deserializer
	ctx.values.contentTypeParams = params
	ctx.values.pathParams = pathParams
	ctx.Request = r.WithContext(&ctx.values)

	handler(ctx)
}

// match matches the section against the segment of the path that starts at
// the given offset, and returns the best matching section found so far.
//
// The path is not split into a slice of segments: the segments are read one
// by one, so that the routing does not allocate.
func match(s section, path string, offset int, method string, best section) section {
	end := len(path)
	if i := strings.IndexByte(path[offset:], '/'); i >= 0 {
		end = offset + i
	}
	part := path[offset:end]
	if ok, _, _ := s.finalMatch(method, part); ok {
		if best == nil || s.level() > best.level() || s.level() == best.level() && s.weight() > best.weight() {
			best = s
		}
	}
	if ok, _, _ := s.middleMatch(part); ok && end < len(path) {
		for _, chdn := range s.children() {
			best = match(chdn, path, end+1, method, best)
		}
	}
	return best
}

// collectPathParams collects the path parameters of the matched route.
//
// The parameters are collected from the matched section up to the root: every
// section of the chain contributes the parameter that it captured, if any.
// The sections are visited from the deepest one to the shallowest one, so that
// the parameter of the shallowest section wins, as it did when the parameters
// were collected in a map.
func collectPathParams(theOne section, path string) map[string]string {
	// The chain of the matched section, from the deepest one to the root.
	var chainArr [maxPathDepth]section
	chain := chainArr[:0]
	for s := theOne; s != nil && s.level() > 0; s = s.parent() {
		chain = append(chain, s)
	}
	if len(chain) == 0 {
		return nil
	}

	// The boundaries of the segments of the path. A section of the level n
	// matched the segment n-1 of the path.
	var segmentArr [maxPathDepth]pathSegment
	segments := segmentArr[:0]
	offset := 0
	if len(path) > 0 && path[0] == '/' {
		offset = 1
	}
	for len(segments) < len(chain) {
		end := len(path)
		if i := strings.IndexByte(path[offset:], '/'); i >= 0 {
			end = offset + i
		}
		segments = append(segments, pathSegment{start: offset, end: end})
		if end >= len(path) {
			break
		}
		offset = end + 1
	}

	var pathParams map[string]string
	for _, s := range chain {
		name := s.paramName()
		if name == "" {
			continue
		}
		lvl := s.level()
		if lvl < 1 || lvl > len(segments) {
			continue
		}
		segment := segments[lvl-1]
		if pathParams == nil {
			pathParams = make(map[string]string, 2)
		}
		pathParams[name] = path[segment.start:segment.end]
	}
	return pathParams
}

// maxPathDepth is the number of path segments that the router handles
// without allocating. A deeper path falls back to an allocated slice.
const maxPathDepth = 16

// pathSegment is the boundary of one segment of the request path.
type pathSegment struct {
	start int
	end   int
}

type section interface {
	finalMatch(method, uriPart string) (bool, string, string)
	middleMatch(uriPart string) (bool, string, string)
	level() int
	weight() int
	parent() section
	children() []section
	handler(method string) func(ctx *requestContext)
	handlerMap() map[string]func(ctx *requestContext)
	pattern() string
	paramName() string
	addHandler(method string, h func(ctx *requestContext))
	addChild(section)
}

type baseSection struct {
	lvl     int
	prnt    section                       // parent section
	chdn    []section                     // children sections. a tail section has no children
	hdlrMap map[string]func(ctx *requestContext) // http endpoint handler, only tail section has handler
	patn    string
}

func (b *baseSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(b.hdlrMap) == 0 || b.hdlrMap[method] == nil {
		return false, "", ""
	}
	return true, "", ""
}

func (b *baseSection) middleMatch(uriPart string) (bool, string, string) {
	if len(b.chdn) == 0 {
		return false, "", ""
	}
	return true, "", ""
}

func (b *baseSection) level() int {
	return b.lvl
}

func (b *baseSection) parent() section {
	return b.prnt
}

func (b *baseSection) children() []section {
	return b.chdn
}

func (b *baseSection) handler(method string) func(ctx *requestContext) {
	return b.hdlrMap[method]
}

func (b *baseSection) weight() int {
	return 0
}

func (b *baseSection) pattern() string {
	return b.patn
}

// paramName returns the name of the path parameter that the section captures.
// The sections that capture no parameter return an empty name.
func (b *baseSection) paramName() string {
	return ""
}

func (b *baseSection) handlerMap() map[string]func(ctx *requestContext) {
	return b.hdlrMap
}

func (b *baseSection) addHandler(method string, h func(ctx *requestContext)) {
	if len(b.hdlrMap) == 0 {
		b.hdlrMap = make(map[string]func(ctx *requestContext))
	}
	method = strings.ToUpper(method)
	if slices.Lookup(allowedMethods, method, func(left, right string) bool {
		return left == right
	}) == -1 {
		log.Panic("Http method [{0}] not allowed", method)
	}
	b.hdlrMap[method] = h
}

func (b *baseSection) addChild(child section) {
	b.chdn = append(b.chdn, child)
}

type staticSection struct {
	baseSection
}

func (s *staticSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(s.hdlrMap) == 0 || s.hdlrMap[method] == nil {
		return false, "", ""
	}
	return uriPart == s.patn, "", ""
}

func (s *staticSection) middleMatch(uriPart string) (bool, string, string) {
	if len(s.chdn) == 0 {
		return false, "", ""
	}
	return uriPart == s.patn, "", ""
}

// staticSection has the maximum weight: 64
func (s *staticSection) weight() int {
	return 64
}

type regexpSection struct {
	baseSection
	exp *regexp.Regexp
}

func (r *regexpSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(r.hdlrMap) == 0 || r.hdlrMap[method] == nil {
		return false, "", ""
	}
	return r.exp.MatchString(uriPart), "", ""
}

func (r *regexpSection) middleMatch(uriPart string) (bool, string, string) {
	if len(r.chdn) == 0 {
		return false, "", ""
	}
	return r.exp.MatchString(uriPart), "", ""
}

func (r *regexpSection) weight() int {
	return 48
}

type formatSection struct {
	baseSection
	validator func(string) bool
}

func (f *formatSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(f.hdlrMap) == 0 || f.hdlrMap[method] == nil {
		return false, "", ""
	}
	return f.validator(uriPart), "", ""
}

func (f *formatSection) middleMatch(uriPart string) (bool, string, string) {
	if len(f.chdn) == 0 {
		return false, "", ""
	}
	return f.validator(uriPart), "", ""
}

func (f *formatSection) weight() int {
	return 56
}

type namedSection struct {
	baseSection
	name string
}

func (n *namedSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(n.hdlrMap) == 0 || n.hdlrMap[method] == nil {
		return false, "", ""
	}
	return true, n.name, uriPart
}

func (n *namedSection) middleMatch(uriPart string) (bool, string, string) {
	if len(n.chdn) == 0 {
		return false, "", ""
	}
	return true, n.name, uriPart
}

func (n *namedSection) weight() int {
	return 8
}

// paramName returns the name of the path parameter that the section captures.
func (n *namedSection) paramName() string {
	return n.name
}

type namedRegexpSection struct {
	baseSection
	name string
	exp  *regexp.Regexp
}

func (n *namedRegexpSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(n.hdlrMap) == 0 || n.hdlrMap[method] == nil {
		return false, "", ""
	}
	return n.exp.MatchString(uriPart), n.name, uriPart
}

func (n *namedRegexpSection) middleMatch(uriPart string) (bool, string, string) {
	if len(n.chdn) == 0 {
		return false, "", ""
	}
	return n.exp.MatchString(uriPart), n.name, uriPart
}

func (n *namedRegexpSection) weight() int {
	return 52
}

// paramName returns the name of the path parameter that the section captures.
func (n *namedRegexpSection) paramName() string {
	return n.name
}

// /*
type matchAllSection struct {
	baseSection
}

func (m *matchAllSection) finalMatch(method, uriPart string) (bool, string, string) {
	if len(m.hdlrMap) == 0 || m.hdlrMap[method] == nil {
		return false, "", ""
	}
	return true, "", ""
}

func (m *matchAllSection) middleMatch(uriPart string) (bool, string, string) {
	if len(m.chdn) == 0 {
		return false, "", ""
	}
	return true, "", ""
}

func (m *matchAllSection) weight() int {
	return 4
}

// /
type rootSection struct {
	baseSection
}

// ""
type emptyFinalSection struct {
	baseSection
}

func (e *emptyFinalSection) middleMatch(uriPart string) (bool, string, string) {
	return false, "", ""
}
