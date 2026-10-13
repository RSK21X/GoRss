package aiservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranslationConnectionReuse(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reply(w, "译文") }))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	s := testService(t, server.URL)
	for _, text := range []string{"first", "second", "third"} {
		if _, err := s.TranslateSelection(context.Background(), text); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("connections = %d, want one reusable connection", connections.Load())
	}
}

func TestTranslationFormatRejectionOncePerDocument(t *testing.T) {
	var rejects, calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload completion
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if payload.ResponseFormat != nil {
			rejects.Add(1)
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"param":"response_format","code":"unsupported_parameter","message":"response_format not supported"}}`)
			return
		}
		var data struct {
			Spans []TranslationSpan `json:"spans"`
		}
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &data); err != nil {
			t.Error(err)
			return
		}
		for i := range data.Spans {
			data.Spans[i].Text = "译文"
		}
		b, _ := json.Marshal(data)
		reply(w, string(b))
	}))
	defer server.Close()
	s := testService(t, server.URL)
	result, err := s.TranslateMarkdown(context.Background(), strings.Repeat("A sentence. ", 500), TranslationOptions{BatchBytes: 1200})
	if err != nil || result.Completed != result.Total {
		t.Fatal(result.Completed, result.Total, err)
	}
	if calls.Load() < 3 || rejects.Load() != 1 {
		t.Fatalf("calls=%d rejects=%d", calls.Load(), rejects.Load())
	}
	// A new task must independently check capability, even on the same service.
	_, err = s.TranslateMarkdown(context.Background(), "Different source", TranslationOptions{})
	if err != nil || rejects.Load() != 2 {
		t.Fatal(err, rejects.Load())
	}
}

func TestSelectionDoesNotWaitForFullTranslation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload completion
		json.NewDecoder(r.Body).Decode(&payload)
		if payload.ResponseFormat != nil {
			close(entered)
			<-release
			var data struct {
				Spans []TranslationSpan `json:"spans"`
			}
			json.Unmarshal([]byte(payload.Messages[1].Content), &data)
			for i := range data.Spans {
				data.Spans[i].Text = "译文"
			}
			b, _ := json.Marshal(data)
			reply(w, string(b))
		} else {
			reply(w, "划词译文")
		}
	}))
	defer server.Close()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	s := testService(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := s.TranslateMarkdown(ctx, "Article", TranslationOptions{}); done <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	value, err := s.TranslateSelection(ctx, "word")
	if err != nil || value != "划词译文" {
		t.Fatal(value, err)
	}
	// Full translation remains blocked while selection has already completed.
	select {
	case err := <-done:
		t.Fatalf("article unexpectedly completed: %v", err)
	default:
	}
	unblock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestSelectionBudgetAndCache(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&payload)
		var budget int
		json.Unmarshal(payload["max_tokens"], &budget)
		if budget < 512 || budget >= 4096 {
			t.Errorf("short selection budget = %d", budget)
		}
		if string(payload["stream"]) != "false" || payload["response_format"] != nil || payload["reasoning_effort"] != nil {
			t.Error("unexpected protocol parameters")
		}
		reply(w, "译文")
	}))
	defer server.Close()
	s := testService(t, server.URL)
	for i := 0; i < 2; i++ {
		if _, err := s.TranslateSelection(context.Background(), "Hello world"); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cache bypass: %d calls", calls.Load())
	}
	if translationBudget(strings.Repeat("中", 4000), 0) != 8192 {
		t.Fatal("long selection lacks headroom")
	}
}
