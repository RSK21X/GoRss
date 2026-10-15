package main

import (
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
	"github.com/oneo-me/onevra/source/shells/plain"
	"github.com/oneo-me/onevra/source/theme"
)

func newNativeReaderWindow(a *nativeReader) *mygo.Window {
	if runtime.GOOS != "darwin" {
		return plain.NewShell(plain.ShellOptions{Title: "GoRss", Width: 1380, Height: 860, MinWidth: 980, MinHeight: 620, Appearance: theme.Light, Content: a.view}).Window()
	}
	a.integratedTitlebar = true
	return mygo.NewWindow(nativeReaderWindowOptions(a))
}

func nativeReaderWindowOptions(a *nativeReader) mygo.WindowOptions {
	return mygo.WindowOptions{
		Title: "GoRss", Width: 1380, Height: 860, MinWidth: 980, MinHeight: 620,
		TitleBarStyle: mygo.TitleBarHidden, TrafficLightPosition: &mygo.Point{X: 16, Y: 16},
		BackgroundColor: "#f0eee8", Content: ui.View(a.windowView),
	}
}

// Native controls overlay only the left pane. Other panes can start at the
// top of the window. The operating system reports no control clearance in
// full screen, so the sidebar and settings release their reserved space.
func (a *nativeReader) windowHeaderHeight(c *ui.Context) float32 {
	if !a.integratedTitlebar {
		return 0
	}
	bar := c.TitleBar()
	if bar.Height <= 0 && bar.Left <= 0 && bar.Right <= 0 {
		return 0
	}
	return max(48, bar.Height)
}
func (a *nativeReader) windowView(c *ui.Context) {
	theme.Apply(c, theme.Light)
	components.WithDialogWindowDrag(c, a.windowHeaderHeight(c), func() { a.view(c) })
}
