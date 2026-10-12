package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const rssFixture = `<?xml version="1.0"?><rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>Example RSS</title><link>https://example.com</link><description>Example</description><item><guid>stable-guid</guid><title>First story</title><link>https://example.com/story</link><author>writer@example.com</author><description><![CDATA[<p>Summary</p>]]></description><content:encoded><![CDATA[<p>Full story</p>]]></content:encoded><pubDate>Mon, 12 Oct 2026 10:00:00 GMT</pubDate></item><item><guid>undated</guid><title>No date</title><link>/undated</link></item></channel></rss>`
const atomFixture = `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Example Atom</title><id>urn:feed</id><link href="https://example.org/"/><updated>2026-10-12T12:00:00Z</updated><entry><id>urn:entry:1</id><title>Atom story</title><link href="https://example.org/article"/><author><name>Alice</name></author><published>2026-10-12T11:00:00Z</published><updated>2026-10-12T12:00:00Z</updated><summary>Short</summary><content type="html">&lt;p&gt;Atom content&lt;/p&gt;</content></entry></feed>`

func newTestReader(t *testing.T) *Reader {
	t.Helper()
	r, err := New(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRSSPersistenceRefreshAndRemove(t *testing.T) {
	var mu sync.Mutex
	fixture := rssFixture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { mu.Lock(); defer mu.Unlock(); fmt.Fprint(w, fixture) }))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Feeds) != 1 || len(s.Articles) != 2 || s.Feeds[0].Title != "Example RSS" || s.Feeds[0].LastFetched == "" {
		t.Fatalf("unexpected RSS state: %+v", s)
	}
	var first, undated Article
	for _, a := range s.Articles {
		if a.Title == "First story" {
			first = a
		} else {
			undated = a
		}
	}
	if first.Content != "<p>Full story</p>" || first.Published != "2026-10-12T10:00:00Z" || undated.URL != server.URL+"/undated" {
		t.Fatalf("incorrect articles: %+v %+v", first, undated)
	}
	if _, err = r.SetRead(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = r.SetStarred(first.ID, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	fixture = strings.Replace(rssFixture, "First story", "Updated story", 1)
	mu.Unlock()
	s, err = r.Refresh(context.Background(), s.Feeds[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.Articles {
		if a.ID == first.ID && (!a.Read || !a.Starred || a.Title != "Updated story") {
			t.Fatalf("refresh lost flags or content: %+v", a)
		}
		if a.ID == undated.ID && a.Published != undated.Published {
			t.Fatal("undated publication changed")
		}
	}
	reloaded, err := New(r.storePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reloaded.Snapshot()
	if len(persisted.Articles) != 2 {
		t.Fatal("articles not persisted")
	}
	for _, a := range persisted.Articles {
		if a.ID == first.ID && (!a.Read || !a.Starred) {
			t.Fatal("flags not persisted")
		}
	}
	persisted.Articles[0].Title = "mutated snapshot"
	fresh, _ := reloaded.Snapshot()
	if fresh.Articles[0].Title == "mutated snapshot" {
		t.Fatal("snapshot aliases service state")
	}
	if _, err = r.MarkAllRead(s.Feeds[0].ID); err != nil {
		t.Fatal(err)
	}
	s, err = r.Remove(s.Feeds[0].ID)
	if err != nil || len(s.Feeds) != 0 || len(s.Articles) != 0 {
		t.Fatalf("remove failed: %+v %v", s, err)
	}
}

func TestAtomAndDeduplication(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, atomFixture) }))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), "  "+server.URL+"#fragment  ")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Articles) != 1 || s.Articles[0].Author != "Alice" || s.Articles[0].Content != "<p>Atom content</p>" || s.Feeds[0].Title != "Example Atom" {
		t.Fatalf("unexpected Atom: %+v", s)
	}
	s, err = r.Add(context.Background(), server.URL+"/")
	if err != nil || len(s.Feeds) != 1 {
		t.Fatalf("duplicate subscription: %+v %v", s, err)
	}
}

func TestErrorsAndURLValidation(t *testing.T) {
	r := newTestReader(t)
	for _, raw := range []string{"", "javascript:alert(1)", "file:///tmp/feed", "ftp://example.com", "https://user:password@example.com", "https:///feed"} {
		if _, err := r.Add(context.Background(), raw); err == nil {
			t.Fatalf("accepted invalid URL %q", raw)
		}
	}
	if got, err := normalizeURL("HTTPS://EXAMPLE.COM:443#frag"); err != nil || got != "https://example.com/" {
		t.Fatalf("normalization: %s %v", got, err)
	}
	if _, err := r.SetRead("missing", true); err == nil {
		t.Fatal("missing article accepted")
	}
	if _, err := r.MarkAllRead("missing"); err == nil {
		t.Fatal("missing feed accepted")
	}
	if _, err := r.Refresh(context.Background(), "missing"); err == nil {
		t.Fatal("missing feed refreshed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	s, err := r.Add(context.Background(), server.URL)
	if err == nil || len(s.Feeds) != 1 || s.Feeds[0].Error == "" {
		t.Fatalf("failure not recorded: %+v %v", s, err)
	}
	if _, err := New(r.storePath); err != nil {
		t.Fatal("failed subscription not persisted", err)
	}
	if err := os.WriteFile(r.storePath, []byte("broken JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(r.storePath); err == nil {
		t.Fatal("corrupt JSON accepted")
	}
}

func TestFetchLimitsAndCancellation(t *testing.T) {
	for _, tc := range []struct{ name, body string }{{"invalid", "not a feed"}, {"oversized", strings.Repeat("x", maxResponseBytes+1)}} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			if _, err := newTestReader(t).Add(context.Background(), server.URL); err == nil {
				t.Fatal("bad response accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newTestReader(t).Add(ctx, "https://example.com/feed"); err == nil {
		t.Fatal("canceled fetch accepted")
	}
}

func TestFlagsDuringRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	block := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		wait := block
		mu.Unlock()
		if wait {
			close(started)
			<-release
		}
		fmt.Fprint(w, atomFixture)
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	block = true
	mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := r.Refresh(context.Background(), ""); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh did not start")
	}
	_, flagErr := r.SetRead(s.Articles[0].ID, true)
	_, starErr := r.SetStarred(s.Articles[0].ID, true)
	close(release)
	if flagErr != nil || starErr != nil {
		t.Fatal(flagErr, starErr)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	s, _ = r.Snapshot()
	if !s.Articles[0].Read || !s.Articles[0].Starred {
		t.Fatal("refresh overwrote concurrent flags")
	}
}

func TestRetentionPreservesStarred(t *testing.T) {
	r := newTestReader(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, atomFixture) }))
	defer server.Close()
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	for i := 0; i < maxArticlesPerFeed+20; i++ {
		r.state.Articles = append(r.state.Articles, Article{ID: fmt.Sprintf("old-%d", i), FeedID: s.Feeds[0].ID, Published: "2000-01-01T00:00:00Z", Starred: i < 2})
	}
	r.mu.Unlock()
	s, err = r.Refresh(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Articles) != maxArticlesPerFeed+2 {
		t.Fatalf("retention count: %d", len(s.Articles))
	}
	stars := 0
	for _, a := range s.Articles {
		if a.Starred {
			stars++
		}
	}
	if stars != 2 {
		t.Fatal("lost old starred articles")
	}
}

func TestOPMLRoundTripAndAtomicValidation(t *testing.T) {
	r := newTestReader(t)
	text := `<opml version="2.0"><body><outline text="Folder"><outline text="A &amp; B" xmlUrl="https://example.com/feed?a=1&amp;b=2" htmlUrl="https://example.com/"/></outline></body></opml>`
	s, err := r.ImportOPML(text)
	if err != nil || len(s.Feeds) != 1 || s.Feeds[0].Title != "A & B" {
		t.Fatalf("import: %+v %v", s, err)
	}
	out, err := r.ExportOPML()
	if err != nil {
		t.Fatal(err)
	}
	r2 := newTestReader(t)
	s, err = r2.ImportOPML(out)
	if err != nil || len(s.Feeds) != 1 || s.Feeds[0].URL != "https://example.com/feed?a=1&b=2" {
		t.Fatalf("round trip: %+v %v", s, err)
	}
	s, err = r2.ImportOPML(out)
	if err != nil || len(s.Feeds) != 1 {
		t.Fatal("import duplicated feed")
	}
	bad := `<opml><body><outline xmlUrl="https://example.org/feed"/><outline xmlUrl="file:///bad"/></body></opml>`
	s, err = r2.ImportOPML(bad)
	if err == nil || len(s.Feeds) != 1 {
		t.Fatal("invalid import partially applied")
	}
}

func TestRefreshFailurePreservesArticlesAndRecovers(t *testing.T) {
	var mu sync.Mutex
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		broken := fail
		mu.Unlock()
		if broken {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, atomFixture)
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	published, fetched := s.Articles[0].Published, s.Feeds[0].LastFetched
	mu.Lock()
	fail = true
	mu.Unlock()
	s, err = r.Refresh(context.Background(), "")
	if err == nil || len(s.Articles) != 1 || s.Feeds[0].Error == "" || s.Feeds[0].LastFetched != fetched || s.Articles[0].Published != published {
		t.Fatalf("failure damaged state: %+v %v", s, err)
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	s, err = r.Refresh(context.Background(), "")
	if err != nil || s.Feeds[0].Error != "" {
		t.Fatalf("did not recover: %+v %v", s, err)
	}
}

func TestRedirectRelativeLinksAndUnsafeArticleURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/" {
			http.Redirect(w, req, "/nested/feed.xml", http.StatusFound)
			return
		}
		fmt.Fprint(w, `<rss version="2.0"><channel><title>Redirect</title><link>./site</link><description>Test</description><item><guid>safe</guid><title>Safe</title><link>article</link></item><item><guid>unsafe</guid><title>Unsafe</title><link>javascript:alert(1)</link></item></channel></rss>`)
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if s.Feeds[0].SiteURL != server.URL+"/nested/site" {
		t.Fatalf("wrong site link %s", s.Feeds[0].SiteURL)
	}
	for _, a := range s.Articles {
		if a.Title == "Safe" && a.URL != server.URL+"/nested/article" {
			t.Fatal("wrong relative article link", a.URL)
		}
		if a.Title == "Unsafe" && (a.URL != "" || r.OpenArticle(a.ID) == nil) {
			t.Fatal("unsafe article link accepted")
		}
	}
}

func TestSaveFailureDoesNotMutateState(t *testing.T) {
	r := newTestReader(t)
	if err := os.Mkdir(r.storePath, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := r.ImportOPML(`<opml><body><outline xmlUrl="https://example.com/feed"/></body></opml>`)
	if err == nil || len(s.Feeds) != 0 {
		t.Fatalf("failed save changed state: %+v %v", s, err)
	}
}

func TestCanceledOperationsDoNotChangeState(t *testing.T) {
	r := newTestReader(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Refresh(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty refresh: %v", err)
	}
	if s, err := r.Add(ctx, "https://example.com/feed"); !errors.Is(err, context.Canceled) || len(s.Feeds) != 0 {
		t.Fatalf("canceled add persisted: %+v %v", s, err)
	}
	if _, err := os.Stat(r.storePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled add wrote state: %v", err)
	}
}

func TestCanceledRefreshPreservesState(t *testing.T) {
	started := make(chan struct{})
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if requests == 1 {
			fmt.Fprint(w, atomFixture)
			return
		}
		close(started)
		<-req.Context().Done()
	}))
	defer server.Close()
	r := newTestReader(t)
	before, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(r.storePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := r.Refresh(ctx, before.Feeds[0].ID); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, _ := r.Snapshot()
	persisted, err := os.ReadFile(r.storePath)
	if err != nil || !reflect.DeepEqual(before, after) || string(data) != string(persisted) {
		t.Fatalf("cancellation changed state: %+v %v", after, err)
	}
}

func TestFallbackIdentityAndUpdatedDateAreStable(t *testing.T) {
	var mu sync.Mutex
	updated := "2026-10-12T12:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, `<feed xmlns="http://www.w3.org/2005/Atom"><title>Example</title><entry><title>Unlinked story</title><updated>%s</updated><content>Story</content></entry></feed>`, updated)
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil || len(s.Articles) != 1 {
		t.Fatalf("add: %+v %v", s, err)
	}
	first := s.Articles[0]
	if _, err := r.SetRead(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetStarred(first.ID, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	updated = "2026-10-13T12:00:00Z"
	mu.Unlock()
	s, err = r.Refresh(context.Background(), "")
	if err != nil || len(s.Articles) != 1 || s.Articles[0].ID != first.ID || s.Articles[0].Published != first.Published || !s.Articles[0].Read || !s.Articles[0].Starred {
		t.Fatalf("update lost identity/date/flags: %+v %v", s, err)
	}
}

func TestStoredStateValidationAndCanonicalization(t *testing.T) {
	for _, data := range []string{
		`null`,
		`{"feeds":[{"id":"a","url":"https://EXAMPLE.com:443/feed#one"},{"id":"b","url":"https://example.com/feed"}]}`,
		`{"feeds":[{"id":"a","url":"https://example.com/feed"}],"articles":[{"id":"one","feedId":"a","published":"invalid"}]}`,
	} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := New(path); err == nil {
				t.Fatal("malformed persisted state accepted")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "state.json")
	state := State{Feeds: []Feed{{ID: "existing", URL: "HTTPS://EXAMPLE.COM:443/feed#old"}}, Articles: []Article{{ID: "older", FeedID: "existing", Published: "2026-10-12T12:00:00+02:00"}, {ID: "newer", FeedID: "existing", Published: "2026-10-12T11:00:00Z"}}}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := r.Add(context.Background(), "https://example.com/feed")
	if err != nil || len(s.Feeds) != 1 || s.Feeds[0].URL != "https://example.com/feed" || s.Articles[0].ID != "newer" {
		t.Fatalf("stored canonicalization/dedup/order: %+v %v", s, err)
	}
}

func TestOPMLRejectsTrailingContent(t *testing.T) {
	for _, suffix := range []string{`<opml/>`, `garbage`, `<broken`} {
		r := newTestReader(t)
		s, err := r.ImportOPML(`<opml><body><outline xmlUrl="https://example.com/feed"/></body></opml>` + suffix)
		if err == nil || len(s.Feeds) != 0 {
			t.Fatalf("accepted trailing content %q: %+v %v", suffix, s, err)
		}
	}
	r := newTestReader(t)
	if _, err := r.ImportOPML(`<opml/> <!-- trailing comment -->`); err != nil {
		t.Fatal(err)
	}
}

func TestFlagSaveFailurePreservesMemoryAndDisk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { fmt.Fprint(w, atomFixture) }))
	defer server.Close()
	r := newTestReader(t)
	before, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	original := r.storePath
	blocked := filepath.Join(filepath.Dir(original), "blocked")
	if err := os.Mkdir(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	r.storePath = blocked
	for _, set := range []func() (State, error){func() (State, error) { return r.SetRead(before.Articles[0].ID, true) }, func() (State, error) { return r.SetStarred(before.Articles[0].ID, true) }, func() (State, error) { return r.MarkAllRead("") }} {
		s, err := set()
		if err == nil || !reflect.DeepEqual(before, s) {
			t.Fatalf("failed flag save mutated memory: %+v %v", s, err)
		}
	}
	reopened, err := New(original)
	if err != nil {
		t.Fatal(err)
	}
	persisted, _ := reopened.Snapshot()
	if !reflect.DeepEqual(before, persisted) {
		t.Fatal("failed flag save mutated disk")
	}
}

func TestLegacyFallbackIDKeepsFlagsAfterAtomUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `<feed xmlns="http://www.w3.org/2005/Atom"><title>Example</title><entry><title>Legacy</title><updated>2026-10-13T12:00:00Z</updated></entry></feed>`)
	}))
	defer server.Close()
	r := newTestReader(t)
	u, err := normalizeURL(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fid := hashID(u)
	date := "2026-10-12T12:00:00Z"
	aid := hashID(fid, "Legacy\x00"+date+"\x00"+date)
	state := State{Feeds: []Feed{{ID: fid, URL: u}}, Articles: []Article{{ID: aid, FeedID: fid, Title: "Legacy", Published: date, Read: true, Starred: true}}}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(r.storePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err = New(r.storePath)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := r.Refresh(context.Background(), "")
		if err != nil || len(s.Articles) != 1 || s.Articles[0].ID != aid || !s.Articles[0].Read || !s.Articles[0].Starred || s.Articles[0].Published != date {
			t.Fatalf("legacy flags/date/identity lost: %+v %v", s, err)
		}
	}
}

func TestRetentionUsesChronologicalDates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `<rss version="2.0"><channel><title>Empty</title><description>Empty</description></channel></rss>`)
	}))
	defer server.Close()
	r := newTestReader(t)
	s, err := r.Add(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fid := s.Feeds[0].ID
	r.mu.Lock()
	for i := 0; i < maxArticlesPerFeed; i++ {
		r.state.Articles = append(r.state.Articles, Article{ID: fmt.Sprintf("new-%d", i), FeedID: fid, Published: "2026-10-12T11:00:00.1Z"})
	}
	r.state.Articles = append(r.state.Articles, Article{ID: "old-offset", FeedID: fid, Published: "2026-10-12T12:00:00+02:00"}, Article{ID: "old-fraction", FeedID: fid, Published: "2026-10-12T11:00:00Z"}, Article{ID: "starred", FeedID: fid, Published: "2000-01-01T00:00:00Z", Starred: true})
	r.mu.Unlock()
	s, err = r.Refresh(context.Background(), "")
	if err != nil || len(s.Articles) != maxArticlesPerFeed+1 {
		t.Fatalf("retention: %d %v", len(s.Articles), err)
	}
	for _, a := range s.Articles {
		if strings.HasPrefix(a.ID, "old-") {
			t.Fatalf("retained older article: %+v", a)
		}
	}
}
