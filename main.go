package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
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
		app.window = newNativeReaderWindow(app)
		app.window.OnClosed(func() { app.close(); mygo.App.Quit() })
		app.start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
