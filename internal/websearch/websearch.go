// Package websearch implements Satchel's narrow standalone search APIs.
//
// Provider protocols, response handling, and safety code are selectively
// adapted from Magpie internal/gateway/search_api.go and
// internal/provider/search_api.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultMaxResults = 6
	maxResultsLimit   = 20
	maxTextBytes      = 1500
	maxResponseBytes  = 4 << 20
	attemptTimeout    = 30 * time.Second
	searchUserAgent   = "satchel-search/1"
)

var defaultBaseURLs = map[string]string{
	"exa-free":  "https://mcp.exa.ai/mcp",
	"bing":      "https://www.bing.com",
	"bocha":     "https://api.bochaai.com",
	"tavily":    "https://api.tavily.com",
	"brave":     "https://api.search.brave.com",
	"exa":       "https://api.exa.ai",
	"jina":      "https://s.jina.ai",
	"firecrawl": "https://api.firecrawl.dev",
}

// Provider is one configured search API. APIKey may contain several keys
// separated by commas or whitespace. BaseURL is required for SearXNG and
// optional for the other providers. HTTPClient defaults to http.DefaultClient.
type Provider struct {
	Name       string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// DefaultProviders returns the keyless provider used when no chain was configured.
func DefaultProviders() []Provider {
	return []Provider{{Name: "exa-free"}}
}

// Hit is one normalized search result. Age is empty when the API gave none.
type Hit struct {
	Title string
	URL   string
	Text  string
	Age   string
}

// Result is the first provider's non-empty answer. Attempts contains a
// sanitized diagnostic for every provider tried, including the successful one.
type Result struct {
	Provider   string
	Hits       []Hit
	Attempts   []string
	Unverified bool
}

// Search tries providers in order until one returns at least one hit.
// A nil provider slice selects DefaultProviders. maxResults defaults to 6 and
// is capped at 20. Empty answers and all errors
// are aggregated when no provider succeeds.
func Search(ctx context.Context, providers []Provider, query string, maxResults int) (result Result, err error) {
	var secrets []string
	for _, provider := range providers {
		secrets = append(secrets, splitKeys(provider.APIKey)...)
	}
	defer func() {
		for i := range result.Hits {
			result.Hits[i].Title = redact(result.Hits[i].Title, secrets)
			result.Hits[i].URL = redact(result.Hits[i].URL, secrets)
			result.Hits[i].Text = redact(result.Hits[i].Text, secrets)
			result.Hits[i].Age = redact(result.Hits[i].Age, secrets)
		}
		for i := range result.Attempts {
			result.Attempts[i] = redact(result.Attempts[i], secrets)
		}
		if err != nil {
			err = errors.New(redact(err.Error(), secrets))
		}
	}()

	query = strings.TrimSpace(query)
	if query == "" {
		return Result{}, errors.New("search query is required")
	}
	if providers == nil {
		providers = DefaultProviders()
	}
	if len(providers) == 0 {
		return Result{}, errors.New("no search providers configured")
	}
	if maxResults <= 0 {
		maxResults = defaultMaxResults
	}
	maxResults = min(maxResults, maxResultsLimit)

	var errs []error
	for _, provider := range providers {
		provider, err = normalizeProvider(provider)
		if err != nil {
			failure := fmt.Errorf("%s: %w", provider.Name, err)
			result.Attempts = append(result.Attempts, failure.Error())
			errs = append(errs, failure)
			continue
		}
		hits, searchErr := searchProvider(ctx, provider, query, maxResults)
		if searchErr != nil {
			failure := fmt.Errorf("%s: %w", provider.Name, searchErr)
			result.Attempts = append(result.Attempts, failure.Error())
			errs = append(errs, failure)
			continue
		}
		if len(hits) == 0 {
			failure := errors.New(provider.Name + ": no results")
			result.Attempts = append(result.Attempts, failure.Error())
			errs = append(errs, failure)
			continue
		}
		result.Provider = provider.Name
		result.Hits = hits
		result.Unverified = provider.Name == "exa-free"
		result.Attempts = append(result.Attempts, fmt.Sprintf("%s: %d results", provider.Name, len(hits)))
		return result, nil
	}
	return result, errors.Join(errs...)
}

func normalizeProvider(provider Provider) (Provider, error) {
	provider.Name = strings.ToLower(strings.TrimSpace(provider.Name))
	base, known := defaultBaseURLs[provider.Name]
	if !known && provider.Name != "searxng" {
		return provider, fmt.Errorf("unsupported search provider %q", provider.Name)
	}
	provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	if provider.BaseURL == "" {
		if provider.Name == "searxng" {
			return provider, errors.New("SearXNG base URL is required")
		}
		provider.BaseURL = base
	}
	u, err := url.Parse(provider.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return provider, fmt.Errorf("base URL %q is not an http:// or https:// address", provider.BaseURL)
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return provider, errors.New("base URL must not contain a query or fragment")
	}
	return provider, nil
}

func searchProvider(ctx context.Context, provider Provider, query string, limit int) ([]Hit, error) {
	keys := splitKeys(provider.APIKey)
	if len(keys) == 0 {
		switch provider.Name {
		case "exa-free", "bing", "searxng":
			keys = []string{""}
		default:
			return nil, errors.New("API key is required")
		}
	}
	stateID := provider.Name + " " + provider.BaseURL
	var errs []error
	for _, i := range keyOrder(stateID, keys, time.Now()) {
		key := keys[i]
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		hits, err := ask(attemptCtx, provider, key, query, limit)
		cancel()
		if err == nil {
			wakeKey(stateID, key)
			return hits, nil
		}
		var refused searchRefusal
		isRefusal := errors.As(err, &refused)
		if isRefusal && key != "" {
			d, why := restFor(provider.Name, refused.status, refused.retryAfter, time.Now())
			restKey(stateID, key, d, why, time.Now())
		}
		if len(keys) > 1 {
			err = fmt.Errorf("%s: %w", keyLabel(i, key), err)
		}
		errs = append(errs, err)
		if !isRefusal {
			break
		}
	}
	return nil, errors.Join(errs...)
}

// searchRefusal is an authentication, credit, or rate-limit failure for
// which another key may work.
type searchRefusal struct {
	status     int
	message    string
	retryAfter string
}

func (e searchRefusal) Error() string { return e.message }

func keyRefusal(status int) bool {
	switch status {
	case 401, 402, 403, 429, 432, 433:
		return true
	}
	return false
}

func ask(ctx context.Context, provider Provider, key, query string, limit int) ([]Hit, error) {
	switch provider.Name {
	case "exa-free":
		return searchExaFree(ctx, provider, query, limit)
	case "bing":
		return searchBing(ctx, provider, query, limit)
	}

	var request *http.Request
	var err error
	post := func(path string, body any) {
		b, _ := json.Marshal(body)
		request, err = http.NewRequestWithContext(ctx, http.MethodPost, provider.BaseURL+path, bytes.NewReader(b))
		if err == nil {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	get := func(path string, values url.Values) {
		request, err = http.NewRequestWithContext(ctx, http.MethodGet, provider.BaseURL+path+"?"+values.Encode(), nil)
	}

	switch provider.Name {
	case "bocha":
		post("/v1/web-search", map[string]any{"query": query, "summary": true, "count": limit, "freshness": "noLimit"})
	case "jina":
		get("/"+url.PathEscape(query), url.Values{"num": {fmt.Sprint(limit)}})
	case "tavily":
		post("/search", map[string]any{"query": query, "max_results": limit, "search_depth": "basic"})
	case "brave":
		get("/res/v1/web/search", url.Values{"q": {query}, "count": {fmt.Sprint(limit)}})
	case "exa":
		post("/search", map[string]any{"query": query, "numResults": limit,
			"contents": map[string]any{"text": map[string]any{"maxCharacters": maxTextBytes}}})
	case "firecrawl":
		post("/v2/search", map[string]any{"query": query, "limit": limit})
	case "searxng":
		get("/search", url.Values{"q": {query}, "format": {"json"}})
	}
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", searchUserAgent)
	if key != "" {
		switch provider.Name {
		case "brave":
			request.Header.Set("X-Subscription-Token", key)
		case "exa":
			request.Header.Set("X-API-Key", key)
		default:
			request.Header.Set("Authorization", "Bearer "+key)
		}
	}

	client := searchHTTPClient(provider)
	response, err := client.Do(request)
	if provider.Name == "firecrawl" && err == nil && response.StatusCode == http.StatusNotFound {
		// Satchel's previous provider uses v1 while Magpie uses v2. A 404 is
		// the endpoint-version signal; never retry a successful or charged call.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))
		_ = response.Body.Close()
		fallback := request.Clone(ctx)
		fallback.URL.Path = strings.TrimSuffix(fallback.URL.Path, "/v2/search") + "/v1/search"
		fallback.URL.RawPath = ""
		if request.GetBody != nil {
			fallback.Body, err = request.GetBody()
		}
		if err == nil {
			response, err = client.Do(fallback)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("no answer in time")
		}
		return nil, err
	}
	defer response.Body.Close()
	body, readErr := readResponse(response.Body)
	if readErr != nil {
		message := fmt.Sprintf("%s: %v", response.Status, readErr)
		if keyRefusal(response.StatusCode) {
			return nil, searchRefusal{response.StatusCode, message, response.Header.Get("Retry-After")}
		}
		return nil, errors.New(message)
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 && response.Header.Get("Location") != "" {
		return nil, errors.New(response.Status + ": cross-origin redirect refused")
	}
	if response.StatusCode >= 300 {
		message := redact(apiError(body, response.Status), []string{key})
		if keyRefusal(response.StatusCode) {
			return nil, searchRefusal{response.StatusCode, message, response.Header.Get("Retry-After")}
		}
		return nil, errors.New(message)
	}
	hits, err := readHits(provider.Name, body)
	if err != nil {
		return nil, fmt.Errorf("unexpected reply: %w", err)
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func searchHTTPClient(provider Provider) *http.Client {
	client := http.DefaultClient
	if provider.HTTPClient != nil {
		client = provider.HTTPClient
	}
	clone := *client
	clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if next.URL.Scheme != via[0].URL.Scheme || !strings.EqualFold(next.URL.Host, via[0].URL.Host) {
			return http.ErrUseLastResponse
		}
		return nil
	}
	return &clone
}

func readResponse(body io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxResponseBytes {
		return nil, errors.New("response exceeds 4 MiB")
	}
	return b, nil
}

func apiError(body []byte, status string) string {
	var v struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Msg     string          `json:"msg"`
		Detail  json.RawMessage `json:"detail"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	message := ""
	if json.Unmarshal(body, &v) == nil {
		if len(v.Detail) > 0 {
			v.Error = v.Detail
		}
		if len(v.Errors) > 0 {
			message = v.Errors[0].Message
		}
		var object struct{ Message string }
		if message == "" && json.Unmarshal(v.Error, &object) == nil {
			message = object.Message
		}
		var text string
		if message == "" && json.Unmarshal(v.Error, &text) == nil {
			message = text
		}
		var nested struct{ Error string }
		if message == "" && json.Unmarshal(v.Error, &nested) == nil {
			message = nested.Error
		}
		if message == "" {
			message = v.Message
		}
		if message == "" {
			message = v.Msg
		}
	}
	if message == "" {
		message = strings.Join(strings.Fields(string(body)), " ")
	}
	if message == "" {
		return status + ": refused with no details"
	}
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return status + ": " + message
}

type rawHit struct {
	Title              string `json:"title"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	Content            string `json:"content"`
	Description        string `json:"description"`
	Text               string `json:"text"`
	Markdown           string `json:"markdown"`
	Summary            string `json:"summary"`
	Snippet            string `json:"snippet"`
	Date               string `json:"date"`
	Age                string `json:"age"`
	PageAge            string `json:"page_age"`
	PublishedDate      string `json:"publishedDate"`
	PublishedDateSnake string `json:"published_date"`
}

func readHits(provider string, body []byte) ([]Hit, error) {
	var out struct {
		Results []rawHit `json:"results"`
		Web     struct {
			Results []rawHit `json:"results"`
		} `json:"web"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	results := out.Results
	switch provider {
	case "brave":
		results = out.Web.Results
	case "firecrawl":
		var v2 struct {
			Web []rawHit `json:"web"`
		}
		if err := json.Unmarshal(out.Data, &v2); err == nil && v2.Web != nil {
			results = v2.Web
		} else if err := json.Unmarshal(out.Data, &results); err != nil {
			return nil, err
		}
	case "bocha":
		var data struct {
			WebPages struct {
				Value []rawHit `json:"value"`
			} `json:"webPages"`
		}
		if len(out.Data) > 0 {
			if err := json.Unmarshal(out.Data, &data); err != nil {
				return nil, err
			}
		}
		results = data.WebPages.Value
	case "jina":
		var data struct {
			Results []rawHit `json:"results"`
		}
		if len(out.Data) > 0 {
			if err := json.Unmarshal(out.Data, &data); err != nil {
				return nil, err
			}
		}
		results = data.Results
	}

	hits := make([]Hit, 0, len(results))
	for _, result := range results {
		if result.URL == "" {
			continue
		}
		text := result.Content
		for _, candidate := range []string{result.Summary, result.Text, result.Description, result.Markdown, result.Snippet} {
			if text == "" {
				text = candidate
			}
		}
		title := plain(result.Title)
		if title == "" {
			title = plain(result.Name)
		}
		if title == "" {
			title = result.URL
		}
		age := result.Date
		for _, candidate := range []string{result.Age, result.PageAge, result.PublishedDate, result.PublishedDateSnake} {
			if age == "" {
				age = candidate
			}
		}
		hits = append(hits, Hit{Title: title, URL: result.URL, Text: clipText(plain(text), maxTextBytes), Age: age})
	}
	return hits, nil
}

var searchTags = regexp.MustCompile(`<[^>]*>`)

func plain(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(searchTags.ReplaceAllString(s, ""))), " ")
}

func clipText(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit] + "…"
}
