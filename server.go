package main

import (
	"cloud.google.com/go/compute/metadata"
	"cloud.google.com/go/firestore"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"github.com/HayoVanLoon/go-commons/logjson"
	"google.golang.org/api/iterator"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"regexp"
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

type KV struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type MockRule struct {
	Priority      int      `json:"priority"`
	Methods       []string `json:"methods"`
	Path          string   `json:"path"`
	PathRegex     string   `json:"path_regex"`
	TextBodyRegex string   `json:"text_body_regex"`
	QueryParams   []KV     `json:"query_params"`
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

func (mr MockRule) matches(method string, u *url.URL, body []byte) bool {
	methodOk := len(mr.Methods) == 0
	if !methodOk {
		for _, m := range mr.Methods {
			if method == m {
				methodOk = true
				break
			}
		}
	}

	pathOk := false
	if mr.PathRegex != "" {
		re, _ := regexp.Compile(mr.PathRegex)
		pathOk = re.MatchString(u.Path)
	} else {
		pathOk = mr.Path == u.Path
	}

	paramsOk := len(mr.QueryParams) == 0
	if !paramsOk {
		for _, kv := range mr.QueryParams {
			valuesMap := u.Query()
			found := false
			for _, v := range valuesMap[kv.Key] {
				if v == kv.Value {
					found = true
					break
				}
			}
			paramsOk = paramsOk && found
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
	ContentType string                 `json:"content_type"`
	TextBody    string                 `json:"text_body"`
	JsonBody    map[string]interface{} `json:"json_body"`
	BytesBody   []byte                 `json:"bytes_body"`
	StatusCode  int                    `json:"status_code"`
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
		if m.Rule.matches(r.Method, r.URL, body) {
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

func (h *handler) Reset() {
	h.mux.Lock()

	h.mappings = nil
	defer h.mux.Unlock()

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
