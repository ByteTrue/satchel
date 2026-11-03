package websearch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	bingMaxAttempts       = 2
	bingSuspiciousBodyLen = 1024
	browserUserAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

var bingRetryDelay = 400 * time.Millisecond

func searchBing(ctx context.Context, provider Provider, query string, limit int) ([]Hit, error) {
	client := searchHTTPClient(provider)
	var lastErr error
	for attempt := 0; attempt < bingMaxAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(bingRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, errors.New("no answer in time")
			case <-timer.C:
			}
		}
		hits, err := askBing(ctx, client, provider.BaseURL, query, limit)
		if err == nil && len(hits) > 0 {
			return hits, nil
		}
		if ctx.Err() != nil {
			return nil, errors.New("no answer in time")
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("Bing search failed: %w", lastErr)
	}
	return nil, nil
}

func askBing(ctx context.Context, client *http.Client, baseURL, query string, limit int) ([]Hit, error) {
	values := url.Values{"q": {query}, "form": {"QBLH"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search?"+values.Encode(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", browserUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("no answer in time")
		}
		return nil, err
	}
	defer response.Body.Close()
	body, readErr := readResponse(response.Body)
	if readErr != nil {
		return nil, fmt.Errorf("%s: %w", response.Status, readErr)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.Header.Get("Location") != "" {
		return nil, errors.New(response.Status + ": cross-origin redirect refused")
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("Bing search error (%s)", response.Status)
	}
	hits := parseBingHTML(string(body), limit)
	if len(hits) == 0 && len(body) >= bingSuspiciousBodyLen {
		return nil, fmt.Errorf("Bing returned 200 with no parseable results (body %d chars, likely a verification page)", len(body))
	}
	return hits, nil
}

func parseBingHTML(body string, limit int) []Hit {
	if limit <= 0 {
		return nil
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return nil
	}
	var hits []Hit
	var visit func(*html.Node) bool
	visit = func(node *html.Node) bool {
		if isElement(node, "li") && hasClass(node, "b_algo") {
			if hit, ok := parseBingResult(node); ok {
				hits = append(hits, hit)
				if len(hits) == limit {
					return true
				}
			}
			return false
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if visit(child) {
				return true
			}
		}
		return false
	}
	visit(document)
	return hits
}

func parseBingResult(block *html.Node) (Hit, bool) {
	heading := findElement(block, "h2")
	if heading == nil {
		return Hit{}, false
	}
	var link *html.Node
	for child := heading.FirstChild; child != nil; child = child.NextSibling {
		if isElement(child, "a") {
			link = child
			break
		}
	}
	if link == nil {
		return Hit{}, false
	}
	location := decodeBingURL(attribute(link, "href"))
	if !validResultURL(location) {
		return Hit{}, false
	}
	title := strings.Join(strings.Fields(nodeText(link)), " ")
	if title == "" {
		return Hit{}, false
	}
	snippet := findLineclamp(block)
	if snippet == nil {
		if caption := findWithClass(block, "div", "b_caption"); caption != nil {
			snippet = findElement(caption, "p")
		}
	}
	return Hit{Title: title, URL: location, Text: clipText(strings.Join(strings.Fields(nodeText(snippet)), " "), maxTextBytes)}, true
}

func decodeBingURL(href string) string {
	base, _ := url.Parse("https://www.bing.com")
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return href
	}
	u = base.ResolveReference(u)
	host := strings.ToLower(u.Hostname())
	if (host == "bing.com" || strings.HasSuffix(host, ".bing.com")) && strings.HasPrefix(u.Path, "/ck/a") {
		raw := u.Query().Get("u")
		if raw != "" {
			raw = strings.TrimPrefix(raw, "a1")
			raw = strings.NewReplacer("-", "+", "_", "/", " ", "+").Replace(raw)
			decoded, decodeErr := base64.RawStdEncoding.DecodeString(strings.TrimRight(raw, "="))
			if decodeErr != nil {
				decoded, decodeErr = base64.StdEncoding.DecodeString(raw)
			}
			if decodeErr == nil && validResultURL(string(decoded)) {
				return string(decoded)
			}
		}
	}
	return u.String()
}

func isElement(node *html.Node, name string) bool {
	return node != nil && node.Type == html.ElementNode && node.Data == name
}

func attribute(node *html.Node, name string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if attribute.Key == name {
			return attribute.Val
		}
	}
	return ""
}

func hasClass(node *html.Node, class string) bool {
	for _, candidate := range strings.Fields(attribute(node, "class")) {
		if candidate == class {
			return true
		}
	}
	return false
}

func findElement(node *html.Node, name string) *html.Node {
	if node == nil {
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if isElement(child, name) {
			return child
		}
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func findLineclamp(node *html.Node) *html.Node {
	if node == nil {
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if isElement(child, "p") && strings.Contains(attribute(child, "class"), "b_lineclamp") {
			return child
		}
		if found := findLineclamp(child); found != nil {
			return found
		}
	}
	return nil
}

func findWithClass(node *html.Node, name, class string) *html.Node {
	if node == nil {
		return nil
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if isElement(child, name) && hasClass(child, class) {
			return child
		}
		if found := findWithClass(child, name, class); found != nil {
			return found
		}
	}
	return nil
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			parts = append(parts, current.Data)
			return
		}
		if isElement(current, "script") || isElement(current, "style") || isElement(current, "noscript") {
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}
