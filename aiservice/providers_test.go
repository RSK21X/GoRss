package aiservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDynamicProvidersIsolateCredentialsConfigAndCache(t *testing.T) {
	a := Provider("provider-" + strings.Repeat("a", 32))
	b := Provider("provider-" + strings.Repeat("b", 32))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); reply(w, r.Header.Get("Authorization")) }))
	defer server.Close()
	c := DefaultConfig()
	pc := c.Custom
	pc.Enabled = true
	pc.BaseURL = server.URL + "/v1"
	pc.Model = "same-model"
	c.Providers = []NamedProvider{{ID: a, Config: pc}, {ID: b, Config: pc}}
	c.ActiveProvider = a
	store := &memorySecrets{keys: map[Provider]string{a: "key-a", b: "key-b", Custom: "legacy-key"}}
	s, err := NewWithCacheDir(c, store, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.Providers[0].Config.Model = "input changed"
	snapshot := s.Config()
	snapshot.Providers[0].Config.Model = "snapshot changed"
	if s.Config().Providers[0].Config.Model != "same-model" {
		t.Fatal("configuration aliases a caller-owned slice")
	}
	for _, p := range []Provider{a, b, a} {
		c = s.Config()
		c.ActiveProvider = p
		if err := s.UpdateConfig(c); err != nil {
			t.Fatal(err)
		}
		value, err := s.Summarize(context.Background(), "same source")
		want := "Bearer key-a"
		if p == b {
			want = "Bearer key-b"
		}
		if err != nil || value != want {
			t.Fatal(value, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("providers shared or missed their persistent cache: %d requests", calls.Load())
	}
	if err := s.DeleteKey(a); err != nil {
		t.Fatal(err)
	}
	if key, _ := store.Get(b); key != "key-b" {
		t.Fatal("deleting one provider affected another")
	}
	if key, _ := store.Get(Custom); key != "legacy-key" {
		t.Fatal("legacy credential account changed")
	}
	pc.Name = "Draft provider"
	draft, err := NewForProvider(b, pc, "draft-key", store)
	if err != nil {
		t.Fatal(err)
	}
	value, err := draft.Summarize(context.Background(), "different source")
	if err != nil || value != "Bearer draft-key" {
		t.Fatal(value, err)
	}
	if key, _ := store.Get(b); key != "key-b" {
		t.Fatal("draft diagnostic persisted its key")
	}
}

func TestLegacyConfigMigrationAndDynamicPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ai.json")
	legacy := `{"active_provider":"magpie","custom":{"enabled":true,"base_url":"https://api.example.org/v1","model":"old","timeout_seconds":90,"language":"中文"},"magpie":{"enabled":false,"base_url":"http://127.0.0.1:3425/v1","model":"local","timeout_seconds":60,"language":"中文"}}`
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Custom.Model != "old" || c.Magpie.Model != "local" || !c.Custom.Stream || !c.Magpie.Stream || c.BilingualStyle != "soft" {
		t.Fatal("legacy settings or defaults lost", c)
	}
	id := Provider("provider-" + strings.Repeat("c", 32))
	pc := c.Custom
	pc.Name = "远程工作服务"
	pc.Stream = false
	pc.SummaryPrompt = "三点摘要 {language}"
	pc.TranslationPrompt = "按新闻风格翻译为 {language}"
	c.Providers = []NamedProvider{{ID: id, Config: pc}}
	c.ActiveProvider = id
	if err := SaveConfig(path, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil || !loaded.Equal(c) {
		t.Fatal("provider settings did not round-trip", err)
	}
	if !validProvider(id) || validProvider("provider-custom") || validProvider("../custom") {
		t.Fatal("invalid credential account accepted")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Providers = append(c.Providers, c.Providers[0]) },
		func(c *Config) { c.Providers[0].ID = Custom },
		func(c *Config) { c.Providers[0].Config.SummaryPrompt = strings.Repeat("x", 16001) },
		func(c *Config) { c.ActiveProvider = "missing" },
	} {
		bad := c.Clone()
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid provider configuration accepted")
		}
	}
}

func TestCustomPromptsApplyAndInvalidateDocumentCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload completion
		json.NewDecoder(r.Body).Decode(&payload)
		system := payload.Messages[0].Content
		if !strings.Contains(system, "中文") || strings.Contains(system, "{language}") {
			t.Error("language placeholder not expanded")
		}
		if payload.ResponseFormat == nil {
			if !strings.Contains(system, "三句话 article") {
				t.Error("summary prompt missing", system)
			}
			reply(w, "自定义摘要")
			return
		}
		if !strings.Contains(system, "专业术语") || !strings.Contains(system, "Return every id exactly once") {
			t.Error("translation style or format contract missing")
		}
		var data struct {
			Spans []TranslationSpan `json:"spans"`
		}
		json.Unmarshal([]byte(payload.Messages[1].Content), &data)
		word := "译文甲"
		if strings.Contains(system, "第二版") {
			word = "译文乙"
		}
		for i := range data.Spans {
			data.Spans[i].Text = word
		}
		raw, _ := json.Marshal(data)
		reply(w, string(raw))
	}))
	defer server.Close()
	s := testService(t, server.URL)
	c := s.Config()
	c.Custom.Language = "中文"
	c.Custom.SummaryPrompt = "三句话 {kind} {language}"
	c.Custom.TranslationPrompt = "专业术语翻译为 {language}"
	if err := s.UpdateConfig(c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summarize(context.Background(), "source"); err != nil {
		t.Fatal(err)
	}
	first, err := s.TranslateMarkdown(context.Background(), "Hello", TranslationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	c.Custom.TranslationPrompt += " 第二版"
	if err := s.UpdateConfig(c); err != nil {
		t.Fatal(err)
	}
	second, err := s.TranslateMarkdown(context.Background(), "Hello", TranslationOptions{Resume: &first})
	if err != nil || second.Markdown == first.Markdown || calls.Load() != 3 {
		t.Fatal("custom prompt reused old document cache", second.Markdown, err, fmt.Sprint(calls.Load()))
	}
}
