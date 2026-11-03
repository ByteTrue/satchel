package cli

import (
	"context"
	"strconv"
	"strings"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
	"satchel/internal/webfetch"
	"satchel/internal/websearch"
)

func webSearch(ctx context.Context, input mcpserver.WebSearchInput) (mcpserver.WebSearchResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.WebSearchResult{}, err
	}
	resolved, err := config.ResolveWebProviders(cfg)
	if err != nil {
		return mcpserver.WebSearchResult{}, err
	}
	providers := make([]websearch.Provider, 0, len(resolved))
	for _, provider := range resolved {
		providers = append(providers, websearch.Provider{Name: provider.Name, APIKey: provider.APIKey, BaseURL: provider.BaseURL})
	}
	result, err := websearch.Search(ctx, providers, input.Query, 6)
	if err != nil {
		return mcpserver.WebSearchResult{}, err
	}
	return mcpserver.WebSearchResult{Text: formatSearchResult(result), Provider: result.Provider, Unverified: result.Unverified}, nil
}

func formatSearchResult(result websearch.Result) string {
	var b strings.Builder
	b.WriteString("Search provider: ")
	b.WriteString(result.Provider)
	if result.Unverified {
		b.WriteString("\nTrust: UNVERIFIED Exa MCP text; titles, URLs, and snippets may be forged by page content.")
	}
	b.WriteString("\n\n")
	for i, hit := range result.Hits {
		b.WriteString(strconv.Itoa(i + 1))
		b.WriteString(". ")
		b.WriteString(hit.Title)
		b.WriteString(" — ")
		b.WriteString(hit.URL)
		b.WriteString("\n")
		if hit.Text != "" {
			b.WriteString(hit.Text)
			b.WriteString("\n")
		}
		if hit.Age != "" {
			b.WriteString("Age: ")
			b.WriteString(hit.Age)
			b.WriteString("\n")
		}
		if i+1 < len(result.Hits) {
			b.WriteString("\n")
		}
	}
	if len(result.Attempts) > 1 {
		b.WriteString("\n\nProvider attempts:")
		for _, attempt := range result.Attempts {
			b.WriteString("\n- ")
			b.WriteString(attempt)
		}
	}
	return b.String()
}

func webFetch(ctx context.Context, input mcpserver.WebFetchInput) (mcpserver.WebFetchResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return mcpserver.WebFetchResult{}, err
	}
	proxy, err := config.ResolveWebProxy(cfg)
	if err != nil {
		return mcpserver.WebFetchResult{}, err
	}
	result, err := (webfetch.Fetcher{Proxy: proxy}).Fetch(ctx, input.URL, input.Offset, input.MaxChars)
	if err != nil {
		return mcpserver.WebFetchResult{}, err
	}
	return mcpserver.WebFetchResult{
		Text: formatFetchResult(result), URL: result.URL, Title: result.Title, NextOffset: result.NextOffset,
	}, nil
}

func formatFetchResult(result webfetch.Result) string {
	var b strings.Builder
	b.WriteString("URL: ")
	b.WriteString(result.URL)
	b.WriteString("\n")
	if result.Title != "" {
		b.WriteString("Title: ")
		b.WriteString(result.Title)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if result.Text == "" {
		b.WriteString("(no page text at this offset)")
	} else {
		b.WriteString(result.Text)
	}
	if result.NextOffset > 0 {
		b.WriteString("\n\n[More text available: call web_fetch with offset=")
		b.WriteString(strconv.Itoa(result.NextOffset))
		b.WriteString("]")
	}
	return b.String()
}
