package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/aiservice"
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

func TestNativeMarkdownReadingTypography(t *testing.T) {
	for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
		for _, source := range []string{"中文正文 `Muse Wonder` 继续阅读", htmlMarkdown("<p>中文正文 <code>Muse Wonder</code> 继续阅读</p>")} {
			prefs := backend.DefaultPreferences()
			doc := markdown.ParseMarkdown(source)
			tt := ui.NewTester(func(c *ui.Context) {
				theme.Apply(c, mode)
				original := c.Theme()
				before := *original
				ui.Column(c).Children(func() {
					renderArticleMarkdown(c, doc, prefs, markdown.MarkdownOptions{})
					if c.Theme() != original || *original != before {
						t.Fatal("article typography changed the surrounding theme")
					}
					ui.RichText(c).Label("expected paragraph").Font(prefs.FontFamily).FontSize(float32(prefs.FontSize)).LineHeight(float32(prefs.LineHeight)).Children(func() {
						ui.Text(c, "中文正文 ")
						ui.Text(c, "Muse Wonder").Font("monospace").FontSize(float32(prefs.FontSize) * .85).Label("expected code")
						ui.Text(c, " 继续阅读")
					})
					ui.Text(c, "outside")
				})
			}, 240, 600)
			var previousHeight float32
			for _, size := range []float64{12, 18, 32} {
				prefs.FontSize = size
				tt.Frame()
				actual, ok := tt.Find("中文正文 Muse Wonder 继续阅读")
				if !ok {
					t.Fatalf("missing inline code: %v", tt.Texts())
				}
				// Use the same literal to compare geometry without depending on
				// platform font metrics or inspecting private toolkit state.
				if actual.H <= previousHeight {
					t.Fatalf("paragraph did not scale at %.0f: height %v, previous %v", size, actual.H, previousHeight)
				}
				previousHeight = actual.H
				expected, ok := tt.Find("expected paragraph")
				if !ok || actual.W != expected.W || actual.H != expected.H {
					t.Fatalf("inline code height at %.0f: %v; expected %v", size, actual, expected)
				}
			}
		}
	}
}

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
	if err := tt.Click("设置 ⌘,"); err != nil {
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

func TestNativeNavigationAndActionScopes(t *testing.T) {
	a := perfReader(4, 1)
	defer a.close()
	a.state.Feeds[0].Title = strings.Repeat("很长的订阅名称", 20)
	a.publishState(a.state)
	if a.sideWidth != 196 {
		t.Fatalf("sidebar default: %v", a.sideWidth)
	}
	for _, width := range []int{1380, 980} {
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, width, 620)
		for _, label := range []string{"添加订阅 ⌘N", "更多订阅操作", "设置 ⌘,", "标记当前文章为未读", a.markAllReadLabel()} {
			box, ok := tt.Find(label)
			if !ok || box.X < 0 || box.X+box.W > float32(width) || box.W < 32 {
				t.Fatalf("width %d, %s: %v %v", width, label, box, ok)
			}
		}
		if err := tt.Click("更多订阅操作"); err != nil {
			t.Fatal(err)
		}
		if !tt.HasText("刷新全部订阅") {
			t.Fatal("missing refresh all")
		}
		tt.Key(0, ui.KeyEscape)
		if err := tt.Click("设置 ⌘,"); err != nil {
			t.Fatal(err)
		}
		a.settingsState.Category = "subscriptions"
		tt.Frame()
		if !tt.HasText("导入 OPML") || !tt.HasText("导出 OPML") {
			t.Fatalf("missing OPML: %v", tt.Texts())
		}
		a.settings = false
	}
	a.filter, a.query = "starred", "does not match"
	if !strings.Contains(a.markAllReadLabel(), "所有订阅") {
		t.Fatal("filter narrowed bulk scope")
	}
	a.feedID = "0"
	if !strings.Contains(a.markAllReadLabel(), a.feedTitle("0")) {
		t.Fatal("feed scope missing")
	}
	a.state.Articles[0].Read = false
	a.publishState(a.state)
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, 500, 620)
	if _, ok := tt.Find("标记当前文章为已读"); !ok {
		t.Fatal("missing unread action")
	}
}

func TestNativePreviewKeepsSourceBody(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	article := backend.Article{ID: "a", Title: "重复标题", Summary: "<p>重复标题</p>", Content: "# 重复标题\n\n原文保留"}
	a.publishState(backend.State{Articles: []backend.Article{article}})
	if a.articlePreview("a") != "" {
		t.Fatal("duplicated generated preview")
	}
	if a.state.Articles[0] != article {
		t.Fatal("source article changed")
	}
}

func TestNativeUXScreenshots(t *testing.T) {
	dir := os.Getenv("GORSS_UI_SCREENSHOTS")
	if dir == "" {
		t.Skip("set GORSS_UI_SCREENSHOTS to save headless previews")
	}
	a := perfReader(10, 2)
	defer a.close()
	a.state.Feeds[0].Title = strings.Repeat("长订阅名称", 15)
	a.state.Articles[1].Read = false
	a.state.Articles[1].Summary = strings.Repeat("摘要应该限制为三行，保留文章标题的视觉层级。", 20)
	a.publishState(a.state)
	for _, width := range []int{1380, 980} {
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, width, 812)
		for _, mode := range []string{"reader", "settings"} {
			a.settings = mode == "settings"
			a.settingsState.Category = "subscriptions"
			tt.Frame()
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ux-%s-%d.png", mode, width)))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		a.settings = false
	}
}

func TestNativeReadButtonsPersistAndKeepBulkScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.json")
	state := backend.State{Feeds: []backend.Feed{{ID: "f", Title: "Feed", URL: "https://example.com/feed"}, {ID: "g", Title: "Other", URL: "https://example.org/feed"}}, Articles: []backend.Article{
		{ID: "one", FeedID: "f", Title: "Visible", Content: "Body", Read: true, Starred: true},
		{ID: "two", FeedID: "g", Title: "Hidden", Content: "Other body"},
	}}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(r)
	defer a.close()
	a.window = &mygo.Window{}
	a.publishState(state)
	a.articleID = "one"
	a.document = markdown.ParseMarkdown("Body")
	a.filter, a.query = "starred", "Visible"
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 980, 620)
	complete := func() {
		t.Helper()
		job := <-a.jobs
		err := job.work(a.ctx)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := r.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		a.pending--
		a.applySnapshot(snapshot)
		if job.done != nil {
			job.done(nil)
		}
		tt.Frame()
	}
	if err := tt.Click("标记当前文章为未读"); err != nil {
		t.Fatal(err)
	}
	complete()
	if a.article().Read || a.presentation.counts["unread"] != 2 {
		t.Fatal("single read toggle did not synchronize counts")
	}
	if err := tt.Click("标记当前文章为已读"); err != nil {
		t.Fatal(err)
	}
	complete()
	if !a.article().Read || a.state.Articles[1].Read {
		t.Fatal("single read toggle changed another article")
	}
	if err := tt.Click(a.markAllReadLabel()); err != nil {
		t.Fatal(err)
	}
	complete()
	if !a.state.Articles[1].Read {
		t.Fatal("bulk action was incorrectly limited to visible/search/starred rows")
	}
	reloaded, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := reloaded.Snapshot()
	if err != nil || !persisted.Articles[0].Read || !persisted.Articles[1].Read {
		t.Fatal("read flags did not persist")
	}
}

func TestNativeAIInitialFoldAndActiveProvider(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	article := backend.Article{ID: "ai-article", Title: "Article", Content: "# Heading\n\nBody"}
	a.state.Articles = []backend.Article{article}
	a.articleID = article.ID
	a.document = markdown.ParseMarkdown(article.Content)
	config := aiservice.DefaultConfig()
	config.Custom.Enabled = true
	config.Custom.Model = "mock"
	config.Magpie.Enabled = false
	a.ai.settings.config = nativeAIConfig(config)
	a.ai.settings.loaded = true
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, 800, 900)
	if _, ok := tt.Find("AI 摘要"); !ok {
		t.Fatal("missing active summary toolbar")
	}
	if _, ok := tt.Find("翻译文章"); !ok {
		t.Fatal("missing translation toolbar")
	}
	if a.ai.summary.open || a.ai.summary.loading || a.ai.translation.loading {
		t.Fatal("article view started AI automatically")
	}
	a.ai.summary.text = "cached summary"
	a.ai.summary.document = markdown.ParseMarkdown("cached summary")
	tt.Frame()
	if tt.HasText("cached summary") {
		t.Fatal("cached summary expanded automatically")
	}
	config.ActiveProvider = aiservice.Magpie
	a.ai.settings.config = nativeAIConfig(config)
	a.ai.settings.loaded = true
	tt.Frame()
	if _, ok := tt.Find("翻译文章"); !ok {
		t.Fatal("disabled provider hid AI toolbar")
	}
	if err := tt.Click("翻译文章"); err != nil {
		t.Fatal(err)
	}
	if !a.settings || a.settingsState.Category != "ai" || a.ai.translation.loading {
		t.Fatal("unconfigured click must guide without request")
	}
	if !tt.HasText("Body") {
		t.Fatal("AI configuration hid original article")
	}
}
func TestNativeAIArticleAndCloseCancellation(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	original := backend.Article{ID: "first", Content: "Body"}
	a.syncAIArticle(original)
	ctx, cancel := context.WithCancel(a.ctx)
	a.ai.summary.cancel = cancel
	a.ai.summary.loading = true
	a.ai.summary.open = true
	before := a.ai.summary.generation
	a.syncAIArticle(backend.Article{ID: "second", Content: "Other"})
	if ctx.Err() == nil || a.ai.summary.generation <= before || a.ai.summary.open || a.ai.summary.loading {
		t.Fatal("article switch did not cancel and clear AI")
	}
	ctx, cancel = context.WithCancel(a.ctx)
	a.ai.selection.cancel = cancel
	a.ai.selection.open = true
	a.ai.selection.loading = true
	a.ai.selectionText = "selected"
	a.closeSelectionAI()
	if ctx.Err() == nil || a.ai.selection.open || a.ai.selectionText != "" {
		t.Fatal("selection close did not cancel")
	}
}
func TestNativeAISelectionAccessorPreservesClipboard(t *testing.T) {
	var selected string
	doc := markdown.ParseMarkdown("First paragraph.\n\nSecond paragraph.")
	tt := ui.NewTester(func(c *ui.Context) {
		theme.Apply(c, theme.Light)
		container := doc.Render(c, markdown.MarkdownOptions{})
		container.ContextMenu(func(m *ui.Menu) {
			m.EditItems()
			selected = nativeSelectedText(container.SelectedTexts())
			if selected != "" {
				m.Separator()
				m.Item("Translate selection")
			}
		})
	}, 600, 400)
	tt.SetClipboard("existing clipboard")
	if err := tt.Click("First paragraph."); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	if err := tt.RightClick("First paragraph."); err != nil {
		t.Fatal(err)
	}
	if selected != "First paragraph.\nSecond paragraph." {
		t.Fatalf("selection %q", selected)
	}
	if tt.Clipboard() != "existing clipboard" {
		t.Fatal("selection accessor changed clipboard")
	}
	menu := strings.Join(tt.Menu(), "|")
	if !strings.Contains(menu, "Copy") || !strings.Contains(menu, "Select All") || !strings.Contains(menu, "Translate selection") {
		t.Fatal(menu)
	}
	if err := tt.ChooseMenuItem("Copy"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != selected {
		t.Fatalf("original Copy command changed: %q", tt.Clipboard())
	}
}

func TestNativeSelectedTextUsesRuneOffsets(t *testing.T) {
	parts := []ui.SelectedText{
		{Text: "前缀中文🙂后缀", Start: 2, End: 5},
		{Text: "second paragraph", Start: 0, End: 6},
	}
	if got := nativeSelectedText(parts); got != "中文🙂\nsecond" {
		t.Fatalf("selected text: %q", got)
	}
}
func TestNativeAIBilingualBlocksPreserveStructure(t *testing.T) {
	source := "# Heading\n\n> Quote **bold**\n\n- First\n- Second\n\n| Name | Value |\n| --- | --- |\n| x | y |\n\n```go\nfmt.Println(1)\n```\n\nTail [link](https://example.com)"
	blocks := nativeAIBlocks(source)
	if len(blocks) != 6 {
		t.Fatalf("blocks %d: %#v", len(blocks), blocks)
	}
	for _, want := range []string{"# Heading", "> Quote **bold**", "- First\n- Second", "| Name | Value |", "```go\nfmt.Println(1)\n```", "Tail [link](https://example.com)"} {
		found := false
		for _, block := range blocks {
			if strings.Contains(block, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("lost structure %q: %#v", want, blocks)
		}
	}
}

func TestNativeAISelectionMenuEmptyAndLengthLimit(t *testing.T) {
	for _, source := range []string{"Small selection", strings.Repeat("字", nativeAISelectionLimit+1)} {
		a := newNativeReader(nil)
		config := aiservice.DefaultConfig()
		config.Custom.Enabled = true
		config.Custom.Model = "mock"
		a.ai.settings.config = nativeAIConfig(config)
		a.ai.settings.loaded = true
		tt := ui.NewTester(func(c *ui.Context) {
			theme.Apply(c, theme.Light)
			a.aiSelectionContextMenu(ui.Column(c).Selectable().Children(func() { ui.Text(c, source) }))
		}, 600, 400)
		if err := tt.RightClick(source); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(tt.Menu(), "|"), "翻译") {
			t.Fatal("empty selection exposes translation")
		}
		tt.CloseMenu()
		if err := tt.Click(source); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		if err := tt.RightClick(source); err != nil {
			t.Fatal(err)
		}
		menu := strings.Join(tt.Menu(), "|")
		if len([]rune(source)) > nativeAISelectionLimit {
			if !strings.Contains(menu, "选中文字过长") {
				t.Fatal(menu)
			}
		} else if !strings.Contains(menu, "翻译所选文字") {
			t.Fatal(menu)
		}
		if a.ai.selection.loading {
			t.Fatal("selection alone started request")
		}
		a.close()
	}
}
func TestNativeAISettingsTwoProviders(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.settings = true
	a.ai.settings.loaded = true
	a.ai.settings.config = nativeAIConfig(aiservice.DefaultConfig())
	a.ai.settings.draft = a.ai.settings.config
	a.ai.settings.draft.Custom.Model = "custom-model"
	a.ai.settings.draft.Magpie.Model = "magpie-model"
	a.settingsState.Category = "ai"
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, 1380, 1200)
	if !tt.HasText("自定义 API") || !tt.HasText("Magpie") {
		t.Fatalf("provider settings missing: %v", tt.Texts())
	}
	if a.ai.settings.draft.Custom.Model == a.ai.settings.draft.Magpie.Model {
		t.Fatal("provider drafts alias")
	}
}

func TestNativeAICacheIdentity(t *testing.T) {
	article := backend.Article{ID: "id", Content: "body", Summary: "summary"}
	identity := nativeAISourceIdentity(article)
	config := nativeAIConfig(aiservice.DefaultConfig())
	key := nativeAICacheKey(identity, config)
	if len(identity) != 64 || len(key) != 64 {
		t.Fatal("identities must be SHA-256 hashes")
	}
	article.Content += " refreshed"
	if nativeAISourceIdentity(article) == identity {
		t.Fatal("content refresh retained identity")
	}
	for _, field := range []string{"language", "base", "model", "provider"} {
		changed := config
		switch field {
		case "language":
			changed.Custom.Language = "日本語"
		case "base":
			changed.Custom.BaseURL = "https://another.example/v1"
		case "model":
			changed.Custom.Model = "other"
		case "provider":
			changed.ActiveProvider = aiservice.Magpie
		}
		if nativeAICacheKey(identity, changed) == key {
			t.Fatalf("%s retained cache key", field)
		}
	}
}

func TestNativeAIRequestWaitsForConfigAndCredentials(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	config := aiservice.DefaultConfig()
	config.Custom.Enabled = true
	config.Custom.Model = "mock"
	a.ai.settings.config = nativeAIConfig(config)
	a.state.Articles = []backend.Article{{ID: "id", Content: "body"}}
	a.articleID = "id"
	a.requestAI("summary", false)
	if a.ai.summary.loading || a.ai.summary.open {
		t.Fatal("request started before settings loaded")
	}
	a.ai.settings.loaded = true
	a.ai.credentialMutations = 1
	a.requestAI("summary", false)
	if a.ai.summary.loading || a.ai.summary.open {
		t.Fatal("request started during credential mutation")
	}
}

func TestNativeAISummaryNoteAndProgressBilingual(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	article := backend.Article{ID: "id", Content: "First original.\n\nSecond original."}
	a.syncAIArticle(article)
	config := aiservice.DefaultConfig()
	config.Custom.Enabled = true
	config.Custom.Model = "mock"
	a.ai.settings.config, a.ai.settings.loaded = nativeAIConfig(config), true
	a.ai.summary.open = true
	a.ai.summary.loading = true
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.aiSummaryView(c, article) }, 800, 400)
	if !tt.HasText("基于订阅提供的可用内容生成，可能不是完整原文。") {
		t.Fatal("expanded summary omitted source note")
	}
	a.ai.summary.open = false
	tt.Frame()
	if tt.HasText("基于订阅提供的可用内容生成，可能不是完整原文。") {
		t.Fatal("collapsed summary exposed note")
	}
	a.ai.mode = "bilingual"
	a.ai.translation.open, a.ai.translation.loading = true, true
	a.ai.translation.completed, a.ai.translation.total = 1, 2
	a.ai.translation.text = "第一段译文。\n\nSecond original."
	a.ai.translation.document = markdown.ParseMarkdown(a.ai.translation.text)
	tt = ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.aiTranslationView(c, article) }, 800, 600)
	for _, want := range []string{"正在翻译 1 / 2…", "First original.", "第一段译文。", "Second original."} {
		if !tt.HasText(want) {
			t.Fatalf("bilingual progress missing %q: %v", want, tt.Texts())
		}
	}
}

func TestNativeAIModelsSelectable(t *testing.T) {
	selected := "old"
	models := []aiservice.Model{{ID: "dynamic-one"}, {ID: "dynamic-two", DisplayName: "第二模型"}}
	tt := ui.NewTester(func(c *ui.Context) {
		theme.Apply(c, theme.Light)
		nativeAIModelSelect(c, &selected, "Magpie", models)
	}, 600, 500)
	if err := tt.Click("Magpie 可用模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("第二模型 · dynamic-two"); err != nil {
		t.Fatal(err)
	}
	if selected != "dynamic-two" {
		t.Fatalf("model selection %q", selected)
	}
}

func TestNativeAICredentialMutationInvalidatesImmediately(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.syncAIArticle(backend.Article{ID: "id", Content: "body"})
	ctx, cancel := context.WithCancel(a.ctx)
	a.ai.summary.cancel, a.ai.summary.loading = cancel, true
	a.ai.summary.text = "old secret result"
	a.ai.cache = map[string]nativeAICacheEntry{"old": {summary: "old secret cache"}}
	before := a.ai.summary.generation
	a.ai.settings.provider(aiservice.Magpie).models = []aiservice.Model{{ID: "old-model"}}
	a.ai.settings.provider(aiservice.Magpie).modelsBase, a.ai.settings.provider(aiservice.Magpie).identityBase = "old-base", "old-base"
	a.beginAICredentialMutation(aiservice.Magpie)
	if ctx.Err() == nil || a.ai.summary.generation <= before || a.ai.summary.loading || a.ai.summary.text != "" || len(a.ai.cache) != 0 {
		t.Fatal("credential mutation retained pending work or old cache")
	}
	if a.ai.credentialMutations != 1 || len(a.ai.settings.provider(aiservice.Magpie).models) != 0 || a.ai.settings.provider(aiservice.Magpie).identityBase != "" {
		t.Fatal("credential mutation retained old provider verification")
	}
}

func TestNativeAISettingsSelectorAndScreenshots(t *testing.T) {
	for _, width := range []int{1380, 980, 760, 420} {
		a := newNativeReader(nil)
		a.settings = true
		a.settingsState.Category = "ai"
		a.ai.settings.loaded = true
		a.ai.settings.config = nativeAIConfig(aiservice.DefaultConfig())
		a.ai.settings.draft = a.ai.settings.config
		a.ai.settings.draft.Custom.Model = "custom-preserved"
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, width, 1000)
		if width >= 1100 {
			if _, ok := tt.Find("类别"); ok {
				t.Fatal("AI forced narrow navigation")
			}
			label, _ := tt.Find("API 地址")
			control, _ := tt.Find("自定义 API API 地址")
			if control.X <= label.X+label.W || control.W > 321 {
				t.Fatalf("not a standard bounded row: %v %v", label, control)
			}
		}
		if tt.HasText("启用 Magpie") || tt.HasText("恢复默认") || tt.HasText("保存设置") {
			t.Fatal("duplicate provider/footer")
		}
		if err := tt.Click("当前 AI 服务"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("Magpie"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.ai.settings.draft.ActiveProvider != aiservice.Magpie || a.ai.settings.draft.Custom.Model != "custom-preserved" || !tt.HasText("启用 Magpie") || tt.HasText("启用 自定义 API") {
			t.Fatal("selector lost state")
		}
		for _, label := range []string{"Magpie API 地址", "Magpie 模型 ID", "检测协议", "保存 AI 设置"} {
			bounds, ok := tt.Find(label)
			if !ok || bounds.X < 0 || bounds.X+bounds.W > float32(width)+1 {
				t.Fatalf("%s overflow %v", label, bounds)
			}
		}
		if dir := os.Getenv("GORSS_UI_SCREENSHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ai-settings-%d.png", width)))
			if err != nil {
				t.Fatal(err)
			}
			if err = png.Encode(f, tt.Image()); err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		a.close()
	}
}

func TestNativeAIToolbarAlwaysVisibleAndGuides(t *testing.T) {
	for _, width := range []int{800, 360} {
		a := perfReader(1, 1)
		a.ai.settings.config = nativeAIConfig(aiservice.DefaultConfig())
		a.ai.settings.loaded = true
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, width, 600)
		var y float32
		for i, label := range []string{"AI 摘要", "翻译文章", "收藏", "标记当前文章为未读", "浏览器打开原文"} {
			b, ok := tt.Find(label)
			if !ok || b.W < 30 || b.X < 0 || b.X+b.W > float32(width) {
				t.Fatalf("toolbar %s %v", label, b)
			}
			if i == 0 {
				y = b.Y
			} else if b.Y != y {
				t.Fatal("toolbar row misaligned")
			}
		}
		if a.ai.summary.loading || a.ai.translation.loading {
			t.Fatal("automatic request")
		}
		if dir := os.Getenv("GORSS_UI_SCREENSHOTS"); dir != "" {
			f, err := os.Create(filepath.Join(dir, fmt.Sprintf("ai-toolbar-%d.png", width)))
			if err != nil {
				t.Fatal(err)
			}
			png.Encode(f, tt.Image())
			f.Close()
		}
		if err := tt.Click("AI 摘要"); err != nil {
			t.Fatal(err)
		}
		if !a.settings || a.settingsState.Category != "ai" || a.ai.summary.loading {
			t.Fatal("missing settings guide")
		}
		a.close()
	}
}

func TestNativeAISelectedProviderPersistence(t *testing.T) {
	config := aiservice.DefaultConfig()
	config.Custom.Model = "retained-custom"
	config.ActiveProvider = aiservice.Magpie
	config.Magpie.Enabled = true
	path := filepath.Join(t.TempDir(), "ai.json")
	if err := aiservice.SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	saved, err := aiservice.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ActiveProvider != aiservice.Magpie || !saved.Magpie.Enabled || saved.Custom.Model != "retained-custom" {
		t.Fatal("active provider not persisted")
	}
	if nativeAIConfig(saved).enabled() {
		t.Fatal("blank model should guide")
	}
	saved.Magpie.Model = "mock"
	if !nativeAIConfig(saved).enabled() {
		t.Fatal("Magpie readiness used custom provider")
	}
	a := newNativeReader(nil)
	defer a.close()
	a.settingsState.Category = "reading"
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, 760, 1000)
	if _, ok := tt.Find("恢复默认"); !ok {
		t.Fatal("reading footer missing")
	}
	a.settingsState.Category = "ai"
	tt.Frame()
	if _, ok := tt.Find("恢复默认"); ok {
		t.Fatal("reading footer on AI")
	}
}

func TestNativeReadingSettingsComparisonScreenshots(t *testing.T) {
	dir := os.Getenv("GORSS_UI_SCREENSHOTS")
	if dir == "" {
		t.Skip("set GORSS_UI_SCREENSHOTS")
	}
	for _, width := range []int{1380, 980, 760, 420} {
		a := newNativeReader(nil)
		a.settings = true
		a.settingsState.Category = "reading"
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, width, 1000)
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("reading-settings-%d.png", width)))
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
		f.Close()
		a.close()
	}
}
