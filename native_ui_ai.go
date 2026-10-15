package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"gorss/aiservice"
	"gorss/backend"
)

// All state here belongs to the UI thread. Each operation has its own context
// and token; completions can never publish into another article or configuration.
type nativeAIResult struct {
	text, errorText  string
	document         *markdown.MarkdownDocument
	loading, open    bool
	finished         bool
	copy             nativeCopyFeedback
	cancel           context.CancelFunc
	generation       uint64
	translation      *aiservice.TranslationResult
	completed, total int
	verifiedText     string
}

func (r *nativeAIResult) stop() {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.generation++
	r.loading = false
}
func (r *nativeAIResult) clear() {
	r.stop()
	r.text, r.errorText = "", ""
	r.document = nil
	r.translation = nil
	r.completed, r.total = 0, 0
	r.verifiedText = ""
	r.open = false
	r.finished = false
	r.copy = nativeCopyFeedback{}
}

type nativeAIState struct {
	// Tests can supply an isolated credential store and cache directory.
	serviceFactory                         func(aiservice.Config) (*aiservice.Service, error)
	settings                               nativeAISettings
	credentialMutations                    int
	summary, translation, selection        nativeAIResult
	sourceKey, selectionText               string
	mode                                   string
	cache                                  map[string]nativeAICacheEntry
	source                                 string
	bilingualSource                        string
	bilingualOriginal, bilingualTranslated []*markdown.MarkdownDocument
	bilingualSame                          []bool
}

type nativeAICacheEntry struct {
	summary     string
	translation *aiservice.TranslationResult
}

func nativeAISource(article backend.Article) string {
	source := article.Content
	if strings.TrimSpace(source) == "" {
		source = article.Summary
	}
	if articleHasHTML(source) {
		source = htmlMarkdown(source)
	}
	return strings.TrimSpace(source)
}
func (a *nativeReader) resetAI() {
	a.ai.summary.clear()
	a.ai.translation.clear()
	a.ai.selection.clear()
	a.ai.selectionText, a.ai.mode, a.ai.sourceKey = "", "original", ""
	a.ai.source, a.ai.bilingualSource = "", ""
	a.ai.bilingualOriginal, a.ai.bilingualTranslated = nil, nil
	a.ai.bilingualSame = nil
}
func nativeAISourceIdentity(article backend.Article) string {
	data, _ := json.Marshal([]string{article.ID, article.Content, article.Summary})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func nativeAICacheKey(sourceIdentity string, config nativeAIConfig) string {
	data, _ := json.Marshal(struct {
		Source, PromptVersion string
		Config                nativeAIConfig
	}{sourceIdentity, aiservice.PromptVersion, config})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (a *nativeReader) aiReady() bool {
	return a.ai.settings.loaded && a.ai.credentialMutations == 0 && a.ai.settings.config.enabled()
}

func (a *nativeReader) syncAIArticle(article backend.Article) {
	// Hash the complete feed source so refreshed content invalidates old results.
	key := nativeAISourceIdentity(article)
	if a.ai.sourceKey != key {
		a.resetAI()
		a.ai.sourceKey = key
		a.ai.source = nativeAISource(article)
	}
}
func (a *nativeReader) closeSelectionAI() { a.ai.selection.clear(); a.ai.selectionText = "" }
func (a *nativeReader) openSummaryAI() {
	r := &a.ai.summary
	if r.finished && r.errorText == "" && r.text != "" {
		r.open = true
		return
	}
	a.requestAI("summary", false)
}
func (a *nativeReader) closeSummaryAI() { a.ai.summary.stop(); a.ai.summary.open = false }

func (a *nativeReader) requestAI(kind string, regenerate bool) {
	if !a.aiReady() {
		return
	}
	article := a.article()
	if article == nil {
		return
	}
	a.syncAIArticle(*article)
	source := a.ai.source
	var result *nativeAIResult
	switch kind {
	case "summary":
		result = &a.ai.summary
	case "translation":
		result = &a.ai.translation
	case "selection":
		result = &a.ai.selection
		source = a.ai.selectionText
	default:
		return
	}
	if source == "" {
		return
	}
	if result.loading {
		return
	}
	cacheKey := nativeAICacheKey(a.ai.sourceKey, a.ai.settings.config)
	retrying := result.errorText != ""
	result.stop()
	result.open, result.loading, result.errorText = true, true, ""
	result.finished = false
	result.copy = nativeCopyFeedback{}
	ctx, cancel := context.WithCancel(a.ctx)
	result.cancel = cancel
	token, sourceKey := result.generation, a.ai.sourceKey
	config := a.ai.settings.config.clone()
	selected := a.ai.selectionText
	resume := result.translation
	// A retry retains valid batches; explicit regeneration clears them.
	if regenerate && !retrying {
		resume = nil
	}
	if kind != "translation" || (regenerate && !retrying) {
		result.text, result.verifiedText = "", ""
		result.document = nil
		result.completed, result.total = 0, 0
	}
	go func() {
		var translated *aiservice.TranslationResult
		var output string
		var err error
		if kind == "translation" {
			service, serviceErr := a.newAIService(config)
			err = serviceErr
			if err == nil {
				if regenerate && !retrying {
					service.ForceRefresh()
				}
				value, translationErr := service.TranslateMarkdown(ctx, source, aiservice.TranslationOptions{Resume: resume, Stream: true, OnProgress: func(progress aiservice.TranslationProgress) {
					doc := markdown.ParseMarkdown(progress.Markdown)
					a.window.Update(func() {
						if ctx.Err() != nil || result.generation != token || a.ai.sourceKey != sourceKey {
							return
						}
						result.completed, result.total = progress.Completed, progress.Total
						result.text, result.document = progress.Markdown, doc
						if !progress.Preview {
							result.verifiedText = progress.Markdown
						}
					})
				}})
				translated = &value
				output, err = value.Markdown, translationErr
			}
		} else {
			output, err = a.executeAI(ctx, config, kind, source, selected, regenerate && !retrying, func(value string) {
				doc := markdown.ParseMarkdown(value)
				a.window.Update(func() {
					if ctx.Err() != nil || result.generation != token || a.ai.sourceKey != sourceKey || a.ctx.Err() != nil {
						return
					}
					result.text, result.document = value, doc
				})
			})
		}
		var document *markdown.MarkdownDocument
		if output != "" {
			document = markdown.ParseMarkdown(output)
		}
		a.window.Update(func() {
			if ctx.Err() != nil || result.generation != token || a.ai.sourceKey != sourceKey || a.ctx.Err() != nil {
				return
			}
			result.loading, result.cancel = false, nil
			cancel()
			if translated != nil {
				result.translation = translated
				result.completed, result.total = translated.Completed, translated.Total
			}
			if output != "" {
				result.text, result.document = output, document
			}
			if err != nil {
				result.errorText = err.Error()
				return
			}
			result.text, result.document = output, document
			result.finished = true
			if a.ai.cache == nil {
				a.ai.cache = make(map[string]nativeAICacheEntry)
			}
			if len(a.ai.cache) >= 64 {
				clear(a.ai.cache)
			}
			entry := a.ai.cache[cacheKey]
			if kind == "summary" {
				entry.summary = output
			}
			if kind == "translation" {
				entry.translation = translated
			}
			a.ai.cache[cacheKey] = entry
		})
	}()
}

func (a *nativeReader) aiToolbar(c *ui.Context, article backend.Article) {
	a.syncAIArticle(article)
	available := !a.aiReady() || a.ai.source != ""
	nativeIconButton(c, "AI 摘要", lucide.Sparkles(), func() {
		if !a.aiReady() {
			a.openAISettingsGuide()
			return
		}
		if a.ai.summary.open {
			a.closeSummaryAI()
		} else {
			a.openSummaryAI()
		}
	}).Disabled(!available)
	nativeIconButton(c, "翻译文章", lucide.Languages(), func() {
		if !a.aiReady() {
			a.openAISettingsGuide()
			return
		}
		if a.ai.mode == "original" {
			a.ai.mode = "translated"
			a.requestAI("translation", false)
		} else {
			a.ai.mode = "original"
		}
	}).Disabled(!available)
}
func (a *nativeReader) aiSummaryView(c *ui.Context, article backend.Article) {
	if !a.aiReady() || a.ai.source == "" {
		return
	}
	r := &a.ai.summary
	ui.Column(c).Key("summary-disclosure").FillWidth().MinWidth(0).Padding(10, 12).Gap(2).Radius(10).Background(nativeSummarySurface).Children(func() {
		var disclosure ui.CollapsibleParts
		ui.Row(c).FillWidth().MinWidth(0).Font("").FontSize(nativeUIBodySize).Gap(4).AlignItems(ui.Center).Children(func() {
			disclosure = ui.CollapsibleBase(c, &r.open)
			trigger := disclosure.Trigger.Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(8).Padding(7, 4).Radius(6)
			label := "展开 AI 摘要"
			if r.open {
				label = "收起 AI 摘要"
			}
			trigger.Label(label)
			if trigger.Changed() {
				if r.open {
					a.openSummaryAI()
				} else {
					a.closeSummaryAI()
				}
			}
			trigger.Children(func() {
				components.Icon(c, lucide.Sparkles()).Size(16, 16).TextColor(c.Theme().Accent)
				components.Text(c, "AI 摘要").FontWeight(600).Grow(1).SingleLine()
				components.Icon(c, lucide.ChevronRight()).Size(14, 14).TextColor(c.Theme().TextMuted).Rotate(90 * disclosure.Progress())
			})
			if r.open {
				nativeCopyButton(c, "复制摘要", r.text, r.loading || r.text == "", true, &r.copy)
				nativeIconButton(c, "重新生成摘要", lucide.RotateCw(), func() { a.requestAI("summary", true) }).Size(30, 30).Disabled(r.loading)
				nativeIconButton(c, "关闭摘要", lucide.X(), a.closeSummaryAI).Size(30, 30)
			}
		})
		disclosure.Panel(func() {
			components.Text(c, "基于订阅提供的可用内容生成，可能不是完整原文。").Font("").Wrap().TextColor(c.Theme().TextMuted).FontSize(12).LineHeight(1.5)
			a.aiResultView(c, r, article.URL, func() { a.requestAI("summary", true) })
		}).FillWidth().MinWidth(0).Padding(4, 4, 8).Gap(10)
	})
}
func (a *nativeReader) aiResultView(c *ui.Context, r *nativeAIResult, base string, retry func()) {
	a.aiResultStatus(c, r, retry)
	if r.document != nil {
		a.aiSelectionContextMenu(renderArticleMarkdown(c, r.document, a.prefs, markdown.MarkdownOptions{CodeHeaders: true, CopyCodeLabel: "复制代码", Images: a.images, OnLink: func(url string) { a.openLink(url, base) }}))
	}
}
func (a *nativeReader) stopAIResult(r *nativeAIResult) {
	r.stop()
	r.errorText = "已停止生成，可重试。"
	r.copy = nativeCopyFeedback{}
	if r == &a.ai.translation {
		r.text = r.verifiedText
		if r.text == "" || r.text == a.ai.source {
			r.document = nil
		} else {
			r.document = markdown.ParseMarkdown(r.text)
		}
	}
}
func (a *nativeReader) aiResultStatus(c *ui.Context, r *nativeAIResult, retry func()) {
	if r.loading {
		ui.Row(c).Gap(8).Children(func() {
			components.Spinner(c, components.SpinnerSpokes).Size(16, 16)
			label := "正在生成…"
			if r.total > 0 {
				label = fmt.Sprintf("正在翻译 %d / %d…", r.completed, r.total)
			}
			components.Text(c, label).TextColor(c.Theme().TextMuted)
			components.Button(c, "停止生成", components.ButtonGhost).OnClick(func() { a.stopAIResult(r) })
		})
	}
	if r.errorText != "" {
		components.Text(c, r.errorText).Wrap().TextColor(c.Theme().Danger)
		components.Button(c, "重试", components.ButtonSecondary).OnClick(retry)
	}
}
func (a *nativeReader) aiTranslationView(c *ui.Context, article backend.Article) bool {
	r := &a.ai.translation
	if a.ai.mode == "" {
		a.ai.mode = "original"
	}
	if r.open || a.ai.mode != "original" {
		ui.Row(c).FillWidth().MinWidth(0).Font("").FontSize(nativeUIBodySize).Gap(8).AlignItems(ui.Center).Children(func() {
			modes := []string{"original", "translated", "bilingual"}
			selected := 0
			for i, mode := range modes {
				if a.ai.mode == mode {
					selected = i
				}
			}
			tabs := nativeSegmented(c, &selected, "译文显示模式", "原文", "译文", "双语对照")
			if tabs.List.Changed() {
				a.ai.mode = modes[selected]
				if a.ai.mode != "original" {
					a.requestAI("translation", false)
				}
			}
			nativeIconButton(c, "重新翻译", lucide.RotateCw(), func() { a.requestAI("translation", true) }).Disabled(r.loading)
			nativeIconButton(c, "关闭翻译", lucide.X(), func() { r.clear(); a.ai.mode = "original" })
		})
	}
	if a.ai.mode == "original" {
		return false
	}
	if r.loading || r.errorText != "" {
		a.aiResultStatus(c, r, func() { a.requestAI("translation", true) })
		if r.document != nil && (r.completed > 0 || (r.text != "" && r.text != a.ai.source)) {
			if a.ai.mode == "bilingual" {
				a.renderBilingualAI(c, article)
			} else {
				a.renderAITranslationDocument(c, article)
			}
			return true
		}
		return false
	}
	if r.document == nil {
		return false
	}
	if a.ai.mode == "bilingual" {
		// Pair the structurally preserved backend translation with source blocks.
		a.renderBilingualAI(c, article)
	} else {
		a.aiResultView(c, r, article.URL, func() { a.requestAI("translation", true) })
	}
	return true
}

// Pair top-level Markdown blocks. Each side retains its own Markdown syntax,
// including lists, tables, quotations, links and code. Pairing requires no model markup.
func nativeAIBlocks(source string) []string {
	data := []byte(source)
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	var starts []int
	for node := root.FirstChild(); node != nil; node = node.NextSibling() {
		start := len(data)
		ast.Walk(node, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
			if !enter {
				return ast.WalkContinue, nil
			}
			if n.Type() == ast.TypeBlock {
				lines := n.Lines()
				if lines != nil && lines.Len() > 0 {
					start = min(start, lines.At(0).Start)
				}
			}
			if value, ok := n.(*ast.Text); ok {
				start = min(start, value.Segment.Start)
			}
			return ast.WalkContinue, nil
		})
		if start == len(data) {
			continue
		}
		for start > 0 && data[start-1] != '\n' {
			start--
		}
		if _, ok := node.(*ast.FencedCodeBlock); ok && start > 0 {
			start--
			for start > 0 && data[start-1] != '\n' {
				start--
			}
		}
		starts = append(starts, start)
	}
	if len(starts) == 0 {
		return []string{source}
	}
	starts[0] = 0
	var blocks []string
	for i, start := range starts {
		end := len(source)
		if i+1 < len(starts) {
			end = starts[i+1]
		}
		blocks = append(blocks, strings.TrimSpace(source[start:end]))
	}
	return blocks
}
func (a *nativeReader) renderAITranslationDocument(c *ui.Context, article backend.Article) {
	if a.ai.translation.document != nil {
		a.aiSelectionContextMenu(renderArticleMarkdown(c, a.ai.translation.document, a.prefs, markdown.MarkdownOptions{CodeHeaders: true, CopyCodeLabel: "复制代码", Images: a.images, OnLink: func(url string) { a.openLink(url, article.URL) }}))
	}
}
func (a *nativeReader) renderBilingualAI(c *ui.Context, article backend.Article) {
	if a.ai.bilingualSource != a.ai.translation.text {
		original, translated := nativeAIBlocks(a.ai.source), nativeAIBlocks(a.ai.translation.text)
		if len(original) != len(translated) {
			// Never silently misalign paragraphs if a future parser changes block count.
			components.Text(c, "双语段落无法对齐，请使用原文或译文视图。").Wrap().TextColor(c.Theme().TextMuted)
			a.renderAITranslationDocument(c, article)
			return
		}
		a.ai.bilingualSource = a.ai.translation.text
		a.ai.bilingualOriginal, a.ai.bilingualTranslated = nil, nil
		a.ai.bilingualSame = nil
		for i := range original {
			a.ai.bilingualSame = append(a.ai.bilingualSame, original[i] == translated[i])
			a.ai.bilingualOriginal = append(a.ai.bilingualOriginal, markdown.ParseMarkdown(original[i]))
			a.ai.bilingualTranslated = append(a.ai.bilingualTranslated, markdown.ParseMarkdown(translated[i]))
		}
	}
	options := markdown.MarkdownOptions{CodeHeaders: true, CopyCodeLabel: "复制代码", Images: a.images, OnLink: func(url string) { a.openLink(url, article.URL) }}
	for i := range a.ai.bilingualOriginal {
		ui.Column(c).FillWidth().MinWidth(0).Gap(10).Children(func() {
			a.aiSelectionContextMenu(renderArticleMarkdown(c, a.ai.bilingualOriginal[i], a.prefs, options))
			if !a.ai.bilingualSame[i] {
				a.bilingualTranslation(c, a.ai.bilingualTranslated[i], options)
			}
		})
	}
}

func (a *nativeReader) bilingualTranslation(c *ui.Context, document *markdown.MarkdownDocument, options markdown.MarkdownOptions) {
	a.aiSelectionContextMenu(nativeBilingualTranslation(c, document, a.prefs, options))
}

// Reading previews and article translations use the same styling and metrics.
func nativeBilingualTranslation(c *ui.Context, document *markdown.MarkdownDocument, prefs backend.Preferences, options markdown.MarkdownOptions) ui.Element {
	panel := ui.Column(c).FillWidth().MinWidth(0).Gap(8).Label("双语译文")
	switch prefs.BilingualStyle {
	case "plain":
		panel.Padding(4, 0, 12)
	case "line":
		panel.Padding(8, 0, 8, 16).BorderWidth(0, 0, 0, 2).BorderColor(ui.RGB(151, 170, 159))
	default:
		panel.Padding(14, 18).Radius(8).Background(ui.RGB(243, 246, 242))
	}
	panel.Children(func() {
		components.Text(c, "译文").FontSize(11).FontWeight(500).TextColor(c.Theme().TextMuted)
		renderArticleMarkdown(c, document, prefs, options)
	})
	return panel
}

// ContextMenu invokes its builder during this build pass. The element is never
// retained across frames; SelectedTexts reads the container's actual selection.
func (a *nativeReader) aiSelectionContextMenu(container ui.Element) ui.Element {
	return container.ContextMenu(func(m *ui.Menu) {
		a.aiSelectionMenu(m, nativeSelectedText(container.SelectedTexts()))
	})
}

func nativeSelectedText(parts []ui.SelectedText) string {
	var selected strings.Builder
	for _, part := range parts {
		runes := []rune(part.Text)
		if part.Start < 0 || part.End > len(runes) || part.Start >= part.End {
			continue
		}
		if selected.Len() > 0 {
			selected.WriteByte('\n')
		}
		selected.WriteString(string(runes[part.Start:part.End]))
	}
	return selected.String()
}

func (a *nativeReader) aiSelectionMenu(m *ui.Menu, text string) {
	m.EditItems()
	selected := strings.TrimSpace(text)
	if selected == "" || !a.aiReady() {
		return
	}
	m.Separator()
	valid := utf8.RuneCountInString(selected) <= nativeAISelectionLimit
	label := "翻译所选文字"
	if !valid {
		label = fmt.Sprintf("选中文字过长（最多 %d 字）", nativeAISelectionLimit)
	}
	if m.Item(label).Disabled(!valid).Chosen() {
		a.closeSelectionAI()
		a.ai.selectionText = selected
		a.requestAI("selection", false)
	}
}

const nativeAISelectionLimit = 4000

func (a *nativeReader) aiSelectionView(c *ui.Context) {
	r := &a.ai.selection
	components.Dialog(c, &r.open, components.DialogOptions{Label: "划词翻译"}, func(panel ui.Element) {
		panel.Width(560).MaxWidth(nativeDialogWidth(c)).Gap(12)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			components.Text(c, "划词翻译").Bold().Grow(1)
			nativeIconButton(c, "关闭划词翻译", lucide.X(), a.closeSelectionAI)
		})
		components.Text(c, shortText(a.ai.selectionText, 160)).Wrap().TextColor(c.Theme().TextMuted)
		components.ScrollView(c, components.ScrollViewOptions{}).MaxHeight(380).FillWidth().Children(func() { a.aiResultView(c, r, "", func() { a.requestAI("selection", true) }) })
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			nativeCopyButton(c, "复制译文", r.text, r.loading || r.text == "", false, &r.copy)
			components.Button(c, "重试", components.ButtonGhost).Disabled(r.loading).OnClick(func() { a.requestAI("selection", true) })
			components.Button(c, "关闭", components.ButtonGhost).OnClick(a.closeSelectionAI)
		})
	})
	// Clicking outside the dialog is also a close, and must cancel pending work.
	if !r.open && r.loading {
		a.closeSelectionAI()
	}
}

func (a *nativeReader) openAISettingsGuide() {
	a.settings = true
	a.settingsState.Category, a.settingsState.Query = "ai", ""
	a.notice = "请在 AI 设置选择服务、启用并填写模型 ID，然后保存。"
}
