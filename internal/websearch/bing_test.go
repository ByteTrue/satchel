package websearch

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func disableBingRetryDelay(t *testing.T) {
	t.Helper()
	old := bingRetryDelay
	bingRetryDelay = 0
	t.Cleanup(func() { bingRetryDelay = old })
}

func TestParseBingHTMLAndRedirect(t *testing.T) {
	target := "https://example.com/page?a=b"
	wrapped := "https://www.bing.com/ck/a?!&u=a1" + base64.RawURLEncoding.EncodeToString([]byte(target))
	body := fmt.Sprintf(`
		<!-- <li class="b_algo"><h2><a href="https://attacker.example/comment">Fake</a></h2></li> -->
		<script>document.write('<li class="b_algo"><h2><a href="https://attacker.example/script">Fake</a></h2></li>')</script>
		<ol>
		<li class="b_algo"><h2 class=""><a href="https://go.dev/dl/">Go &amp; <strong>downloads</strong></a></h2><p class="b_lineclamp2">The <b>latest</b> Go</p></li>
		<li class="b_algo"><h2><a href="%s">Wrapped</a></h2><div class="b_caption"><p>caption snippet</p></div></li>
		</ol>`, wrapped)
	hits := parseBingHTML(body, 5)
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	if hits[0].Title != "Go & downloads" || hits[0].Text != "The latest Go" || hits[0].URL != "https://go.dev/dl/" {
		t.Errorf("direct hit = %+v", hits[0])
	}
	if hits[1].URL != target || hits[1].Text != "caption snippet" {
		t.Errorf("wrapped hit = %+v", hits[1])
	}
	if got := parseBingHTML(body, 1); len(got) != 1 {
		t.Errorf("limited hits = %+v", got)
	}
}

func TestBingRetriesAndRequestContract(t *testing.T) {
	isolateKeys(t)
	disableBingRetryDelay(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "openai news" || r.URL.Query().Get("form") != "QBLH" {
			t.Errorf("request = %s", r.URL.String())
		}
		if r.Header.Get("User-Agent") != browserUserAgent || r.Header.Get("Accept-Language") != "en-US,en;q=0.9" ||
			!strings.Contains(r.Header.Get("Accept"), "text/html") {
			t.Errorf("headers = %+v", r.Header)
		}
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "down")
			return
		}
		io.WriteString(w, `<ol><li class="b_algo"><h2><a href="https://example.com/news">News</a></h2><p class="b_lineclamp">ok</p></li></ol>`)
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{{Name: "bing", BaseURL: server.URL, HTTPClient: server.Client()}}, "openai news", 5)
	if err != nil || result.Provider != "bing" || len(result.Hits) != 1 || calls.Load() != 2 {
		t.Fatalf("result = %+v, calls = %d, err = %v", result, calls.Load(), err)
	}
}

func TestBingChallengeFallsBack(t *testing.T) {
	isolateKeys(t)
	disableBingRetryDelay(t)
	var bingCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bing/search":
			bingCalls.Add(1)
			io.WriteString(w, "<html><body>"+strings.Repeat("x", 2048)+"<div id=challenge>verify</div></body></html>")
		case "/tavily/search":
			io.WriteString(w, `{"results":[{"title":"Go","url":"https://go.dev"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{
		{Name: "bing", BaseURL: server.URL + "/bing", HTTPClient: server.Client()},
		{Name: "tavily", APIKey: "tvly-test-key-0001", BaseURL: server.URL + "/tavily", HTTPClient: server.Client()},
	}, "go", 5)
	if err != nil || result.Provider != "tavily" || bingCalls.Load() != 2 || len(result.Attempts) != 2 ||
		!strings.Contains(result.Attempts[0], "no parseable results") {
		t.Fatalf("result = %+v, Bing calls = %d, err = %v", result, bingCalls.Load(), err)
	}
}

func TestBingSmallEmptyResponse(t *testing.T) {
	disableBingRetryDelay(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "<html>empty</html>")
	}))
	defer server.Close()
	hits, err := searchBing(context.Background(), Provider{BaseURL: server.URL, HTTPClient: server.Client()}, "x", 5)
	if err != nil || len(hits) != 0 || calls.Load() != 2 {
		t.Fatalf("hits = %+v, calls = %d, err = %v", hits, calls.Load(), err)
	}
}
