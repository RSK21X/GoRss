package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/oneo-me/onevra/source/shells/plain"
	"github.com/oneo-me/onevra/source/theme"
	"gorss/backend"
)

func main() {
	config, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	reader, err := backend.New(filepath.Join(config, "GoRss", "reader.json"))
	if err != nil {
		log.Fatalf("无法读取 GoRss 数据（请检查 Application Support/GoRss/reader.json）：%v", err)
	}
	app := newNativeReader(reader)
	mygo.App.SetName("GoRss")
	mygo.App.WhenReady(func() {
		mygo.Theme.SetSource(mygo.ThemeLight)
		shell := plain.NewShell(plain.ShellOptions{
			Title: "GoRss", Width: 1380, Height: 860, MinWidth: 980, MinHeight: 620,
			Appearance: theme.Light, Content: app.view,
		})
		app.window = shell.Window()
		app.window.OnClosed(func() { app.close(); mygo.App.Quit() })
		app.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
