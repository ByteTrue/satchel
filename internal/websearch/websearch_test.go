package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func isolateKeys(t *testing.T) {
	t.Helper()
	resetKeyState()
	t.Cleanup(resetKeyState)
}

func TestProviderContracts(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		method     string
		path       string
		authHeader string
		authValue  string
		query      url.Values
		body       map[string]any
		response   string
		wantTitle  string
		wantText   string
		wantAge    string
	}{
		{
			name: "tavily", key: "tvly-test-key-0001", method: http.MethodPost, path: "/search",
			authHeader: "Authorization", authValue: "Bearer tvly-test-key-0001",
			body:      map[string]any{"query": "latest go", "max_results": float64(6), "search_depth": "basic"},
			response:  `{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","content":"Go 1.27.1 is out","published_date":"2026-10-01"}]}`,
			wantTitle: "Go downloads", wantText: "Go 1.27.1 is out", wantAge: "2026-10-01",
		},
		{
			name: "brave", key: "brave-test-key", method: http.MethodGet, path: "/res/v1/web/search",
			authHeader: "X-Subscription-Token", authValue: "brave-test-key",
			query:     url.Values{"q": {"latest go"}, "count": {"6"}},
			response:  `{"web":{"results":[{"title":"Go &amp; <strong>downloads</strong>","url":"https://go.dev/dl/","description":"The <strong>latest</strong> Go","age":"2 days ago"}]}}`,
			wantTitle: "Go & downloads", wantText: "The latest Go", wantAge: "2 days ago",
		},
		{
			name: "exa", key: "exa-test-key", method: http.MethodPost, path: "/search",
			authHeader: "X-API-Key", authValue: "exa-test-key",
			body: map[string]any{"query": "latest go", "numResults": float64(6),
				"contents": map[string]any{"text": map[string]any{"maxCharacters": float64(1500)}}},
			response:  `{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","text":"Go 1.27.1 was released","publishedDate":"2026-10-01T00:00:00Z"}]}`,
			wantTitle: "Go downloads", wantText: "Go 1.27.1 was released", wantAge: "2026-10-01T00:00:00Z",
		},
		{
			name: "firecrawl", key: "fc-test-key", method: http.MethodPost, path: "/v2/search",
			authHeader: "Authorization", authValue: "Bearer fc-test-key",
			body:      map[string]any{"query": "latest go", "limit": float64(6)},
			response:  `{"success":true,"data":{"web":[{"title":"Go downloads","url":"https://go.dev/dl/","description":"Download Go 1.27.1"}]}}`,
			wantTitle: "Go downloads", wantText: "Download Go 1.27.1",
		},
		{
			name: "searxng", method: http.MethodGet, path: "/search",
			query:     url.Values{"q": {"latest go"}, "format": {"json"}},
			response:  `{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","content":"Go 1.27.1 is available"},{"title":"ignored","url":""}]}`,
			wantTitle: "Go downloads", wantText: "Go 1.27.1 is available",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateKeys(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if got := r.Header.Get(test.authHeader); got != test.authValue {
					t.Errorf("%s = %q, want %q", test.authHeader, got, test.authValue)
				}
				if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") != searchUserAgent {
					t.Errorf("headers = %+v", r.Header)
				}
				if test.query != nil && !reflect.DeepEqual(r.URL.Query(), test.query) {
					t.Errorf("query = %v, want %v", r.URL.Query(), test.query)
				}
				if test.body != nil {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode request: %v", err)
					} else if !reflect.DeepEqual(body, test.body) {
						t.Errorf("body = %#v, want %#v", body, test.body)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, test.response)
			}))
			defer server.Close()

			result, err := Search(context.Background(), []Provider{{
				Name: test.name, APIKey: test.key, BaseURL: server.URL,
			}}, " latest go ", 0)
			if err != nil {
				t.Fatal(err)
			}
			if result.Provider != test.name || len(result.Hits) != 1 || len(result.Attempts) != 1 {
				t.Fatalf("result = %+v", result)
			}
			hit := result.Hits[0]
			if hit.Title != test.wantTitle || hit.URL != "https://go.dev/dl/" || hit.Text != test.wantText || hit.Age != test.wantAge {
				t.Errorf("hit = %+v", hit)
			}
		})
	}
}

func TestFirecrawlV1Response(t *testing.T) {
	hits, err := readHits("firecrawl", []byte(`{"data":[{"title":"Go","url":"https://go.dev","markdown":"Go **downloads**"}]}`))
	if err != nil || len(hits) != 1 || hits[0].Text != "Go **downloads**" {
		t.Fatalf("hits = %+v, err = %v", hits, err)
	}
}

func TestMaxResultsCap(t *testing.T) {
	isolateKeys(t)
	gotLimit := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxResults int `json:"max_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		gotLimit = body.MaxResults
		results := make([]map[string]string, 25)
		for i := range results {
			results[i] = map[string]string{"title": fmt.Sprint(i), "url": fmt.Sprintf("https://example.com/%d", i)}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	result, err := Search(context.Background(), []Provider{{Name: "tavily", APIKey: "tvly-test-key-0001", BaseURL: server.URL}}, "go", 100)
	if err != nil {
		t.Fatal(err)
	}
	if gotLimit != 20 || len(result.Hits) != 20 {
		t.Fatalf("requested %d, received %d hits", gotLimit, len(result.Hits))
	}
}

func TestFallbackAndErrorAggregation(t *testing.T) {
	isolateKeys(t)
	var mu sync.Mutex
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := strings.TrimPrefix(r.URL.Path, "/")
		kind = strings.TrimSuffix(kind, "/search")
		mu.Lock()
		asked = append(asked, kind)
		mu.Unlock()
		switch kind {
		case "empty":
			io.WriteString(w, `{"results":[]}`)
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, `{"error":{"message":"down"}}`)
		case "ok":
			io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev","content":"downloads"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	providers := []Provider{
		{Name: "tavily", APIKey: "tvly-test-key-0001", BaseURL: server.URL + "/empty"},
		{Name: "tavily", APIKey: "tvly-test-key-0002", BaseURL: server.URL + "/broken"},
		{Name: "tavily", APIKey: "tvly-test-key-0003", BaseURL: server.URL + "/ok"},
	}

	result, err := Search(context.Background(), providers, "go", 6)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotAsked := slices.Clone(asked)
	mu.Unlock()
	if !slices.Equal(gotAsked, []string{"empty", "broken", "ok"}) {
		t.Errorf("asked = %v", gotAsked)
	}
	if result.Provider != "tavily" || len(result.Hits) != 1 || len(result.Attempts) != 3 ||
		!strings.Contains(result.Attempts[0], "no results") || !strings.Contains(result.Attempts[1], "500") {
		t.Errorf("result = %+v", result)
	}

	failed, err := Search(context.Background(), providers[:2], "go", 6)
	if err == nil || !strings.Contains(err.Error(), "no results") || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "down") {
		t.Fatalf("aggregate error = %v", err)
	}
	if failed.Provider != "" || failed.Hits != nil || len(failed.Attempts) != 2 {
		t.Errorf("failed result = %+v", failed)
	}
}

func TestKeysRotateAndRefusedKeyRests(t *testing.T) {
	isolateKeys(t)
	bad := "tvly-dev-SECRETbad00000000aaaa"
	good := "tvly-dev-SECRETgood0000000bbbb"
	var mu sync.Mutex
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		asked = append(asked, key)
		mu.Unlock()
		if key == bad {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, `{"detail":{"error":"invalid key %s"}}`, key)
			return
		}
		io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev","content":"ok"}]}`)
	}))
	defer server.Close()
	provider := Provider{Name: "tavily", APIKey: bad + ", \n" + good + "," + good, BaseURL: server.URL}

	if _, err := Search(context.Background(), []Provider{provider}, "go", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := Search(context.Background(), []Provider{provider}, "go", 6); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	got := slices.Clone(asked)
	mu.Unlock()
	if !slices.Equal(got, []string{bad, good, good}) {
		t.Errorf("keys asked = %v", got)
	}
	rest, ok := currentKeyRest("tavily "+server.URL, bad, time.Now())
	if !ok || rest.why != "turned away" || time.Until(rest.until) < 9*time.Minute {
		t.Errorf("bad key rest = %+v, %v", rest, ok)
	}
}

func TestSuccessfulKeysRoundRobin(t *testing.T) {
	isolateKeys(t)
	first, second := "tvly-test-key-0001", "tvly-test-key-0002"
	var mu sync.Mutex
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		mu.Unlock()
		io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev"}]}`)
	}))
	defer server.Close()
	provider := Provider{Name: "tavily", APIKey: first + " " + second, BaseURL: server.URL}
	for range 3 {
		if _, err := Search(context.Background(), []Provider{provider}, "go", 6); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	got := slices.Clone(asked)
	mu.Unlock()
	if !slices.Equal(got, []string{first, second, first}) {
		t.Errorf("keys asked = %v", got)
	}
}

func TestNonRefusalDoesNotRotateKey(t *testing.T) {
	isolateKeys(t)
	first, second := "tvly-test-key-0001", "tvly-test-key-0002"
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"message":"server down"}`)
	}))
	defer server.Close()
	_, err := Search(context.Background(), []Provider{{Name: "tavily", APIKey: first + "," + second, BaseURL: server.URL}}, "go", 6)
	if err == nil || !slices.Equal(asked, []string{first}) {
		t.Fatalf("asked = %v, err = %v", asked, err)
	}
	if _, ok := currentKeyRest("tavily "+server.URL, first, time.Now()); ok {
		t.Error("a 500 put the key in backoff")
	}
}

func TestRefusalBackoffDurations(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		provider   string
		status     int
		retryAfter string
		want       time.Duration
		why        string
	}{
		{"tavily", 401, "", turnedAwayRest, "turned away"},
		{"brave", 403, "", turnedAwayRest, "turned away"},
		{"exa", 402, "", outOfCreditRest, "out of credit"},
		{"tavily", 432, "", outOfCreditRest, "out of credit"},
		{"tavily", 433, "", outOfCreditRest, "out of credit"},
		{"brave", 432, "", rateLimitRest, "rate limited"},
		{"tavily", 429, "120", 2 * time.Minute, "rate limited"},
		{"tavily", 429, "1", rateLimitRestMin, "rate limited"},
		{"tavily", 429, "86400", rateLimitRestMax, "rate limited"},
		{"tavily", 429, now.Add(3 * time.Minute).Format(http.TimeFormat), 3 * time.Minute, "rate limited"},
	}
	for _, test := range tests {
		got, why := restFor(test.provider, test.status, test.retryAfter, now)
		if got != test.want || why != test.why {
			t.Errorf("%s %d %q = %v %q, want %v %q", test.provider, test.status, test.retryAfter, got, why, test.want, test.why)
		}
	}
}

func TestErrorsAndStateRedactKeys(t *testing.T) {
	isolateKeys(t)
	first := "tvly-dev-SECRETone00000000aaaa"
	second := "tvly-dev-SECRETtwo00000000bbbb"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"detail":{"error":"invalid key %s"}}`, key)
	}))
	defer server.Close()

	result, err := Search(context.Background(), []Provider{{Name: "tavily", APIKey: first + "," + second, BaseURL: server.URL}}, "go", 6)
	if err == nil {
		t.Fatal("both keys were refused, want an error")
	}
	said := err.Error() + "\n" + strings.Join(result.Attempts, "\n")
	keyRests.Range(func(key, value any) bool {
		said += fmt.Sprintf("\n%v %+v", key, value)
		return true
	})
	for _, secret := range []string{first, second, "SECRET"} {
		if strings.Contains(said, secret) {
			t.Errorf("%q leaked in %s", secret, said)
		}
	}
	if !strings.Contains(said, maskKey(first)) || !strings.Contains(said, "key 2 ("+maskKey(second)+")") {
		t.Errorf("masked keys missing from %s", said)
	}
}

func TestCrossOriginRedirectDoesNotLeakKey(t *testing.T) {
	isolateKeys(t)
	key := "exa-SECRET-redirect-key"
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("redirect forwarded credentials: %+v", r.Header)
		}
		io.WriteString(w, `{"results":[]}`)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != key {
			t.Errorf("source did not get key")
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	_, err := Search(context.Background(), []Provider{{Name: "exa", APIKey: key, BaseURL: source.URL}}, "go", 6)
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect refused") {
		t.Fatalf("err = %v", err)
	}
	if targetCalls.Load() != 0 {
		t.Errorf("target received %d requests", targetCalls.Load())
	}
	if strings.Contains(err.Error(), key) || strings.Contains(err.Error(), target.URL) {
		t.Errorf("redirect error leaked details: %v", err)
	}
}

func TestResponseLimit(t *testing.T) {
	isolateKeys(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte{' '}, maxResponseBytes+1))
	}))
	defer server.Close()
	_, err := Search(context.Background(), []Provider{{Name: "tavily", APIKey: "tvly-test-key-0001", BaseURL: server.URL}}, "go", 6)
	if err == nil || !strings.Contains(err.Error(), "response exceeds 4 MiB") {
		t.Fatalf("err = %v", err)
	}
}

func TestSuccessfulHitsAreRedacted(t *testing.T) {
	isolateKeys(t)
	const key = "reflected-search-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"results":[{"title":%q,"url":%q,"content":%q,"published_date":%q}]}`,
			"title "+key, "https://example.test/"+key, "content "+key, "date "+key)
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{{Name: "tavily", APIKey: key, BaseURL: server.URL}}, "go", 6)
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("Search = %+v, %v", result, err)
	}
	hit := result.Hits[0]
	for _, value := range []string{hit.Title, hit.URL, hit.Text, hit.Age} {
		if strings.Contains(value, key) {
			t.Errorf("hit was not redacted: %+v", hit)
		}
	}
}

func TestValidationAndDefaultBaseURLs(t *testing.T) {
	isolateKeys(t)
	if _, err := Search(context.Background(), []Provider{{Name: "searxng"}}, "go", 6); err == nil || !strings.Contains(err.Error(), "base URL is required") {
		t.Errorf("SearXNG without URL: %v", err)
	}
	if _, err := Search(context.Background(), []Provider{{Name: "you", APIKey: "secret"}}, "go", 6); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unsupported provider: %v", err)
	}
	if _, err := Search(context.Background(), []Provider{{Name: "tavily"}}, "go", 6); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Errorf("missing key: %v", err)
	}
	for name, want := range defaultBaseURLs {
		provider, err := normalizeProvider(Provider{Name: name})
		if err != nil || provider.BaseURL != want {
			t.Errorf("%s base = %q, %v; want %q", name, provider.BaseURL, err, want)
		}
	}
}

func TestCanceledContext(t *testing.T) {
	isolateKeys(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Search(ctx, []Provider{{Name: "tavily", APIKey: "tvly-test-key-0001", BaseURL: server.URL}}, "go", 6)
	if err == nil || !strings.Contains(err.Error(), "no answer in time") {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("server received %d calls", calls.Load())
	}
}
