package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func awaitFeedRequest(t *testing.T, started <-chan int) int {
	t.Helper()
	select {
	case i := <-started:
		return i
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not start four concurrent requests or advance its queue")
		return -1
	}
}

func TestConcurrentRefreshLimitProgressAndFlags(t *testing.T) {
	const count = 8
	var refresh atomic.Bool
	var active, peak, requests atomic.Int32
	started := make(chan int, count)
	release := make([]chan struct{}, count)
	once := make([]sync.Once, count)
	for i := range release {
		release[i] = make(chan struct{})
	}
	unblock := func(i int) { once[i].Do(func() { close(release[i]) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		i, _ := strconv.Atoi(strings.TrimPrefix(req.URL.Path, "/"))
		title, tag := fmt.Sprintf("Feed %d", i), fmt.Sprintf(`"%d-v1"`, i)
		if refresh.Load() {
			n := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); n > previous; previous = peak.Load() {
				if peak.CompareAndSwap(previous, n) {
					break
				}
			}
			requests.Add(1)
			started <- i
			select {
			case <-release[i]:
			case <-req.Context().Done():
				return
			}
			if req.Header.Get("If-None-Match") != tag {
				t.Errorf("feed %d conditional tag: %q", i, req.Header.Get("If-None-Match"))
			}
			if i%2 == 0 {
				w.Header().Set("ETag", tag)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			title, tag = "Updated "+title, fmt.Sprintf(`"%d-v2"`, i)
		}
		w.Header().Set("ETag", tag)
		fmt.Fprint(w, strings.Replace(atomFixture, "Atom story", title, 1))
	}))
	defer server.Close()
	defer func() {
		for i := range release {
			unblock(i)
		}
	}()
	r := newTestReader(t)
	feedIndex := make(map[string]int)
	for i := range count {
		s, err := r.Add(context.Background(), fmt.Sprintf("%s/%d", server.URL, i))
		if err != nil {
			t.Fatal(err)
		}
		feedIndex[s.Feeds[i].ID] = i
	}
	before, _ := r.Snapshot()
	for _, article := range before.Articles {
		if feedIndex[article.FeedID]%3 != 0 {
			r.SetRead(article.ID, true)
			r.SetStarred(article.ID, true)
		}
	}
	refresh.Store(true)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Refresh(ctx, ""); done <- err }()
	first := -1
	for range maxRefreshWorkers {
		first = awaitFeedRequest(t, started)
	}
	// Change flags in both directions while requests are blocked. A worker
	// must merge the latest values, including changes on other feeds.
	for _, article := range before.Articles {
		value := feedIndex[article.FeedID]%3 == 0
		if _, err := r.SetRead(article.ID, value); err != nil {
			t.Fatal(err)
		}
		if _, err := r.SetStarred(article.ID, value); err != nil {
			t.Fatal(err)
		}
	}
	if requests.Load() != maxRefreshWorkers {
		t.Fatalf("started %d requests before releasing a worker", requests.Load())
	}
	unblock(first)
	awaitFeedRequest(t, started) // A free worker advances while the other three wait.
	for i := range release {
		unblock(i)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if requests.Load() != count || peak.Load() != maxRefreshWorkers {
		t.Fatalf("requests=%d, peak=%d", requests.Load(), peak.Load())
	}
	after, _ := r.Snapshot()
	if len(after.Articles) != count {
		t.Fatalf("concurrent merges lost articles: %d", len(after.Articles))
	}
	for _, article := range after.Articles {
		i := feedIndex[article.FeedID]
		want := fmt.Sprintf("Feed %d", i)
		if i%2 != 0 {
			want = "Updated " + want
		}
		if article.Read != (i%3 == 0) || article.Starred != (i%3 == 0) || article.Title != want {
			t.Fatalf("concurrent refresh lost flags/content: %+v", article)
		}
	}
	reopened, err := New(r.storePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reopened.Snapshot()
	if !reflect.DeepEqual(after, persisted) {
		t.Fatal("concurrent results and flags were not persisted")
	}
}

func TestConcurrentRefreshCancellationStopsQueuedRequests(t *testing.T) {
	const count = 10
	var requests atomic.Int32
	started := make(chan int, count)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		started <- 1
		<-req.Context().Done()
	}))
	defer server.Close()
	r := newTestReader(t)
	var opml strings.Builder
	opml.WriteString(`<opml><body>`)
	for i := range count {
		fmt.Fprintf(&opml, `<outline xmlUrl="%s/%d"/>`, server.URL, i)
	}
	opml.WriteString(`</body></opml>`)
	before, err := r.ImportOPML(opml.String())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(r.storePath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Refresh(ctx, ""); done <- err }()
	for range maxRefreshWorkers {
		awaitFeedRequest(t, started)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := r.Snapshot()
	persisted, err := os.ReadFile(r.storePath)
	if err != nil || !reflect.DeepEqual(before, after) || string(data) != string(persisted) || requests.Load() != maxRefreshWorkers {
		t.Fatalf("cancelled batch changed data or fetched queued feeds: requests=%d, err=%v", requests.Load(), err)
	}
}

func TestConcurrentRefreshReportsFailuresAndKeepsSuccessfulFeeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/bad1", "/bad2":
			w.WriteHeader(http.StatusBadGateway)
		default:
			fmt.Fprint(w, atomFixture)
		}
	}))
	defer server.Close()
	r := newTestReader(t)
	_, err := r.ImportOPML(fmt.Sprintf(`<opml><body><outline text="first failure" xmlUrl="%s/bad1"/><outline xmlUrl="%s/good"/><outline text="second failure" xmlUrl="%s/bad2"/></body></opml>`, server.URL, server.URL, server.URL))
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Refresh(context.Background(), "")
	if err == nil || len(s.Articles) != 1 || s.Feeds[0].Error == "" || s.Feeds[1].Error != "" || s.Feeds[2].Error == "" {
		t.Fatalf("batch failure lost results: %+v, %v", s, err)
	}
	if strings.Index(err.Error(), "first failure") >= strings.Index(err.Error(), "second failure") {
		t.Fatal("batch errors changed order with request completion")
	}
}
