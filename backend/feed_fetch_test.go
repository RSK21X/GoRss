package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	modifiedV1 = "Mon, 12 Oct 2026 10:00:00 GMT"
	modifiedV2 = "Tue, 13 Oct 2026 10:00:00 GMT"
)

func TestConditionalRefreshPersistsValidatorsAndProtectsArticles(t *testing.T) {
	for _, test := range []struct {
		name, firstTag, nextTag, firstDate, nextDate string
	}{
		{"etag", `"v1"`, `"v2"`, "", ""},
		{"weak etag", `W/"v1"`, `W/"v2"`, "", ""},
		{"last modified", "", "", modifiedV1, modifiedV2},
		{"both", `"v1"`, `"v2"`, modifiedV1, modifiedV2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var phase atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				step := phase.Load()
				wantTag, wantDate := test.firstTag, test.firstDate
				if step >= 3 {
					wantTag, wantDate = test.nextTag, test.nextDate
				}
				if step == 0 || step == 6 {
					wantTag, wantDate = "", ""
				}
				if req.Header.Get("If-None-Match") != wantTag || req.Header.Get("If-Modified-Since") != wantDate {
					t.Errorf("phase %d: conditions %q / %q, want %q / %q", step, req.Header.Get("If-None-Match"), req.Header.Get("If-Modified-Since"), wantTag, wantDate)
				}
				switch step {
				case 1, 4:
					// Omitted response validators must not discard stored ones.
					w.WriteHeader(http.StatusNotModified)
					return
				case 0:
					w.Header().Set("ETag", test.firstTag)
					w.Header().Set("Last-Modified", test.firstDate)
				case 2:
					w.Header().Set("ETag", test.nextTag)
					w.Header().Set("Last-Modified", test.nextDate)
				case 3:
					w.Header().Set("ETag", `"invalid-body"`)
					w.Header().Set("Last-Modified", modifiedV2)
					fmt.Fprint(w, "<broken XML")
					return
				}
				fixture := atomFixture
				if step >= 2 {
					fixture = strings.Replace(fixture, "Atom story", "Updated story", 1)
				}
				fmt.Fprint(w, fixture)
			}))
			defer server.Close()
			r := newTestReader(t)
			s, err := r.Add(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if s.Feeds[0].ETag != test.firstTag || s.Feeds[0].LastModified != test.firstDate || s.Feeds[0].ValidatorURL != server.URL+"/" {
				t.Fatalf("missing response validators: %+v", s.Feeds[0])
			}
			r.SetRead(s.Articles[0].ID, true)
			r.SetStarred(s.Articles[0].ID, true)
			r, err = New(r.storePath)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := r.Snapshot()
			beforeData, _ := os.ReadFile(r.storePath)
			beforeFile, _ := os.Stat(r.storePath)
			phase.Store(1)
			s, err = r.Refresh(context.Background(), "")
			afterFile, _ := os.Stat(r.storePath)
			afterData, _ := os.ReadFile(r.storePath)
			if err != nil || !reflect.DeepEqual(before, s) || !os.SameFile(beforeFile, afterFile) || string(beforeData) != string(afterData) {
				t.Fatalf("unchanged 304 rewrote state or lost articles: %+v, %v", s, err)
			}
			phase.Store(2)
			s, err = r.Refresh(context.Background(), s.Feeds[0].ID)
			if err != nil || s.Articles[0].Title != "Updated story" || !s.Articles[0].Read || !s.Articles[0].Starred || s.Feeds[0].ETag != test.nextTag || s.Feeds[0].LastModified != test.nextDate {
				t.Fatalf("200 update lost flags/validators: %+v, %v", s, err)
			}
			before = s
			phase.Store(3)
			s, err = r.Refresh(context.Background(), "")
			if err == nil || !reflect.DeepEqual(before.Articles, s.Articles) || s.Feeds[0].ETag != before.Feeds[0].ETag || s.Feeds[0].LastModified != before.Feeds[0].LastModified || s.Feeds[0].LastFetched != before.Feeds[0].LastFetched {
				t.Fatalf("invalid body poisoned cached data: %+v, %v", s, err)
			}
			phase.Store(4)
			s, err = r.Refresh(context.Background(), "")
			if err != nil || s.Feeds[0].Error != "" || !reflect.DeepEqual(before, s) {
				t.Fatalf("304 did not recover a failed refresh: %+v, %v", s, err)
			}
			phase.Store(5)
			s, err = r.Refresh(context.Background(), "")
			if err != nil || s.Feeds[0].ETag != "" || s.Feeds[0].LastModified != "" || s.Feeds[0].ValidatorURL != "" {
				t.Fatalf("200 without validators retained stale ones: %+v, %v", s.Feeds, err)
			}
			phase.Store(6)
			if _, err := r.Refresh(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConditionalRefreshScopesValidatorsToFinalRedirectURL(t *testing.T) {
	var phase atomic.Int32
	var destination atomic.Value
	final := func(w http.ResponseWriter, req *http.Request) {
		step := phase.Load()
		wantTag := ""
		if step == 1 {
			wantTag = `"v0"`
		} else if step == 5 {
			wantTag = `"v4"`
		}
		if req.Header.Get("If-None-Match") != wantTag {
			t.Errorf("redirect phase %d: tag %q, want %q", step, req.Header.Get("If-None-Match"), wantTag)
		}
		if wantTag != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, step))
		fmt.Fprint(w, atomFixture)
	}
	first := httptest.NewServer(http.HandlerFunc(final))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(final))
	defer second.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != "" {
			t.Error("validators were sent to an intermediate redirect resource")
		}
		if req.URL.Path == "/" {
			http.Redirect(w, req, "/hop", http.StatusFound)
		} else {
			http.Redirect(w, req, destination.Load().(string), http.StatusFound)
		}
	}))
	defer origin.Close()
	targets := []string{first.URL + "/feed?q=1", first.URL + "/feed?q=1", first.URL + "/feed?q=2", first.URL + "/other?q=2", second.URL + "/other?q=2", second.URL + "/other?q=2"}
	destination.Store(targets[0])
	r := newTestReader(t)
	s, err := r.Add(context.Background(), origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.SetRead(s.Articles[0].ID, true)
	r.SetStarred(s.Articles[0].ID, true)
	for i, target := range targets {
		if i == 0 {
			continue
		}
		phase.Store(int32(i))
		destination.Store(target)
		s, err = r.Refresh(context.Background(), "")
		if err != nil || s.Feeds[0].ValidatorURL != target || !s.Articles[0].Read || !s.Articles[0].Starred {
			t.Fatalf("redirect phase %d: %+v, %v", i, s, err)
		}
	}
}

func TestConditionalRefreshMetadataCommitIsAtomic(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotModified} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var refresh atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if refresh.Load() {
					if req.Header.Get("If-None-Match") != `"v1"` {
						t.Error("failed persistence replaced the cached validator")
					}
					w.Header().Set("ETag", `W/"v1"`)
					w.Header().Set("Last-Modified", modifiedV2)
					if status == http.StatusNotModified {
						w.WriteHeader(status)
						return
					}
				} else {
					w.Header().Set("ETag", `"v1"`)
					w.Header().Set("Last-Modified", modifiedV1)
				}
				fmt.Fprint(w, atomFixture)
			}))
			defer server.Close()
			r := newTestReader(t)
			before, err := r.Add(context.Background(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			original := r.storePath
			blocked := filepath.Join(t.TempDir(), "blocked")
			if err := os.Mkdir(blocked, 0700); err != nil {
				t.Fatal(err)
			}
			r.storePath = blocked
			refresh.Store(true)
			after, err := r.Refresh(context.Background(), "")
			if err == nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed commit changed validators/articles: %+v, %v", after, err)
			}
			reopened, err := New(original)
			if err != nil {
				t.Fatal(err)
			}
			persisted, _ := reopened.Snapshot()
			if !reflect.DeepEqual(before, persisted) {
				t.Fatal("failed metadata commit changed disk state")
			}
			r.storePath = original
			after, err = r.Refresh(context.Background(), "")
			if err != nil || after.Feeds[0].ETag != `W/"v1"` || after.Feeds[0].LastModified != modifiedV2 {
				t.Fatalf("metadata recovery: %+v, %v", after, err)
			}
		})
	}
}

func TestConditionalRefreshRejectsUnsolicited304(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("ETag", `"not-cached"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	s, err := newTestReader(t).Add(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "304") || len(s.Articles) != 0 || s.Feeds[0].LastFetched != "" || s.Feeds[0].ETag != "" {
		t.Fatalf("unsolicited 304 was accepted as cached content: %+v, %v", s, err)
	}
}

func TestConditionalRefreshIgnoresInvalidStoredHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != "" {
			t.Error("invalid validators reached the server")
		}
		fmt.Fprint(w, atomFixture)
	}))
	defer server.Close()
	r := newTestReader(t)
	u := server.URL + "/"
	r.mu.Lock()
	_, err := r.commit(State{Feeds: []Feed{{ID: hashID(u), URL: u, Title: "Legacy", ValidatorURL: u, ETag: "*", LastModified: "not a date"}}, Articles: []Article{}})
	r.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	r, err = New(r.storePath)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Refresh(context.Background(), "")
	if err != nil || len(s.Articles) != 1 || s.Feeds[0].ETag != "" || s.Feeds[0].LastModified != "" {
		t.Fatalf("invalid cached headers prevented recovery: %+v, %v", s, err)
	}
}

func TestConditionalRefreshCancellationAfterHeadersPreservesState(t *testing.T) {
	var requests atomic.Int32
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Last-Modified", modifiedV1)
			fmt.Fprint(w, atomFixture)
			return
		}
		if req.Header.Get("If-None-Match") != `"v1"` || req.Header.Get("If-Modified-Since") != modifiedV1 {
			t.Error("missing conditions on cancelled refresh")
		}
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Last-Modified", modifiedV2)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-req.Context().Done()
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.SetRead(s.Articles[0].ID, true)
	r.SetStarred(s.Articles[0].ID, true)
	before, _ := r.Snapshot()
	data, _ := os.ReadFile(r.storePath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Refresh(ctx, ""); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("conditional refresh did not receive response headers")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := r.Snapshot()
	persisted, err := os.ReadFile(r.storePath)
	if err != nil || !reflect.DeepEqual(before, after) || string(data) != string(persisted) {
		t.Fatal("cancelled body download changed metadata or read/starred flags")
	}
}

func TestConditionalRefreshCachesValidEmptyFeed(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if requests.Add(1) > 1 {
			if req.Header.Get("If-None-Match") != `"empty-feed"` {
				t.Error("valid empty feed was not conditionally refreshed")
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"empty-feed"`)
		fmt.Fprint(w, `<rss version="2.0"><channel><title>Empty feed</title></channel></rss>`)
	}))
	defer server.Close()
	r := newTestReader(t)
	before, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	after, err := r.Refresh(context.Background(), "")
	if err != nil || len(after.Articles) != 0 || !reflect.DeepEqual(before, after) {
		t.Fatalf("empty feed 304: %+v, %v", after, err)
	}
}
