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
	"os"
	"regexp"
	"strings"
	"time"
)

type MockMapping struct {
	UpdateTime time.Time    `json:"update_time"`
	Rule       MockRule     `json:"rule"`
	Response   MockResponse `json:"response"`
}

func (m MockMapping) Name() string {
	s := fmt.Sprintf("%x", m.Rule.Hash())
	if len(s) < 8 {
		return s
	}
	return s[:8]
}

type MockRule struct {
	Methods       []string `json:"methods"`
	Path          string   `json:"path"`
	PathRegex     string   `json:"path_regex"`
	TextBodyRegex string   `json:"text_body_regex"`
}

func (mr MockRule) Hash() []byte {
	h := sha1.New()
	h.Write([]byte(mr.Path))
	h.Write([]byte("~~~"))
	h.Write([]byte(mr.PathRegex))
	return h.Sum(nil)
}

func (mr MockRule) matches(method string, path string, body []byte) bool {
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
		pathOk = re.MatchString(path)
	} else {
		pathOk = mr.Path == path
	}

	bodyOk := false
	if mr.TextBodyRegex != "" {
		re, _ := regexp.Compile(mr.TextBodyRegex)
		bodyOk = re.Match(body)
	} else {
		bodyOk = true
	}

	return methodOk && pathOk && bodyOk
}

type MockResponse struct {
	ContentType string `json:"content_type"`
	TextBody    string `json:"text_body"`
	JsonBody    string `json:"json_body"`
	BytesBody   []byte `json:"bytes_body"`
	StatusCode  int    `json:"status_code"`
}

const collection = "services/mockmate/mapping"

type handler struct {
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

func (h *handler) Sync(ctx context.Context) {
	if h.client == nil {
		return
	}

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
	} else if mr.JsonBody != "" {
		_, _ = w.Write([]byte(mr.JsonBody))
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
	http.NotFound(w, r)
}

func (h *handler) getMockResponse(ctx context.Context, r *http.Request) (MockResponse, bool) {
	h.Sync(ctx)
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		logjson.Warn("could not read body")
		return MockResponse{}, false
	}
	for _, m := range h.mappings {
		if m.Rule.matches(r.Method, r.URL.Path, body) {
			return m.Response, true
		}
	}
	return MockResponse{}, false
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

	name := m.Name()
	var newMappings []MockMapping
	for _, existing := range h.mappings {
		if existing.Name() != name {
			newMappings = append(newMappings, existing)
		}
	}
	newMappings = append(newMappings, *m)
	h.mappings = newMappings
	logjson.Info("cached mapping %s", name)
	h.Sync(ctx)

	return m, nil
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
	if m.Response.JsonBody != "" {
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

	if err := http.ListenAndServe(":"+port, h); err != nil {
		logjson.Critical(err)
	}
}
