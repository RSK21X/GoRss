package aiservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// TranslationSpan identifies a source text span with byte offsets in the input.
// All Markdown syntax and excluded AST nodes remain byte-for-byte unchanged.
type TranslationSpan struct {
	ID    string `json:"id"`
	Text  string `json:"text"`
	Start int    `json:"-"`
	End   int    `json:"-"`
}

// TranslationSegment is a plain-text pair for a comparison view. Render Text
// and Translation as text, never as raw HTML or Markdown.
type TranslationSegment struct {
	ID          string
	Text        string
	Translation string
	Start       int
	End         int
	Complete    bool
}
type TranslationOptions struct {
	Resume     *TranslationResult
	OnProgress func(TranslationProgress)
	BatchBytes int
	Stream     bool
}
type TranslationProgress struct {
	Completed int
	Total     int
	Markdown  string
	Segments  []TranslationSegment
	Preview   bool
}
type TranslationResult struct {
	Markdown    string
	Spans       []TranslationSpan
	Segments    []TranslationSegment
	Completed   int
	Total       int
	fingerprint [32]byte
	translated  map[string]string
}

func excluded(n ast.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		switch p.(type) {
		case *ast.CodeSpan, *ast.Image, *ast.AutoLink, *ast.RawHTML, *ast.HTMLBlock, *ast.FencedCodeBlock, *ast.CodeBlock:
			return true
		}
	}
	return false
}

// sentenceRanges partitions long text at sentence/word boundaries, never inside
// a UTF-8 rune. A single exceptionally long word is split at a rune boundary.
func sentenceRanges(s string, limit int) [][2]int {
	var out [][2]int
	start := 0
	for start < len(s) {
		end := start + limit
		if end >= len(s) {
			out = append(out, [2]int{start, len(s)})
			break
		}
		for end > start && !utf8.RuneStart(s[end]) {
			end--
		}
		best := 0
		for i, r := range s[start:end] {
			if strings.ContainsRune(".!?。！？\n", r) {
				best = start + i + utf8.RuneLen(r)
			}
		}
		if best == 0 {
			for i, r := range s[start:end] {
				if r == ' ' || r == '\t' {
					best = start + i + utf8.RuneLen(r)
				}
			}
		}
		if best > start {
			end = best
		}
		out = append(out, [2]int{start, end})
		start = end
	}
	return out
}
func extractSpans(markdown string) []TranslationSpan {
	src := []byte(markdown)
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var spans []TranslationSpan
	ast.Walk(doc, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
		if !enter {
			return ast.WalkContinue, nil
		}
		t, ok := n.(*ast.Text)
		if !ok || excluded(n) {
			return ast.WalkContinue, nil
		}
		a, b := t.Segment.Start, t.Segment.Stop
		if a < 0 || b > len(src) || a >= b {
			return ast.WalkContinue, nil
		}
		raw := markdown[a:b]
		for _, r := range sentenceRanges(raw, 1200) {
			start, end := a+r[0], a+r[1]
			value := markdown[start:end]
			if strings.TrimSpace(value) == "" {
				continue
			}
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", start, end, value)))
			spans = append(spans, TranslationSpan{hex.EncodeToString(sum[:12]), decodeText(value), start, end})
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	return spans
}
func decodeText(raw string) string {
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\\' && i+1 < len(raw) && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(raw[i+1])) {
			i++
		}
		b.WriteByte(raw[i])
	}
	return html.UnescapeString(b.String())
}

// escapeText neutralizes every Markdown punctuation character and line break.
// Thus model output cannot introduce images, links, headings, HTML, or blocks.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString("&#10;")
		case r == '\r':
			b.WriteString("&#13;")
		case r == '\t':
			b.WriteString("&#9;")
		case r == ' ':
			b.WriteString("&#32;")
		case r < 32 || r == 127:
			continue
		case strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
func renderTranslation(src string, spans []TranslationSpan, values map[string]string) string {
	var b strings.Builder
	pos := 0
	for _, span := range spans {
		b.WriteString(src[pos:span.Start])
		if v, ok := values[span.ID]; ok {
			b.WriteString(escapeText(v))
		} else {
			b.WriteString(src[span.Start:span.End])
		}
		pos = span.End
	}
	b.WriteString(src[pos:])
	return b.String()
}
func parseTranslation(raw string, batch []TranslationSpan) (map[string]string, error) {
	var response struct {
		Spans []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"spans"`
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("%w：%w", ErrTranslation, ErrInvalidJSON)
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&response) != nil {
		return nil, ErrTranslation
	}
	var tail any
	if d.Decode(&tail) != io.EOF {
		return nil, fmt.Errorf("%w：%w", ErrTranslation, ErrInvalidJSON)
	}
	if len(response.Spans) != len(batch) {
		return nil, ErrTranslation
	}
	allowed := map[string]bool{}
	for _, sp := range batch {
		allowed[sp.ID] = true
	}
	values := map[string]string{}
	for _, sp := range response.Spans {
		if !allowed[sp.ID] || strings.TrimSpace(sp.Text) == "" || len(sp.Text) > 16000 || strings.ContainsRune(sp.Text, 0) {
			return nil, ErrTranslation
		}
		if _, ok := values[sp.ID]; ok {
			return nil, ErrTranslation
		}
		values[sp.ID] = sp.Text
	}
	return values, nil
}
func (s *Service) TranslateMarkdown(ctx context.Context, markdown string, opts TranslationOptions) (TranslationResult, error) {
	result := TranslationResult{Markdown: markdown}
	if len(markdown) > 1024*1024 || !utf8.ValidString(markdown) {
		return result, ErrConfig
	}
	x, e := s.snapshot("", true)
	if e != nil {
		return result, e
	}
	result.Spans = extractSpans(markdown)
	result.Total = len(result.Spans)
	result.translated = map[string]string{}
	system := translationSystem(x.config, true)
	result.fingerprint = s.cacheIdentity(x, "translation-document", markdown, system, 0)
	s.mu.Lock()
	force := s.forceRefresh
	s.mu.Unlock()
	if !force {
		if raw, ok := s.diskGet(result.fingerprint); ok {
			// Validate the entire map against spans reconstructed from the source.
			if values, err := parseTranslation(raw, result.Spans); err == nil {
				result.translated = values
			}
		}
	}
	if !force && opts.Resume != nil && opts.Resume.fingerprint == result.fingerprint {
		for id, v := range opts.Resume.translated {
			result.translated[id] = v
		}
	}
	refresh := func() {
		result.Completed = len(result.translated)
		result.Markdown = renderTranslation(markdown, result.Spans, result.translated)
		result.Segments = make([]TranslationSegment, 0, len(result.Spans))
		for _, span := range result.Spans {
			value, complete := result.translated[span.ID]
			result.Segments = append(result.Segments, TranslationSegment{span.ID, span.Text, value, span.Start, span.End, complete})
		}
		if opts.OnProgress != nil {
			opts.OnProgress(TranslationProgress{Completed: result.Completed, Total: result.Total, Markdown: result.Markdown, Segments: append([]TranslationSegment(nil), result.Segments...)})
		}
	}
	if e := s.current(x); e != nil {
		return result, e
	}
	refresh()
	limit := opts.BatchBytes
	if limit <= 0 {
		limit = 6000
	}
	if limit < 1200 || limit > 16000 {
		return result, ErrConfig
	}
	var pending []TranslationSpan
	for _, sp := range result.Spans {
		if _, ok := result.translated[sp.ID]; !ok {
			pending = append(pending, sp)
		}
	}
	for len(pending) > 0 {
		if e = ctx.Err(); e != nil {
			return result, e
		}
		n, size := 0, 0
		for n < len(pending) {
			cost := len(pending[n].Text) + len(pending[n].ID) + 64
			if n > 0 && size+cost > limit {
				break
			}
			size += cost
			n++
		}
		batch := pending[:n]
		var batchText strings.Builder
		for _, span := range batch {
			batchText.WriteString(span.Text)
		}
		payload, _ := json.Marshal(struct {
			Spans []TranslationSpan `json:"spans"`
		}{batch})
		var onDelta func(string)
		if opts.Stream && opts.OnProgress != nil {
			onDelta = func(raw string) {
				preview := previewTranslations(raw, batch)
				if len(preview) == 0 {
					return
				}
				for id, value := range result.translated {
					preview[id] = value
				}
				// Preview text is escaped like final text and remains provisional.
				// Only the fully validated batch below enters resume/disk caches.
				opts.OnProgress(TranslationProgress{Completed: result.Completed, Total: result.Total, Markdown: renderTranslation(markdown, result.Spans, preview), Segments: append([]TranslationSegment(nil), result.Segments...), Preview: true})
			}
		}
		raw, e := s.cachedChatProgress(ctx, x, "translation", string(payload), system, translationBudget(batchText.String(), len(batch)), func(raw string) error { _, err := parseTranslation(raw, batch); return err }, onDelta)
		if e != nil {
			return result, e
		}
		values, e := parseTranslation(raw, batch)
		if e != nil {
			return result, e
		}
		for id, v := range values {
			result.translated[id] = v
		}
		refresh()
		pending = pending[n:]
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	if e := s.current(x); e != nil {
		return result, e
	}
	completed := make([]TranslationSpan, 0, len(result.Spans))
	for _, sp := range result.Spans {
		completed = append(completed, TranslationSpan{ID: sp.ID, Text: result.translated[sp.ID]})
	}
	raw, _ := json.Marshal(struct {
		Spans []TranslationSpan `json:"spans"`
	}{completed})
	s.mu.Lock()
	if s.revision == x.revision {
		s.diskPut(result.fingerprint, string(raw))
	}
	s.mu.Unlock()
	return result, nil
}
