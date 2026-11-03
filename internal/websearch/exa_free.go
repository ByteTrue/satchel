package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	exaProtocolVersion = "2025-03-26"
	exaSnippetBytes    = 300
	exaRateLimitHint   = "Exa MCP free tier rate-limited the request. Wait a moment and retry, or run satchel setup to configure a key-backed provider (Tavily, Exa, Brave) for higher reliability."
)

var (
	exaBlockSeparator = regexp.MustCompile(`\n---+\n`)
	exaHighlights     = regexp.MustCompile(`(?m)^Highlights:[ \t]*`)
	exaTitle          = regexp.MustCompile(`(?m)^Title:[ \t]*(.+)$`)
	exaURL            = regexp.MustCompile(`(?m)^URL:[ \t]*(https?://\S+)$`)
	exaMarkdownLink   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	exaBareURL        = regexp.MustCompile(`(?m)^https?://\S+`)
)

// searchExaFree uses one official SDK MCP session per search. The hosted Exa
// tool returns untrusted Markdown, which is parsed separately below.
func searchExaFree(ctx context.Context, provider Provider, query string, limit int) ([]Hit, error) {
	client := mcp.NewClient(
		&mcp.Implementation{Name: "satchel-search", Version: "1"},
		&mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}},
	)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             provider.BaseURL,
		HTTPClient:           withResponseBudget(searchHTTPClient(provider)),
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
		MaxEventSize:         maxResponseBytes,
	}
	session, err := client.Connect(ctx, transport, &mcp.ClientSessionOptions{ProtocolVersion: exaProtocolVersion})
	if err != nil {
		return nil, exaSearchError(ctx, err)
	}
	defer func() { _ = session.Close() }()
	if session.ID() == "" {
		return nil, errors.New("Exa MCP: no session ID returned from initialize")
	}

	answer, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search_exa",
		Arguments: map[string]any{"query": query, "numResults": limit},
	})
	if err != nil {
		return nil, exaSearchError(ctx, err)
	}
	if answer.IsError {
		for _, content := range answer.Content {
			if value, ok := content.(*mcp.TextContent); ok && isExaRateLimit(value.Text) {
				return nil, errors.New(exaRateLimitHint)
			}
		}
		return nil, errors.New("Exa MCP tool returned an error")
	}
	text := ""
	for _, content := range answer.Content {
		if value, ok := content.(*mcp.TextContent); ok {
			text = value.Text
			break
		}
	}
	if text == "" {
		return nil, nil
	}
	hits := parseExaSearchText(text)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func exaSearchError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.New("no answer in time")
	}
	if isExaRateLimit(err.Error()) {
		return errors.New(exaRateLimitHint)
	}
	return fmt.Errorf("Exa MCP: %w", err)
}

func isExaRateLimit(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "too many requests") || strings.Contains(message, "rate limit") ||
		strings.Contains(message, "rate-limit") || strings.Contains(message, "429")
}

// The SDK bounds each SSE event, but its JSON and notification responses have
// no body limit. Cap every HTTP body at the transport boundary as well.
func withResponseBudget(client *http.Client) *http.Client {
	clone := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(request)
		if response != nil && response.Body != nil {
			response.Body = &budgetReadCloser{ReadCloser: response.Body, left: maxResponseBytes + 1}
		}
		return response, err
	})
	return &clone
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type budgetReadCloser struct {
	io.ReadCloser
	left int64
}

func (r *budgetReadCloser) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, errors.New("response exceeds 4 MiB")
	}
	if int64(len(p)) > r.left {
		p = p[:r.left]
	}
	n, err := r.ReadCloser.Read(p)
	r.left -= int64(n)
	if r.left == 0 {
		return n, errors.New("response exceeds 4 MiB")
	}
	return n, err
}

// parseExaSearchText extracts useful structure from Exa's plain-text response.
// The hosted MCP offers no structured metadata, so parsed blocks remain
// unverified: page content can imitate a complete Title/URL/Highlights block.
func parseExaSearchText(text string) []Hit {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var hits []Hit
	for _, block := range exaBlockSeparator.Split(text, -1) {
		highlights := exaHighlights.FindStringIndex(block)
		if highlights == nil {
			continue
		}
		header := block[:highlights[0]]
		title := exaTitle.FindStringSubmatch(header)
		location := exaURL.FindStringSubmatch(header)
		if len(title) == 0 || len(location) == 0 {
			continue
		}
		titleText := strings.TrimSpace(title[1])
		locationText := strings.TrimSpace(location[1])
		if titleText == "" || !validResultURL(locationText) {
			continue
		}
		snippet := strings.Join(strings.Fields(block[highlights[1]:]), " ")
		hits = append(hits, Hit{
			Title: titleText,
			URL:   locationText,
			Text:  clipText(snippet, exaSnippetBytes),
		})
	}
	if len(hits) > 0 {
		return hits[:min(len(hits), 10)]
	}

	matches := exaMarkdownLink.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		for _, location := range exaBareURL.FindAllString(text, -1) {
			if validResultURL(location) {
				hits = append(hits, Hit{Title: location, URL: location})
			}
		}
		return hits[:min(len(hits), 10)]
	}
	for i, match := range matches {
		location := text[match[4]:match[5]]
		if !validResultURL(location) {
			continue
		}
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		rawSnippet := strings.TrimLeft(text[match[1]:end], " \t\r\n:|-–—")
		hits = append(hits, Hit{
			Title: strings.TrimSpace(text[match[2]:match[3]]),
			URL:   location,
			Text:  clipText(strings.Join(strings.Fields(rawSnippet), " "), exaSnippetBytes),
		})
	}
	return hits
}

func validResultURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) && u.Host != ""
}
