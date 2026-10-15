package main

import (
	"fmt"
	"image/color"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestNativeDocumentationScreenshots(t *testing.T) {
	if os.Getenv("GORSS_UI_SCREENSHOTS") == "" {
		t.Skip("set GORSS_UI_SCREENSHOTS to capture documentation previews")
	}
	a := designReader()
	defer a.close()
	a.integratedTitlebar = true
	tt := ui.NewTester(a.windowView, 1380, 860)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 82})
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	if err := tt.Click("展开 AI 摘要"); err != nil {
		t.Fatal(err)
	}
	captureAIView(t, "reader", tt)
	a.ai.summary.open = false
	a.ai.mode = "bilingual"
	a.ai.translation.open = true
	tt.Frame()
	captureAIView(t, "bilingual", tt)
	a.settings = true
	a.settingsState.Category = "reading"
	a.settingsState.Query = "双语"
	tt.Frame()
	captureAIView(t, "settings", tt)
}

func TestNativeIntegratedTitlebarLayoutAndSettings(t *testing.T) {
	for _, width := range []int{1380, 980} {
		a := designReader()
		a.integratedTitlebar = true
		tt := ui.NewTester(a.windowView, width, 860)
		tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 82})
		tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
		reserve, ok := tt.Find("窗口拖动区")
		if !ok || reserve.X != 0 || reserve.Y != 0 || reserve.H < 46 || reserve.W < 82 {
			t.Fatalf("native controls not reserved: %+v", reserve)
		}
		list, _ := tt.Find("列表窗口拖动区")
		reading, _ := tt.Find("阅读窗口拖动区")
		if list.Y >= 46 || reading.Y != 0 || reading.H < 46 {
			t.Fatalf("extra full-width title strip remains: list %+v, reading %+v", list, reading)
		}
		for _, pane := range []struct {
			label string
			face  ui.Color
		}{{"文章列表栏", nativeListSurface}, {"文章阅读栏", nativePaperSurface}} {
			bounds, _ := tt.Find(pane.label)
			got := color.RGBAModel.Convert(tt.Image().At(int(bounds.X+4), 2))
			want := color.RGBA{R: pane.face.R, G: pane.face.G, B: pane.face.B, A: 255}
			if got != want {
				t.Fatal("pane surface does not reach window top", pane.label, got)
			}
		}
		// Interactive descendants must still receive clicks inside drag headers.
		if err := tt.Click("取消收藏"); err != nil {
			t.Fatal(err)
		}
		if a.pending != 1 || len(a.jobs) != 1 {
			t.Fatal("window drag header swallowed star button")
		}
		<-a.jobs
		a.pending = 0
		captureAIView(t, fmt.Sprintf("integrated-titlebar-%d", width), tt)
		a.settings = true
		a.settingsState.Category = "reading"
		tt.Frame()
		drag, ok := tt.Find("设置内容窗口拖动区")
		back, found := tt.Find("返回阅读")
		if !ok || drag.Y != 0 || drag.H < 46 || !found || back.Y+back.H > drag.H {
			t.Fatalf("settings titlebar retained a full-width gap: %+v %+v", drag, back)
		}
		if reserve, ok := tt.Find("设置窗口拖动区"); width >= 1100 {
			if !ok || reserve.Y != 0 || reserve.H < 46 || reserve.W >= float32(width) {
				t.Fatalf("settings sidebar does not reserve native controls: %+v", reserve)
			}
		} else if ok {
			t.Fatal("narrow settings retained a separate titlebar gap")
		}
		if width < 1100 {
			if title, ok := tt.Find("设置"); !ok || title.X < 94 {
				t.Fatalf("narrow settings title overlaps native controls: %+v", title)
			}
		}
		if footer, ok := tt.Find("保存设置"); !ok || footer.Y+footer.H > 860 {
			t.Fatalf("settings footer clipped below window: %+v", footer)
		}
		captureAIView(t, fmt.Sprintf("integrated-settings-%d", width), tt)
		tt.SetTitleBar(ui.TitleBar{})
		if _, ok := tt.Find("设置窗口拖动区"); ok {
			t.Fatal("full-screen settings retained titlebar gap")
		}
		if err := tt.Click("返回阅读"); err != nil {
			t.Fatal(err)
		}
		if a.settings {
			t.Fatal("return button blocked by titlebar")
		}
		if _, ok := tt.Find("窗口拖动区"); ok {
			t.Fatal("full-screen sidebar retained titlebar gap")
		}
		captureAIView(t, fmt.Sprintf("integrated-fullscreen-%d", width), tt)
		a.close()
	}
}

func TestNativeIntegratedSettingsNavigationAndSearch(t *testing.T) {
	for _, width := range []int{1380, 980} {
		a := designReader()
		a.integratedTitlebar, a.settings = true, true
		a.settingsState.Category = "reading"
		tt := ui.NewTester(a.windowView, width, 860)
		tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 82})
		tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
		a.settingsState.Query = "双语译文样式"
		tt.Frame()
		if !tt.HasText("双语预览") || tt.HasText("自定义字体") || a.settingsState.Category != "reading" {
			t.Fatal("settings search lost filtering, preview or selected category")
		}
		if width < 1100 {
			if err := tt.Click("类别"); err != nil {
				t.Fatal(err)
			}
		}
		if err := tt.Click("订阅管理"); err != nil {
			t.Fatal(err)
		}
		if a.settingsState.Query != "" || a.settingsState.Category != "subscriptions" || !tt.HasText("导入订阅") {
			t.Fatal("category navigation failed to clear search or show subscription controls")
		}
		if _, ok := tt.Find("恢复默认"); ok {
			t.Fatal("reading footer leaked into subscription settings")
		}
		if width < 1100 {
			if err := tt.Click("类别"); err != nil {
				t.Fatal(err)
			}
		}
		if err := tt.Click("AI"); err != nil {
			t.Fatal(err)
		}
		if a.settingsState.Category != "ai" || !tt.HasText("自定义 API") {
			t.Fatal("AI settings lost provider controls")
		}
		if footer, ok := tt.Find("保存 AI 设置"); !ok || footer.Y+footer.H > 860 {
			t.Fatalf("AI footer clipped below window: %+v", footer)
		}
		a.settingsState.Query = "no-such-setting"
		tt.Frame()
		if !tt.HasText("没有匹配的设置") {
			t.Fatal("empty search result missing")
		}
		a.close()
	}
}

func TestNativeIntegratedTitlebarEmptyReadingAndDialog(t *testing.T) {
	a := designReader()
	defer a.close()
	a.integratedTitlebar = true
	a.articleID = ""
	tt := ui.NewTester(a.windowView, 980, 700)
	tt.SetTitleBar(ui.TitleBar{Height: 46, Left: 82})
	if b, ok := tt.Find("阅读窗口拖动区"); !ok || b.Y != 0 || b.H < 46 {
		t.Fatal("empty reader lost drag area", b)
	}
	a.addOpen = true
	tt.Frame()
	if _, ok := tt.Find("添加订阅"); !ok {
		t.Fatal("dialog missing under integrated chrome")
	}
	tt.Key(0, ui.KeyEscape)
	if a.addOpen {
		t.Fatal("dialog Escape no longer works")
	}
}
