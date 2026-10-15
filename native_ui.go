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
	settings, addOpen, removeOpen, moreOpen   bool
	refreshing                                bool
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
	ai                                        nativeAIState
}

func newNativeReader(r *backend.Reader) *nativeReader {
	ctx, cancel := context.WithCancel(context.Background())
	defaults := backend.DefaultPreferences()
	a := &nativeReader{reader: r, ctx: ctx, cancel: cancel, jobs: make(chan nativeJob, 64), filter: "all", sideWidth: 196, listWidth: 340, selected: -1, prefs: defaults, draft: defaults}
	// New has already read the store. Publish cached articles before any network work.
	if r != nil {
		state, err := r.Snapshot()
		if err != nil {
			a.errorText = err.Error()
		} else {
			a.publishState(state)
		}
	}
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
				a.window.Update(func() {
					a.completeJob(job, state, err, snapshotErr)
				})
			}
		}
	}()
	a.loadPreferences()
	a.refreshOnStartup()
	a.loadAISettings()
}

// completeJob runs on the UI thread after a worker has taken its snapshot.
func (a *nativeReader) completeJob(job nativeJob, state backend.State, err, snapshotErr error) {
	if a.ctx.Err() != nil {
		return
	}
	if err == nil {
		err = snapshotErr
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
}

func (a *nativeReader) refreshOnStartup() {
	if len(a.state.Feeds) > 0 {
		// Preferences may still be pending. Queue behind them instead of using
		// refreshFeed, whose busy guard is for user-initiated refreshes.
		a.queueRefresh("")
	}
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
	a.resetAI()
	a.cancelAISettings()
	a.cancel()
	if a.articleCancel != nil {
		a.articleCancel()
	}
}

func (a *nativeReader) run(work func(context.Context) error, done func(error)) bool {
	if a.ctx.Err() != nil {
		return false
	}
	a.pending++
	a.errorText, a.notice = "", ""
	select {
	case a.jobs <- nativeJob{work: work, done: done}:
	default:
		a.pending--
		a.errorText = "操作过多，请等待当前任务完成。"
		a.window.Invalidate()
		return false
	}
	a.window.Invalidate()
	return true
}

func (a *nativeReader) refresh() {
	if a.pending != 0 {
		return
	}
	a.refreshFeed(a.feedID)
}

func (a *nativeReader) refreshFeed(id string) {
	if a.pending != 0 {
		return
	}
	a.queueRefresh(id)
}

func (a *nativeReader) queueRefresh(id string) {
	queued := a.run(func(ctx context.Context) error { _, err := a.reader.Refresh(ctx, id); return err }, func(err error) {
		a.refreshing = false
		if err == nil {
			a.notice = "刷新完成"
		}
	})
	if queued {
		a.refreshing = true
	}
}

func (a *nativeReader) view(c *ui.Context) {
	if a.ai.settings.wasVisible && !a.settings {
		a.cancelAISettings()
	}
	a.ai.settings.wasVisible = a.settings
	c.OnShortcut(ui.Cmd, ui.KeyQ, mygo.App.Quit)
	c.OnShortcut(ui.Cmd, ui.KeyW, a.window.Close)
	c.OnShortcut(ui.Cmd, ui.KeyComma, func() { a.settings = true })
	c.OnShortcut(ui.Cmd, ui.KeyR, a.refresh)
	c.OnShortcut(ui.Cmd, ui.KeyN, func() { a.addOpen = true })
	if c.Shortcut(0, ui.KeyEscape) {
		if a.ai.selection.open {
			a.closeSelectionAI()
		} else if a.ai.settings.removeOpen {
			a.ai.settings.removeOpen = false
		} else {
			a.settings, a.addOpen, a.removeOpen, a.moreOpen = false, false, false, false
		}
	}
	ui.Column(c).Fill().MinWidth(0).Children(func() {
		if a.settings {
			a.settingsView(c)
		} else {
			components.Split(c, &a.sideWidth, func() { a.sidebar(c) }, func() {
				components.Split(c, &a.listWidth, func() { a.articleList(c) }, func() { a.articleView(c) }, components.SplitOptions{FirstMin: 260, SecondMin: 350}).Label("调整文章列表宽度").Fill()
			}, components.SplitOptions{FirstMin: 180, SecondMin: 650}).Label("调整侧栏宽度").Fill()
		}
		a.feedback(c)
	})
	a.addDialog(c)
	a.removeDialog(c)
	a.aiSelectionView(c)
}

func nativeIconButton(c *ui.Context, label string, icon *ui.SVG, action func()) ui.Element {
	b := components.Button(c, "", components.ButtonGhost).Label(label).Size(32, 32).Shrink(0).Padding(6).Children(func() { components.Icon(c, icon).Size(18, 18).TextColor(c.Theme().Text) })
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
			label := "正在处理…"
			if a.refreshing {
				label = "正在刷新订阅…"
			}
			components.Text(c, label).FontSize(12)
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
				ui.Column(c).FillWidth().MinWidth(0).Gap(c.Theme().Space(1)).Children(func() {
					ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Children(func() {
						components.Text(c, "订阅").FontSize(c.Theme().Rem(.8)).Bold().TextColor(c.Theme().TextMuted).Grow(1).Padding(0, c.Theme().Space(2))
						nativeIconButton(c, "添加订阅 ⌘N", lucide.Plus(), func() { a.addOpen = true })
						more := nativeIconButton(c, "更多订阅操作", lucide.Ellipsis(), func() { a.moreOpen = !a.moreOpen })
						components.Popover(c, more, &a.moreOpen, func(panel ui.Element) {
							panel.Label("订阅操作").Width(200).Padding(8).Gap(4)
							label := "刷新全部订阅"
							if a.refreshing {
								label = "正在刷新订阅…"
							}
							components.Button(c, label, components.ButtonGhost).FillWidth().Disabled(a.pending > 0).OnClick(func() { a.moreOpen = false; a.refreshFeed("") })
							if a.feedID != "" {
								components.Button(c, "刷新当前订阅 ⌘R", components.ButtonGhost).FillWidth().Disabled(a.pending > 0).OnClick(func() { a.moreOpen = false; a.refresh() })
							}
						})
					})
					for _, feed := range a.state.Feeds {
						f := feed
						count := a.presentation.unread[f.ID]
						ui.Column(c).Key(f.ID).Children(func() {
							b := components.Button(c, "", components.ButtonGhost).Key(f.ID).Label(fmt.Sprintf("%s，%d 篇未读", f.Title, count)).FillWidth().MinWidth(0).Justify(ui.Start).Padding(c.Theme().Space(1.75), c.Theme().Space(2)).Gap(c.Theme().Space(2)).Selected(a.feedID == f.ID)
							if a.feedID == f.ID {
								b.Background(c.Theme().Text.Alpha(.1))
							}
							b.Children(func() {
								components.Text(c, f.Title).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
								components.Text(c, fmt.Sprint(count)).FontSize(11).TextColor(c.Theme().TextMuted).Shrink(0)
							})
							components.Tooltip(c, b, f.Title)
							b.OnClick(func() { a.feedID, a.filter = f.ID, "all" })
							if f.Error != "" {
								failure := components.Text(c, "刷新失败").FontSize(11).TextColor(c.Theme().Danger).Padding(0, 8).Label(f.Error)
								components.Tooltip(c, failure, f.Error)
							}
						})
					}
					if len(a.state.Feeds) == 0 {
						components.Text(c, "添加 RSS 或 Atom 订阅，开始阅读。").Wrap().FontSize(12).TextColor(c.Theme().TextMuted).Padding(8)
					}
				})
			})
		})
		ui.Column(c).FillWidth().Padding(12).BorderWidth(1, 0, 0, 0).BorderColor(c.Theme().Border).Children(func() {
			components.SidebarItem(c, "设置", lucide.Settings2(), false).Label("设置 ⌘,").OnClick(func() { a.settings = true })
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
				nativeIconButton(c, a.markAllReadLabel(), lucide.CheckCheck(), func() {
					id := a.feedID
					a.run(func(context.Context) error { _, err := a.reader.MarkAllRead(id); return err }, func(err error) {
						if err == nil {
							a.notice = "已将" + a.markReadScope(id) + "全部标为已读"
						}
					})
				}).Disabled(a.pending > 0 || len(a.rows) == 0)
				if a.feedID != "" {
					nativeIconButton(c, "移除当前订阅及其文章", lucide.Trash(), func() { a.removeID, a.removeTitle, a.removeOpen = a.feedID, title, true }).Disabled(a.pending > 0)
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
				ui.Column(c).FillWidth().MinWidth(0).Padding(16).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(c.Theme().Border.Alpha(.55)).Children(func() {
					ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
						if !article.Read {
							ui.Box(c).Size(6, 6).Radius(3).Background(c.Theme().Accent).Shrink(0).Label("未读文章")
						}
						weight := 500
						if !article.Read {
							weight = 600
						}
						components.Text(c, article.Title).Wrap().MaxLines(3).Ellipsis("…").Grow(1).MinWidth(0).FontSize(16).FontWeight(weight).LineHeight(1.4)
						if article.Starred {
							components.Icon(c, lucide.Star()).Size(12, 12).TextColor(c.Theme().Accent).Shrink(0).Label("已收藏")
						}
					})
					if preview := a.articlePreview(article.ID); preview != "" {
						components.Text(c, preview).Wrap().MaxLines(3).Ellipsis("…").FontSize(12).TextColor(c.Theme().TextMuted).LineHeight(1.5)
					}
					ui.Row(c).FillWidth().MinWidth(0).Gap(8).Children(func() {
						if a.feedID == "" {
							components.Text(c, a.feedTitle(article.FeedID)).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Ellipsis("…").Grow(1).MinWidth(0)
						}
						components.Text(c, articleDate(article.Published)).SingleLine().FontSize(11).TextColor(c.Theme().TextMuted).Shrink(0)
					})
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
			a.aiToolbar(c, v)
			starLabel, starIcon := "收藏", lucide.Star()
			if v.Starred {
				starLabel, starIcon = "取消收藏", lucide.StarOff()
			}
			nativeIconButton(c, starLabel, starIcon, func() {
				a.run(func(context.Context) error { _, err := a.reader.SetStarred(v.ID, !v.Starred); return err }, nil)
			}).Disabled(a.pending > 0)
			readLabel, readIcon := "标记当前文章为已读", lucide.CheckCheck()
			if v.Read {
				readLabel, readIcon = "标记当前文章为未读", lucide.CircleDot()
			}
			nativeIconButton(c, readLabel, readIcon, func() {
				a.run(func(context.Context) error { _, err := a.reader.SetRead(v.ID, !v.Read); return err }, nil)
			}).Disabled(a.pending > 0)
			nativeIconButton(c, "浏览器打开原文", lucide.ExternalLink(), func() { a.run(func(context.Context) error { return a.reader.OpenArticle(v.ID) }, nil) }).Disabled(v.URL == "")
		})
		components.ScrollView(c, components.ScrollViewOptions{}).Key(v.ID).TrackScroll(&a.articleScroll).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(36, 32, 72).Children(func() {
				width := float32(a.prefs.ContentWidth)
				if width <= 0 {
					width = 760
				}
				ui.Column(c).FillWidth().MaxWidth(width).MinWidth(0).Gap(20).Font(a.prefs.FontFamily).FontSize(float32(a.prefs.FontSize)).Children(func() {
					a.aiSelectionContextMenu(ui.Column(c).FillWidth().MinWidth(0).Selectable().Children(func() {
						components.Text(c, v.Title).FontSize(30).FontWeight(600).LineHeight(1.25).Wrap()
					}))
					meta := articleDate(v.Published)
					if v.Author != "" {
						meta = v.Author + "  /  " + meta
					}
					components.Text(c, meta).FontSize(12).TextColor(c.Theme().TextMuted)
					if a.articleLoading {
						components.Spinner(c, components.SpinnerSpokes).Size(20, 20).Label("正在加载文章")
					}
					a.aiSummaryView(c, v)
					translated := a.aiTranslationView(c, v)
					if a.document != nil && !translated {
						var images map[string]*ui.Bitmap
						if a.prefs.ShowImages {
							images = a.images
						}
						a.aiSelectionContextMenu(renderArticleMarkdown(c, a.document, a.prefs, markdown.MarkdownOptions{CodeHeaders: true, CopyCodeLabel: "复制代码", Images: images, OnLink: func(destination string) { a.openLink(destination, v.URL) }}))
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
		panel.Width(460).MaxWidth(nativeDialogWidth(c)).Gap(16)
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
		panel.Width(420).MaxWidth(nativeDialogWidth(c)).Gap(16)
		components.Text(c, "移除订阅？").FontSize(22).Bold()
		components.Text(c, "将移除「"+a.removeTitle+"」及其文章、收藏和阅读记录。此操作无法撤销。").Wrap()
		if a.errorText != "" {
			components.Text(c, a.errorText).Wrap().TextColor(c.Theme().Danger)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			components.Button(c, "取消", components.ButtonGhost).OnClick(func() { a.removeOpen = false })
			components.Button(c, "移除订阅", components.ButtonPrimary).Disabled(a.pending > 0).OnClick(func() {
				id := a.removeID
				a.run(func(context.Context) error { _, err := a.reader.Remove(id); return err }, func(err error) {
					if err == nil {
						a.removeOpen = false
						a.notice = "订阅及其文章已移除"
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
	completed := false
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
		completed = err == nil
		return err
	}, func(err error) {
		if err == nil && completed {
			a.notice = "OPML 订阅已导入"
		}
	})
}
func (a *nativeReader) exportOPML() {
	completed := false
	a.run(func(context.Context) error {
		text, err := a.reader.ExportOPML()
		if err != nil {
			return err
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: a.window, Title: "导出订阅", DefaultPath: "GoRss-subscriptions.opml", Filters: []mygo.FileFilter{{Name: "OPML", Extensions: []string{"opml"}}}})
		if err != nil || path == "" {
			return err
		}
		err = os.WriteFile(path, []byte(text), 0600)
		completed = err == nil
		return err
	}, func(err error) {
		if err == nil && completed {
			a.notice = "OPML 订阅已导出"
		}
	})
}

func articleDate(raw string) string {
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return raw
}

func nativeDialogWidth(c *ui.Context) float32 {
	width, _ := c.Size()
	return max(0, width-32)
}
func (a *nativeReader) markReadScope(id string) string {
	if id == "" {
		return "所有订阅的文章"
	}
	return "订阅「" + a.feedTitle(id) + "」的文章"
}
func (a *nativeReader) markAllReadLabel() string {
	return "将" + a.markReadScope(a.feedID) + "全部标为已读（不受搜索或筛选影响）"
}
