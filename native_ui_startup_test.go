package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo"
	"gorss/backend"
)

// Run the queued worker and its UI completion without starting a native event loop.
func finishNextNativeJob(t *testing.T, a *nativeReader) error {
	t.Helper()
	select {
	case job := <-a.jobs:
		err := job.work(a.ctx)
		state, snapshotErr := a.reader.Snapshot()
		a.completeJob(job, state, err, snapshotErr)
		return err
	default:
		t.Fatal("no queued job")
		return nil
	}
}

func TestNativeStartupRefreshesAllFeedsAfterPreferences(t *testing.T) {
	var updated atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		title := "Cached"
		if updated.Load() {
			title = "Updated"
		}
		fmt.Fprintf(w, `<rss version="2.0"><channel><title>%s</title><item><guid>stable</guid><title>%s</title><description>Body</description></item></channel></rss>`, req.URL.Path, title)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "reader.json")
	r, err := backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, feed := range []string{"/one", "/two"} {
		if _, err := r.Add(context.Background(), server.URL+feed); err != nil {
			t.Fatal(err)
		}
	}
	cached, _ := r.Snapshot()
	if _, err := r.SetRead(cached.Articles[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetStarred(cached.Articles[0].ID, true); err != nil {
		t.Fatal(err)
	}
	prefs := backend.DefaultPreferences()
	prefs.FontSize = 22
	if _, err := r.SavePreferences(prefs); err != nil {
		t.Fatal(err)
	}
	// A new Reader models a later application launch using the persisted store.
	r, err = backend.New(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(r)
	defer a.close()
	a.window = &mygo.Window{}
	if len(a.state.Articles) != 2 || a.state.Articles[0].Title != "Cached" {
		t.Fatal("cached articles are unavailable before startup jobs")
	}
	updated.Store(true)
	a.feedID, a.filter, a.query = cached.Feeds[0].ID, "starred", "unmatched"
	a.loadPreferences()
	a.refreshOnStartup()
	if a.pending != 2 || !a.refreshing || requests.Load() != 2 {
		t.Fatal("startup must queue refresh behind preferences without blocking or fetching inline")
	}
	a.refreshFeed("")
	if len(a.jobs) != 2 {
		t.Fatal("manual refresh duplicated pending startup refresh")
	}
	if err := finishNextNativeJob(t, a); err != nil || a.prefs != prefs || a.pending != 1 {
		t.Fatalf("preferences: %v, %+v, pending %d", err, a.prefs, a.pending)
	}
	if err := finishNextNativeJob(t, a); err != nil {
		t.Fatal(err)
	}
	if a.pending != 0 || a.refreshing || a.notice != "刷新完成" || requests.Load() != 4 {
		t.Fatalf("startup completion: pending %d, refreshing %v, notice %q, requests %d", a.pending, a.refreshing, a.notice, requests.Load())
	}
	for _, article := range a.state.Articles {
		if article.Title != "Updated" {
			t.Fatal("startup refreshed only the selected feed")
		}
		if article.ID == cached.Articles[0].ID && (!article.Read || !article.Starred) {
			t.Fatal("startup refresh lost read/starred flags")
		}
	}
}

func TestNativeStartupRefreshFailureAndCancellationPreserveCache(t *testing.T) {
	for _, mode := range []string{"network failure", "cancelled", "invalid preferences"} {
		t.Run(mode, func(t *testing.T) {
			var offline atomic.Bool
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if offline.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				fmt.Fprint(w, `<rss version="2.0"><channel><title>Feed</title><item><guid>one</guid><title>Cached</title></item></channel></rss>`)
			}))
			defer server.Close()
			storePath := filepath.Join(t.TempDir(), "reader.json")
			r, err := backend.New(storePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.Add(context.Background(), server.URL); err != nil {
				t.Fatal(err)
			}
			a := newNativeReader(r)
			defer a.close()
			a.window = &mygo.Window{}
			before, _ := r.Snapshot()
			if mode == "invalid preferences" {
				if err := os.WriteFile(filepath.Join(filepath.Dir(storePath), "preferences.json"), []byte("{broken"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			a.loadPreferences()
			a.refreshOnStartup()
			finishNextNativeJob(t, a)
			preferenceError := a.errorText
			if mode == "cancelled" {
				a.close()
			} else if mode == "network failure" {
				offline.Store(true)
			}
			err = finishNextNativeJob(t, a)
			after, _ := r.Snapshot()
			if !reflect.DeepEqual(before.Articles, after.Articles) {
				t.Fatal("startup failure changed cached articles")
			}
			if mode == "cancelled" {
				if err != context.Canceled || requests.Load() != 1 || !reflect.DeepEqual(before, after) {
					t.Fatalf("cancelled startup changed state or sent a request: %v", err)
				}
			} else if mode == "network failure" {
				if err == nil || !strings.Contains(a.errorText, "503") || a.refreshing || after.Feeds[0].Error == "" {
					t.Fatal("startup refresh failure was not reported")
				}
			} else if err != nil || requests.Load() != 2 || preferenceError == "" || a.errorText != preferenceError || a.refreshing {
				t.Fatal("preference failure prevented refresh or disappeared")
			}
		})
	}
}

func TestNativeStartupSkipsEmptySubscriptionsAndClosedReader(t *testing.T) {
	r, err := backend.New(filepath.Join(t.TempDir(), "reader.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := newNativeReader(r)
	defer a.close()
	a.window = &mygo.Window{}
	a.refreshOnStartup()
	if a.pending != 0 || len(a.jobs) != 0 || a.refreshing || a.notice != "" {
		t.Fatal("empty startup queued a refresh")
	}
	a.close()
	a.refreshFeed("")
	if a.pending != 0 || len(a.jobs) != 0 || a.refreshing {
		t.Fatal("closed reader queued a refresh")
	}
}
