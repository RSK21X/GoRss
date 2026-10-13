package aiservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memorySecrets struct {
	mu   sync.Mutex
	keys map[Provider]string
}

func (m *memorySecrets) Get(p Provider) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.keys[p]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}
func (m *memorySecrets) Set(p Provider, v string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys[p] = v
	return nil
}
func (m *memorySecrets) Delete(p Provider) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.keys, p)
	return nil
}
func testService(t *testing.T, url string) *Service {
	t.Helper()
	c := DefaultConfig()
	c.Custom.Enabled = true
	c.Custom.BaseURL = url + "/v1"
	c.Custom.Model = "mock"
	s, e := NewWithCacheDir(c, &memorySecrets{keys: map[Provider]string{Custom: "secret-test"}}, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func reply(w http.ResponseWriter, text string) {
	json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text}, "finish_reason": "stop"}}})
}
func TestConfig(t *testing.T) {
	c := DefaultConfig()
	if c != c {
		t.Fatal("not comparable")
	}
	c.Custom.Model = "custom"
	c.Magpie.Model = "magpie"
	path := filepath.Join(t.TempDir(), "config.json")
	if e := SaveConfig(path, c); e != nil {
		t.Fatal(e)
	}
	got, e := LoadConfig(path)
	if e != nil || got != c {
		t.Fatal(got, e)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "key") {
		t.Fatal("key persisted")
	}
	os.WriteFile(path, []byte(`{"api_key":"secret"}`), 0600)
	if _, e = LoadConfig(path); e == nil {
		t.Fatal("accepted secret field")
	}
}
func TestURLPolicy(t *testing.T) {
	for _, v := range []string{"http://example.com/v1", "http://127.0.0.1.evil/v1", "http://localhost.evil/v1", "http://2130706433/v1", "https://user:key@example.com/v1", "https://example.com/v1?key=secret", "https://example.com/v1#key", "file:///tmp/foo", "http://192.168.1.1/v1"} {
		if _, e := validateBase(v); e == nil {
			t.Error("accepted", v)
		}
	}
	for _, v := range []string{"http://127.0.0.1:3425/v1", "http://[::1]:3425/v1", "http://localhost:3425/v1", "https://example.com/api/v1"} {
		if _, e := validateBase(v); e != nil {
			t.Error(v, e)
		}
	}
}
func TestModelsAndTestModel(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-test" {
			t.Error("missing credential")
		}
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"z"},{"id":"a"},{"id":"a"}]}`)
		case "/v1/chat/completions":
			posts.Add(1)
			var c completion
			json.NewDecoder(r.Body).Decode(&c)
			if c.MaxTokens != 2048 || c.Model != "mock" || c.Stream {
				t.Error(c)
			}
			reply(w, "OK")
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	s := testService(t, server.URL)
	models, e := s.ListModels(context.Background(), Custom)
	if e != nil || len(models) != 2 || models[0].ID != "a" {
		t.Fatal(models, e)
	}
	if e = s.TestModel(context.Background(), Custom); e != nil || posts.Load() != 1 {
		t.Fatal(e)
	}
	c := s.Config()
	c.Custom.Enabled = false
	s.UpdateConfig(c)
	if _, e = s.Summarize(context.Background(), "hello"); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	if e = s.Probe(context.Background(), Custom); e != nil {
		t.Fatal(e)
	}
}
func TestRedirectAndRedaction(t *testing.T) {
	var leaks atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaks.Add(1); t.Error("cross-origin redirect followed") }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL+"/secret-test", 302) }))
	defer server.Close()
	s := testService(t, server.URL)
	e := s.Probe(context.Background(), Custom)
	if !errors.Is(e, ErrNetwork) || leaks.Load() != 0 {
		t.Fatal(e)
	}
	if strings.Contains(e.Error(), server.URL) || strings.Contains(e.Error(), "secret-test") {
		t.Fatal("unsafe error")
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, "secret-test "+server.URL)
	}))
	defer bad.Close()
	s = testService(t, bad.URL)
	e = s.Probe(context.Background(), Custom)
	var he *HTTPError
	if !errors.As(e, &he) || he.StatusCode != 401 || strings.Contains(e.Error(), "secret-test") {
		t.Fatal(e)
	}
}
func TestSameOriginRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			http.Redirect(w, r, "/catalog", 307)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-test" {
			t.Error("credential lost")
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	if e := testService(t, server.URL).Probe(context.Background(), Custom); e != nil {
		t.Fatal(e)
	}
}
func TestCacheAndRace(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, "summary") }))
	defer server.Close()
	s := testService(t, server.URL)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, e := s.Summarize(ctx, "article"); e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	s.SetKey(Custom, "rotated")
	s.Summarize(ctx, "article")
	if calls.Load() != 2 {
		t.Fatal("credential revision did not invalidate")
	}
	c := s.Config()
	c.Custom.Language = "French"
	s.UpdateConfig(c)
	s.Summarize(ctx, "article")
	if calls.Load() != 3 {
		t.Fatal("language not keyed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				s.SetKey(Custom, "rotated")
			} else if i%3 == 1 {
				s.UpdateConfig(c)
			} else {
				s.Summarize(ctx, "article")
			}
		}(i)
	}
	wg.Wait()
}
func TestCancellationAndMalformed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	s := testService(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := s.Probe(ctx, Custom); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"choices":[]}`) }))
	defer bad.Close()
	s = testService(t, bad.URL)
	if e := s.TestModel(context.Background(), Custom); !errors.Is(e, ErrProtocol) {
		t.Fatal(e)
	}
}
func TestMagpieDefaultToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer magpie-gorss" {
			t.Error(r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/hello":
			fmt.Fprint(w, `{"name":"magpie","version":"test"}`)
		case "/v1/models":
			fmt.Fprint(w, `{"data":[]}`)
		default:
			t.Error(r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	c := DefaultConfig()
	c.Magpie.BaseURL = server.URL + "/v1"
	s, _ := New(c, &memorySecrets{keys: map[Provider]string{}})
	if e := s.Probe(context.Background(), Magpie); e != nil {
		t.Fatal(e)
	}
}

func TestOversizedAndInvalidResponses(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":[{"id":""}]}`, `not JSON`, strings.Repeat("x", 4*1024*1024+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		e := testService(t, server.URL).Probe(context.Background(), Custom)
		server.Close()
		if !errors.Is(e, ErrProtocol) {
			t.Fatal(e)
		}
	}
}
func TestConfigAtomicReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := DefaultConfig()
	if e := SaveConfig(path, c); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, e := LoadConfig(path); e != nil {
				t.Error(e)
			}
		}
	}()
	for i := 0; i < 30; i++ {
		c.Custom.Model = fmt.Sprint(i)
		if e := SaveConfig(path, c); e != nil {
			t.Fatal(e)
		}
	}
	wg.Wait()
}
func TestSelectionAndProviderIsolation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, "result") }))
	defer server.Close()
	s := testService(t, server.URL)
	ctx := context.Background()
	s.SummarizeSelection(ctx, "text")
	s.TranslateSelection(ctx, "text")
	if calls.Load() != 2 {
		t.Fatal("operation cache collision")
	}
	s.SummarizeSelection(ctx, "text")
	if calls.Load() != 2 {
		t.Fatal("cache miss")
	}
	c := s.Config()
	c.Magpie = c.Custom
	c.ActiveProvider = Magpie
	s.UpdateConfig(c)
	s.SummarizeSelection(ctx, "text")
	if calls.Load() != 3 {
		t.Fatal("provider cache collision")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e := s.SummarizeSelection(cancelled, "text"); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func magpieTestService(t *testing.T, base string) *Service {
	t.Helper()
	c := DefaultConfig()
	c.Magpie.BaseURL = base
	s, err := New(c, &memorySecrets{keys: map[Provider]string{Magpie: "secret-test"}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDetectMagpieIndependent(t *testing.T) {
	for _, suffix := range []string{"/v1", "/v1/", "/gateway/v1", ""} {
		t.Run(suffix, func(t *testing.T) {
			var helloCalls, modelCalls atomic.Int32
			prefix := strings.TrimSuffix(strings.TrimRight(suffix, "/"), "/v1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer secret-test" {
					t.Error("unexpected request")
				}
				switch r.URL.Path {
				case prefix + "/api/hello":
					helloCalls.Add(1)
					fmt.Fprint(w, `{"name":"magpie","version":"test"}`)
				case strings.TrimRight(suffix, "/") + "/models":
					modelCalls.Add(1)
					fmt.Fprint(w, `{"data":[{"id":"discovered"}]}`)
				default:
					t.Error(r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			s := magpieTestService(t, server.URL+suffix)
			if err := s.DetectMagpie(context.Background()); err != nil {
				t.Fatal(err)
			}
			if helloCalls.Load() != 1 || modelCalls.Load() != 0 {
				t.Fatal("detection queried models")
			}
			if err := s.Probe(context.Background(), Magpie); err != nil {
				t.Fatal(err)
			}
			if helloCalls.Load() != 2 || modelCalls.Load() != 1 {
				t.Fatal("probe did not check identity and models")
			}
			models, err := s.ListModels(context.Background(), Magpie)
			if err != nil || len(models) != 1 || models[0].ID != "discovered" {
				t.Fatal(models, err)
			}
		})
	}
}

func TestMagpieIdentityRequired(t *testing.T) {
	for _, body := range []string{`{"name":"other","version":"secret-test"}`, `{"name":"Magpie"}`, `{"data":[]}`, `{"name":null}`, `not JSON`} {
		t.Run(body, func(t *testing.T) {
			var modelCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/hello" {
					fmt.Fprint(w, body)
					return
				}
				modelCalls.Add(1)
				fmt.Fprint(w, `{"data":[]}`)
			}))
			defer server.Close()
			s := magpieTestService(t, server.URL+"/v1")
			if err := s.Probe(context.Background(), Magpie); !errors.Is(err, ErrProtocol) {
				t.Fatal(err)
			}
			if modelCalls.Load() != 0 {
				t.Fatal("queried models after invalid identity")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/hello" {
			fmt.Fprint(w, `{"name":"magpie"}`)
			return
		}
		fmt.Fprint(w, `{"data":null}`)
	}))
	defer server.Close()
	s := magpieTestService(t, server.URL+"/v1")
	if err := s.DetectMagpie(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Probe(context.Background(), Magpie); !errors.Is(err, ErrProtocol) {
		t.Fatal("probe accepted invalid catalog", err)
	}
}

func TestMagpieRedirectsAndSafeErrors(t *testing.T) {
	var leaks atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaks.Add(1) }))
	defer destination.Close()
	for _, redirectPath := range []string{"/api/hello", "/v1/models"} {
		t.Run(redirectPath, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == redirectPath {
					http.Redirect(w, r, destination.URL+"/secret-test", 307)
					return
				}
				fmt.Fprint(w, `{"name":"magpie"}`)
			}))
			defer server.Close()
			err := magpieTestService(t, server.URL+"/v1").Probe(context.Background(), Magpie)
			if !errors.Is(err, ErrNetwork) || leaks.Load() != 0 {
				t.Fatal(err, leaks.Load())
			}
			if strings.Contains(err.Error(), "secret-test") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("unsafe error", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/hello" {
			http.Redirect(w, r, "/hello", 307)
			return
		}
		if r.URL.Path == "/hello" {
			if r.Header.Get("Authorization") != "Bearer secret-test" {
				t.Error("credential lost")
			}
			fmt.Fprint(w, `{"name":"magpie"}`)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer server.Close()
	if err := magpieTestService(t, server.URL+"/v1").Probe(context.Background(), Magpie); err != nil {
		t.Fatal(err)
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "secret-test "+server.URL)
	}))
	defer bad.Close()
	err := magpieTestService(t, bad.URL+"/v1").DetectMagpie(context.Background())
	var status *HTTPError
	if !errors.As(err, &status) || status.StatusCode != 401 || strings.Contains(err.Error(), "secret-test") || strings.Contains(err.Error(), server.URL) {
		t.Fatal(err)
	}
}

func TestMagpieHelloTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	s := magpieTestService(t, server.URL+"/v1")
	started := time.Now()
	if err := s.DetectMagpie(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < time.Second || elapsed > 4*time.Second {
		t.Fatal("unexpected hello timeout", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.DetectMagpie(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestEnabledConfigCanDiscoverBeforeModelAndLanguage(t *testing.T) {
	for _, provider := range []Provider{Custom, Magpie} {
		for _, missing := range []string{"model", "language"} {
			t.Run(string(provider)+"/"+missing, func(t *testing.T) {
				c := DefaultConfig()
				pc := &c.Custom
				if provider == Magpie {
					pc = &c.Magpie
				}
				pc.Model = "mock"
				if missing == "model" {
					pc.Model = " \t"
				} else {
					pc.Language = " \t"
				}
				if err := c.Validate(); err != nil {
					t.Fatal("disabled discovery config rejected", err)
				}
				pc.Enabled = true
				if err := c.Validate(); err != nil {
					t.Fatal("enabled discovery rejected", err)
				}
				c.ActiveProvider = provider
				service, err := newService(c, &memorySecrets{})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := service.snapshot(provider, true); !errors.Is(err, ErrConfig) {
					t.Fatal("generation accepted missing field", err)
				}
			})
		}
	}
}

func TestMagpieProbeUsesOneSnapshot(t *testing.T) {
	var otherCalls atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherCalls.Add(1)
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer other.Close()
	var s *Service
	var models atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/hello":
			c := s.Config()
			c.Magpie.BaseURL = other.URL + "/v1"
			if err := s.UpdateConfig(c); err != nil {
				t.Error(err)
			}
			fmt.Fprint(w, `{"name":"magpie"}`)
		case "/v1/models":
			models.Add(1)
			fmt.Fprint(w, `{"data":[]}`)
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	s = magpieTestService(t, server.URL+"/v1")
	c := s.Config()
	c.ActiveProvider = Magpie
	if err := s.UpdateConfig(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Probe(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if models.Load() != 1 || otherCalls.Load() != 0 {
		t.Fatal("probe mixed endpoints from different config snapshots")
	}
}

func TestDraftDiscoveryBlankEnabledAndCredentials(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Header.Get("Authorization") != "Bearer draft-only" {
				t.Error("draft credential not used")
			}
			switch r.URL.Path {
			case "/api/hello":
				fmt.Fprint(w, `{"name":"magpie","version":"mock"}`)
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"mock-model"}]}`)
			default:
				t.Error("unexpected model request")
				w.WriteHeader(404)
			}
		}))
		pc := DefaultConfig().Magpie
		pc.BaseURL = server.URL + "/v1"
		pc.Enabled = enabled
		store := &memorySecrets{keys: map[Provider]string{Magpie: "saved-only"}}
		s, err := NewForProvider(Magpie, pc, "draft-only", store)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Probe(context.Background(), Magpie); err != nil {
			t.Fatal(err)
		}
		models, err := s.ListModels(context.Background(), Magpie)
		if err != nil || len(models) != 1 {
			t.Fatal(models, err)
		}
		if err = s.TestModel(context.Background(), Magpie); !errors.Is(err, ErrConfig) {
			t.Fatal(err)
		}
		if calls != 3 || store.keys[Magpie] != "saved-only" || s.cacheDir != "" {
			t.Fatal("discovery mutated state or requested model")
		}
		server.Close()
	}
}

func TestDiscoveryWithoutModelEndpointKeepsManualModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	defer server.Close()
	pc := DefaultConfig().Custom
	pc.BaseURL = server.URL + "/v1"
	pc.Model = "manual-model"
	s, err := NewForProvider(Custom, pc, "", &memorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ListModels(context.Background(), Custom)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 404 || s.Config().Custom.Model != "manual-model" {
		t.Fatal("missing endpoint lost manual config", err)
	}
}

// Exercise the same HTTP and parsing path used by the settings model test.
func TestChatCompletionVariants(t *testing.T) {
	cases := []struct {
		name, body string
		want       error
	}{
		{"plain non OK", `{"choices":[{"message":{"content":"Hello there"},"finish_reason":"stop"}]}`, nil},
		{"array", `{"choices":[{"message":{"content":[{"type":"text","text":"Hello"},{"type":"text","text":" there"}]},"finish_reason":"stop"}]}`, nil},
		{"reasoning with final", `{"choices":[{"message":{"reasoning_content":"private-thought","content":"Hello"},"finish_reason":"stop"}]}`, nil},
		{"reasoning only", `{"choices":[{"message":{"reasoning_content":"private-thought","content":null},"finish_reason":"stop"}]}`, ErrEmptyContent},
		{"length before empty", `{"choices":[{"message":{"content":null,"reasoning_content":"private-thought"},"finish_reason":"length"}]}`, ErrOutputLength},
		{"length text", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, ErrOutputLength},
		{"responses protocol", `{"object":"response","output":[]}`, ErrUnsupportedProtocol},
		{"missing", `{"choices":[]}`, ErrMissingChoices},
		{"empty", `{"choices":[{"message":{"content":" "}}]}`, ErrEmptyContent},
		{"bad json", `not-json-private-thought`, ErrInvalidJSON},
		{"wrong protocol", `{"choices":[{"message":{"content":{"text":"private-thought"}}}]}`, ErrUnsupportedProtocol},
		{"tool call", `{"choices":[{"message":{"content":null},"finish_reason":"tool_calls"}]}`, ErrUnsupportedProtocol},
		{"reasoning array", `{"choices":[{"message":{"content":[{"type":"reasoning","text":"private-thought"}]}}]}`, ErrEmptyContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload completion
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/v1/chat/completions" || payload.MaxTokens < 1024 || payload.ResponseFormat != nil {
					t.Error("wrong diagnostic request")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			pc := DefaultConfig().Magpie
			pc.BaseURL = server.URL + "/v1"
			pc.Model = "reasoning-mock"
			s, err := NewForProvider(Magpie, pc, "", &memorySecrets{})
			if err != nil {
				t.Fatal(err)
			}
			err = s.TestModel(context.Background(), Magpie)
			if !errors.Is(err, tc.want) || calls != 1 {
				t.Fatalf("error %v, calls %d", err, calls)
			}
			if err != nil && strings.Contains(err.Error(), "private-thought") {
				t.Fatal("response leaked")
			}
		})
	}
}

func TestTranslationFormatRetryIsControlled(t *testing.T) {
	for _, mode := range []string{"supported", "unsupported", "generic400", "invalid", "length"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload completion
				json.NewDecoder(r.Body).Decode(&payload)
				if (calls == 1) != (payload.ResponseFormat != nil) {
					t.Error("incorrect response_format")
				}
				if calls == 1 && (mode == "unsupported" || mode == "generic400") {
					w.WriteHeader(400)
					if mode == "unsupported" {
						fmt.Fprint(w, `{"error":{"param":"response_format","code":"unsupported_parameter","message":"response_format is not supported private-thought"}}`)
					} else {
						fmt.Fprint(w, `{"error":{"message":"bad model private-thought"}}`)
					}
					return
				}
				if mode == "invalid" {
					reply(w, `{"spans":[]}`)
					return
				}
				if mode == "length" {
					fmt.Fprint(w, `{"choices":[{"message":{"content":null},"finish_reason":"length"}]}`)
					return
				}
				var data struct {
					Spans []TranslationSpan `json:"spans"`
				}
				json.Unmarshal([]byte(payload.Messages[1].Content), &data)
				for i := range data.Spans {
					data.Spans[i].Text = "译文"
				}
				b, _ := json.Marshal(data)
				reply(w, string(b))
			}))
			defer server.Close()
			s := testService(t, server.URL)
			result, err := s.TranslateMarkdown(context.Background(), "Hello world", TranslationOptions{})
			wantCalls := 1
			if mode == "unsupported" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Fatalf("unexpected paid retry: %d", calls)
			}
			switch mode {
			case "supported", "unsupported":
				if err != nil || result.Completed != result.Total {
					t.Fatal(err, result)
				}
			case "invalid":
				if !errors.Is(err, ErrTranslation) {
					t.Fatal(err)
				}
			case "length":
				if !errors.Is(err, ErrOutputLength) {
					t.Fatal(err)
				}
			case "generic400":
				var he *HTTPError
				if !errors.As(err, &he) {
					t.Fatal(err)
				}
			}
			if err != nil && strings.Contains(err.Error(), "private-thought") {
				t.Fatal("response leaked")
			}
		})
	}
}
