// Package backend implements the persistent RSS and Atom reader service.
package backend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/mmcdole/gofeed/atom"
)

const (
	maxResponseBytes   = 8 << 20
	maxArticlesPerFeed = 500 // Starred articles are retained in addition to this limit.
)

type State struct {
	Feeds    []Feed    `json:"feeds"`
	Articles []Article `json:"articles"`
}

type Feed struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	Title       string `json:"title"`
	SiteURL     string `json:"siteUrl"`
	LastFetched string `json:"lastFetched"`
	Error       string `json:"error"`
}

type Article struct {
	ID        string `json:"id"`
	FeedID    string `json:"feedId"`
	Title     string `json:"title"`
	URL       string `json:"url"`
	Author    string `json:"author"`
	Published string `json:"published"`
	Content   string `json:"content"`
	Summary   string `json:"summary"`
	Read      bool   `json:"read"`
	Starred   bool   `json:"starred"`
}

// Reader performs network requests without holding mu. subscriptions serializes
// subscription changes and fetches; article flags can still change during a fetch.
// All state changes are persisted before becoming visible in memory.
type Reader struct {
	mu            sync.Mutex
	subscriptions sync.Mutex
	state         State
	storePath     string
	client        *http.Client
}

func New(storePath string) (*Reader, error) {
	if strings.TrimSpace(storePath) == "" {
		return nil, errors.New("store path is required")
	}
	r := &Reader{storePath: storePath, state: State{Feeds: []Feed{}, Articles: []Article{}}, client: &http.Client{
		Timeout: 25 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			_, err := normalizeURL(req.URL.String())
			return err
		},
	}}
	data, err := os.ReadFile(storePath)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read subscriptions: %w", err)
	}
	var stored *State
	if err = json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("decode reader state: %w", err)
	}
	if stored == nil {
		return nil, errors.New("reader state must be a JSON object")
	}
	r.state = *stored
	if r.state.Feeds == nil {
		r.state.Feeds = []Feed{}
	}
	if r.state.Articles == nil {
		r.state.Articles = []Article{}
	}
	seen := map[string]bool{}
	urls := map[string]bool{}
	for i, f := range r.state.Feeds {
		u, err := normalizeURL(f.URL)
		if err != nil {
			return nil, fmt.Errorf("invalid stored feed: %w", err)
		}
		if f.ID == "" || seen[f.ID] {
			return nil, errors.New("invalid or duplicate stored feed ID")
		}
		if urls[u] {
			return nil, errors.New("duplicate stored feed URL")
		}
		urls[u] = true
		r.state.Feeds[i].URL = u
		seen[f.ID] = true
	}
	articleIDs := map[string]bool{}
	for i, a := range r.state.Articles {
		if !seen[a.FeedID] || a.ID == "" || articleIDs[a.ID] {
			return nil, errors.New("invalid stored article ID or feed reference")
		}
		if a.Published != "" {
			published, err := time.Parse(time.RFC3339Nano, a.Published)
			if err != nil {
				return nil, fmt.Errorf("invalid stored article date: %w", err)
			}
			r.state.Articles[i].Published = published.UTC().Format(time.RFC3339Nano)
		}
		articleIDs[a.ID] = true
	}
	sortArticles(r.state.Articles)
	return r, nil
}

func cloneState(s State) State {
	return State{Feeds: append([]Feed{}, s.Feeds...), Articles: append([]Article{}, s.Articles...)}
}

func (r *Reader) Snapshot() (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneState(r.state), nil
}

// commit requires mu. Rename in the same directory makes replacement atomic.
func (r *Reader) commit(next State) (State, error) {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return cloneState(r.state), err
	}
	dir := filepath.Dir(r.storePath)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return cloneState(r.state), err
	}
	f, err := os.CreateTemp(dir, ".reader-*.json")
	if err != nil {
		return cloneState(r.state), err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, r.storePath)
	}
	if err != nil {
		return cloneState(r.state), fmt.Errorf("save reader state: %w", err)
	}
	r.state = next
	return cloneState(next), nil
}

func normalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errors.New("invalid URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", errors.New("URL must be http or https with a host and no credentials")
	}
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "http" && u.Port() == "80") || (u.Scheme == "https" && u.Port() == "443") {
		u.Host = u.Hostname()
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

func safeLink(raw, base string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	normalized, err := normalizeURL(b.ResolveReference(u).String())
	if err != nil {
		return ""
	}
	return normalized
}

func hashID(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (r *Reader) Add(ctx context.Context, rawURL string) (State, error) {
	if err := ctx.Err(); err != nil {
		s, _ := r.Snapshot()
		return s, err
	}
	u, err := normalizeURL(rawURL)
	if err != nil {
		s, _ := r.Snapshot()
		return s, err
	}
	r.subscriptions.Lock()
	defer r.subscriptions.Unlock()
	r.mu.Lock()
	if err := ctx.Err(); err != nil {
		s := cloneState(r.state)
		r.mu.Unlock()
		return s, err
	}
	for _, f := range r.state.Feeds {
		if f.URL == u {
			s := cloneState(r.state)
			r.mu.Unlock()
			return s, nil
		}
	}
	id := hashID(u)
	next := cloneState(r.state)
	next.Feeds = append(next.Feeds, Feed{ID: id, URL: u, Title: u})
	s, err := r.commit(next)
	r.mu.Unlock()
	if err != nil {
		return s, err
	}
	return r.refreshFeed(ctx, id)
}

func (r *Reader) Remove(id string) (State, error) {
	r.subscriptions.Lock()
	defer r.subscriptions.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	next := cloneState(r.state)
	found := false
	next.Feeds = []Feed{}
	for _, f := range r.state.Feeds {
		if f.ID == id {
			found = true
		} else {
			next.Feeds = append(next.Feeds, f)
		}
	}
	if !found {
		return cloneState(r.state), errors.New("feed not found")
	}
	next.Articles = []Article{}
	for _, a := range r.state.Articles {
		if a.FeedID != id {
			next.Articles = append(next.Articles, a)
		}
	}
	return r.commit(next)
}

func (r *Reader) Refresh(ctx context.Context, id string) (State, error) {
	r.subscriptions.Lock()
	defer r.subscriptions.Unlock()
	if err := ctx.Err(); err != nil {
		s, _ := r.Snapshot()
		return s, err
	}
	if id != "" {
		return r.refreshFeed(ctx, id)
	}
	s, _ := r.Snapshot()
	var errs []error
	for _, f := range s.Feeds {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if _, err := r.refreshFeed(ctx, f.ID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Title, err))
		}
	}
	s, _ = r.Snapshot()
	return s, errors.Join(errs...)
}

func (r *Reader) fetch(ctx context.Context, address string) (*gofeed.Feed, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "GoRss/1.0")
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("feed exceeds 8 MiB response limit")
	}
	parser := gofeed.NewParser()
	parser.AtomTranslator = atomPublicationTranslator{}
	parsed, err := parser.ParseString(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	// Relative links are resolved against the final location after redirects.
	parsed.Link = safeLink(parsed.Link, resp.Request.URL.String())
	for _, item := range parsed.Items {
		if item != nil {
			item.Link = safeLink(item.Link, resp.Request.URL.String())
		}
	}
	return parsed, nil
}

// gofeed normally substitutes Updated for an absent Atom Published value.
// Keep them distinct so update-only entries retain their identity and first date.
type atomPublicationTranslator struct{}

func (atomPublicationTranslator) Translate(feed interface{}) (*gofeed.Feed, error) {
	parsed, err := (&gofeed.DefaultAtomTranslator{}).Translate(feed)
	if err != nil {
		return nil, err
	}
	source := feed.(*atom.Feed)
	for i, entry := range source.Entries {
		parsed.Items[i].Published = entry.Published
		parsed.Items[i].PublishedParsed = entry.PublishedParsed
	}
	return parsed, nil
}

// refreshFeed requires subscriptions, but only holds mu when taking/merging state.
func (r *Reader) refreshFeed(ctx context.Context, id string) (State, error) {
	s, _ := r.Snapshot()
	var feed Feed
	found := false
	for _, f := range s.Feeds {
		if f.ID == id {
			feed = f
			found = true
			break
		}
	}
	if !found {
		return s, errors.New("feed not found")
	}
	parsed, fetchErr := r.fetch(ctx, feed.URL)
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return cloneState(r.state), err
	}
	next := cloneState(r.state)
	for i := range next.Feeds {
		if next.Feeds[i].ID != id {
			continue
		}
		if fetchErr != nil {
			next.Feeds[i].Error = fetchErr.Error()
			continue
		}
		next.Feeds[i].Error = ""
		next.Feeds[i].LastFetched = time.Now().UTC().Format(time.RFC3339)
		if strings.TrimSpace(parsed.Title) != "" {
			next.Feeds[i].Title = parsed.Title
		}
		next.Feeds[i].SiteURL = safeLink(parsed.Link, feed.URL)
	}
	if fetchErr == nil {
		old := map[string]Article{}
		other := []Article{}
		for _, a := range next.Articles {
			if a.FeedID == id {
				old[a.ID] = a
			} else {
				other = append(other, a)
			}
		}
		for _, item := range parsed.Items {
			if item == nil {
				continue
			}
			link := safeLink(item.Link, feed.URL)
			key := item.GUID
			if key == "" {
				key = link
			}
			if key == "" {
				key = item.Title + "\x00" + item.Published
			}
			aid := hashID(id, key)
			// Retain the legacy ID when its exact fallback hash is known.
			if _, exists := old[aid]; !exists && item.GUID == "" && link == "" {
				legacyPublished := item.Published
				if parsed.FeedType == "atom" && legacyPublished == "" {
					legacyPublished = item.Updated
				}
				legacy := hashID(id, item.Title+"\x00"+legacyPublished+"\x00"+item.Updated)
				if _, exists := old[legacy]; exists {
					aid = legacy
				} else if parsed.FeedType == "atom" && item.Published == "" {
					match := ""
					for oldID, a := range old {
						if a.Title == item.Title && a.URL == "" && oldID == hashID(id, item.Title+"\x00"+a.Published+"\x00"+a.Published) {
							if match != "" {
								match = ""
								break
							}
							match = oldID
						}
					}
					if match != "" {
						aid = match
					}
				}
			}
			prior := old[aid]
			published := prior.Published
			if item.PublishedParsed != nil {
				published = item.PublishedParsed.UTC().Format(time.RFC3339Nano)
			} else if published == "" && item.UpdatedParsed != nil {
				published = item.UpdatedParsed.UTC().Format(time.RFC3339Nano)
			}
			if published == "" {
				published = time.Now().UTC().Format(time.RFC3339)
			}
			author := ""
			if item.Author != nil {
				author = item.Author.Name
			}
			content := item.Content
			if content == "" {
				content = item.Description
			}
			old[aid] = Article{ID: aid, FeedID: id, Title: item.Title, URL: link, Author: author, Published: published, Content: content, Summary: item.Description, Read: prior.Read, Starred: prior.Starred}
		}
		articles := make([]Article, 0, len(old))
		for _, a := range old {
			articles = append(articles, a)
		}
		sortArticles(articles)
		count := 0
		for _, a := range articles {
			if a.Starred {
				other = append(other, a)
			} else if count < maxArticlesPerFeed {
				other = append(other, a)
				count++
			}
		}
		sortArticles(other)
		next.Articles = other
	}
	result, saveErr := r.commit(next)
	return result, errors.Join(fetchErr, saveErr)
}

func sortArticles(articles []Article) {
	sort.Slice(articles, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, articles[i].Published)
		b, _ := time.Parse(time.RFC3339Nano, articles[j].Published)
		if a.Equal(b) {
			return articles[i].ID < articles[j].ID
		}
		return a.After(b)
	})
}

func (r *Reader) SetRead(id string, value bool) (State, error)    { return r.setFlag(id, value, false) }
func (r *Reader) SetStarred(id string, value bool) (State, error) { return r.setFlag(id, value, true) }
func (r *Reader) setFlag(id string, value, starred bool) (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	next := cloneState(r.state)
	for i := range next.Articles {
		if next.Articles[i].ID == id {
			if starred {
				next.Articles[i].Starred = value
			} else {
				next.Articles[i].Read = value
			}
			return r.commit(next)
		}
	}
	return cloneState(r.state), errors.New("article not found")
}

// MarkAllRead with an empty feedId marks every article read.
func (r *Reader) MarkAllRead(feedId string) (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if feedId != "" {
		found := false
		for _, f := range r.state.Feeds {
			if f.ID == feedId {
				found = true
				break
			}
		}
		if !found {
			return cloneState(r.state), errors.New("feed not found")
		}
	}
	next := cloneState(r.state)
	for i := range next.Articles {
		if feedId == "" || next.Articles[i].FeedID == feedId {
			next.Articles[i].Read = true
		}
	}
	return r.commit(next)
}

func (r *Reader) OpenArticle(id string) error {
	s, _ := r.Snapshot()
	for _, a := range s.Articles {
		if a.ID != id {
			continue
		}
		u, err := normalizeURL(a.URL)
		if err != nil {
			return err
		}
		if runtime.GOOS != "darwin" {
			return errors.New("opening articles is supported on macOS only")
		}
		return exec.Command("open", u).Run()
	}
	return errors.New("article not found")
}
