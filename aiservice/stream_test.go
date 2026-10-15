package aiservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func streamEvent(w http.ResponseWriter, text, finish string) {
	data, _ := json.Marshal(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": text}, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func TestSummaryStreamsBeforeCompletionAndCachesOnlyFinal(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload completion
		if json.NewDecoder(r.Body).Decode(&payload) != nil || !payload.Stream {
			t.Error("missing stream request")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		streamEvent(w, "第一段", "")
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, ": heartbeat\n\nevent: message\n")
		streamEvent(w, "🙂 完整摘要", "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	defer unblock()
	s := testService(t, server.URL)
	partial := make(chan string, 8)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		v, err := s.SummarizeStream(ctx, "source", func(text string) { partial <- text })
		if err == nil && v != "第一段🙂 完整摘要" {
			err = fmt.Errorf("unexpected result %q", v)
		}
		done <- err
	}()
	select {
	case text := <-partial:
		if text != "第一段" {
			t.Fatal(text)
		}
	case <-ctx.Done():
		t.Fatal("no text before final response")
	}
	select {
	case <-done:
		t.Fatal("stream completed while server was blocked")
	default:
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	restarted, err := NewWithCacheDir(s.Config(), s.secrets, s.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	var hit string
	v, err := restarted.SummarizeStream(ctx, "source", func(text string) { hit = text })
	if err != nil || v != "第一段🙂 完整摘要" || hit != v || calls.Load() != 1 {
		t.Fatalf("stream cache: %q %q %v calls=%d", v, hit, err, calls.Load())
	}
}

func TestStreamCancellationDoesNotCachePartialOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			streamEvent(w, "partial", "")
			<-r.Context().Done()
			return
		}
		streamEvent(w, "complete", "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	s := testService(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := s.SummarizeStream(ctx, "same source", func(string) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	v, err := s.SummarizeStream(context.Background(), "same source", func(string) {})
	if err != nil || v != "complete" || calls.Load() != 2 {
		t.Fatalf("partial stream reused: %q %v calls=%d", v, err, calls.Load())
	}
}

func TestStreamRejectsIncompleteInvalidAndReasoningOnly(t *testing.T) {
	chunk := func(content, finish string) string {
		data, _ := json.Marshal(map[string]any{"object": "chat.completion.chunk", "choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": finish}}})
		return "data: " + string(data) + "\n\n"
	}
	for _, test := range []struct {
		name, wire string
		err        error
	}{
		{"incomplete", chunk("partial", "") + "data: [DONE]\n\n", ErrProtocol},
		{"disconnect", chunk("partial", ""), ErrProtocol},
		{"length", chunk("partial", "length"), ErrOutputLength},
		{"tool call", chunk("", "tool_calls"), ErrUnsupportedProtocol},
		{"reasoning only", `data: {"choices":[{"delta":{"reasoning_content":"private chain"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n", ErrEmptyContent},
		{"invalid JSON", "data: {invalid}\n\n", ErrInvalidJSON},
		{"remote error", `data: {"error":{"message":"secret remote text"}}` + "\n\n", ErrProtocol},
		{"wrong object", `data: {"object":"response.output_text.delta","choices":[]}` + "\n\n", ErrUnsupportedProtocol},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readChatStream(context.Background(), strings.NewReader(test.wire), func(string) {})
			if !errors.Is(err, test.err) || strings.Contains(err.Error(), "private chain") || strings.Contains(err.Error(), "secret remote text") {
				t.Fatal(err)
			}
		})
	}
	wire := "\ufeff: heartbeat\n\n" + chunk("多行🙂", "stop") + "data: [DONE]\n\n"
	value, err := readChatStream(context.Background(), strings.NewReader(wire), nil)
	if err != nil || value != "多行🙂" {
		t.Fatal(value, err)
	}
}

func TestExplicitUnsupportedStreamFallsBackOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload completion
		json.NewDecoder(r.Body).Decode(&payload)
		if calls.Add(1) == 1 {
			if !payload.Stream {
				t.Error("first request was not streaming")
			}
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"param":"stream","code":"unsupported_parameter","message":"stream not supported"}}`)
			return
		}
		if payload.Stream {
			t.Error("fallback still streamed")
		}
		reply(w, "JSON fallback")
	}))
	defer server.Close()
	var shown string
	v, err := testService(t, server.URL).SummarizeStream(context.Background(), "source", func(s string) { shown = s })
	if err != nil || v != "JSON fallback" || shown != v || calls.Load() != 2 {
		t.Fatal(v, shown, err, calls.Load())
	}
}

func TestTranslationStreamsPreviewAndValidatesFinal(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			var calls atomic.Int32
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var payload completion
				json.NewDecoder(r.Body).Decode(&payload)
				if !payload.Stream {
					t.Error("translation did not stream")
				}
				var data struct {
					Spans []TranslationSpan `json:"spans"`
				}
				json.Unmarshal([]byte(payload.Messages[1].Content), &data)
				w.Header().Set("Content-Type", "text/event-stream")
				prefix := fmt.Sprintf(`{"spans":[{"id":%q,"text":"逐步`, data.Spans[0].ID)
				streamEvent(w, prefix, "")
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				tail := `翻译🙂"}]}`
				if invalid {
					tail = `翻译🙂"},{"id":"extra","text":"bad"}]}`
				}
				streamEvent(w, tail, "stop")
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			defer unblock()
			s := testService(t, server.URL)
			preview := make(chan TranslationProgress, 1)
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			go func() {
				result, err := s.TranslateMarkdown(ctx, "Hello", TranslationOptions{Stream: true, OnProgress: func(p TranslationProgress) {
					if p.Preview {
						select {
						case preview <- p:
						default:
						}
					}
				}})
				if invalid && result.Completed != 0 {
					t.Error("invalid preview counted as complete")
				}
				if !invalid && result.Completed != 1 {
					t.Error("translation did not complete")
				}
				done <- err
			}()
			select {
			case p := <-preview:
				if !strings.Contains(p.Markdown, "逐步") || p.Completed != 0 {
					t.Fatal(p)
				}
			case <-ctx.Done():
				t.Fatal("no translation preview before final response")
			}
			unblock()
			err := <-done
			if invalid && !errors.Is(err, ErrTranslation) {
				t.Fatal(err)
			}
			if !invalid && err != nil {
				t.Fatal(err)
			}
			if invalid {
				if len(s.cache) != 0 {
					t.Fatal("invalid span response was cached")
				}
			}
		})
	}
}

func TestTranslationPreviewHandlesEscapesAndRejectsUnknownIDs(t *testing.T) {
	batch := []TranslationSpan{{ID: "one", Text: "source"}}
	for _, test := range []struct{ raw, want string }{
		{`{"spans":[{"id":"one","text":"中文\n\uD83D`, "中文\n"},
		{`{"spans":[{"id":"one","text":"中文\n\uD83D\uDE42`, "中文\n🙂"},
		{`{"spans":[{"id":"unknown","text":"other`, ""},
	} {
		if got := previewTranslations(test.raw, batch)["one"]; got != test.want {
			t.Fatalf("preview %q, want %q", got, test.want)
		}
	}
}

func TestStreamSwitchAndServersReturningJSON(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload completion
				json.NewDecoder(r.Body).Decode(&payload)
				if payload.Stream != enabled {
					t.Error("stream preference ignored")
				}
				reply(w, "complete JSON")
			}))
			defer server.Close()
			s := testService(t, server.URL)
			c := s.Config()
			c.Custom.Stream = enabled
			s.UpdateConfig(c)
			var shown string
			value, err := s.SummarizeStream(context.Background(), "source", func(text string) { shown = text })
			if err != nil || value != "complete JSON" || shown != value {
				t.Fatal(value, shown, err)
			}
		})
	}
}

func TestConfigChangeDuringStreamRejectsStaleOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		streamEvent(w, "first", "")
		streamEvent(w, " final", "stop")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	s := testService(t, server.URL)
	changes := 0
	_, err := s.SummarizeStream(context.Background(), "source", func(string) {
		changes++
		c := s.Config()
		c.Custom.SummaryPrompt = "Changed prompt"
		if err := s.UpdateConfig(c); err != nil {
			t.Error(err)
		}
	})
	if !errors.Is(err, ErrStale) || changes != 1 || len(s.cache) != 0 {
		t.Fatal("stale stream published or cached", err, changes)
	}
}

func TestUnsupportedStreamIsRememberedAcrossTranslationBatches(t *testing.T) {
	var rejected, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload completion
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.Stream {
			rejected.Add(1)
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"param":"stream","code":"unsupported_parameter","message":"not supported"}}`)
			return
		}
		var data struct {
			Spans []TranslationSpan `json:"spans"`
		}
		json.Unmarshal([]byte(payload.Messages[1].Content), &data)
		for i := range data.Spans {
			data.Spans[i].Text = "译文"
		}
		raw, _ := json.Marshal(data)
		reply(w, string(raw))
	}))
	defer server.Close()
	result, err := testService(t, server.URL).TranslateMarkdown(context.Background(), strings.Repeat("A sentence with some words. ", 200), TranslationOptions{BatchBytes: 1200, Stream: true, OnProgress: func(TranslationProgress) {}})
	if err != nil || result.Completed != result.Total || calls.Load() < 3 || rejected.Load() != 1 {
		t.Fatal("stream rejection repeated across batches", err, calls.Load(), rejected.Load())
	}
}
