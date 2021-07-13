package main

import (
	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/firestore"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"github.com/HayoVanLoon/go-commons/logjson"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
	"google.golang.org/api/iterator"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type MockMapping struct {
	UpdateTime time.Time    `json:"update_time"`
	Rule       MockRule     `json:"rule"`
	Response   MockResponse `json:"response"`
	name       string
}

func (m MockMapping) Name() string {
	if m.name != "" {
		return m.name
	}
	s := fmt.Sprintf("%x", m.Rule.Hash())
	if len(s) < 8 {
		m.name = s
	} else {
		m.name = s[:8]
	}
	return m.name
}

type MockRule struct {
	Priority      int                 `json:"priority"`
	Methods       []string            `json:"methods"`
	Path          string              `json:"path"`
	PathRegex     string              `json:"path_regex"`
	TextBodyRegex string              `json:"text_body_regex"`
	Headers       map[string][]string `json:"headers"`
	QueryParams   map[string][]string `json:"query_params"`
	name          string
}

func (mr MockRule) Name() string {
	if mr.name != "" {
		return mr.name
	}
	s := fmt.Sprintf("%x", mr.Hash())
	if len(s) < 8 {
		mr.name = s
	} else {
		mr.name = s[:8]
	}
	return mr.name
}

func (mr MockRule) Hash() []byte {
	h := sha1.New()
	h.Write([]byte(mr.Path))
	h.Write([]byte("~~~"))
	h.Write([]byte(mr.PathRegex))
	return h.Sum(nil)
}

func (mr MockRule) matches(r *http.Request, body []byte) bool {
	methodOk := len(mr.Methods) == 0
	if !methodOk {
		for _, m := range mr.Methods {
			if r.Method == m {
				methodOk = true
				break
			}
		}
	}

	pathOk := false
	if mr.PathRegex != "" {
		re, _ := regexp.Compile(mr.PathRegex)
		pathOk = re.MatchString(r.URL.Path)
	} else {
		pathOk = mr.Path == r.URL.Path
	}

	headersOk := len(mr.QueryParams) == 0
	if !headersOk {
		for k, vs := range mr.Headers {
			values := r.Header[k]
			if len(vs) == len(values) {
				sort.Strings(vs)
				sort.Strings(values)
				headersOk = true
				for i := 0; headersOk && i < len(vs); i += 1 {
					headersOk = vs[i] == values[i]
				}
			}
		}
	}

	paramsOk := len(mr.QueryParams) == 0
	if !paramsOk {
		for k, vs := range mr.QueryParams {
			values := r.URL.Query()[k]
			if len(vs) == len(values) {
				sort.Strings(vs)
				sort.Strings(values)
				paramsOk = true
				for i := 0; paramsOk && i < len(vs); i += 1 {
					paramsOk = vs[i] == values[i]
				}
			}
		}
	}

	bodyOk := false
	if mr.TextBodyRegex != "" {
		re, _ := regexp.Compile(mr.TextBodyRegex)
		bodyOk = re.Match(body)
	} else {
		bodyOk = true
	}

	logjson.Debug("method: %v, path: %v, params: %v, body: %v", methodOk, pathOk, paramsOk, bodyOk)
	return methodOk && pathOk && paramsOk && bodyOk
}

type MockResponse struct {
	ContentType string                 `json:"content_type,omitempty"`
	TextBody    string                 `json:"text_body,omitempty"`
	JsonBody    map[string]interface{} `json:"json_body,omitempty"`
	BytesBody   []byte                 `json:"bytes_body,omitempty"`
	StatusCode  int                    `json:"status_code"`
	Headers     map[string][]string    `json:"headers,omitempty"`
}

type SimpleRequest struct {
	Scheme      string              `json:"scheme,omitempty"`
	Method      string              `json:"method"`
	Path        string              `json:"path,omitempty"`
	QueryParams map[string][]string `json:"query_params,omitempty"`
	TextBody    string              `json:"text_body,omitempty"`
	Headers     map[string][]string `json:"headers,omitempty"`
}

type Recording struct {
	Request  *SimpleRequest `json:"request"`
	Response *MockResponse  `json:"response"`
}

const collection = "services/mockmate/mapping"

type handler struct {
	mux      sync.Mutex
	client   *firestore.Client
	mappings []MockMapping
}

func newHandler(ctx context.Context) (*handler, error) {
	project, err := metadata.ProjectID()
	if err != nil {
		logjson.Warn(err)
	}
	if project == "" {
		project = os.Getenv("PROJECT")
	}
	h := &handler{}

	if project == "" {
		logjson.Warn("no project could be determined, cannot persist rules")
	} else {
		c, err := firestore.NewClient(ctx, project)
		if err != nil {
			logjson.Warn("could not init Firestore client: %s", err)
		} else {
			h.client = c
		}
	}
	return h, nil
}

func (h *handler) reset(ctx context.Context) {
	h.mux.Lock()
	defer h.mux.Unlock()

	h.mappings = nil

	if h.client == nil {
		return
	}
	iter := h.client.Collection(collection).Documents(ctx)
	for {
		ds, err := iter.Next()
		if ds != nil {
			if _, err := h.client.Doc(ds.Ref.Path).Delete(ctx); err != nil {
				logjson.Warn("could not delete rule %s", ds.Ref.Path)
			}
		}
		if err != nil {
			if err == iterator.Done {
				break
			}
			logjson.Warn("error while deleting mappings: %s", err)
		}
	}
}

func (h *handler) Sync(ctx context.Context) {
	if h.client == nil {
		return
	}
	h.mux.Lock()
	defer h.mux.Unlock()

	fsMappings := make(map[string]MockMapping)
	iter := h.client.Collection(collection).Documents(ctx)
	for {
		ds, err := iter.Next()
		if ds != nil {
			m := &MockMapping{}
			if err := ds.DataTo(m); err != nil {
				logjson.Warn("could not parse Firestore document to MockMapping")
			} else {
				fsMappings[m.Name()] = *m
			}
		}
		if err != nil {
			if err == iterator.Done {
				break
			}
			logjson.Warn("error while persisting mappings: %s", err)
		}
	}
	var newLocals []MockMapping
	var toStore []MockMapping
	for _, m := range h.mappings {
		fs, found := fsMappings[m.Name()]
		if found {
			if fs.UpdateTime.After(m.UpdateTime) {
				newLocals = append(newLocals, fs)
			} else {
				newLocals = append(newLocals, m)
				toStore = append(toStore, m)
			}
		} else {
			toStore = append(toStore, m)
			newLocals = append(newLocals, m)
		}
	}
	h.mappings = newLocals
	for _, m := range toStore {
		docName := collection + "/" + m.Name()
		if _, err := h.client.Doc(docName).Set(ctx, m); err != nil {
			logjson.Warn("could not save mapping: %s", err)
		} else {
			logjson.Info("stored new mapping %s", docName)
		}
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	if strings.HasPrefix(r.URL.Path, "/mockmate-mappings") {
		h.handleMockMateSettings(ctx, w, r)
		return
	}

	mr, found := h.getMockResponse(ctx, r)
	if !found {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(mr.StatusCode)
	w.Header().Set("content-type", mr.ContentType)
	if mr.TextBody != "" {
		_, _ = w.Write([]byte(mr.TextBody))
	} else if mr.JsonBody != nil {
		bs, _ := json.Marshal(mr.JsonBody)
		_, _ = w.Write(bs)
	} else if mr.BytesBody != nil {
		_, _ = w.Write(mr.BytesBody)
	}
}

func (h *handler) handleMockMateSettings(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/mockmate-mappings" {
		m, err := h.setMockMapping(ctx, r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(err.Error()))
			return
		} else {
			bs, _ := json.Marshal(m)
			_, _ = w.Write(bs)
			return
		}
	}
	if r.Method == http.MethodPost && r.URL.Path == "/mockmate-mappings:record" {
		record(w, r)
		return
	}
	if r.Method == http.MethodGet && r.URL.Path == "/mockmate-mappings" {
		h.listMappings(ctx, w, r)
		return
	}
	if r.Method == http.MethodDelete && r.URL.Path == "/mockmate-mappings" {
		h.reset(ctx)
		return
	}
	http.NotFound(w, r)
}

func (h *handler) getMockResponse(ctx context.Context, r *http.Request) (MockResponse, bool) {
	h.Sync(ctx)
	h.mux.Lock()
	defer h.mux.Unlock()

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		logjson.Warn("could not read body")
		return MockResponse{}, false
	}
	var candidates []MockMapping
	for _, m := range h.mappings {
		if m.Rule.matches(r, body) {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		return MockResponse{}, false
	}
	max := candidates[0]
	for i := 1; i < len(candidates); i += 1 {
		if max.Rule.Priority < candidates[i].Rule.Priority {
			max = candidates[i]
		}
	}
	logjson.Debug("rule %s wins with priority %v", max.Name(), max.Rule.Priority)
	return max.Response, true
}

func (h *handler) setMockMapping(ctx context.Context, r *http.Request) (*MockMapping, error) {
	bs, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, fmt.Errorf("could not read body: %s", err)
	}
	m := &MockMapping{}
	if err := json.Unmarshal(bs, m); err != nil {
		return nil, fmt.Errorf("could not parse body: %s", err)
	}

	if err := validateMockMapping(m); err != nil {
		return nil, err
	}

	m.UpdateTime = time.Now().UTC()
	if m.Response.StatusCode == 0 {
		m.Response.StatusCode = http.StatusOK
	}

	h.mux.Lock()
	var newMappings []MockMapping
	for _, existing := range h.mappings {
		if existing.Name() != m.Name() {
			newMappings = append(newMappings, existing)
		}
	}
	newMappings = append(newMappings, *m)
	h.mappings = newMappings
	h.mux.Unlock()

	logjson.Info("cached mapping %s", m.Name())
	h.Sync(ctx)

	return m, nil
}

func (h *handler) listMappings(ctx context.Context, w http.ResponseWriter, _ *http.Request) {
	h.Sync(ctx)
	h.mux.Lock()
	defer h.mux.Unlock()
	resp := struct {
		Mappings []MockMapping `json:"mappings"`
	}{}
	if len(h.mappings) == 0 {
		resp.Mappings = []MockMapping{}
	} else {
		resp.Mappings = h.mappings
	}
	bs, _ := json.Marshal(resp)
	w.Header().Set("content-type", "application/json")
	_, _ = w.Write(bs)
}

func record(w http.ResponseWriter, r *http.Request) {
	bs, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not read body: %s", err), http.StatusBadRequest)
		return
	}
	req := &SimpleRequest{}
	if err := json.Unmarshal(bs, req); err != nil {
		http.Error(w, fmt.Sprintf("could not parse body: %s", err), http.StatusBadRequest)
		return
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	u, err := url.Parse(req.Scheme + req.Path)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not parse url: %s%s", req.Scheme, req.Path), http.StatusBadRequest)
		return
	}
	for k, vs := range req.QueryParams {
		for _, v := range vs {
			u.Query().Add(k, v)
		}
	}
	var body io.Reader
	if len(req.TextBody) > 0 {
		body = strings.NewReader(req.TextBody)
	}

	out, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		http.Error(w, "invalid request data", http.StatusBadRequest)
		return
	}
	for k, vs := range req.Headers {
		for _, v := range vs {
			out.Header.Add(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(out)
	if err != nil {
		http.Error(w, fmt.Sprintf("error calling service: %s", err), http.StatusServiceUnavailable)
		return
	}
	bs, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not read response: %s", err), http.StatusInternalServerError)
		return
	}
	ct := resp.Header.Get("content-type")
	text, bs := guessBody(ct, bs)
	rec := Recording{
		Request: &SimpleRequest{
			Method:      method,
			Path:        u.Path,
			QueryParams: req.QueryParams,
			TextBody:    req.TextBody,
			Headers:     req.Headers,
		},
		Response: &MockResponse{
			ContentType: ct,
			StatusCode:  resp.StatusCode,
			TextBody:    text,
			BytesBody:   bs,
			Headers:     make(map[string][]string),
		},
	}
	for k, vs := range resp.Header {
		rec.Response.Headers[k] = vs
	}

	bs, _ = json.Marshal(rec)
	w.Header().Set("content-type", "application/json")
	_, _ = w.Write(bs)
}

func guessBody(contentType string, body []byte) (string, []byte) {
	xs := strings.Split(contentType, ";")
	enc := getEncoding(xs)
	t := strings.Trim(xs[0], " ")
	switch t {
	case "application/octet-stream":
		return "", body
	case "text/xml":
		return decode(enc, body), nil
	case "application/xml":
		return decode(enc, body), nil
	case "text/plain":
		return decode(enc, body), nil
	case "text/html":
		return decode(enc, body), nil
	case "application/json":
		return decode(enc, body), nil
	}
	logjson.Debug("received content type %s, defaulting to string", contentType)
	return string(body), nil
}

func getEncoding(xs []string) encoding.Encoding {
	if len(xs) > 1 {
		for _, x := range xs {
			clean := strings.ToLower(strings.Trim(x, " "))
			if strings.HasPrefix(clean, "charset=") {
				name := strings.Split(clean, "charset=")
				if len(name) < 2 {
					continue
				}
				enc, err := ianaindex.IANA.Encoding(strings.Trim(name[1], " "))
				if err != nil {
					logjson.Warn("%s: %s", err, name[1])
				} else {
					logjson.Info("found encoding %s", name[1])
					return enc
				}
			}
		}
	}
	logjson.Warn("could not determine encoding, defaulting to UTF-8")
	return unicode.UTF8
}

func decode(enc encoding.Encoding, in []byte) string {
	dec := enc.NewDecoder()
	bs, err := dec.Bytes(in)
	if err != nil {
		logjson.Warn("could not decode body")
		return ""
	}
	return string(bs)
}

func validateMockMapping(m *MockMapping) error {
	if m.Rule.PathRegex != "" {
		if m.Rule.Path != "" {
			return fmt.Errorf("cannot have both path and path regex")
		}
		if _, err := regexp.Compile(m.Rule.PathRegex); err != nil {
			return fmt.Errorf("could not compile regex: %s", err)
		}
	}
	i := 0
	if m.Response.TextBody != "" {
		i += 1
	}
	if m.Response.JsonBody != nil {
		i += 1
	}
	if m.Response.BytesBody != nil {
		i += 1
	}
	if i > 1 {
		return fmt.Errorf("only fill one body type")
	}
	return nil
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx := context.Background()
	h, _ := newHandler(ctx)

	logjson.Notice("listening on port %s", port)
	if err := http.ListenAndServe(":"+port, h); err != nil {
		logjson.Critical(err)
	}
}
