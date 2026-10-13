package main

import (
	"context"
	"os"

	"github.com/egoist/mygo"
	"gorss/backend"
)

// Desktop provides native file dialogs for subscription backups.
type Desktop struct{ reader *backend.Reader }

func (d *Desktop) ExportSubscriptions(ctx context.Context) (string, error) {
	text, err := d.reader.ExportOPML()
	if err != nil {
		return "", err
	}
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent: mygo.CallerWindow(ctx), DefaultPath: "GoRss-subscriptions.opml",
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		return "", err
	}
	return path, nil
}
