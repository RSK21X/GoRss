package main

import (
	"context"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/backend"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeHTMLDetectionSkipsMarkdownCode(t *testing.T) {
	for _, source := range []string{"```html\n<p>Example</p>\n```", "Inline `<strong>example</strong>`", "    <img src=\"example\">"} {
		if articleHasHTML(source) {
			t.Fatalf("Markdown code detected as HTML: %q", source)
		}
	}
	for _, source := range []string{"<code>a_b</code>", "<b>bold</b>", "<p>body</p>"} {
		if !articleHasHTML(source) {
			t.Fatalf("HTML not detected: %q", source)
		}
	}
}

func TestNativeHTMLLiteralText(t *testing.T) {
	source := htmlMarkdown(`<p><code>a_b * c &lt;b&gt; &amp;copy; ` + "`" + `</code></p><p>&lt;tag&gt; &amp;copy;</p>`)
	doc := markdown.ParseMarkdown(source)
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); doc.Render(c, markdown.MarkdownOptions{}) }, 600, 400)
	if !tt.HasText("a_b * c <b> &copy; `") || !tt.HasText("<tag> &copy;") {
		t.Fatalf("HTML literal text changed: %v; source %q", tt.Texts(), source)
	}
}

func TestNativeImageDestinations(t *testing.T) {
	for _, source := range []string{
		`![one](</photo?a=1&amp;b=2>) ![two](</photo?a=1&#38;b=2>)`,
		htmlMarkdown(`<img src="/photo?a=1&amp;amp;b=2">`),
	} {
		want := []string{"/photo?a=1&b=2"}
		if strings.Contains(source, "amp;amp;") {
			want = []string{"/photo?a=1&amp;b=2"}
		}
		if got := markdownImages(source); !reflect.DeepEqual(got, want) {
			t.Fatalf("image destinations %v; want %v (source %q)", got, want, source)
		}
	}
}

func TestNativeStartupPreferencesPreserveDraft(t *testing.T) {
	r, err := backend.New(filepath.Join(t.TempDir(), "reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	p := backend.Preferences{FontFamily: "Georgia", FontSize: 20, LineHeight: 1.8, ContentWidth: 760, ShowImages: true, Theme: "light"}
	if _, err := r.SavePreferences(p); err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(r)
	defer a.close()
	a.window = &mygo.Window{}
	a.loadPreferences()
	job := <-a.jobs
	if err := job.work(a.ctx); err != nil {
		t.Fatal(err)
	}
	a.draft.FontFamily = "Menlo"
	job.done(nil)
	if a.prefs != p || a.draft.FontFamily != "Menlo" {
		t.Fatalf("lost startup draft: prefs %+v draft %+v", a.prefs, a.draft)
	}
}

func TestNativePreferencesAndSettingsPreserveScroll(t *testing.T) {
	a := perfReader(1, 100)
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 812)
	tt.Scroll(1100, 500, 0, 400)
	before := a.articleScroll.Y
	if before <= 0 {
		t.Fatal("article did not scroll")
	}
	doc, generation := a.document, a.articleGeneration
	p := a.prefs
	p.FontFamily = "Georgia"
	a.applyPreferences(p)
	if a.document != doc || a.articleGeneration != generation {
		t.Fatal("typography restarted article loading")
	}
	a.settings = true
	tt.Frame()
	a.settings = false
	tt.Frame()
	if a.articleScroll.Y != before {
		t.Fatalf("settings reset scroll: %v -> %v", before, a.articleScroll.Y)
	}
}

func TestNativeSnapshotReloadsChangedImageBase(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.window = &mygo.Window{}
	a.prefs.ShowImages = false
	state := backend.State{Articles: []backend.Article{{ID: "one", URL: "https://example.com/old/story", Content: "Text"}}}
	a.publishState(state)
	a.articleID, a.loadedArticleID = "one", "one"
	a.document = markdown.ParseMarkdown("Text")
	doc := a.document
	// Read/star flags must invalidate presentation without restarting content.
	state.Articles = append([]backend.Article(nil), state.Articles...)
	state.Articles[0].Read = true
	a.applySnapshot(state)
	if a.articleGeneration != 0 {
		t.Fatal("read state reloaded content")
	}
	state.Articles = append([]backend.Article(nil), state.Articles...)
	state.Articles[0].URL = "https://example.com/new/story"
	a.applySnapshot(state)
	if a.articleGeneration != 1 || !a.articleLoading || a.document != doc {
		t.Fatal("base URL change did not reload while preserving content")
	}
	ctxDone := a.articleCancel
	a.close()
	// Cancellation is idempotent and closes the worker context.
	ctxDone()
	if a.ctx.Err() == nil {
		t.Fatal("close did not cancel workers")
	}
}

func TestNativeImageRedirectAndCancellation(t *testing.T) {
	bitmap := image.NewRGBA(image.Rect(0, 0, 2, 3))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/image?a=1&b=2", http.StatusFound)
			return
		}
		if r.URL.Path != "/image" || r.URL.Query().Get("b") != "2" {
			http.NotFound(w, r)
			return
		}
		if err := png.Encode(w, bitmap); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	got, err := fetchArticleImage(context.Background(), "redirect", server.URL+"/story")
	if err != nil {
		t.Fatal(err)
	}
	if w, h := got.Size(); w != 2 || h != 3 {
		t.Fatalf("redirect image size %dx%d", w, h)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchArticleImage(ctx, "redirect", server.URL+"/story"); err == nil {
		t.Fatal("canceled image request succeeded")
	}
}

func TestNativeContent(t *testing.T) {
	source := htmlMarkdown(`<p>Hello <strong>reader</strong></p><p><a href="/story">Read more</a><img src="/photo.png" alt="A picture"></p><script>alert(1)</script><pre><code>a &lt; b</code></pre><table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table>`)
	for _, want := range []string{"Hello **reader**", "[Read more](</story>)", "![A picture](</photo.png>)", "a < b", "| A | B |"} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %s: %s", want, source)
		}
	}
	if strings.Contains(source, "alert") {
		t.Fatal("script content retained")
	}
	targets := markdownImages(source)
	if len(targets) != 1 || targets[0] != "/photo.png" {
		t.Fatalf("images: %#v", targets)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		png.Encode(w, image.NewUniform(color.RGBA{50, 100, 200, 255}))
	}))
	server.Close()
	bitmapImage := image.NewRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			bitmapImage.Set(x, y, color.RGBA{30, 100, 200, 255})
		}
	}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { png.Encode(w, bitmapImage) }))
	defer server.Close()
	bitmap, err := fetchArticleImage(context.Background(), "/photo.png", server.URL+"/article")
	if err != nil {
		t.Fatal(err)
	}
	w, h := bitmap.Size()
	if w != 80 || h != 40 {
		t.Fatalf("image size %dx%d", w, h)
	}
	if _, err := resolveArticleURL("javascript:alert(1)", server.URL); err == nil {
		t.Fatal("unsafe link accepted")
	}
}
func TestNativeLayout(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.state = backend.State{Feeds: []backend.Feed{{ID: "feed", Title: "Example publication"}}, Articles: []backend.Article{{ID: "article", FeedID: "feed", Title: "A good story", Summary: "A readable summary", Content: "## Heading\n\nSelectable article text", Published: "2026-10-12T12:00:00Z", Read: true}}}
	a.articleID = "article"
	a.document = markdown.ParseMarkdown(a.state.Articles[0].Content)
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 812)
	if !tt.HasText("A good story") || !tt.HasText("Selectable article text") {
		t.Fatalf("texts %v", tt.Texts())
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "native-reader-preview.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, tt.Image())
	f.Close()
	if err := tt.Click("阅读设置"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("自定义字体") || !tt.HasText("保存设置") {
		t.Fatalf("settings texts %v", tt.Texts())
	}
	f, err = os.Create(filepath.Join(t.TempDir(), "native-settings-preview.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, tt.Image())
	f.Close()
	if err := tt.Click("返回阅读"); err != nil {
		t.Fatal(err)
	}
	if a.settings {
		t.Fatal("settings did not close")
	}
	tt.SetSize(980, 572)
	if !tt.HasText("A good story") {
		t.Fatal("minimum width lost content")
	}
}
