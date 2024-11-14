package sprout

import (
	"context"
	"fmt"
	"github.com/wxy365/basal/ds/slices"
	"github.com/wxy365/basal/lei"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type mux struct {
	root *rootSection
}

func newMux(handlers map[epSig]func(http.ResponseWriter, *http.Request)) *mux {
	m := &mux{}
	if len(handlers) == 0 {
		panic("No handler specified when creating new http route mux")
	}
	m.root = &rootSection{
		baseSection{
			lvl:     0,
			patn:    "/",
			hdlrMap: make(map[string]func(http.ResponseWriter, *http.Request)),
		},
	}
	// scan for endpoints with pattern "/"
	for e, h := range handlers {
		if e.pattern == "/" {
			if _, exists := m.root.hdlrMap[e.method]; exists {
				panic(fmt.Sprintf("Duplicate endpoint definitions with the same uri pattern(/) and methods(%s)", e.method))
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

func addSection(parent section, i int, parts []string, method string, h func(writer http.ResponseWriter, request *http.Request)) {
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
						panic(fmt.Sprintf("Duplicate endpoint definitions with the same uri pattern(/%s) and method(%s)", strings.Join(parts, "/"), method))
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

func newSection(parent section, i int, parts []string, method string, h func(writer http.ResponseWriter, request *http.Request)) section {
	var s section
	isEndPart := i == len(parts)-1
	pattern := parts[i]
	base := baseSection{
		lvl:  parent.level() + 1,
		prnt: parent,
		patn: pattern,
	}
	expNamed, err := regexp.Compile("\\{\\w+}")
	if err != nil {
		panic(err)
	}
	expNamedRegexp, err := regexp.Compile("\\{\\w+:~[\\s\\S]+}")
	if err != nil {
		panic(err)
	}
	expStatic, err := regexp.Compile("[\\w.-]+")
	if err != nil {
		panic(err)
	}
	if pattern == "*" {
		s = &matchAllSection{base}
	} else if strings.HasPrefix(pattern, "~") {
		reg, err := regexp.Compile(pattern[1:])
		if err != nil {
			panic(err)
		}
		s = &regexpSection{
			baseSection: base,
			exp:         reg,
		}
	} else if strings.EqualFold(pattern, "%s") {
		s = &formatSection{
			baseSection: base,
			validator: func(s string) bool {
				return s != ""
			},
		}
	} else if strings.EqualFold(pattern, "%d") {
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
			panic(err)
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

func (m *mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	acceptType, _, err := mime.ParseMediaType(r.Header.Get("Accept"))
	if err != nil {
		acceptType = MimeJson
	}
	if acceptType == "*/*" {
		acceptType = MimeJson
	}
	serializer := serializers[acceptType]
	logAndSerialize := func(er error) {
		lei.ErrorErr(er)
		err = serializer(er, w)
		// this should never happen
		if err != nil {
			panic(lei.Wrap("Error in serializing error message", err))
		}
	}

	r = r.WithContext(context.WithValue(r.Context(), ctxKeySerializer, serializer))

	r = r.WithContext(context.WithValue(r.Context(), ctxKeyAcceptType, acceptType))

	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		contentType = MimeJson
	}
	deserializer := deserializers[contentType]
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyDeserializer, deserializer))
	if len(params) > 0 {
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyContentTypeParams, params))
	}

	if m.root == nil {
		logAndSerialize(lei.New("No endpoint defined").WithStatus(http.StatusNotFound))
		return
	}
	path := r.URL.Path
	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	rootFm, _, _ := m.root.finalMatch(r.Method, "")
	if path == "" || path == "/" {
		if rootFm {
			m.root.handler(r.Method)(w, r)
		} else {
			logAndSerialize(lei.New("Resource not found").WithStatus(http.StatusNotFound))
			return
		}
		return
	}

	theOne := new(section)
	pathParams := make(map[section][2]string)
	if rootFm {
		*theOne = m.root
	}
	if len(m.root.chdn) > 0 {
		parts := strings.Split(path, "/")
		if parts[0] == "" {
			parts = parts[1:]
		}
		for _, s := range m.root.children() {
			match(s, 0, parts, r.Method, pathParams, theOne)
		}
	}
	if *theOne == nil {
		logAndSerialize(lei.New("Resource not found").WithStatus(http.StatusNotFound))
		return
	}
	if len(pathParams) > 0 {
		pm := make(map[string]string)
		for s := *theOne; s != nil; {
			if param, exists := pathParams[s]; exists {
				pm[param[0]] = param[1]
			}
			s = s.parent()
		}
		r = r.WithContext(context.WithValue(r.Context(), ctxKeyPathParams, pm))
	}

	(*theOne).handler(r.Method)(w, r)
}

func match(s section, idx int, uriParts []string, method string, pathParams map[section][2]string, theOne *section) {
	part := uriParts[idx]
	if ok, k, v := s.finalMatch(method, part); ok {
		if *theOne == nil || s.level() > (*theOne).level() || s.level() == (*theOne).level() && s.weight() > (*theOne).weight() {
			*theOne = s
			if len(k) > 0 {
				pathParams[s] = [2]string{k, v}
			}
		}
	}
	if ok, k, v := s.middleMatch(part); ok {
		if len(k) > 0 {
			pathParams[s] = [2]string{k, v}
		}
		for _, chdn := range s.children() {
			match(chdn, idx+1, uriParts, method, pathParams, theOne)
		}
	}
}

type section interface {
	finalMatch(method, uriPart string) (bool, string, string)
	middleMatch(uriPart string) (bool, string, string)
	level() int
	weight() int
	parent() section
	children() []section
	handler(method string) func(writer http.ResponseWriter, request *http.Request)
	handlerMap() map[string]func(writer http.ResponseWriter, request *http.Request)
	pattern() string
	addHandler(method string, h func(writer http.ResponseWriter, request *http.Request))
	addChild(section)
}

type baseSection struct {
	lvl     int
	prnt    section                                                            // parent section
	chdn    []section                                                          // children sections. a tail section has no children
	hdlrMap map[string]func(writer http.ResponseWriter, request *http.Request) // http endpoint handler, only tail section has handler
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

func (b *baseSection) handler(method string) func(http.ResponseWriter, *http.Request) {
	return b.hdlrMap[method]
}

func (b *baseSection) weight() int {
	return 0
}

func (b *baseSection) pattern() string {
	return b.patn
}

func (b *baseSection) handlerMap() map[string]func(http.ResponseWriter, *http.Request) {
	return b.hdlrMap
}

func (b *baseSection) addHandler(method string, h func(writer http.ResponseWriter, request *http.Request)) {
	if len(b.hdlrMap) == 0 {
		b.hdlrMap = make(map[string]func(w http.ResponseWriter, r *http.Request))
	}
	method = strings.ToUpper(method)
	allowedMethods := []string{
		http.MethodConnect, http.MethodGet, http.MethodHead,
		http.MethodPut, http.MethodPost, http.MethodPatch,
		http.MethodOptions, http.MethodTrace, http.MethodDelete,
	}
	if slices.Lookup(allowedMethods, method, func(left, right string) bool {
		return left == right
	}) == -1 {
		panic("Http method '" + method + "' not allowed")
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
