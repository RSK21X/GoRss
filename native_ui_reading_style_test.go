package main

import (
	"fmt"
	"image"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/backend"
)

func previewColorCount(tt *ui.Tester, rgb [3]uint32) int {
	bounds, ok := tt.Find("双语阅读预览")
	if !ok {
		return 0
	}
	img := tt.Image()
	rect := image.Rect(int(bounds.X), int(bounds.Y), int(bounds.X+bounds.W), int(bounds.Y+bounds.H)).Intersect(img.Bounds())
	count := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if r>>8 == rgb[0] && g>>8 == rgb[1] && b>>8 == rgb[2] {
				count++
			}
		}
	}
	return count
}

func TestNativeReadingBilingualPreviewAndSave(t *testing.T) {
	reader, err := backend.New(filepath.Join(t.TempDir(), "reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(reader)
	defer a.close()
	a.window = &mygo.Window{}
	a.settings = true
	// Search from AI must expose the reading controls and their save button.
	a.settingsState.Category, a.settingsState.Query = "ai", "双语"
	originalAI := a.ai.settings.config.clone()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, 1380, 900)
	if !tt.HasText("A little space makes a good story easier to read.") || !tt.HasText("留出一些空间，让好故事读起来更舒适。") {
		t.Fatal("bilingual preview content missing")
	}
	if _, ok := tt.Find("保存设置"); !ok {
		t.Fatal("search result has no reading save action")
	}
	if _, ok := tt.Find("保存 AI 设置"); ok {
		t.Fatal("reading search result uses AI save action")
	}
	if previewColorCount(tt, [3]uint32{243, 246, 242}) < 1000 {
		t.Fatal("soft preview background missing")
	}
	before, _ := tt.Find("双语阅读预览")
	if err := tt.Click("选择双语译文样式"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("侧线"); err != nil {
		t.Fatal(err)
	}
	if a.draft.BilingualStyle != "line" || a.prefs.BilingualStyle != "soft" {
		t.Fatal("preview selection changed saved settings before save")
	}
	if previewColorCount(tt, [3]uint32{151, 170, 159}) < 50 || previewColorCount(tt, [3]uint32{243, 246, 242}) != 0 {
		t.Fatal("preview did not switch from background to side rule")
	}
	a.draft.FontSize, a.draft.LineHeight = 26, 2.2
	tt.Frame()
	after, _ := tt.Find("双语阅读预览")
	if after.H <= before.H {
		t.Fatal("preview did not respond to draft reading metrics")
	}
	captureAIView(t, "reading-bilingual-large", tt)
	if err := tt.Click("保存设置"); err != nil {
		t.Fatal(err)
	}
	job := <-a.jobs
	workErr := job.work(a.ctx)
	snapshot, snapshotErr := reader.Snapshot()
	a.completeJob(job, snapshot, workErr, snapshotErr)
	saved, err := reader.Preferences()
	if err != nil || saved != a.draft || a.prefs != saved || saved.BilingualStyle != "line" || !a.ai.settings.config.equal(originalAI) {
		t.Fatalf("reading save changed AI config or did not persist: %+v %v", saved, err)
	}
	a.settingsState.Query = ""
	a.settingsState.Category = "ai"
	tt.Frame()
	if _, ok := tt.Find("选择双语译文样式"); ok {
		t.Fatal("style control remains in AI settings")
	}
}

func TestNativeReadingBilingualPreviewWidthsAndStyles(t *testing.T) {
	for _, width := range []int{1380, 420} {
		a := newNativeReader(nil)
		a.settings = true
		a.settingsState.Category = "reading"
		a.settingsState.Query = "双语"
		tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, width, 1000)
		for _, choice := range []struct{ id, label string }{{"line", "侧线"}, {"plain", "纯文本"}, {"soft", "浅色底"}} {
			if err := tt.Click("选择双语译文样式"); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click(choice.label); err != nil {
				t.Fatal(err)
			}
			if a.draft.BilingualStyle != choice.id {
				t.Fatal("style selection not applied")
			}
			for _, label := range []string{"选择双语译文样式", "双语阅读预览", "保存设置"} {
				b, ok := tt.Find(label)
				if !ok || b.X < 0 || b.X+b.W > float32(width)+1 || b.Y < 0 || b.Y+b.H > 1000 {
					t.Fatalf("%s at width %d overflows: %+v", label, width, b)
				}
			}
			soft := previewColorCount(tt, [3]uint32{243, 246, 242})
			line := previewColorCount(tt, [3]uint32{151, 170, 159})
			if choice.id == "plain" && (soft != 0 || line != 0) {
				t.Fatal("plain preview kept background or side rule")
			}
			captureAIView(t, fmt.Sprintf("reading-bilingual-%s-%d", choice.id, width), tt)
		}
		if err := tt.Click("恢复默认"); err != nil {
			t.Fatal(err)
		}
		if a.draft != backend.DefaultPreferences() {
			t.Fatal("restore defaults missed reading style")
		}
		a.close()
	}
}
