package aiservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

func translateReply(w http.ResponseWriter, r *http.Request, mutate func(string) string) {
	var c completion
	json.NewDecoder(r.Body).Decode(&c)
	var data struct {
		Spans []TranslationSpan `json:"spans"`
	}
	json.Unmarshal([]byte(c.Messages[1].Content), &data)
	for i := range data.Spans {
		data.Spans[i].Text = mutate(data.Spans[i].Text)
	}
	b, _ := json.Marshal(data)
	reply(w, string(b))
}
func TestMarkdownPreservation(t *testing.T) {
	src := "# Heading\n\n> Quote **bold** and [label](https://example.com/a \"title\").\n\n- Item with `inline code` ![image alt](https://example.com/image.png)\n\n```go\nfmt.Println(\"unchanged\")\n```\n\n<div>raw html</div>\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateReply(w, r, func(s string) string { return "译" + s })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	res, e := s.TranslateMarkdown(context.Background(), src, TranslationOptions{})
	if e != nil {
		t.Fatal(e)
	}
	for _, keep := range []string{"# ", "> ", "**", "[", " ]", "(https://example.com/a \"title\")", "`inline code`", "![image alt](https://example.com/image.png)", "```go\nfmt.Println(\"unchanged\")\n```", "<div>raw html</div>"} {
		if keep == " ]" {
			continue
		}
		if !strings.Contains(res.Markdown, keep) {
			t.Errorf("lost %q in %s", keep, res.Markdown)
		}
	}
	if res.Completed != res.Total || res.Total == 0 || !strings.Contains(res.Markdown, "译") {
		t.Fatal(res)
	}
	for _, sp := range res.Spans {
		if strings.Contains(sp.Text, "inline code") || strings.Contains(sp.Text, "image alt") || strings.Contains(sp.Text, "unchanged") || strings.Contains(sp.Text, "raw html") {
			t.Fatal("protected content sent", sp)
		}
	}
}
func TestTranslationInjectionEscaped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateReply(w, r, func(s string) string { return "\n# H\n![x](https://evil) <script> &copy; `code`" })
	}))
	defer server.Close()
	res, e := testService(t, server.URL).TranslateMarkdown(context.Background(), "Hello", TranslationOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(res.Markdown, "\n") || strings.Contains(res.Markdown, "![x]") || strings.Contains(res.Markdown, "<script>") {
		t.Fatal(res.Markdown)
	}
}
func TestTranslationPartialRetry(t *testing.T) {
	var calls atomic.Int32
	var fail atomic.Bool
	fail.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 2 && fail.Load() {
			w.WriteHeader(503)
			return
		}
		translateReply(w, r, func(s string) string { return "translated" })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	src := strings.Repeat("Sentence with several words. ", 160)
	var progress []int
	opts := TranslationOptions{BatchBytes: 1200, OnProgress: func(p TranslationProgress) { progress = append(progress, p.Completed) }}
	partial, e := s.TranslateMarkdown(context.Background(), src, opts)
	if e == nil || partial.Completed == 0 || partial.Completed == partial.Total {
		t.Fatal(partial, e)
	}
	completed := partial.Completed
	fail.Store(false)
	opts.Resume = &partial
	s, e = NewWithCacheDir(s.Config(), s.secrets, s.cacheDir)
	if e != nil {
		t.Fatal(e)
	}
	res, e := s.TranslateMarkdown(context.Background(), src, opts)
	if e != nil || res.Completed != res.Total {
		t.Fatal(res, e)
	}
	if partial.Completed != completed {
		t.Fatal("resume mutated")
	}
	if len(progress) < 3 {
		t.Fatal(progress)
	}
}
func TestStableIDsAndValidation(t *testing.T) {
	spans := extractSpans("Hello **world** [label](url)")
	again := extractSpans("Hello **world** [label](url)")
	if fmt.Sprint(spans) != fmt.Sprint(again) {
		t.Fatal("unstable")
	}
	for _, raw := range []string{`{"spans":[]}`, `{"spans":[{"id":"fake","text":"x"}]}`, `{"spans":[]} garbage`, `{"spans":[],"extra":true}`} {
		if _, e := parseTranslation(raw, spans); !errors.Is(e, ErrTranslation) {
			t.Fatal(raw, e)
		}
	}
	one := spans[:1]
	raw := fmt.Sprintf(`{"spans":[{"id":%q,"text":"a"},{"id":%q,"text":"b"}]}`, one[0].ID, one[0].ID)
	if _, e := parseTranslation(raw, append(one, one[0])); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestSentenceSplitting(t *testing.T) {
	src := strings.Repeat("中文句子。Hello world! ", 300)
	rs := sentenceRanges(src, 1200)
	var rebuilt strings.Builder
	for _, r := range rs {
		piece := src[r[0]:r[1]]
		if len(piece) > 1200 || !utf8.ValidString(piece) {
			t.Fatal("bad boundary")
		}
		rebuilt.WriteString(piece)
	}
	if rebuilt.String() != src {
		t.Fatal("text lost")
	}
}

func TestMalformedTranslationCanRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			reply(w, `{"spans":[]}`)
			return
		}
		translateReply(w, r, func(s string) string { return "translation" })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	partial, e := s.TranslateMarkdown(context.Background(), "Hello", TranslationOptions{})
	if !errors.Is(e, ErrTranslation) {
		t.Fatal(e)
	}
	res, e := s.TranslateMarkdown(context.Background(), "Hello", TranslationOptions{Resume: &partial})
	if e != nil || res.Completed != 1 || calls.Load() != 2 {
		t.Fatal(res, e, calls.Load())
	}
}
func TestResumeIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateReply(w, r, func(s string) string { return "translated" })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	first, e := s.TranslateMarkdown(context.Background(), "Original", TranslationOptions{})
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.TranslateMarkdown(context.Background(), "Other", TranslationOptions{Resume: &first})
	if e != nil || second.Spans[0].Text != "Other" {
		t.Fatal(second, e)
	}
}

func TestComparisonSegments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translateReply(w, r, func(s string) string { return "译文" })
	}))
	defer server.Close()
	s := testService(t, server.URL)
	res, e := s.TranslateMarkdown(context.Background(), "# Title\n\nText **bold**.", TranslationOptions{OnProgress: func(p TranslationProgress) {
		if p.Completed > 0 && len(p.Segments) > 0 {
			p.Segments[0].Translation = "mutated"
		}
	}})
	if e != nil {
		t.Fatal(e)
	}
	for _, segment := range res.Segments {
		if !segment.Complete || segment.Translation != "译文" || segment.Text == "" {
			t.Fatal(segment)
		}
	}
}
