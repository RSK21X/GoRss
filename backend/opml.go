package backend

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

type opmlDocument struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    opmlHead `xml:"head"`
	Body    opmlBody `xml:"body"`
}
type opmlHead struct {
	Title string `xml:"title"`
}
type opmlBody struct {
	Outlines []opmlOutline `xml:"outline"`
}
type opmlOutline struct {
	Text     string        `xml:"text,attr"`
	Title    string        `xml:"title,attr,omitempty"`
	Type     string        `xml:"type,attr,omitempty"`
	XMLURL   string        `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string        `xml:"htmlUrl,attr,omitempty"`
	Outlines []opmlOutline `xml:"outline"`
}

// ImportOPML validates the entire document before atomically adding subscriptions.
// It does not fetch; call Refresh afterward to populate imported articles.
func (r *Reader) ImportOPML(text string) (State, error) {
	if len(text) > maxResponseBytes {
		s, _ := r.Snapshot()
		return s, errors.New("OPML exceeds 8 MiB limit")
	}
	var doc opmlDocument
	decoder := xml.NewDecoder(strings.NewReader(text))
	if err := decoder.Decode(&doc); err != nil {
		s, _ := r.Snapshot()
		return s, fmt.Errorf("parse OPML: %w", err)
	}
	// Decode stops after the root element. Consume the remainder so a
	// malformed suffix or second document cannot silently pass validation.
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil {
			switch token := token.(type) {
			case xml.Comment, xml.ProcInst:
				continue
			case xml.CharData:
				if strings.TrimSpace(string(token)) == "" {
					continue
				}
			}
			err = errors.New("unexpected content after OPML document")
		}
		s, _ := r.Snapshot()
		return s, fmt.Errorf("parse OPML: %w", err)
	}
	feeds := []Feed{}
	var visit func([]opmlOutline) error
	visit = func(outlines []opmlOutline) error {
		for _, o := range outlines {
			if strings.TrimSpace(o.XMLURL) != "" {
				u, err := normalizeURL(o.XMLURL)
				if err != nil {
					return fmt.Errorf("invalid OPML subscription: %w", err)
				}
				title := o.Title
				if title == "" {
					title = o.Text
				}
				if title == "" {
					title = u
				}
				feeds = append(feeds, Feed{ID: hashID(u), URL: u, Title: title, SiteURL: safeLink(o.HTMLURL, u)})
			}
			if err := visit(o.Outlines); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(doc.Body.Outlines); err != nil {
		s, _ := r.Snapshot()
		return s, err
	}
	r.subscriptions.Lock()
	defer r.subscriptions.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	next := cloneState(r.state)
	seen := map[string]bool{}
	for _, f := range next.Feeds {
		seen[f.URL] = true
	}
	for _, f := range feeds {
		if !seen[f.URL] {
			next.Feeds = append(next.Feeds, f)
			seen[f.URL] = true
		}
	}
	return r.commit(next)
}

func (r *Reader) ExportOPML() (string, error) {
	s, _ := r.Snapshot()
	doc := opmlDocument{Version: "2.0", Head: opmlHead{Title: "GoRss subscriptions"}, Body: opmlBody{Outlines: []opmlOutline{}}}
	for _, f := range s.Feeds {
		doc.Body.Outlines = append(doc.Body.Outlines, opmlOutline{Text: f.Title, Title: f.Title, Type: "rss", XMLURL: f.URL, HTMLURL: f.SiteURL})
	}
	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(data), nil
}
