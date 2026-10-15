package main

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/aiservice"
	"gorss/backend"
)

func designReader() *nativeReader {
	a := newNativeReader(nil)
	a.window = &mygo.Window{}
	a.publishState(backend.State{Feeds: []backend.Feed{
		{ID: "design", Title: "设计与日常", URL: "https://example.com/design.xml"},
		{ID: "tech", Title: "技术笔记", URL: "https://example.com/tech.xml"},
		{ID: "reading", Title: "阅读札记", URL: "https://example.com/reading.xml"},
	}, Articles: []backend.Article{
		{ID: "one", FeedID: "design", Title: "把时间留给值得读的内容", Author: "Editorial", Published: "2026-10-15T08:30:00Z", Summary: "阅读不需要更多干扰。用清楚的排版、稳定的界面和一点留白，把注意力还给文章本身。", Content: "Good tools leave room for the story.\n\nRead at your own pace, with fewer interruptions. A quiet interface makes the next paragraph easier to find.\n\n## Keep the essentials\n\n- Clear typography\n- A steady reading position\n\n```go\nreader.Refresh(ctx)\n```", Read: true, Starred: true},
		{ID: "two", FeedID: "tech", Title: "让软件在后台完成那些小事", Published: "2026-10-15T07:15:00Z", Summary: "缓存先出现，刷新随后完成。快速与可靠，可以一起做到。"},
		{ID: "three", FeedID: "reading", Title: "重新发现纸张与屏幕之间的节奏", Published: "2026-10-14T14:00:00Z", Summary: "关于字体、段落和阅读时的呼吸。", Read: true},
		{ID: "four", FeedID: "design", Title: "细节如何让一次点击变得明确", Published: "2026-10-14T10:10:00Z", Summary: "反馈应该清楚，也应该适时退场。", Starred: true},
		{ID: "five", FeedID: "tech", Title: "小而稳定的工具", Published: "2026-10-13T12:00:00Z", Summary: "从简单的工作流开始，逐步打磨每天都会使用的功能。", Read: true},
	}})
	a.articleID = "one"
	a.document = markdown.ParseMarkdown(a.article().Content)
	a.syncAIArticle(*a.article())
	config := aiservice.DefaultConfig()
	config.Custom.Enabled = true
	config.Custom.Model = "preview-model"
	a.ai.settings.config = nativeAIConfig(config)
	a.ai.settings.draft = a.ai.settings.config.clone()
	a.ai.settings.loaded = true
	a.ai.summary.text = "好的阅读体验来自清晰的排版、稳定的位置和适当的留白。保留必要操作，让文章成为视线的中心。"
	a.ai.summary.document = markdown.ParseMarkdown(a.ai.summary.text)
	a.ai.summary.finished = true
	a.ai.translation.text = "好的工具为故事留出空间。\n\n按自己的节奏阅读，少一些打扰。安静的界面，让下一段更容易找到。\n\n## 保留必要的部分\n\n- 清晰的排版\n- 稳定的阅读位置\n\n```go\nreader.Refresh(ctx)\n```"
	a.ai.translation.document = markdown.ParseMarkdown(a.ai.translation.text)
	a.ai.translation.finished = true
	return a
}

func TestNativeSegmentedKeyboardAndReducedMotion(t *testing.T) {
	selected, changes := 0, 0
	tt := ui.NewTester(func(c *ui.Context) {
		theme.Apply(c, theme.Light)
		ui.Column(c).Width(272).Children(func() {
			tabs := nativeSegmented(c, &selected, "译文显示模式", "原文", "译文", "双语对照")
			if tabs.List.Changed() {
				changes++
			}
		})
	}, 300, 160)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if err := tt.Click("译文"); err != nil {
		t.Fatal(err)
	}
	if selected != 1 || changes != 1 {
		t.Fatalf("selection %d, events %d", selected, changes)
	}
	tt.Key(0, ui.KeyRight)
	if selected != 2 || !tt.Focused("双语对照") {
		t.Fatal("arrow did not move selection and focus")
	}
	tt.Key(0, ui.KeyHome)
	if selected != 0 || !tt.Focused("原文") {
		t.Fatal("Home did not restore the first tab")
	}
	tt.Key(0, ui.KeyEnd)
	if selected != 2 || !tt.Focused("双语对照") {
		t.Fatal("End did not select last tab")
	}
	track, _ := tt.Find("译文显示模式")
	tab, _ := tt.Find("双语对照")
	img := tt.Image()
	x, y := int(tab.X+tab.W/2), int(track.Y+5)
	if color.RGBAModel.Convert(img.At(x, y)) != (color.RGBA{R: nativePaperSurface.R, G: nativePaperSurface.G, B: nativePaperSurface.B, A: 255}) {
		t.Fatal("reduced-motion thumb did not move to selected tab")
	}
}

func TestNativeSummaryDisclosureReusesCompletedResultAndCancelsPartial(t *testing.T) {
	a := designReader()
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.aiSummaryView(c, *a.article()) }, 560, 500)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	generation := a.ai.summary.generation
	if tt.HasText(a.ai.summary.text) {
		t.Fatal("summary expanded without an action")
	}
	if err := tt.Click("展开 AI 摘要"); err != nil {
		t.Fatal(err)
	}
	if !a.ai.summary.open || a.ai.summary.loading || a.ai.summary.generation != generation || !tt.HasText(a.ai.summary.text) {
		t.Fatal("completed summary started a new request or lost content")
	}
	if err := tt.Click("收起 AI 摘要"); err != nil {
		t.Fatal(err)
	}
	if a.ai.summary.open || tt.HasText(a.ai.summary.text) {
		t.Fatal("collapsed summary remains visible with reduced motion")
	}
	tt.Key(0, ui.KeyEnter)
	if !a.ai.summary.open || !tt.HasText(a.ai.summary.text) {
		t.Fatal("keyboard did not reopen summary")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.ai.summary.cancel = cancel
	a.ai.summary.loading = true
	a.ai.summary.finished = false
	tt.Frame()
	if err := tt.Click("收起 AI 摘要"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || a.ai.summary.loading || a.ai.summary.finished {
		t.Fatal("collapsing partial summary did not cancel generation")
	}
}

func TestNativeCopyFeedbackAndExpiry(t *testing.T) {
	a := designReader()
	defer a.close()
	a.ai.selection.open = true
	a.ai.selection.text = "保留空格、中文与换行。\nSecond line."
	a.ai.selection.document = markdown.ParseMarkdown(a.ai.selection.text)
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.aiSelectionView(c) }, 700, 600)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if err := tt.Click("复制译文"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != a.ai.selection.text || !tt.HasText("已复制") {
		t.Fatal("copy changed the content or omitted feedback")
	}
	a.ai.selection.copy.until = time.Now().Add(-time.Second)
	tt.Frame()
	if tt.HasText("已复制") {
		t.Fatal("copy feedback did not expire")
	}
	tt.SetClipboard("unchanged")
	a.ai.selection.loading = true
	tt.Frame()
	if err := tt.Click("复制译文"); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != "unchanged" {
		t.Fatal("disabled copy overwrote the clipboard")
	}
	a.ai.selection.clear()
	if a.ai.selection.copy.active(time.Now(), a.ai.selection.text) {
		t.Fatal("clearing output retained copy feedback")
	}
}

func TestNativeReadingTypographyAndSurfaceHierarchy(t *testing.T) {
	a := designReader()
	defer a.close()
	a.ai.settings.config.Custom.Enabled = false
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 860)
	for _, item := range []struct {
		label string
		face  ui.Color
	}{{"订阅导航栏", nativeSidebarSurface}, {"文章列表栏", nativeListSurface}, {"文章阅读栏", nativePaperSurface}} {
		b, ok := tt.Find(item.label)
		if !ok {
			t.Fatal("missing pane", item.label)
		}
		// Empty bottom edge of each pane exposes its face without text or selection.
		got := color.RGBAModel.Convert(tt.Image().At(int(b.X+20), int(b.Y+b.H-5)))
		if got != (color.RGBA{R: item.face.R, G: item.face.G, B: item.face.B, A: 255}) {
			t.Fatalf("%s has incorrect face %v", item.label, got)
		}
	}
	metaText := ""
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "Editorial  /  ") {
			metaText = s
			break
		}
	}
	caption, ok := tt.Find(metaText)
	if !ok || metaText == "" {
		t.Fatal("article metadata missing")
	}
	titleBefore, _ := tt.Find("让软件在后台完成那些小事")
	bodyBefore, _ := tt.Find("Good tools leave room for the story.")
	document := a.document
	article := *a.article()
	a.prefs.FontSize = 28
	a.prefs.LineHeight = 2.1
	a.prefs.FontFamily = "Songti SC"
	tt.Frame()
	titleAfter, _ := tt.Find("让软件在后台完成那些小事")
	bodyAfter, _ := tt.Find("Good tools leave room for the story.")
	captionAfter, _ := tt.Find(metaText)
	if captionAfter.H != caption.H || titleAfter != titleBefore || bodyAfter.H <= bodyBefore.H || a.document != document || *a.article() != article {
		t.Fatal("reading typography leaked into list or changed source/state")
	}
}

func TestNativeTranslationSegmentedIntegrationAndWidths(t *testing.T) {
	for _, width := range []int{360, 800} {
		a := designReader()
		a.ai.translation.open = true
		a.ai.translation.loading = true
		tt := ui.NewTester(func(c *ui.Context) {
			theme.Apply(c, theme.Light)
			ui.Column(c).Fill().Padding(16).Children(func() { a.aiTranslationView(c, *a.article()) })
		}, width, 1000)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
		for _, choice := range []struct{ label, mode string }{{"双语对照", "bilingual"}, {"原文", "original"}, {"译文", "translated"}} {
			if err := tt.Click(choice.label); err != nil {
				t.Fatal(err)
			}
			if a.ai.mode != choice.mode {
				t.Fatalf("%s did not set %s", choice.label, choice.mode)
			}
			for _, label := range []string{"译文显示模式", "重新翻译", "关闭翻译"} {
				b, ok := tt.Find(label)
				if !ok || b.X < 0 || b.X+b.W > float32(width) {
					t.Fatalf("%s at %d overflows: %+v", label, width, b)
				}
			}
		}
		a.close()
	}
}

func TestNativeVisualRoundOneScreenshots(t *testing.T) {
	for _, width := range []int{1380, 980} {
		a := designReader()
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, width, 860)
		tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
		captureAIView(t, fmt.Sprintf("reader-%d", width), tt)
		if err := tt.Click("展开 AI 摘要"); err != nil {
			t.Fatal(err)
		}
		captureAIView(t, fmt.Sprintf("summary-expanded-%d", width), tt)
		a.ai.summary.open = false
		a.ai.mode = "bilingual"
		a.ai.translation.open = true
		tt.Frame()
		captureAIView(t, fmt.Sprintf("bilingual-%d", width), tt)
		a.close()
	}
}

func TestNativeStarFeedbackPersistsWithoutChangingReadState(t *testing.T) {
	a := designReader()
	defer a.close()
	path := filepath.Join(t.TempDir(), "reader.json")
	data, err := json.Marshal(a.state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	a.reader = reader
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.articleView(c) }, 800, 700)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	for _, action := range []string{"取消收藏", "收藏"} {
		before := *a.article()
		if err := tt.Click(action); err != nil {
			t.Fatal(err)
		}
		job := <-a.jobs
		workErr := job.work(a.ctx)
		snapshot, snapshotErr := reader.Snapshot()
		a.completeJob(job, snapshot, workErr, snapshotErr)
		tt.Frame()
		after := *a.article()
		if workErr != nil || snapshotErr != nil || after.Starred == before.Starred || after.Read != before.Read || after.Content != before.Content {
			t.Fatal("star action lost content or reading state")
		}
	}
	reloaded, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reloaded.Snapshot()
	if err != nil || !state.Articles[0].Starred || !state.Articles[0].Read {
		t.Fatal("star feedback did not correspond to persisted state")
	}
}

// Capture real toolkit animation frames only when a visual artifact is requested.
func TestNativeVisualMotionCapture(t *testing.T) {
	dir := os.Getenv("GORSS_UI_SCREENSHOTS")
	if dir == "" || os.Getenv("GORSS_UI_MOTION") != "1" {
		t.Skip("set GORSS_UI_MOTION=1 to capture native motion")
	}
	if err := os.MkdirAll(filepath.Join(dir, "motion"), 0755); err != nil {
		t.Fatal(err)
	}
	a := designReader()
	defer a.close()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 860)
	for frame := 0; frame < 76; frame++ {
		switch frame {
		case 10:
			if err := tt.Click("展开 AI 摘要"); err != nil {
				t.Fatal(err)
			}
		case 29:
			if err := tt.Click("复制摘要"); err != nil {
				t.Fatal(err)
			}
		case 38:
			if err := tt.Click("收起 AI 摘要"); err != nil {
				t.Fatal(err)
			}
		case 48:
			a.ai.translation.open = true
			a.ai.mode = "translated"
		case 58:
			a.ai.mode = "bilingual"
		case 68:
			a.ai.mode = "original"
		}
		tt.Frame()
		captureAIView(t, fmt.Sprintf("motion/frame-%03d", frame), tt)
		time.Sleep(35 * time.Millisecond)
	}
}
