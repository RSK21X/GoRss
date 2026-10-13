package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components/markdown"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/net/html"
	"gorss/backend"
)

var htmlMarkup = regexp.MustCompile(`(?i)<(?:p|div|br|hr|img|a|h[1-6]|ul|ol|li|blockquote|pre|code|table|span|strong|b|em|i|del|s|figure|section|article|iframe|script|style)(?:\s|/?>)`)
var imageClient = &http.Client{Timeout: 20 * time.Second}

// Onevra's explicit rem sizes (including inline code) use the context theme,
// not the document container's inherited font size. Scope the reading metrics
// to construction of this document; SetTheme also changes the window root,
// so restore it before building any other UI.
func renderArticleMarkdown(c *ui.Context, document *markdown.MarkdownDocument, prefs backend.Preferences, options markdown.MarkdownOptions) ui.Element {
	original := c.Theme()
	reading := *original
	reading.Font = prefs.FontFamily
	reading.FontSize = float32(prefs.FontSize)
	c.SetTheme(&reading)
	defer c.SetTheme(original)
	return document.Render(c, options).FillWidth().Font(prefs.FontFamily).FontSize(float32(prefs.FontSize)).LineHeight(float32(prefs.LineHeight))
}

// Derived data is owned by the UI thread and discarded for every published
// snapshot. Keep strings/indices only; retaining UI Elements across frames is
// invalid, and virtualizing Markdown would break whole-document selection.
type nativePresentation struct {
	articleBase             *backend.Article
	feedBase                *backend.Feed
	articleCount, feedCount int
	articles                map[string]int
	titles                  map[string]string
	counts                  map[string]int
	unread                  map[string]int
	previews                map[string]string
	search                  map[string]string
	rowIndices              map[string]int
	rowsValid               bool
	filter, feedID, query   string
}

// publishState also invalidates when a snapshot reuses the same slice storage.
func (a *nativeReader) publishState(state backend.State) {
	a.state = state
	a.presentation = nativePresentation{}
}

// Reload only when the displayed source or the base for relative images changes.
func (a *nativeReader) applySnapshot(state backend.State) {
	var previous backend.Article
	if article := a.article(); article != nil {
		previous = *article
	}
	a.publishState(state)
	if current := a.article(); current != nil && (current.Content != previous.Content || current.Summary != previous.Summary || current.URL != previous.URL) {
		a.loadArticle(*current)
	}
}

// Typography is rendered from prefs each frame. Only image visibility requires
// restarting article work; retain the document while replacement work runs.
func (a *nativeReader) applyPreferences(p backend.Preferences) {
	imagesChanged := a.prefs.ShowImages != p.ShowImages
	a.prefs = p
	if imagesChanged {
		if article := a.article(); article != nil {
			a.loadArticle(*article)
		}
	}
}

func (a *nativeReader) ensurePresentation() {
	var articleBase *backend.Article
	var feedBase *backend.Feed
	if len(a.state.Articles) > 0 {
		articleBase = &a.state.Articles[0]
	}
	if len(a.state.Feeds) > 0 {
		feedBase = &a.state.Feeds[0]
	}
	p := &a.presentation
	if p.articles != nil && p.articleBase == articleBase && p.feedBase == feedBase && p.articleCount == len(a.state.Articles) && p.feedCount == len(a.state.Feeds) {
		return
	}
	*p = nativePresentation{articleBase: articleBase, feedBase: feedBase, articleCount: len(a.state.Articles), feedCount: len(a.state.Feeds), articles: make(map[string]int, len(a.state.Articles)), titles: make(map[string]string, len(a.state.Feeds)), counts: map[string]int{"all": len(a.state.Articles)}, unread: make(map[string]int), previews: make(map[string]string), search: make(map[string]string)}
	for _, feed := range a.state.Feeds {
		p.titles[feed.ID] = feed.Title
	}
	for i, article := range a.state.Articles {
		p.articles[article.ID] = i
		if !article.Read {
			p.counts["unread"]++
			p.unread[article.FeedID]++
		}
		if article.Starred {
			p.counts["starred"]++
		}
	}
}

func (a *nativeReader) updateRows() {
	a.ensurePresentation()
	p := &a.presentation
	query := strings.ToLower(strings.TrimSpace(a.query))
	if p.rowsValid && p.filter == a.filter && p.feedID == a.feedID && p.query == query {
		return
	}
	a.rows = a.rows[:0]
	p.rowIndices = make(map[string]int)
	for _, article := range a.state.Articles {
		if a.feedID != "" && article.FeedID != a.feedID || a.filter == "unread" && article.Read || a.filter == "starred" && !article.Starred {
			continue
		}
		if query != "" {
			search, ok := p.search[article.ID]
			if !ok {
				search = strings.ToLower(article.Title + " " + article.Summary + " " + article.Author)
				p.search[article.ID] = search
			}
			if !strings.Contains(search, query) {
				continue
			}
		}
		p.rowIndices[article.ID] = len(a.rows)
		a.rows = append(a.rows, article)
	}
	p.rowsValid, p.filter, p.feedID, p.query = true, a.filter, a.feedID, query
}

func (a *nativeReader) articlePreview(id string) string {
	a.ensurePresentation()
	p := &a.presentation
	if preview, ok := p.previews[id]; ok {
		return preview
	}
	i, ok := p.articles[id]
	if !ok {
		return ""
	}
	article := a.state.Articles[i]
	preview := shortText(article.Summary, 240)
	// Only omit an application-generated summary that exactly repeats its title.
	// Source Markdown/HTML and the stored article remain untouched.
	if preview == strings.Join(strings.Fields(article.Title), " ") {
		preview = ""
	}
	p.previews[id] = preview
	return preview
}

func (a *nativeReader) clearArticle() {
	a.resetAI()
	if a.articleCancel != nil {
		a.articleCancel()
	}
	a.articleGeneration++
	a.document = nil
	a.loadedArticleID = ""
	a.images = nil
	a.articleLoading = false
	a.imageErrors = 0
}

func (a *nativeReader) loadArticle(article backend.Article) {
	// Keep the old layout until replacement content is ready, so a refresh or
	// image setting change cannot clamp a scrolled article to an empty view.
	previousDocument := a.document
	sameArticle := a.loadedArticleID == article.ID
	a.clearArticle()
	a.loadedArticleID = article.ID
	if sameArticle {
		a.document = previousDocument
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.articleCancel = cancel
	generation := a.articleGeneration
	showImages := a.prefs.ShowImages
	a.articleLoading = true
	go func() {
		source := article.Content
		if strings.TrimSpace(source) == "" {
			source = article.Summary
		}
		if articleHasHTML(source) {
			source = htmlMarkdown(source)
		}
		if strings.TrimSpace(source) == "" {
			source = "此订阅没有提供文章正文，请在浏览器中阅读原文。"
		}
		document := markdown.ParseMarkdown(source)
		var targets []string
		if showImages {
			targets = markdownImages(source)
		}
		a.window.Update(func() {
			if ctx.Err() != nil || generation != a.articleGeneration {
				return
			}
			a.document = document
			a.images = make(map[string]*ui.Bitmap)
			a.articleLoading = false
		})
		// Four concurrent fetches per article, bounded count, body size and decoded pixels.
		semaphore := make(chan struct{}, 4)
		var wg sync.WaitGroup
		for _, destination := range targets {
			if ctx.Err() != nil {
				break
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return
			}
			wg.Add(1)
			go func(target string) {
				defer wg.Done()
				defer func() { <-semaphore }()
				bitmap, err := fetchArticleImage(ctx, target, article.URL)
				if ctx.Err() != nil {
					return
				}
				a.window.Update(func() {
					if ctx.Err() != nil || generation != a.articleGeneration {
						return
					}
					if err != nil {
						a.imageErrors++
					} else {
						a.images[target] = bitmap
					}
				})
			}(destination)
		}
		wg.Wait()
	}()
}

func (a *nativeReader) openLink(destination, base string) {
	target, err := resolveArticleURL(destination, base)
	if err != nil {
		a.errorText = err.Error()
		return
	}
	a.run(func(context.Context) error { return mygo.Shell.OpenExternal(target) }, nil)
}

func resolveArticleURL(raw, base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if b, err := url.Parse(base); err == nil {
		u = b.ResolveReference(u)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return "", fmt.Errorf("不支持的链接地址")
	}
	return u.String(), nil
}

func fetchArticleImage(ctx context.Context, raw, base string) (*ui.Bitmap, error) {
	var data []byte
	if strings.HasPrefix(raw, "data:image/") {
		parts := strings.SplitN(raw, ",", 2)
		if len(parts) != 2 || !strings.HasSuffix(parts[0], ";base64") || len(parts[1]) > 16<<20 {
			return nil, fmt.Errorf("无效的图片数据")
		}
		var err error
		data, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, err
		}
	} else {
		target, err := resolveArticleURL(raw, base)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "GoRss/1.0")
		if strings.HasPrefix(base, "http") {
			req.Header.Set("Referer", base)
		}
		response, err := imageClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("图片 HTTP %d", response.StatusCode)
		}
		data, err = io.ReadAll(io.LimitReader(response.Body, (12<<20)+1))
		if err != nil {
			return nil, err
		}
		if len(data) > 12<<20 {
			return nil, fmt.Errorf("图片超过 12 MB")
		}
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 24_000_000 {
		return nil, fmt.Errorf("图片尺寸过大")
	}
	return ui.DecodeBitmap(data)
}

// HTML examples in Markdown code spans/fences are literal text, not feed HTML.
func articleHasHTML(source string) bool {
	if !htmlMarkup.MatchString(source) {
		return false
	}
	data := []byte(source)
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	found := false
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.HTMLBlock:
			found = htmlMarkup.Match(n.Lines().Value(data))
		case *ast.RawHTML:
			found = htmlMarkup.Match(n.Segments.Value(data))
		}
		if found {
			return ast.WalkStop, nil
		}
		return ast.WalkContinue, nil
	})
	return found
}

func markdownImages(source string) []string {
	data := []byte(source)
	root := goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser().Parse(text.NewReader(data))
	var result []string
	seen := map[string]bool{}
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if im, ok := n.(*ast.Image); ok && entering {
			// Match Onevra's image map lookup and link destination decoding.
			destination := string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(im.Destination))))
			if !seen[destination] && len(result) < 32 {
				seen[destination] = true
				result = append(result, destination)
			}
		}
		return ast.WalkContinue, nil
	})
	return result
}

func shortText(source string, limit int) string {
	if strings.Contains(source, "<") {
		root, err := html.Parse(strings.NewReader(source))
		if err == nil {
			var b strings.Builder
			var walk func(*html.Node)
			walk = func(n *html.Node) {
				if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
					return
				}
				if n.Type == html.TextNode {
					b.WriteString(n.Data)
					b.WriteByte(' ')
				}
				for child := n.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(root)
			source = b.String()
		}
	}
	source = strings.Join(strings.Fields(source), " ")
	runes := []rune(source)
	if len(runes) > limit {
		source = string(runes[:limit]) + "…"
	}
	return source
}

// Convert feed HTML to readable CommonMark. Images become standalone blocks,
// which allows Onevra's native bitmap renderer to show them rather than alt text.
func htmlMarkdown(source string) string {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return shortText(source, 200000)
	}
	// Code text must bypass Markdown escaping and HTML reparsing.
	var plainText func(*html.Node) string
	plainText = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			b.WriteString(plainText(child))
		}
		return b.String()
	}
	var render func(*html.Node) string
	children := func(n *html.Node) string {
		var b strings.Builder
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			b.WriteString(render(child))
		}
		return b.String()
	}
	attr := func(n *html.Node, key string) string {
		for _, a := range n.Attr {
			if a.Key == key {
				return a.Val
			}
		}
		return ""
	}
	escape := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "`", "\\`")
		return r.Replace(s)
	}
	render = func(n *html.Node) string {
		if n.Type == html.TextNode {
			value := n.Data
			// Retain word separation without HTML source indentation.
			fields := strings.Fields(value)
			if len(fields) == 0 {
				if value != "" {
					return " "
				}
				return ""
			}
			v := escape(strings.Join(fields, " "))
			if strings.ContainsAny(value[:1], " \n\r\t") {
				v = " " + v
			}
			if strings.ContainsAny(value[len(value)-1:], " \n\r\t") {
				v += " "
			}
			return v
		}
		if n.Type != html.ElementNode {
			return children(n)
		}
		switch n.Data {
		case "script", "style", "noscript", "iframe", "object", "embed", "svg", "form", "button", "input", "head":
			return ""
		case "img":
			src := attr(n, "src")
			if src == "" {
				src = attr(n, "data-src")
			}
			if src == "" {
				src = attr(n, "data-original")
			}
			if src == "" {
				return escape(attr(n, "alt"))
			}
			src = strings.NewReplacer("&", "&amp;", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(src)
			return "\n\n![" + escape(attr(n, "alt")) + "](<" + src + ">)\n\n"
		case "br":
			return "  \n"
		case "hr":
			return "\n\n---\n\n"
		case "p", "div", "section", "article", "figure", "figcaption", "header", "footer":
			return "\n\n" + strings.TrimSpace(children(n)) + "\n\n"
		case "h1", "h2", "h3", "h4", "h5", "h6":
			return "\n\n" + strings.Repeat("#", int(n.Data[1]-'0')) + " " + strings.TrimSpace(children(n)) + "\n\n"
		case "strong", "b":
			return "**" + strings.TrimSpace(children(n)) + "**"
		case "em", "i":
			return "*" + strings.TrimSpace(children(n)) + "*"
		case "del", "s":
			return "~~" + strings.TrimSpace(children(n)) + "~~"
		case "a":
			label := strings.TrimSpace(children(n))
			href := attr(n, "href")
			if href == "" || label == "" {
				return label
			}
			if strings.Contains(label, "![") {
				return label
			}
			return "[" + label + "](<" + strings.NewReplacer("&", "&amp;", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(href) + ">)"
		case "pre":
			var b strings.Builder
			var plain func(*html.Node)
			plain = func(x *html.Node) {
				if x.Type == html.TextNode {
					b.WriteString(x.Data)
				}
				for child := x.FirstChild; child != nil; child = child.NextSibling {
					plain(child)
				}
			}
			plain(n)
			fence := "```"
			for strings.Contains(b.String(), fence) {
				fence += "`"
			}
			return "\n\n" + fence + "\n" + strings.TrimSuffix(b.String(), "\n") + "\n" + fence + "\n\n"
		case "code":
			v := strings.ReplaceAll(plainText(n), "\n", " ")
			fence := "`"
			for strings.Contains(v, fence) {
				fence += "`"
			}
			return fence + " " + v + " " + fence
		case "blockquote":
			lines := strings.Split(strings.TrimSpace(children(n)), "\n")
			for i := range lines {
				lines[i] = "> " + lines[i]
			}
			return "\n\n" + strings.Join(lines, "\n") + "\n\n"
		case "ul", "ol":
			var b strings.Builder
			i := 1
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				if child.Type != html.ElementNode || child.Data != "li" {
					continue
				}
				marker := "- "
				if n.Data == "ol" {
					marker = fmt.Sprintf("%d. ", i)
				}
				i++
				value := strings.TrimSpace(children(child))
				value = strings.ReplaceAll(value, "\n", "\n    ")
				b.WriteString(marker + value + "\n")
			}
			return "\n\n" + b.String() + "\n"
		case "table":
			var rows [][]string
			var find func(*html.Node)
			find = func(x *html.Node) {
				if x.Type == html.ElementNode && x.Data == "tr" {
					var row []string
					for cell := x.FirstChild; cell != nil; cell = cell.NextSibling {
						if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
							row = append(row, strings.ReplaceAll(strings.Join(strings.Fields(children(cell)), " "), "|", "\\|"))
						}
					}
					if len(row) > 0 {
						rows = append(rows, row)
					}
					return
				}
				for child := x.FirstChild; child != nil; child = child.NextSibling {
					find(child)
				}
			}
			find(n)
			if len(rows) == 0 {
				return ""
			}
			width := 0
			for _, row := range rows {
				width = max(width, len(row))
			}
			var b strings.Builder
			b.WriteString("\n\n")
			for i, row := range rows {
				b.WriteString("| ")
				for j := 0; j < width; j++ {
					if j < len(row) {
						b.WriteString(row[j])
					}
					b.WriteString(" | ")
				}
				b.WriteByte('\n')
				if i == 0 {
					b.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
				}
			}
			b.WriteString("\n\n")
			return b.String()
		default:
			return children(n)
		}
	}
	result := strings.TrimSpace(render(root))
	// Do not collapse line breaks inside fenced code blocks.
	return result
}
