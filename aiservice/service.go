package aiservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const PromptVersion = "gorss-ai-2"

type Service struct {
	mu           sync.Mutex
	config       Config
	secrets      SecretStore
	revision     uint64
	cache        map[[32]byte]string
	cacheDir     string
	salt         []byte
	forceRefresh bool
}

func New(c Config, store SecretStore) (*Service, error) {
	return NewWithCacheDir(c, store, defaultCacheDir())
}
func newService(c Config, store SecretStore) (*Service, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if store == nil {
		store = NewKeychainStore()
	}
	return &Service{config: c.Clone(), secrets: store, cache: make(map[[32]byte]string)}, nil
}

// NewForProvider isolates draft diagnostics from saved configuration and disk cache.
// A nonempty draft key overrides Keychain for this operation only.
func NewForProvider(p Provider, pc ProviderConfig, key string, fallback SecretStore) (*Service, error) {
	c := DefaultConfig()
	c.ActiveProvider = p
	if p == Custom {
		c.Custom = pc
	} else if p == Magpie {
		c.Magpie = pc
	} else if validProvider(p) {
		c.Providers = []NamedProvider{{ID: p, Config: pc}}
	} else {
		return nil, ErrConfig
	}
	if fallback == nil {
		fallback = NewKeychainStore()
	}
	if key != "" {
		fallback = draftSecrets{p, key, fallback}
	}
	return newService(c, fallback)
}

type draftSecrets struct {
	provider Provider
	key      string
	fallback SecretStore
}

func (d draftSecrets) Get(p Provider) (string, error) {
	if p == d.provider {
		return d.key, nil
	}
	return d.fallback.Get(p)
}
func (d draftSecrets) Set(p Provider, key string) error { return d.fallback.Set(p, key) }
func (d draftSecrets) Delete(p Provider) error          { return d.fallback.Delete(p) }

func (s *Service) Config() Config { s.mu.Lock(); defer s.mu.Unlock(); return s.config.Clone() }
func (s *Service) UpdateConfig(c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.config.Equal(c) {
		s.revision++
		clear(s.cache)
	}
	s.config = c.Clone()
	return nil
}
func (s *Service) SetKey(p Provider, key string) error {
	if !validProvider(p) || strings.ContainsAny(key, "\r\n") || len(key) > 16384 {
		return ErrConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.secrets.Set(p, key); e != nil {
		return ErrSecretStore
	}
	s.revision++
	clear(s.cache)
	s.invalidateDiskCredentials()
	return nil
}
func (s *Service) DeleteKey(p Provider) error {
	if !validProvider(p) {
		return ErrConfig
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.secrets.Delete(p); e != nil && !errors.Is(e, ErrSecretNotFound) {
		return ErrSecretStore
	}
	s.revision++
	clear(s.cache)
	s.invalidateDiskCredentials()
	return nil
}

type session struct {
	provider   Provider
	config     ProviderConfig
	key        string
	revision   uint64
	credential string
	// Shared only by requests in this snapshot, never across providers or tasks.
	formatUnsupported *atomic.Bool
	streamUnsupported *atomic.Bool
}

func (s *Service) snapshot(p Provider, enabled bool) (session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p == "" {
		p = s.config.ActiveProvider
	}
	c, e := s.config.Provider(p)
	if e != nil {
		return session{}, e
	}
	if enabled && !c.Enabled {
		return session{}, ErrDisabled
	}
	if enabled && strings.TrimSpace(c.Model) == "" {
		return session{}, fmt.Errorf("%w：请填写或选择模型 ID", ErrConfig)
	}
	if enabled && strings.TrimSpace(c.Language) == "" {
		return session{}, fmt.Errorf("%w：请填写目标语言", ErrConfig)
	}
	key, e := s.secrets.Get(p)
	if e != nil && !errors.Is(e, ErrSecretNotFound) {
		return session{}, ErrSecretStore
	}
	if key == "" && p == Magpie {
		u, _ := validateBase(c.BaseURL)
		if u.Scheme == "http" {
			key = "magpie-gorss"
		}
	}
	if strings.ContainsAny(key, "\r\n") || len(key) > 16384 {
		return session{}, ErrSecretStore
	}
	return session{provider: p, config: c, key: key, revision: s.revision, credential: s.credentialEpoch(p, key), formatUnsupported: new(atomic.Bool), streamUnsupported: new(atomic.Bool)}, nil
}

// HTTPError exposes only the HTTP status; remote error text is never returned.
type HTTPError struct {
	StatusCode        int
	unsupportedFormat bool // only a sanitized, explicit parameter rejection
	unsupportedStream bool
}

func (e *HTTPError) Error() string { return fmt.Sprintf("AI 服务返回 HTTP %d", e.StatusCode) }
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return strings.ToLower(u.Scheme+"://"+u.Hostname()) + ":" + port
}

// Transport pools are safe for concurrent requests. Keep direct networking and
// the existing per-request redirect/origin checks; do not share client headers.
var directTransport = func() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return transport
}()

func request(ctx context.Context, x session, path string, payload any, out any, timeout time.Duration) error {
	return requestResponse(ctx, x, path, payload, timeout, func(ctx context.Context, resp *http.Response) error {
		data, err := readResponse(ctx, resp)
		if err != nil {
			return err
		}
		if json.Unmarshal(data, out) != nil {
			return ErrInvalidJSON
		}
		return nil
	})
}
func readResponse(ctx context.Context, resp *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrNetwork
	}
	if len(data) > 4*1024*1024 {
		return nil, ErrProtocol
	}
	return data, nil
}

func requestResponse(ctx context.Context, x session, path string, payload any, timeout time.Duration, consume func(context.Context, *http.Response) error) error {
	base, e := validateBase(x.config.BaseURL)
	if e != nil {
		return e
	}
	target := *base
	target.Path += "/" + path
	target.RawPath = ""
	var body io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return ErrProtocol
		}
		body = strings.NewReader(string(b))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, map[bool]string{true: "POST", false: "GET"}[payload != nil], target.String(), body)
	if e != nil {
		return ErrConfig
	}
	req.Header.Set("User-Agent", "GoRss/1")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if x.key != "" {
		req.Header.Set("Authorization", "Bearer "+x.key)
	}
	client := http.Client{Transport: directTransport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return ErrNetwork
		}
		if _, e := validateBase(r.URL.String()); e != nil {
			return ErrNetwork
		}
		if origin(r.URL) != origin(base) {
			return ErrNetwork
		}
		return nil
	}}
	resp, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrNetwork
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Inspect only a bounded error to classify an explicit format rejection.
		// Never expose or log remote messages, credentials or model output.
		var remote struct {
			Error struct{ Param, Code, Message string }
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
		_ = json.Unmarshal(b, &remote)
		msg := strings.ToLower(remote.Error.Message)
		unsupported := (resp.StatusCode == 400 || resp.StatusCode == 422) &&
			(remote.Error.Param == "response_format" || strings.Contains(msg, "response_format")) &&
			(strings.Contains(strings.ToLower(remote.Error.Code), "unsupported") || strings.Contains(msg, "not supported") || strings.Contains(msg, "does not support") || strings.Contains(msg, "unsupported"))
		unsupportedStream := (resp.StatusCode == 400 || resp.StatusCode == 422) && remote.Error.Param == "stream" &&
			(strings.Contains(strings.ToLower(remote.Error.Code), "unsupported") || strings.Contains(msg, "unsupported") || strings.Contains(msg, "not supported") || strings.Contains(msg, "does not support"))
		return &HTTPError{StatusCode: resp.StatusCode, unsupportedFormat: unsupported, unsupportedStream: unsupportedStream}
	}
	return consume(ctx, resp)
}

type Model struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name,omitempty"`
	OwnedBy     string `json:"owned_by,omitempty"`
}

func (s *Service) ListModels(ctx context.Context, p Provider) ([]Model, error) {
	x, e := s.snapshot(p, false)
	if e != nil {
		return nil, e
	}
	return listModels(ctx, x)
}

func listModels(ctx context.Context, x session) ([]Model, error) {
	var result struct {
		Data []Model `json:"data"`
	}
	if e := request(ctx, x, "models", nil, &result, 5*time.Second); e != nil {
		return nil, e
	}
	if result.Data == nil {
		return nil, ErrProtocol
	}
	seen := map[string]bool{}
	models := []Model{}
	for _, m := range result.Data {
		if m.ID == "" || len(m.ID) > 512 || strings.ContainsAny(m.ID, "\r\n") {
			return nil, ErrProtocol
		}
		if !seen[m.ID] {
			seen[m.ID] = true
			models = append(models, m)
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// DetectMagpie verifies the configured Magpie server's identity independently
// of its model catalog and enabled switch. A model list alone is not identity.
func (s *Service) DetectMagpie(ctx context.Context) error {
	x, e := s.snapshot(Magpie, false)
	if e != nil {
		return e
	}
	return detectMagpie(ctx, x)
}

func detectMagpie(ctx context.Context, x session) error {
	base, e := validateBase(x.config.BaseURL)
	if e != nil {
		return e
	}
	// The hello endpoint sits beside /v1, including under a reverse proxy prefix.
	base.Path = strings.TrimSuffix(base.Path, "/v1")
	base.RawPath = ""
	x.config.BaseURL = base.String()
	var hello struct {
		Name string `json:"name"`
	}
	if e := request(ctx, x, "api/hello", nil, &hello, 2*time.Second); e != nil {
		return e
	}
	if hello.Name != "magpie" {
		return ErrProtocol
	}
	return nil
}

// Probe verifies identity (for Magpie) and the models protocol without requiring
// the provider to be enabled. Both requests use the same configuration snapshot.
func (s *Service) Probe(ctx context.Context, p Provider) error {
	x, e := s.snapshot(p, false)
	if e != nil {
		return e
	}
	if x.provider == Magpie {
		if e := detectMagpie(ctx, x); e != nil {
			return e
		}
	}
	_, e = listModels(ctx, x)
	return e
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type completion struct {
	Model          string          `json:"model"`
	Messages       []message       `json:"messages"`
	MaxTokens      int             `json:"max_tokens"`
	Stream         bool            `json:"stream"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}
type responseFormat struct {
	Type string `json:"type"`
}

func chat(ctx context.Context, x session, system, user string, max int, timeout time.Duration) (string, error) {
	return chatWithFormat(ctx, x, system, user, max, timeout, false)
}
func chatWithFormat(ctx context.Context, x session, system, user string, max int, timeout time.Duration, structured bool) (string, error) {
	return chatProgress(ctx, x, system, user, max, timeout, structured, nil)
}
func chatProgress(ctx context.Context, x session, system, user string, max int, timeout time.Duration, structured bool, onDelta func(string)) (string, error) {
	if strings.TrimSpace(x.config.Model) == "" {
		return "", fmt.Errorf("%w：请填写或选择模型 ID", ErrConfig)
	}
	payload := completion{Model: x.config.Model, Messages: []message{{"system", system}, {"user", user}}, MaxTokens: max, Stream: onDelta != nil && x.config.Stream && (x.streamUnsupported == nil || !x.streamUnsupported.Load())}
	if structured && (x.formatUnsupported == nil || !x.formatUnsupported.Load()) {
		payload.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for attempt := 0; attempt < 3; attempt++ {
		value, err := requestCompletion(ctx, x, payload, timeout, onDelta)
		var remote *HTTPError
		if !errors.As(err, &remote) || ctx.Err() != nil {
			return value, err
		}
		if payload.ResponseFormat != nil && remote.unsupportedFormat {
			if x.formatUnsupported != nil {
				x.formatUnsupported.Store(true)
			}
			payload.ResponseFormat = nil
		} else if payload.Stream && remote.unsupportedStream {
			payload.Stream = false
			if x.streamUnsupported != nil {
				x.streamUnsupported.Store(true)
			}
		} else {
			return "", err
		}
	}
	return "", ErrProtocol
}

func completionText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ErrEmptyContent
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if strings.TrimSpace(value) == "" {
			return "", ErrEmptyContent
		}
		return value, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", ErrUnsupportedProtocol
	}
	var text strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "text":
			text.WriteString(part.Text)
		case "reasoning", "thinking": // never substitute reasoning for final output
		default:
			return "", ErrUnsupportedProtocol
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", ErrEmptyContent
	}
	return text.String(), nil
}

// TestModel makes a real, small completion, independent of the enabled switch.
func (s *Service) TestModel(ctx context.Context, p Provider) error {
	x, e := s.snapshot(p, false)
	if e != nil {
		return e
	}
	_, e = chat(ctx, x, "Reply briefly with a short final answer.", "Say hello in a few words.", 2048, time.Duration(x.config.TimeoutSeconds)*time.Second)
	return e
}
func (s *Service) cachedChat(ctx context.Context, x session, kind, content, system string, max int) (string, error) {
	return s.cachedValidatedChat(ctx, x, kind, content, system, max, nil)
}
func (s *Service) cachedValidatedChat(ctx context.Context, x session, kind, content, system string, max int, validate func(string) error) (string, error) {
	return s.cachedChatProgress(ctx, x, kind, content, system, max, validate, nil)
}
func (s *Service) cachedChatProgress(ctx context.Context, x session, kind, content, system string, max int, validate func(string) error, onDelta func(string)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key := s.cacheIdentity(x, kind, content, system, max)
	s.mu.Lock()
	hit, ok := s.cache[key]
	force := s.forceRefresh
	s.mu.Unlock()
	if !force && !ok {
		hit, ok = s.diskGet(key)
	}
	if !force && ok && (validate == nil || validate(hit) == nil) {
		if e := s.current(x); e != nil {
			return "", e
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if onDelta != nil {
			onDelta(hit)
		}
		return hit, nil
	}
	emit := onDelta
	if onDelta != nil {
		emit = func(value string) {
			s.mu.Lock()
			current := s.revision == x.revision
			s.mu.Unlock()
			if ctx.Err() == nil && current {
				onDelta(value)
			}
		}
	}
	v, e := chatProgress(ctx, x, system, content, max, time.Duration(x.config.TimeoutSeconds)*time.Second, kind == "translation", emit)
	if e != nil {
		return "", e
	}
	if validate != nil {
		if e := validate(v); e != nil {
			return "", e
		}
	}
	if e := ctx.Err(); e != nil {
		return "", e
	}
	if e := s.current(x); e != nil {
		return "", e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != x.revision {
		return "", ErrStale
	}
	if len(s.cache) >= 256 {
		clear(s.cache)
	}
	s.cache[key] = v
	s.diskPut(key, v)
	return v, nil
}
func (s *Service) Summarize(ctx context.Context, markdown string) (string, error) {
	return s.summary(ctx, markdown, "article", nil)
}
func (s *Service) SummarizeSelection(ctx context.Context, selection string) (string, error) {
	return s.summary(ctx, selection, "selection", nil)
}
func (s *Service) SummarizeStream(ctx context.Context, text string, onDelta func(string)) (string, error) {
	return s.summary(ctx, text, "article", onDelta)
}
func (s *Service) summary(ctx context.Context, text, kind string, onDelta func(string)) (string, error) {
	if strings.TrimSpace(text) == "" || len(text) > 1024*1024 {
		return "", ErrConfig
	}
	x, e := s.snapshot("", true)
	if e != nil {
		return "", e
	}
	return s.cachedChatProgress(ctx, x, "summary-"+kind, text, summarySystem(x.config, kind), 1024, nil, onDelta)
}

// translationBudget allows two tokens per source rune plus fixed/schema headroom.
// It is a bounded output allowance, not a token estimate or a speed guarantee;
// truncated output still fails explicitly without an automatic paid retry.
func translationBudget(text string, spans int) int {
	return min(8192, max(512, 512+2*utf8.RuneCountInString(text)+48*spans))
}

// TranslateSelection translates selected plain text without Markdown rewriting.
func (s *Service) TranslateSelection(ctx context.Context, selection string) (string, error) {
	return s.TranslateSelectionStream(ctx, selection, nil)
}
func (s *Service) TranslateSelectionStream(ctx context.Context, selection string, onDelta func(string)) (string, error) {
	if strings.TrimSpace(selection) == "" || len(selection) > 65536 {
		return "", ErrConfig
	}
	x, e := s.snapshot("", true)
	if e != nil {
		return "", e
	}
	return s.cachedChatProgress(ctx, x, "translation-selection", selection, translationSystem(x.config, false), translationBudget(selection, 0), nil, onDelta)
}

// CredentialsChanged invalidates session caches after credentials are changed
// directly through the SecretStore. Prefer SetKey/DeleteKey for UI changes.
func (s *Service) CredentialsChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	clear(s.cache)
	s.invalidateDiskCredentials()
}

// Re-read Keychain before returning results, including cache hits. External
// credential changes must not publish a completion from the previous account.
var ErrStale = errors.New("AI 设置或凭据已变化，请重试")

func (s *Service) current(x session) error {
	now, err := s.snapshot(x.provider, true)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != x.revision || now.revision != x.revision || now.key != x.key || now.credential != x.credential || now.config != x.config || s.config.ActiveProvider != x.provider {
		return ErrStale
	}
	return nil
}
