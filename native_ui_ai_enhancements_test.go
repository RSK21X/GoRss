package main

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/aiservice"
	"gorss/backend"
)

func captureAIView(t *testing.T, name string, tt *ui.Tester) {
	t.Helper()
	dir := os.Getenv("GORSS_UI_SCREENSHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAIProviderCreationPromptEditingAndDiscard(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.settings = true
	a.settingsState.Category = "ai"
	s := &a.ai.settings
	s.loaded = true
	s.config = nativeAIConfig(aiservice.DefaultConfig())
	s.draft = s.config.clone()
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.settingsView(c) }, 1380, 1000)
	if err := tt.Click("添加供应商"); err != nil {
		t.Fatal(err)
	}
	p := s.draft.ActiveProvider
	if len(s.draft.Providers) != 1 || p == aiservice.Custom || len(s.config.Providers) != 0 {
		t.Fatal("new provider modified saved configuration")
	}
	pc := (*aiservice.Config)(&s.draft).ProviderDraft(p)
	pc.Name = "工作服务"
	pc.BaseURL = "https://ai.example.org/v1"
	pc.Model = "work-model"
	tt.Frame()
	if err := tt.Click("提示词"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("摘要提示词编辑器"); err != nil {
		t.Fatal(err)
	}
	tt.Type("用三点概括文章，使用 {language}。")
	if !strings.Contains(pc.SummaryPrompt, "三点") {
		t.Fatal("prompt editor did not update the selected provider")
	}
	if err := tt.Click("填入默认翻译"); err != nil {
		t.Fatal(err)
	}
	if pc.TranslationPrompt != aiservice.DefaultTranslationPrompt {
		t.Fatal("default translation prompt did not populate")
	}
	captureAIView(t, "ai-prompts", tt)
	if err := tt.Click("当前 AI 服务"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Magpie"); err != nil {
		t.Fatal(err)
	}
	if s.draft.Magpie.SummaryPrompt != "" || (*aiservice.Config)(&s.draft).ProviderDraft(p).SummaryPrompt == "" {
		t.Fatal("provider prompts alias")
	}
	if err := tt.Click("放弃更改"); err != nil {
		t.Fatal(err)
	}
	if !s.draft.equal(s.config) || len(s.draft.Providers) != 0 || s.draft.ActiveProvider != aiservice.Custom {
		t.Fatal("discard lost saved provider state")
	}
	// Search must expose prompt editors even when the connection tab is active.
	s.tab = "connection"
	a.settingsState.Query = "摘要提示词"
	tt.Frame()
	if _, ok := tt.Find("摘要提示词编辑器"); !ok {
		t.Fatal("prompt editor cannot be found through settings search")
	}
}

func TestNativeAIDraftsAndRuntimeStateAreProviderScoped(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	s := &a.ai.settings
	s.loaded = true
	s.config = nativeAIConfig(aiservice.DefaultConfig())
	s.draft = s.config.clone()
	a.addAIProvider()
	p := s.draft.ActiveProvider
	s.draft.Providers[0].Config.BaseURL = "https://one.example/v1"
	s.draft.Providers[0].Config.Model = "first"
	s.config = s.draft.clone()
	s.draft = s.config.clone()
	s.draft.Providers[0].Config.Model = "edited"
	if s.config.Providers[0].Config.Model != "first" {
		t.Fatal("saved provider and draft share mutable storage")
	}
	a.addAIProvider()
	other := s.draft.ActiveProvider
	state := s.provider(p)
	state.key = "ephemeral-one"
	s.provider(other).key = "ephemeral-two"
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	state.busy = true
	a.removeAIProvider(p)
	if ctx.Err() == nil || state.key != "" || state.busy || s.provider(other).key != "ephemeral-two" || len(s.config.Providers) != 1 {
		t.Fatal("removal affected another provider or saved config")
	}
	a.cancelAISettings()
	if s.provider(other).key != "" {
		t.Fatal("leaving settings retained a draft credential")
	}
}

func TestNativeAIStreamShowsTextBeforeCompletionAndStops(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	r := &a.ai.summary
	r.loading, r.open = true, true
	r.text = "流式摘要已经收到第一段。"
	r.document = markdown.ParseMarkdown(r.text)
	ctx, cancel := context.WithCancel(a.ctx)
	r.cancel = cancel
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.aiResultView(c, r, "", func() {}) }, 700, 400)
	if !tt.HasText(r.text) || !tt.HasText("正在生成…") {
		t.Fatal("partial stream remains hidden behind the loading state")
	}
	if err := tt.Click("停止生成"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || r.loading || r.errorText == "" {
		t.Fatal("stop did not cancel the stream")
	}
	a.ai.source = "Original"
	r = &a.ai.translation
	r.loading = true
	r.text = "未验证预览"
	r.verifiedText = "已验证译文"
	r.document = markdown.ParseMarkdown(r.text)
	a.stopAIResult(r)
	if r.text != "已验证译文" {
		t.Fatal("stopping translation promoted provisional text")
	}
}

func TestNativeAIBilingualStylesAndVisuals(t *testing.T) {
	for _, style := range []string{"soft", "line", "plain"} {
		a := newNativeReader(nil)
		a.ai.settings.config = nativeAIConfig(aiservice.DefaultConfig())
		a.prefs.BilingualStyle = style
		a.ai.source = "## A quieter way to read\n\nGood tools leave room for the story. Read at your own pace, with fewer interruptions.\n\n> Keep the original close to the translation.\n\n```go\nfmt.Println(42)\n```"
		a.ai.translation.text = "## 让阅读安静下来\n\n好的工具为内容留出空间。按自己的节奏阅读，少一些打扰。\n\n> 让原文与译文保持相邻，便于对照。\n\n```go\nfmt.Println(42)\n```"
		tt := ui.NewTester(func(c *ui.Context) {
			theme.Apply(c, theme.Light)
			ui.Column(c).Fill().Padding(28).Gap(18).Children(func() { a.renderBilingualAI(c, backend.Article{}) })
		}, 820, 1000)
		if !tt.HasText("A quieter way to read") || !tt.HasText("让阅读安静下来") || !tt.HasText("译文") {
			t.Fatalf("bilingual style %s lost content", style)
		}
		count := 0
		for _, text := range tt.Texts() {
			if text == "fmt.Println(42)" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("unchanged code duplicated in %s: %d", style, count)
		}
		captureAIView(t, "bilingual-"+style, tt)
		a.close()
	}
}

func TestNativeAIModelSearchAndSelection(t *testing.T) {
	selected := "old"
	models := []aiservice.Model{{ID: "fast-model"}, {ID: "reasoning-model", DisplayName: "推理模型"}}
	tt := ui.NewTester(func(c *ui.Context) {
		theme.Apply(c, theme.Light)
		nativeAIModelSelect(c, &selected, "工作服务", models)
	}, 600, 500)
	if err := tt.Click("工作服务 可用模型"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("工作服务 搜索模型"); err != nil {
		t.Fatal(err)
	}
	tt.Type("推理")
	if tt.HasText("fast-model") || !tt.HasText("推理模型 · reasoning-model") {
		t.Fatal("model search did not filter by display name")
	}
	if err := tt.Click("推理模型 · reasoning-model"); err != nil {
		t.Fatal(err)
	}
	if selected != "reasoning-model" {
		t.Fatal("filtered model selection failed")
	}
	if err := tt.Click("工作服务 可用模型"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("fast-model") {
		t.Fatal("model search was not reset on reopen")
	}
}

func TestNativeAIProviderRemovalEscapeKeepsSettingsOpen(t *testing.T) {
	a := newNativeReader(nil)
	defer a.close()
	a.settings = true
	a.settingsState.Category = "ai"
	a.ai.settings.loaded = true
	a.ai.settings.config = nativeAIConfig(aiservice.DefaultConfig())
	a.ai.settings.draft = a.ai.settings.config.clone()
	a.addAIProvider()
	a.ai.settings.removeOpen = true
	tt := ui.NewTester(func(c *ui.Context) { theme.Apply(c, theme.Light); a.view(c) }, 1380, 1000)
	tt.Key(0, ui.KeyEscape)
	if a.ai.settings.removeOpen || !a.settings || len(a.ai.settings.draft.Providers) != 1 {
		t.Fatal("Escape removed a provider or closed the settings page")
	}
}
