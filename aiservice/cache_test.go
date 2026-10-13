package aiservice

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPersistentCacheRestartAndIdentity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, "summary") }))
	defer server.Close()
	dir := t.TempDir()
	secrets := &memorySecrets{keys: map[Provider]string{Custom: "secret", Magpie: "secret"}}
	c := DefaultConfig()
	c.Custom.Enabled = true
	c.Custom.Model = "mock"
	c.Custom.BaseURL = server.URL + "/v1"
	c.Magpie = c.Custom
	makeService := func(c Config) *Service {
		s, e := NewWithCacheDir(c, secrets, dir)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	run := func(s *Service, text string) {
		if _, e := s.Summarize(context.Background(), text); e != nil {
			t.Fatal(e)
		}
	}
	run(makeService(c), "article")
	run(makeService(c), "article")
	if calls.Load() != 1 {
		t.Fatal("restart missed", calls.Load())
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.Custom.Model = "other" }, func(c *Config) { c.Custom.Language = "中文" }, func(c *Config) { c.Custom.BaseURL += "/other" }, func(c *Config) { c.ActiveProvider = Magpie }} {
		changed := c
		mutate(&changed)
		before := calls.Load()
		run(makeService(changed), "article")
		if calls.Load() != before+1 {
			t.Fatal("identity collision")
		}
	}
	run(makeService(c), "changed article")
	secrets.Set(Custom, "second-secret")
	run(makeService(c), "article")
	before := calls.Load()
	secrets.Set(Custom, "secret")
	run(makeService(c), "article")
	if calls.Load() != before+1 {
		t.Fatal("old credential resurrected")
	}
	s := makeService(c)
	s.ForceRefresh()
	before = calls.Load()
	run(s, "article")
	if calls.Load() != before+1 {
		t.Fatal("regenerate hit cache")
	}
	files, _ := os.ReadDir(dir)
	for _, f := range files {
		b, _ := os.ReadFile(filepath.Join(dir, f.Name()))
		if strings.Contains(string(b), "secret") {
			t.Fatal("credential persisted")
		}
		info, _ := f.Info()
		if info.Mode().Perm() != 0600 {
			t.Fatal(info.Mode())
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".json") {
			os.WriteFile(filepath.Join(dir, f.Name()), []byte("corrupt"), 0600)
		}
	}
	before = calls.Load()
	run(makeService(c), "article")
	if calls.Load() != before+1 {
		t.Fatal("corrupt cache accepted")
	}
}
func TestPersistentFullTranslationRestartDifferentBatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body completion
		json.NewDecoder(r.Body).Decode(&body)
		var spans struct {
			Spans []TranslationSpan `json:"spans"`
		}
		json.Unmarshal([]byte(body.Messages[1].Content), &spans)
		for i := range spans.Spans {
			spans.Spans[i].Text = "译文"
		}
		b, _ := json.Marshal(spans)
		reply(w, string(b))
	}))
	defer server.Close()
	c := DefaultConfig()
	c.Custom.Enabled = true
	c.Custom.Model = "mock"
	c.Custom.BaseURL = server.URL + "/v1"
	dir := t.TempDir()
	store := &memorySecrets{keys: map[Provider]string{}}
	source := "# Heading\n\n[Body](https://example.com) `code`\n\n" + strings.Repeat("Sentence. ", 300)
	s, _ := NewWithCacheDir(c, store, dir)
	first, e := s.TranslateMarkdown(context.Background(), source, TranslationOptions{BatchBytes: 1200})
	if e != nil {
		t.Fatal(e)
	}
	before := calls.Load()
	s, _ = NewWithCacheDir(c, store, dir)
	second, e := s.TranslateMarkdown(context.Background(), source, TranslationOptions{BatchBytes: 16000})
	if e != nil || second.Markdown != first.Markdown || second.Completed != second.Total || calls.Load() != before {
		t.Fatal("full translation restart", e, calls.Load())
	}
	if !strings.Contains(second.Markdown, "https://example.com") || !strings.Contains(second.Markdown, "`code`") {
		t.Fatal("structure changed")
	}
}
func TestStaleCompletionRejected(t *testing.T) {
	for _, mode := range []string{"config", "external-key", "set-key"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-release; reply(w, "old") }))
			defer server.Close()
			s := testService(t, server.URL)
			done := make(chan error, 1)
			go func() { _, e := s.Summarize(context.Background(), "article"); done <- e }()
			<-started
			switch mode {
			case "config":
				c := s.Config()
				c.ActiveProvider = Magpie
				c.Magpie = c.Custom
				s.UpdateConfig(c)
			case "external-key":
				s.secrets.Set(Custom, "changed")
			case "set-key":
				s.SetKey(Custom, "changed")
			}
			close(release)
			if e := <-done; !errors.Is(e, ErrStale) {
				t.Fatal("old completion accepted", e)
			}
			files, _ := filepath.Glob(filepath.Join(s.cacheDir, "*.json"))
			if len(files) != 0 {
				t.Fatal("old response persisted")
			}
		})
	}
}
func TestCacheEvictionAndUnavailable(t *testing.T) {
	s := testService(t, "http://127.0.0.1:1")
	x, _ := s.snapshot("", true)
	for i := 0; i < cacheCountLimit+4; i++ {
		key := s.cacheIdentity(x, "summary", strings.Repeat("x", i+1), "prompt", 1)
		s.diskPut(key, "value")
	}
	files, _ := filepath.Glob(filepath.Join(s.cacheDir, "*.json"))
	if len(files) != cacheCountLimit {
		t.Fatal("count", len(files))
	}
	key := s.cacheIdentity(x, "summary", "large", "prompt", 1)
	s.diskPut(key, strings.Repeat("x", cacheEntryLimit))
	if _, ok := s.diskGet(key); ok {
		t.Fatal("oversize persisted")
	}
	block := filepath.Join(t.TempDir(), "file")
	os.WriteFile(block, []byte("x"), 0600)
	other, e := NewWithCacheDir(s.Config(), s.secrets, block)
	if e != nil {
		t.Fatal(e)
	}
	if len(other.salt) != 0 {
		t.Fatal("unavailable cache enabled")
	}
}

func TestPartialCacheRestartWithoutResume(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			w.WriteHeader(503)
			return
		}
		translateReply(w, r, func(s string) string { return "translated" })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	source := strings.Repeat("Sentence with several words. ", 160)
	first, e := s.TranslateMarkdown(context.Background(), source, TranslationOptions{BatchBytes: 1200})
	if e == nil || first.Completed == 0 {
		t.Fatal("expected partial", e)
	}
	s, e = NewWithCacheDir(s.Config(), s.secrets, s.cacheDir)
	if e != nil {
		t.Fatal(e)
	}
	var initial int
	second, e := s.TranslateMarkdown(context.Background(), source, TranslationOptions{BatchBytes: 1200, OnProgress: func(p TranslationProgress) {
		if p.Completed > 0 && initial == 0 {
			initial = p.Completed
		}
	}})
	if e != nil || second.Completed != second.Total || initial != first.Completed || int(calls.Load()) != second.Total+1 {
		t.Fatal("partial restart requested completed batch", e, calls.Load(), second.Total, initial, first.Completed)
	}
}
func TestCacheByteBudget(t *testing.T) {
	s := testService(t, "http://127.0.0.1:1")
	x, _ := s.snapshot("", true)
	for i := 0; i < 12; i++ {
		s.diskPut(s.cacheIdentity(x, "summary", strings.Repeat("x", i+1), "prompt", 1), strings.Repeat("v", 3<<20))
	}
	files, _ := filepath.Glob(filepath.Join(s.cacheDir, "*.json"))
	var size int64
	for _, path := range files {
		info, _ := os.Stat(path)
		size += info.Size()
	}
	if size > cacheTotalLimit || len(files) >= 12 {
		t.Fatal("byte budget not enforced", size, len(files))
	}
}
