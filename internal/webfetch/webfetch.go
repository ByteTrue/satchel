// Package webfetch safely retrieves public web pages as paginated text.
//
// Fetching and error handling are selectively adapted from Magpie
// internal/gateway/search_fetch.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. Magpie is Copyright (c) 2026
// yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	fetchTimeout  = 30 * time.Second
	maxBodyBytes  = 5 << 20
	defaultChars  = 20000
	maxFetchChars = 100000
	maxRedirects  = 5
)

// Fetcher retrieves public HTTP(S) resources. Its zero value is ready to use.
// Proxy is an optional explicit HTTP, HTTPS, or SOCKS5 proxy URL. Environment
// proxies and private-address bypasses are intentionally not consulted.
type Fetcher struct {
	Proxy string

	lookupIP   lookupIPFunc
	dialPinned dialPinnedFunc
}

// Result is one rune-delimited page of a fetched resource.
type Result struct {
	URL        string `json:"url"`
	Title      string `json:"title,omitempty"`
	Text       string `json:"text"`
	NextOffset int    `json:"next_offset,omitempty"`
}

// Fetch gets rawURL, converts HTML to Markdown-like text, and returns at most
// maxChars runes beginning at offset. maxChars defaults to 20000 and is capped
// at 100000. NextOffset is nonzero only when more text remains.
func (f Fetcher) Fetch(ctx context.Context, rawURL string, offset, maxChars int) (Result, error) {
	if strings.TrimSpace(rawURL) == "" {
		return Result{}, errors.New("web_fetch needs a URL")
	}
	if offset < 0 {
		return Result{}, errors.New("offset cannot be negative")
	}
	if maxChars <= 0 {
		maxChars = defaultChars
	}
	maxChars = min(maxChars, maxFetchChars)
	u, err := parseHTTPURL(rawURL)
	if err != nil {
		return Result{}, err
	}
	proxy, err := parseProxy(f.Proxy)
	if err != nil {
		return Result{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	client := &http.Client{
		Transport: &guardedTransport{proxy: proxy, lookupIP: f.lookupIP, dialPinned: f.dialPinned},
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return validateHTTPURL(next.URL)
		},
	}
	title, text, finalURL, err := fetchPage(ctx, client, u)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, fmt.Errorf("web_fetch exceeded its 30 second budget: %w", context.DeadlineExceeded)
		}
		return Result{}, fmt.Errorf("web_fetch: %w", err)
	}

	runes := []rune(text)
	if offset >= len(runes) {
		return Result{URL: finalURL, Title: title}, nil
	}
	end := min(offset+maxChars, len(runes))
	next := 0
	if end < len(runes) {
		next = end
	}
	return Result{URL: finalURL, Title: title, Text: string(runes[offset:end]), NextOffset: next}, nil
}

func parseProxy(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
		// Do not echo a proxy URL: it may contain credentials.
		return nil, errors.New("proxy must be a valid http://, https://, or socks5:// URL")
	}
	return u, nil
}

// fetchPage downloads u and returns its title, text, and final URL after
// redirects. The guarded transport validates DNS again on every redirect.
func fetchPage(ctx context.Context, client *http.Client, u *url.URL) (title, text, finalURL string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; satchel-web-fetch)")

	last := req.URL
	c := *client
	checkRedirect := c.CheckRedirect
	c.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if checkRedirect != nil {
			if err := checkRedirect(next, via); err != nil {
				return err
			}
		}
		last = next.URL
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		// net/http's redirect error quotes the next URL; do not let it echo
		// userinfo that validateHTTPURL just rejected.
		if errors.Is(err, errEmbeddedCredentials) {
			return "", "", "", errEmbeddedCredentials
		}
		if errors.Is(err, errPrivateAddress) {
			return "", "", "", errPrivateAddress
		}
		return "", "", "", err
	}
	defer resp.Body.Close()
	finalURL = last.Redacted()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", "", "", err
	}
	if len(body) > maxBodyBytes {
		return "", "", "", errors.New("response body exceeds the 5 MiB limit")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	kind := fetchKind(mediaType)
	if resp.StatusCode >= 400 {
		message := fmt.Sprintf("the site answered %s", resp.Status)
		if kind != "" {
			if excerpt := fetchText(body, contentType, kind, last)[1]; strings.TrimSpace(excerpt) != "" {
				message += ": " + fetchClip(excerpt, 300)
			}
		}
		return "", "", "", errors.New(message)
	}
	if kind == "" {
		return "", "", "", fmt.Errorf("content type %q is not supported; web_fetch reads text only", mediaType)
	}
	got := fetchText(body, contentType, kind, last)
	return got[0], got[1], finalURL, nil
}

// fetchKind is "html" for HTML, "text" for other textual content, and ""
// for content outside the whitelist.
func fetchKind(mediaType string) string {
	switch {
	case mediaType == "text/html" || mediaType == "application/xhtml+xml":
		return "html"
	case strings.HasPrefix(mediaType, "text/"), mediaType == "application/json",
		mediaType == "application/xml", strings.HasSuffix(mediaType, "+json"),
		strings.HasSuffix(mediaType, "+xml"), mediaType == "application/javascript",
		mediaType == "application/x-ndjson":
		return "text"
	}
	return ""
}
