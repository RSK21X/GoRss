package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/backend"
)

// Headless CPU frame measurements include building, layout and painting through
// MyGo's Tester. They do not measure display scheduling or macOS/Metal FPS.
func perfReader(articles, paragraphs int) *nativeReader {
	a := newNativeReader(nil)
	for i := 0; i < 40; i++ {
		a.state.Feeds = append(a.state.Feeds, backend.Feed{ID: fmt.Sprint(i), Title: fmt.Sprintf("Feed %d", i)})
	}
	summary := strings.Repeat("<p>A substantial <strong>HTML summary</strong> with readable words.</p>", 100)
	for i := 0; i < articles; i++ {
		a.state.Articles = append(a.state.Articles, backend.Article{ID: fmt.Sprint(i), FeedID: fmt.Sprint(i % 40), Title: fmt.Sprintf("Article %d", i), Summary: summary, Read: true, Published: "2026-10-12T12:00:00Z"})
	}
	a.articleID = "0"
	a.document = markdown.ParseMarkdown(strings.Repeat("## Heading\n\nSelectable **Markdown** with a [link](https://example.com), 中文正文 and enough prose to wrap over multiple lines.\n\n", paragraphs))
	return a
}

func BenchmarkNativeReaderFrame(b *testing.B) {
	for _, n := range []int{100, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			a := perfReader(n, 250)
			defer a.close()
			tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 812)
			tt.Frame()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tt.Frame()
			}
		})
	}
}

func BenchmarkNativeLongArticleFrame(b *testing.B) {
	a := perfReader(100, 1000)
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, 820, 812)
	tt.Frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

func BenchmarkNativeReaderScroll(b *testing.B) {
	a := perfReader(10000, 250)
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 812)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		delta := float32(40)
		if i%40 >= 20 {
			delta = -40
		}
		tt.Scroll(1100, 500, 0, delta)
	}
}

func TestNativePresentationInvalidation(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	state := backend.State{Feeds: []backend.Feed{{ID: "f", Title: "Original"}}, Articles: []backend.Article{
		{ID: "one", FeedID: "f", Title: "Alpha", Summary: "<p>old <strong>summary</strong></p>"},
		{ID: "two", FeedID: "f", Title: "Beta", Author: "An Author", Read: true, Starred: true},
	}}
	a.publishState(state)
	a.updateRows()
	if len(a.rows) != 2 || a.presentation.counts["unread"] != 1 || a.presentation.counts["starred"] != 1 || a.presentation.unread["f"] != 1 {
		t.Fatal("incorrect initial counts/rows")
	}
	if a.articlePreview("one") != "old summary" || a.feedTitle("f") != "Original" {
		t.Fatal("incorrect presentation")
	}
	a.filter = "unread"
	a.updateRows()
	if len(a.rows) != 1 || a.rows[0].ID != "one" {
		t.Fatal("unread filter")
	}
	a.filter = "starred"
	a.updateRows()
	if len(a.rows) != 1 || a.rows[0].ID != "two" {
		t.Fatal("starred filter")
	}
	a.filter = "all"
	a.query = " AUTHOR "
	a.updateRows()
	if len(a.rows) != 1 || a.rows[0].ID != "two" {
		t.Fatal("author query")
	}
	a.query = "summary"
	a.updateRows()
	if len(a.rows) != 1 || a.rows[0].ID != "one" {
		t.Fatal("summary query")
	}
	a.feedID = "missing"
	a.updateRows()
	if len(a.rows) != 0 {
		t.Fatal("feed filter")
	}
	// Same storage, changed flags, summary, searchable title and feed title.
	state.Articles[0].Read = true
	state.Articles[0].Summary = "new preview"
	state.Articles[0].Title = "Gamma"
	state.Feeds[0].Title = "Renamed"
	a.publishState(state)
	a.feedID = ""
	a.query = "gamma"
	a.updateRows()
	if len(a.rows) != 1 || a.rows[0].Title != "Gamma" || a.presentation.counts["unread"] != 0 || a.articlePreview("one") != "new preview" || a.feedTitle("f") != "Renamed" {
		t.Fatal("snapshot did not invalidate caches")
	}
	a.articleID = "one"
	if a.article() == nil || a.article().Title != "Gamma" {
		t.Fatal("article lookup")
	}
	a.publishState(backend.State{})
	a.updateRows()
	if len(a.rows) != 0 || a.article() != nil || a.feedTitle("f") != "订阅" || len(a.presentation.previews) != 0 {
		t.Fatal("removed snapshot retained data")
	}
}

func TestNativePresentationWarmFrame(t *testing.T) {
	a := perfReader(100, 5)
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 812)
	a.updateRows()
	first := &a.rows[0]
	for i := 0; i < 5; i++ {
		tt.Frame()
	}
	if &a.rows[0] != first {
		t.Fatal("warm frames rebuilt rows")
	}
	if len(a.presentation.previews) == 0 || len(a.presentation.previews) >= len(a.state.Articles) {
		t.Fatal("previews must be lazy for virtualized list")
	}
	if !tt.HasText("Selectable Markdown with a link, 中文正文 and enough prose to wrap over multiple lines.") {
		t.Fatalf("lost Markdown text: %v", tt.Texts())
	}
	a.prefs.FontSize = 24
	a.prefs.LineHeight = 2
	a.prefs.ContentWidth = 480
	tt.SetSize(980, 572)
	tt.Frame()
	if a.document == nil || len(a.rows) != 100 {
		t.Fatal("preference/resize lost document or rows")
	}
}

func TestNativeReaderWholeDocumentSelection(t *testing.T) {
	a := perfReader(1, 1)
	defer a.close()
	a.document = markdown.ParseMarkdown("Before\n\n```go\nfmt.Println(42)\n```\n\n" + strings.Repeat("Middle paragraph.\n\n", 100) + "Last offscreen paragraph.")
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, 820, 812)
	r, ok := tt.Find("Before")
	if !ok {
		t.Fatal("missing first paragraph")
	}
	tt.ClickAt(r.X+2, r.Y+r.H/2)
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	copied := tt.Clipboard()
	if !strings.HasPrefix(copied, "Before\nfmt.Println(42)") || !strings.HasSuffix(copied, "Last offscreen paragraph.") || strings.Count(copied, "Middle paragraph.") != 100 {
		t.Fatalf("whole-document selection lost content: %q", copied)
	}
}
