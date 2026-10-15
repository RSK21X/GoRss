package main

// Settings result rendering is adapted from Onevra components/settings.go.
// Copyright (c) 2026 ONEO. MIT license: see THIRD_PARTY_NOTICES.md.

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// Keep the settings catalog, search and controls shared with Onevra. The native
// window owns the surrounding layout so only the sidebar reserves traffic-light
// space; a narrow window instead places its title beside the native controls.
func (a *nativeReader) settingsSurface(c *ui.Context, categories []components.SettingsCategory) {
	state := &a.settingsState
	selected := 0
	for i := range categories {
		if categories[i].ID == state.Category {
			selected = i
			break
		}
	}
	state.Category = categories[selected].ID
	narrow := nativeSettingsNarrow(c)
	height := a.windowHeaderHeight(c)
	search := func() {
		components.TextBox(c, &state.Query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).Label("搜索设置").Placeholder("搜索设置").FillWidth().MinWidth(0)
	}
	ui.Row(c).Fill().MinHeight(0).MinWidth(0).Children(func() {
		if !narrow {
			ui.Column(c).Width(216).FillHeight().Shrink(0).Background(nativeSidebarSurface).Label("设置导航栏").Children(func() {
				if height > 0 {
					ui.Box(c).FillWidth().Height(height).Shrink(0).DragWindow().Label("设置窗口拖动区")
				}
				ui.Row(c).FillWidth().Height(48).Shrink(0).Padding(0, 16).AlignItems(ui.Center).Children(func() { components.Text(c, "设置").Bold() })
				ui.Column(c).FillWidth().Padding(0, 12, 16, 12).Children(search)
				components.ScrollView(c, components.ScrollViewOptions{}).Fill().Children(func() {
					components.Sidebar(c).Children(func() {
						for i, category := range categories {
							if components.SidebarItem(c, category.Title, category.Icon, selected == i).Key(category.ID).Clicked() {
								selected, state.Category, state.Query = i, category.ID, ""
							}
						}
					})
				})
			})
			ui.Box(c).Width(1).FillHeight().Shrink(0).Background(c.Theme().Border)
		}
		ui.Column(c).Fill().MinHeight(0).MinWidth(0).Background(nativePaperSurface).Label("设置内容栏").Children(func() {
			title := categories[selected].Title
			if narrow {
				title = "设置"
			}
			if strings.TrimSpace(state.Query) != "" {
				title = "搜索结果"
			}
			left := float32(20)
			if narrow && height > 0 {
				left = max(left, c.TitleBar().Left+12)
			}
			ui.Row(c).FillWidth().MinHeight(max(48, height)).Shrink(0).Padding(8, 20, 8, left).Gap(8).AlignItems(ui.Center).DragWindow().Label("设置内容窗口拖动区").BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
				components.Text(c, title).Bold().SingleLine().MinWidth(0)
				ui.Spacer(c)
				components.Button(c, "返回阅读", components.ButtonGhost).OnClick(func() { a.settings = false })
			})
			if narrow {
				ui.Column(c).FillWidth().Shrink(0).Padding(12).Gap(8).Children(func() {
					sel := components.Select(c, &state.Category)
					sel.Trigger.Label("类别").FillWidth().Children(func() { components.Text(c, categories[selected].Title).SingleLine() })
					sel.Popup(func(panel ui.Element) {
						for _, category := range categories {
							sel.Item(category.ID).Label(category.Title).Children(func() { components.Text(c, category.Title) })
						}
					})
					if sel.Trigger.Changed() {
						state.Query = ""
						for i := range categories {
							if categories[i].ID == state.Category {
								selected = i
							}
						}
					}
					search()
				})
			}
			a.settingsResults(c, categories, selected, narrow)
		})
	})
}

func (a *nativeReader) settingsResults(c *ui.Context, categories []components.SettingsCategory, selected int, narrow bool) {
	searching := strings.TrimSpace(a.settingsState.Query) != ""
	results := components.SearchSettings(categories, a.settingsState.Query)
	t := c.Theme()
	components.ScrollView(c, components.ScrollViewOptions{}).Fill().MinHeight(0).Children(func() {
		ui.Column(c).Key(a.settingsState.Category).FillWidth().MinWidth(0).Padding(20).Gap(20).Children(func() {
			count, lastCategory, lastGroup := 0, -1, -1
			for _, result := range results {
				if !searching && result.Category != selected {
					continue
				}
				category := categories[result.Category]
				group := category.Groups[result.Group]
				item := group.Items[result.Item]
				if lastCategory != result.Category {
					if searching {
						components.Text(c, category.Title).Bold()
					}
					if category.Description != "" {
						components.Text(c, category.Description).TextColor(t.TextMuted).Wrap()
					}
				}
				if (lastCategory != result.Category || lastGroup != result.Group) && (group.Title != "" || group.Actions != nil) {
					ui.Column(c).FillWidth().Gap(12).Children(func() {
						ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(12).Children(func() {
							components.Text(c, group.Title).Grow(1).MinWidth(0).Wrap()
							if group.Actions != nil {
								group.Actions(c)
							}
						})
						ui.Box(c).FillWidth().Height(1).Background(t.Border)
					})
				}
				lastCategory, lastGroup = result.Category, result.Group
				count++
				var row ui.Element
				if narrow {
					row = ui.Column(c)
				} else {
					row = ui.Row(c)
				}
				row.Key(struct{ Category, Group, Item string }{category.ID, group.ID, item.ID}).FillWidth().MinWidth(0).Gap(12).Children(func() {
					ui.Column(c).FillWidth().Shrink(1).MinWidth(0).Gap(4).Children(func() {
						components.Text(c, item.Title).Wrap()
						if item.DescriptionContent != nil {
							ui.RichText(c).FillWidth().TextColor(t.TextMuted).FontSize(t.Rem(.875)).Wrap().Children(func() { item.DescriptionContent(c) })
						} else if item.Description != "" {
							components.Text(c, item.Description).TextColor(t.TextMuted).FontSize(t.Rem(.875)).Wrap()
						}
					})
					if item.Build != nil {
						item.Build(c)
					}
				})
			}
			if count == 0 {
				components.Text(c, "没有匹配的设置").TextColor(t.TextMuted).Wrap()
			}
		})
	})
}
