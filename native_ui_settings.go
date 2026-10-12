package main

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"gorss/backend"
)

func (a *nativeReader) settingsView(c *ui.Context) {
	fonts := installedNativeFonts.get(enumerateNativeFontFamilies)
	ui.Column(c).Fill().Background(c.Theme().Background).Children(func() {
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			components.Settings(c, &a.settingsState, []components.SettingsCategory{{
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
				}, {ID: "content", Title: "内容与外观", Items: []components.SettingsItem{
					{ID: "images", Title: "显示文章图片", Description: "显示 Markdown 与 RSS 正文中的图片。关闭后保留替代文字。", Build: func(c *ui.Context) {
						components.Switch(c, &a.draft.ShowImages, "显示文章图片", components.SwitchOptions{HideLabel: true}).Shrink(0)
					}},
					{ID: "appearance", Title: "浅色外观", Description: "阅读界面与设置页始终使用浅色主题。", Build: func(c *ui.Context) {
						components.Text(c, "浅色").SingleLine().Shrink(0).TextColor(c.Theme().TextMuted)
					}},
				}}},
			}}, components.SettingsOptions{Title: "阅读设置", SearchLabel: "搜索设置", SearchPlaceholder: "搜索设置", CategoryLabel: "类别", ResultsTitle: "搜索结果", EmptyText: "没有匹配的设置", Narrow: false, HeaderActions: func(c *ui.Context) {
				components.Button(c, "返回阅读", components.ButtonGhost).OnClick(func() { a.settings = false })
			}})
		})
		ui.Row(c).FillWidth().Shrink(0).Padding(12, 24).Gap(10).Justify(ui.End).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
			components.Button(c, "恢复默认", components.ButtonGhost).OnClick(func() {
				a.draft = backend.Preferences{FontFamily: "", FontSize: 18, LineHeight: 1.8, ContentWidth: 760, ShowImages: true, Theme: "light"}
			})
			components.Button(c, "保存设置", components.ButtonPrimary).Disabled(a.pending > 0 || a.draft == a.prefs).OnClick(a.saveSettings)
		})
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
