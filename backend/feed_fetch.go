package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mmcdole/gofeed"
)

type feedResponse struct {
	parsed             *gofeed.Feed
	notModified        bool
	etag, lastModified string
	validatorURL       string
}

// Entity tags are opaque, including the optional weak prefix. Ignore malformed
// server/store values instead of sending invalid or wildcard conditions.
func validETag(value string) string {
	tag := strings.TrimPrefix(value, "W/")
	if len(tag) < 2 || tag[0] != '"' || tag[len(tag)-1] != '"' {
		return ""
	}
	for i := 1; i < len(tag)-1; i++ {
		if tag[i] < 0x21 || tag[i] == '"' || tag[i] == 0x7f {
			return ""
		}
	}
	return value
}

func validLastModified(value string) string {
	if _, err := http.ParseTime(value); err != nil {
		return ""
	}
	return value
}

func setFeedConditions(req *http.Request, feed Feed) {
	req.Header.Del("If-None-Match")
	req.Header.Del("If-Modified-Since")
	address, err := normalizeURL(req.URL.String())
	if err != nil || address != feed.ValidatorURL {
		return
	}
	if tag := validETag(feed.ETag); tag != "" {
		req.Header.Set("If-None-Match", tag)
	}
	if modified := validLastModified(feed.LastModified); modified != "" {
		req.Header.Set("If-Modified-Since", modified)
	}
}

func (r *Reader) fetch(ctx context.Context, feed Feed) (feedResponse, error) {
	var result feedResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("User-Agent", "GoRss/1.0")
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml;q=0.9, */*;q=0.1")
	setFeedConditions(req, feed)
	// Copy only the client configuration; requests still share its transport and
	// connection pool. Validators belong to the exact final representation URL,
	// including path and query, rather than to every step of a redirect chain.
	client := *r.client
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if checkRedirect != nil {
			if err := checkRedirect(next, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		setFeedConditions(next, feed)
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	result.etag = validETag(resp.Header.Get("ETag"))
	result.lastModified = validLastModified(resp.Header.Get("Last-Modified"))
	result.validatorURL, err = normalizeURL(resp.Request.URL.String())
	if err != nil {
		return result, err
	}
	if resp.StatusCode == http.StatusNotModified {
		if resp.Request.Header.Get("If-None-Match") == "" && resp.Request.Header.Get("If-Modified-Since") == "" {
			return result, errors.New("feed returned HTTP 304 without a matching conditional request")
		}
		result.notModified = true
		return result, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return result, err
	}
	if len(data) > maxResponseBytes {
		return result, errors.New("feed exceeds 8 MiB response limit")
	}
	parser := gofeed.NewParser()
	parser.AtomTranslator = atomPublicationTranslator{}
	parsed, err := parser.ParseString(string(data))
	if err != nil {
		return result, fmt.Errorf("parse feed: %w", err)
	}
	// Relative links are resolved against the final location after redirects.
	parsed.Link = safeLink(parsed.Link, resp.Request.URL.String())
	for _, item := range parsed.Items {
		if item != nil {
			item.Link = safeLink(item.Link, resp.Request.URL.String())
		}
	}
	if result.etag == "" && result.lastModified == "" {
		result.validatorURL = ""
	}
	result.parsed = parsed
	return result, nil
}
