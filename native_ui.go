package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/oneo-me/onevra/source/icons/lucide"
	"gorss/backend"
)

type nativeJob struct {
	work func(context.Context) error
	done func(error)
}

// Application state belongs to the UI thread. Workers publish through Window.Update;
// no ui.Element is retained after a build pass.
type nativeReader struct {
	reader                                    *backend.Reader
	window                                    *mygo.Window
	ctx                                       context.Context
	cancel                                    context.CancelFunc
	jobs                                      chan nativeJob
	state                                     backend.State
	presentation                              nativePresentation
	prefs, draft                              backend.Preferences
	settingsState                             components.SettingsState
	settings, addOpen, removeOpen             bool
	filter, feedID, articleID, query, address string
	pending                                   int
	errorText, notice                         string
	sideWidth, listWidth                      float32
	rows                                      []backend.Article
	listState                                 components.ListState
	selected                                  int
	articleScroll                             ui.ScrollState
	document                                  *markdown.MarkdownDocument
	images                                    map[string]*ui.Bitmap
	articleCancel                             context.CancelFunc
	articleGeneration                         uint64
	loadedArticleID                           string
	articleLoading                            bool
	imageErrors                               int
	removeID, removeTitle                     string
}

func newNativeReader(r *backend.Reader) *nativeReader {
	ctx, cancel := context.WithCancel(context.Background())
	defaults := backend.Preferences{FontFamily: "", FontSize: 18, LineHeight: 1.8, ContentWidth: 760, ShowImages: true, Theme: "light"}
	a := &nativeReader{reader: r, ctx: ctx, cancel: cancel, jobs: make(chan nativeJob, 64), filter: "all", sideWidth: 220, listWidth: 340, selected: -1, prefs: defaults, draft: defaults}
	a.listState.Selected = &a.selected
	a.listState.Key = func(i int) any { return a.rows[i].ID }
	a.listState.Label = func(i int) string { return a.rows[i].Title }
	a.listState.OnChange = a.selectRow
	a.listState.OnSubmit = func(i int) { a.selected = i; a.selectRow() }
	return a
}

func (a *nativeReader) start() {
	go func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case job := <-a.jobs:
				err := job.work(a.ctx)
				state, snapshotErr := a.reader.Snapshot()
				if err == nil {
					err = snapshotErr
				}
				a.window.Update(func() {
					if a.ctx.Err() != nil {
						return
					}
					a.pending--
					if snapshotErr == nil {
						a.applySnapshot(state)
					}
					if err != nil {
						a.errorText = err.Error()
					}
					if job.done != nil {
						job.done(err)
					}
					if a.articleID != "" && a.article() == nil {
						a.articleID = ""
						a.clearArticle()
					}
				})
			}
		}
	}()
	a.loadPreferences()
}

func (a *nativeReader) loadPreferences() {
	initialDraft := a.draft
	var prefs backend.Preferences
	a.run(func(context.Context) error { var err error; prefs, err = a.reader.Preferences(); return err }, func(err error) {
		if err == nil {
			if a.draft == initialDraft {
				a.draft = prefs
			}
			a.applyPreferences(prefs)
		}
	})
}

func (a *nativeReader) close() {
	a.cancel()
	if a.articleCancel != nil {
		a.articleCancel()
	}
}

func (a *nativeReader) run(work func(context.Context) error, done func(error)) {
	if a.ctx.Err() != nil {
		return
	}
	a.pending++
	a.errorText, a.notice = "", ""
	select {
	case a.jobs <- nativeJob{work: work, done: done}:
	default:
		a.pending--
		a.errorText = "操作过多，请等待当前任务完成。"
	}
	a.window.Invalidate()
}

func (a *nativeReader) refresh() {
	if a.pending != 0 {
		return
	}
	id := a.feedID
	a.run(func(ctx context.Context) error { _, err := a.reader.Refresh(ctx, id); return err }, func(err error) {
		if err == nil {
			a.notice = "刷新完成"
		}
	})
}

func (a *nativeReader) view(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
	c.OnShortcut(ui.Cmd, ui.KeyW, a.window.Close)
	c.OnShortcut(ui.Cmd, ui.KeyComma, func() { a.settings = true })
	c.OnShortcut(ui.Cmd, ui.KeyR, a.refresh)
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.addOpen = true })
	if c.Shortcut(0, ui.KeyEscape) {
		a.settings, a.addOpen, a.removeOpen = false, false, false
	}
	ui.Column(c).Fill().MinWidth(0).Children(func() {
		if a.settings {
			a.settingsView(c)
		} else {
			components.Split(c, &a.sideWidth, func() { a.sidebar(c) }, func() {
				components.Split(c, &a.listWidth, func() { a.articleList(c) }, func() { a.articleView(c) }, components.SplitOptions{FirstMin: 260, SecondMin: 350}).Fill()
			}, components.SplitOptions{FirstMin: 180, SecondMin: 650}).Fill()
		}
		a.feedback(c)
	})
	a.addDialog(c)
	a.removeDialog(c)
}

func nativeIconButton(c *ui.Context, label string, icon *ui.SVG, action func()) ui.Element {
	b := components.Button(c, "", components.ButtonGhost).Label(label).Size(32, 32).Padding(6).Children(func() { components.Icon(c, icon).Size(18, 18) })
	components.Tooltip(c, b, label)
	b.OnClick(action)
	return b
}

func (a *nativeReader) feedback(c *ui.Context) {
	if a.errorText == "" && a.notice == "" && a.pending == 0 {
		return
	}
	ui.Row(c).FillWidth().MinWidth(0).Padding(8, 16).Gap(8).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
		if a.pending > 0 {
			components.Spinner(c, components.SpinnerSpokes).Size(16, 16).Label("正在处理")
			components.Text(c, "正在处理…").FontSize(12)
		}
		message := a.notice
		color := c.Theme().TextMuted
		if a.errorText != "" {
			message, color = a.errorText, c.Theme().Danger
		}
		components.Text(c, message).Wrap().Grow(1).MinWidth(0).FontSize(12).TextColor(color)
		if message != "" {
			nativeIconButton(c, "关闭提示", lucide.X(), func() { a.errorText, a.notice = "", "" })
		}
	})
}

func (a *nativeReader) sidebar(c *ui.Context) {
	a.ensurePresentation()
	ui.Column(c).Fill().Background(c.Theme().Surface).Children(func() {
		components.ScrollView(c, components.ScrollViewOptions{}).Grow(1).MinHeight(0).Children(func() {
			components.Sidebar(c).Children(func() {
				components.SidebarSection(c, "阅读", func() {
					for _, item := range []struct {
						id, label string
						icon      *ui.SVG
					}{{"all", "全部文章", lucide.Inbox()}, {"unread", "未读", lucide.Mail()}, {"starred", "收藏", lucide.Star()}} {
						count := a.presentation.counts[item.id]
						id := item.id
						components.SidebarItem(c, fmt.Sprintf("%s  %d", item.label, count), item.icon, a.filter == id && a.feedID == "").OnClick(func() { a.filter, a.feedID = id, "" })
					}
				})
				components.SidebarSection(c, "订阅", func() {
					for _, feed := range a.state.Feeds {
						f := feed
						count := a.presentation.unread[f.ID]
						ui.Column(c).Key(f.ID).Children(func() {
							components.SidebarItem(c, fmt.Sprintf("%s  %d", f.Title, count), nil, a.feedID == f.ID).OnClick(func() { a.feedID, a.filter = f.ID, "all" })
							if f.Error != "" {
								components.Text(c, "刷新失败").FontSize(11).TextColor(c.Theme().Danger).Padding(0, 8).Label(f.Error)
							}
						})
					}
					if len(a.state.Feeds) == 0 {
						components.Text(c, "添加 RSS 或 Atom 订阅，开始阅读。").Wrap().FontSize(12).TextColor(c.Theme().TextMuted).Padding(8)
					}
				})
			})
		})
		ui.Column(c).FillWidth().Padding(12).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
			components.Button(c, "添加订阅", components.ButtonSecondary).FillWidth().OnClick(func() { a.addOpen = true })
			components.SidebarItem(c, "阅读设置", lucide.Settings2(), false).OnClick(func() { a.settings = true })
			ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
				nativeIconButton(c, "刷新订阅 ⌘R", lucide.RefreshCw(), a.refresh).Disabled(a.pending > 0)
				nativeIconButton(c, "导入 OPML", lucide.Upload(), a.importOPML).Disabled(a.pending > 0)
				nativeIconButton(c, "导出 OPML", lucide.Download(), a.exportOPML).Disabled(a.pending > 0)
			})
		})
	})
}

func (a *nativeReader) articleList(c *ui.Context) {
	a.updateRows()
	a.selected = -1
	if i, ok := a.presentation.rowIndices[a.articleID]; ok {
		a.selected = i
	}

	title := "全部文章"
	if a.filter == "unread" {
		title = "未读"
	}
	if a.filter == "starred" {
		title = "收藏"
	}
	if a.feedID != "" {
		title = a.feedTitle(a.feedID)
	}
	ui.Column(c).Fill().MinWidth(0).Children(func() {
		ui.Column(c).FillWidth().Padding(16).Gap(12).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
				components.Text(c, title).FontSize(18).Bold().SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
				nativeIconButton(c, "全部标为已读", lucide.CheckCheck(), func() {
					id := a.feedID
					a.run(func(context.Context) error { _, err := a.reader.MarkAllRead(id); return err }, nil)
				}).Disabled(a.pending > 0 || len(a.rows) == 0)
				if a.feedID != "" {
					nativeIconButton(c, "移除订阅", lucide.Trash(), func() { a.removeID, a.removeTitle, a.removeOpen = a.feedID, title, true }).Disabled(a.pending > 0)
				}
			})
			components.TextBox(c, &a.query, components.TextBoxOptions{LeadingIcon: lucide.Search()}).Label("搜索文章").Placeholder("搜索文章").FillWidth()
			components.Text(c, fmt.Sprintf("%d 篇文章", len(a.rows))).FontSize(12).TextColor(c.Theme().TextMuted)
		})
		if len(a.rows) == 0 {
			ui.Column(c).Grow(1).Padding(24).Gap(8).Children(func() {
				components.Text(c, "暂无文章").FontSize(18)
				components.Text(c, "添加订阅或刷新；也可以调整筛选条件。").Wrap().TextColor(c.Theme().TextMuted)
			})
		} else {
			components.List(c, &a.listState, len(a.rows), func(i int) {
				article := a.rows[i]
				ui.Column(c).FillWidth().MinWidth(0).Padding(14, 16).Gap(6).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border.Alpha(.55)).Children(func() {
					ui.Row(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
						components.Text(c, a.feedTitle(article.FeedID)).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
						if article.Starred {
							components.Icon(c, lucide.Star()).Size(12, 12).TextColor(c.Theme().Accent)
						}
						if !article.Read {
							ui.Box(c).Size(6, 6).Radius(3).Background(c.Theme().Accent).AlignSelf(ui.Center)
						}
					})
					weight := 500
					if !article.Read {
						weight = 600
					}
					components.Text(c, article.Title).Wrap().FontSize(15).FontWeight(weight).LineHeight(1.4)
					components.Text(c, a.articlePreview(article.ID)).Wrap().FontSize(12).TextColor(c.Theme().TextMuted).LineHeight(1.45)
					components.Text(c, articleDate(article.Published)).FontSize(11).TextColor(c.Theme().TextMuted)
				})
			}).Grow(1).MinHeight(0).FillWidth()
		}
	})
}

func (a *nativeReader) selectRow() {
	if a.selected < 0 || a.selected >= len(a.rows) {
		return
	}
	article := a.rows[a.selected]
	if a.articleID != article.ID {
		a.articleID = article.ID
		a.articleScroll = ui.ScrollState{}
		a.loadArticle(article)
	}
	if !article.Read {
		id := article.ID
		a.run(func(context.Context) error { _, err := a.reader.SetRead(id, true); return err }, nil)
	}
}

func (a *nativeReader) article() *backend.Article {
	a.ensurePresentation()
	if i, ok := a.presentation.articles[a.articleID]; ok {
		return &a.state.Articles[i]
	}
	return nil
}
func (a *nativeReader) feedTitle(id string) string {
	a.ensurePresentation()
	if title, ok := a.presentation.titles[id]; ok {
		return title
	}
	return "订阅"
}

func (a *nativeReader) articleView(c *ui.Context) {
	article := a.article()
	if article == nil {
		ui.Column(c).Fill().Center().Padding(48).Gap(12).Children(func() {
			components.Text(c, "留一点时间给阅读").FontSize(24).FontWeight(500)
			components.Text(c, "从左侧选择一篇文章。").TextColor(c.Theme().TextMuted)
		})
		return
	}
	v := *article
	ui.Column(c).Fill().MinWidth(0).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Padding(8, 16).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border).Children(func() {
			components.Text(c, a.feedTitle(v.FeedID)).SingleLine().Ellipsis("…").FontSize(12).TextColor(c.Theme().TextMuted).Grow(1).MinWidth(0)
			starLabel, starIcon := "收藏", lucide.Star()
			if v.Starred {
				starLabel, starIcon = "取消收藏", lucide.StarOff()
			}
			nativeIconButton(c, starLabel, starIcon, func() {
				a.run(func(context.Context) error { _, err := a.reader.SetStarred(v.ID, !v.Starred); return err }, nil)
			}).Disabled(a.pending > 0)
			readLabel := "标为已读"
			if v.Read {
				readLabel = "标为未读"
			}
			nativeIconButton(c, readLabel, lucide.Mail(), func() {
				a.run(func(context.Context) error { _, err := a.reader.SetRead(v.ID, !v.Read); return err }, nil)
			}).Disabled(a.pending > 0)
			nativeIconButton(c, "浏览器打开原文", lucide.ExternalLink(), func() { a.run(func(context.Context) error { return a.reader.OpenArticle(v.ID) }, nil) }).Disabled(v.URL == "")
			nativeIconButton(c, "阅读设置 ⌘,", lucide.Settings2(), func() { a.settings = true })
		})
		components.ScrollView(c, components.ScrollViewOptions{}).Key(v.ID).TrackScroll(&a.articleScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(36, 32, 72).Children(func() {
				width := float32(a.prefs.ContentWidth)
				if width <= 0 {
					width = 760
				}
				ui.Column(c).FillWidth().MaxWidth(width).MinWidth(0).Gap(20).Font(a.prefs.FontFamily).FontSize(float32(a.prefs.FontSize)).Children(func() {
					components.Text(c, v.Title).FontSize(30).FontWeight(600).LineHeight(1.25).Wrap().Selectable()
					meta := articleDate(v.Published)
					if v.Author != "" {
						meta = v.Author + "  /  " + meta
					}
					components.Text(c, meta).FontSize(12).TextColor(c.Theme().TextMuted)
					if a.articleLoading {
						components.Spinner(c, components.SpinnerSpokes).Size(20, 20).Label("正在加载文章")
					}
					if a.document != nil {
						var images map[string]*ui.Bitmap
						if a.prefs.ShowImages {
							images = a.images
						}
						a.document.Render(c, markdown.MarkdownOptions{CodeHeaders: true, CopyCodeLabel: "复制代码", Images: images, OnLink: func(destination string) { a.openLink(destination, v.URL) }}).FillWidth().Font(a.prefs.FontFamily).FontSize(float32(a.prefs.FontSize)).LineHeight(float32(a.prefs.LineHeight))
					}
					if a.imageErrors > 0 && a.prefs.ShowImages {
						components.Text(c, fmt.Sprintf("%d 张图片未能加载", a.imageErrors)).FontSize(12).TextColor(c.Theme().TextMuted)
					}
					if v.URL != "" {
						components.Button(c, "在浏览器中阅读原文", components.ButtonLink).OnClick(func() { a.run(func(context.Context) error { return a.reader.OpenArticle(v.ID) }, nil) })
					}
				})
			})
		})
	})
}

func (a *nativeReader) addDialog(c *ui.Context) {
	components.Dialog(c, &a.addOpen, components.DialogOptions{Label: "添加订阅"}, func(panel ui.Element) {
		panel.Width(460).Gap(16)
		components.Text(c, "添加订阅").FontSize(22).Bold()
		components.Text(c, "输入 RSS 或 Atom 订阅地址。").Wrap().TextColor(c.Theme().TextMuted)
		field := components.TextBox(c, &a.address, components.TextBoxOptions{}).Label("订阅地址").Placeholder("https://example.com/feed.xml").FillWidth().AutoFocus()
		if a.errorText != "" {
			components.Text(c, a.errorText).Wrap().TextColor(c.Theme().Danger)
		}
		add := func() {
			if a.pending > 0 || strings.TrimSpace(a.address) == "" {
				return
			}
			address := a.address
			a.run(func(ctx context.Context) error { _, err := a.reader.Add(ctx, address); return err }, func(err error) {
				if err == nil {
					a.addOpen = false
					a.address = ""
					a.notice = "订阅已添加"
				}
			})
		}
		if field.Submitted() {
			add()
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "取消", components.ButtonGhost).OnClick(func() { a.addOpen = false })
			label := "添加订阅"
			if a.pending > 0 {
				label = "正在添加…"
			}
			components.Button(c, label, components.ButtonPrimary).Disabled(a.pending > 0 || strings.TrimSpace(a.address) == "").OnClick(add)
		})
	})
}

func (a *nativeReader) removeDialog(c *ui.Context) {
	components.Dialog(c, &a.removeOpen, components.DialogOptions{Label: "移除订阅"}, func(panel ui.Element) {
		panel.Width(420).Gap(16)
		components.Text(c, "移除订阅？").FontSize(22).Bold()
		components.Text(c, "将移除「"+a.removeTitle+"」及其文章。").Wrap()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "取消", components.ButtonGhost).OnClick(func() { a.removeOpen = false })
			components.Button(c, "移除订阅", components.ButtonPrimary).Disabled(a.pending > 0).OnClick(func() {
				id := a.removeID
				a.run(func(context.Context) error { _, err := a.reader.Remove(id); return err }, func(err error) {
					if err == nil {
						a.removeOpen = false
						if a.feedID == id {
							a.feedID = ""
						}
					}
				})
			})
		})
	})
}

func (a *nativeReader) importOPML() {
	a.run(func(context.Context) error {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.window, Title: "导入订阅", Filters: []mygo.FileFilter{{Name: "OPML", Extensions: []string{"opml", "xml"}}}})
		if err != nil || len(paths) == 0 {
			return err
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			return err
		}
		_, err = a.reader.ImportOPML(string(data))
		return err
	}, nil)
}
func (a *nativeReader) exportOPML() {
	a.run(func(context.Context) error {
		text, err := a.reader.ExportOPML()
		if err != nil {
			return err
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.window, Title: "导出订阅", DefaultPath: "GoRss-subscriptions.opml", Filters: []mygo.FileFilter{{Name: "OPML", Extensions: []string{"opml"}}}})
		if err != nil || path == "" {
			return err
		}
		return os.WriteFile(path, []byte(text), 0600)
	}, nil)
}

func articleDate(raw string) string {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return raw
}
