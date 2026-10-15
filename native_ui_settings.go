package main

import (
	"context"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"gorss/backend"
)

func (a *nativeReader) settingsView(c *ui.Context) {
	fonts := installedNativeFonts.get(enumerateNativeFontFamilies)
	categories := []components.SettingsCategory{{
		ID: "reading", Title: "阅读", Icon: lucide.BookOpen(), Description: "文章排版与图片", Groups: []components.SettingsGroup{{
			ID: "typography", Title: "文章排版", Items: []components.SettingsItem{
				{ID: "family", Title: "自定义字体", Description: fonts.description(), Build: func(c *ui.Context) {
					nativeFontDropdown(c, &a.draft.FontFamily, fonts)
				}},
				{ID: "size", Title: "字号", Description: "文章正文字号（像素）。", Build: func(c *ui.Context) { components.NumberInput(c, &a.draft.FontSize, 12, 32, 1).Label("文章字号") }},
				{ID: "lineHeight", Title: "行距", Description: "相对于字号的行高倍数。", Build: func(c *ui.Context) {
					components.NumberInput(c, &a.draft.LineHeight, 1.2, 2.4, .1).Label("文章行距")
				}},
				{ID: "width", Title: "内容宽度", Description: "文章最大宽度（像素）。窗口较窄时自动收缩。", Build: func(c *ui.Context) {
					components.NumberInput(c, &a.draft.ContentWidth, 420, 1100, 20).Label("文章内容宽度")
				}},
				{ID: "preview", Title: "排版预览", Build: func(c *ui.Context) {
					ui.Column(c).Width(260).Shrink(0).MinWidth(0).Padding(16).Border(1, c.Theme().Border).Radius(8).Font(a.draft.FontFamily).FontSize(float32(a.draft.FontSize)).LineHeight(float32(a.draft.LineHeight)).Children(func() {
						components.Text(c, "好的阅读，从舒适的排版开始。\nA little space for a good story.").Wrap()
					})
				}},
			},
		}, {ID: "bilingual", Title: "双语阅读", Items: []components.SettingsItem{
			{ID: "bilingual-style", Title: "双语译文样式", Description: "译文与原文使用相同字号和行距，通过背景或侧线区分。", Build: func(c *ui.Context) {
				controls := ui.Column(c).MinWidth(0)
				if nativeSettingsNarrow(c) {
					controls.FillWidth()
				} else {
					controls.Width(320).Shrink(0)
				}
				controls.Children(func() { nativeBilingualStyleSelect(c, &a.draft.BilingualStyle) })
			}},
			{ID: "bilingual-preview", Title: "双语预览", Description: "随字体、字号、行距和译文样式实时更新，无需请求 AI。", Keywords: []string{"双语译文样式"}, Build: func(c *ui.Context) { a.bilingualReadingPreview(c) }},
		}}, {ID: "content", Title: "内容与外观", Items: []components.SettingsItem{
			{ID: "images", Title: "显示文章图片", Description: "显示 Markdown 与 RSS 正文中的图片。关闭后保留替代文字。", Build: func(c *ui.Context) {
				components.Switch(c, &a.draft.ShowImages, "显示文章图片", components.SwitchOptions{HideLabel: true}).Shrink(0)
			}},
			{ID: "appearance", Title: "浅色外观", Description: "阅读界面与设置页始终使用浅色主题。", Build: func(c *ui.Context) {
				components.Text(c, "浅色").SingleLine().Shrink(0).TextColor(c.Theme().TextMuted)
			}},
		}}},
	}, {
		ID: "subscriptions", Title: "订阅管理", Icon: lucide.Rss(), Description: "导入与备份订阅", Groups: []components.SettingsGroup{{
			ID: "opml", Title: "OPML", Items: []components.SettingsItem{
				{ID: "import", Title: "导入订阅", Description: "从 OPML 文件添加 RSS 或 Atom 订阅。", Build: func(c *ui.Context) {
					components.Button(c, "导入 OPML", components.ButtonSecondary).Disabled(a.pending > 0).OnClick(a.importOPML)
				}},
				{ID: "export", Title: "备份订阅", Description: "将订阅地址导出为 OPML 文件。", Build: func(c *ui.Context) {
					components.Button(c, "导出 OPML", components.ButtonSecondary).Disabled(a.pending > 0).OnClick(a.exportOPML)
				}},
			},
		}},
	}, a.aiSettingsCategory()}
	ui.Column(c).Fill().MinHeight(0).MinWidth(0).Background(c.Theme().Background).Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			if a.integratedTitlebar {
				a.settingsSurface(c, categories)
				return
			}
			components.Settings(c, &a.settingsState, categories, components.SettingsOptions{Title: "设置", SearchLabel: "搜索设置", SearchPlaceholder: "搜索设置", CategoryLabel: "类别", ResultsTitle: "搜索结果", EmptyText: "没有匹配的设置", Narrow: nativeSettingsNarrow(c), HeaderActions: func(c *ui.Context) {
				components.Button(c, "返回阅读", components.ButtonGhost).OnClick(func() { a.settings = false })
			}})
		})
		if nativeSettingsCategoryVisible(categories, a.settingsState, "ai") {
			a.aiSettingsFooter(c)
		}
		if nativeSettingsCategoryVisible(categories, a.settingsState, "reading") {
			ui.Row(c).FillWidth().Shrink(0).Padding(12, 24).Gap(10).Justify(ui.End).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
				components.Button(c, "恢复默认", components.ButtonGhost).OnClick(func() {
					a.draft = backend.DefaultPreferences()
				})
				components.Button(c, "保存设置", components.ButtonPrimary).Disabled(a.pending > 0 || a.draft == a.prefs).OnClick(a.saveSettings)
			})
		}
	})
}

func (a *nativeReader) saveSettings() {
	p := a.draft
	p.Theme = "light"
	var saved backend.Preferences
	a.run(func(context.Context) error { var err error; saved, err = a.reader.SavePreferences(p); return err }, func(err error) {
		if err != nil {
			return
		}
		// Preserve edits made while persistence was running.
		if a.draft == p {
			a.draft = saved
		}
		a.applyPreferences(saved)
		a.notice = "阅读设置已保存"
	})
}

func nativeSettingsNarrow(c *ui.Context) bool { width, _ := c.Size(); return width < 1100 }

func nativeSettingsCategoryVisible(categories []components.SettingsCategory, state components.SettingsState, id string) bool {
	if strings.TrimSpace(state.Query) == "" {
		return state.Category == id
	}
	for _, result := range components.SearchSettings(categories, state.Query) {
		if categories[result.Category].ID == id {
			return true
		}
	}
	return false
}

var bilingualPreviewOriginal = markdown.ParseMarkdown("A little space makes a good story easier to read.")
var bilingualPreviewTranslation = markdown.ParseMarkdown("留出一些空间，让好故事读起来更舒适。")

func (a *nativeReader) bilingualReadingPreview(c *ui.Context) {
	panel := ui.Column(c).MinWidth(0).Padding(16).Gap(14).Border(1, c.Theme().Border).Radius(8).Label("双语阅读预览")
	if nativeSettingsNarrow(c) {
		panel.FillWidth().MaxWidth(float32(a.draft.ContentWidth))
	} else {
		panel.Width(360).Shrink(0)
	}
	panel.Children(func() {
		renderArticleMarkdown(c, bilingualPreviewOriginal, a.draft, markdown.MarkdownOptions{})
		nativeBilingualTranslation(c, bilingualPreviewTranslation, a.draft, markdown.MarkdownOptions{})
	})
}

func nativeBilingualStyleSelect(c *ui.Context, value *string) {
	labels := map[string]string{"soft": "浅色底", "line": "侧线", "plain": "纯文本"}
	sel := components.Select(c, value)
	sel.Trigger.Label("选择双语译文样式").FillWidth().Children(func() {
		label := labels[*value]
		if label == "" {
			label = labels["soft"]
		}
		components.Text(c, label)
	})
	sel.Popup(func(panel ui.Element) {
		for _, id := range []string{"soft", "line", "plain"} {
			c.Key(id)
			sel.Item(id).Label(labels[id]).FillWidth().Children(func() { components.Text(c, labels[id]) })
		}
	})
}
