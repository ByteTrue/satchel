package webfetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func publicTestURL(t *testing.T, serverURL, path string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort("public.example", u.Port())
	u.Path = path
	return u.String()
}

func fakePublicFetcher(server *httptest.Server) Fetcher {
	address := server.Listener.Addr().String()
	return Fetcher{
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dialPinned: func(ctx context.Context, _ *net.Dialer, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
}

func TestFetchPublicPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/old":
			http.Redirect(w, r, "/page", http.StatusFound)
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title> Go &amp; notes </title></head><body>
<h1> Release   notes</h1>
<p>Go is out. See <a href="/dl?v=1">the downloads</a> &amp; <a href="#top">top</a>; escaped: &amp;lt;.</p>
<table><tr><th>Name</th><th>Value</th></tr><tr><td>Go</td><td><code>1.27</code></td></tr></table>
<pre>go test ./...
go vet ./...</pre><ul><li>fast <code>go test</code></li></ul>
<div hidden>hidden-secret</div><script>script-secret</script><style>style-secret</style><noscript>noscript-secret</noscript></body></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	fetcher := fakePublicFetcher(server)

	result, err := fetcher.Fetch(context.Background(), publicTestURL(t, server.URL, "/old"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := publicTestURL(t, server.URL, "/page"); result.URL != want {
		t.Fatalf("final URL = %q, want %q", result.URL, want)
	}
	if result.Title != "Go & notes" {
		t.Fatalf("title = %q", result.Title)
	}
	for _, want := range []string{
		"# Release notes",
		"[the downloads](" + publicTestURL(t, server.URL, "/dl") + "?v=1)",
		"escaped: &lt;.",
		"Name | Value",
		"Go | `1.27`",
		"```\ngo test ./...\ngo vet ./...\n```",
		"- fast `go test`",
	} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("text does not contain %q:\n%s", want, result.Text)
		}
	}
	for _, unwanted := range []string{"hidden-secret", "script-secret", "style-secret", "noscript-secret"} {
		if strings.Contains(result.Text, unwanted) {
			t.Errorf("text leaked %q:\n%s", unwanted, result.Text)
		}
	}
}

func TestFetchGBKFromHTMLMeta(t *testing.T) {
	// 中文 and 你好 encoded as GBK; the HTTP header deliberately has no charset.
	gbk := []byte("<html><head><meta charset=\"gbk\"><title>\xd6\xd0\xce\xc4</title></head><body><p>\xc4\xe3\xba\xc3</p></body></html>")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(gbk)
	}))
	defer server.Close()

	result, err := fakePublicFetcher(server).Fetch(context.Background(), publicTestURL(t, server.URL, "/gbk"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "中文" || result.Text != "你好" {
		t.Fatalf("GBK result = %+v", result)
	}
}

func TestFetchPaginationUsesRunes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("A中🙂BCD"))
	}))
	defer server.Close()
	fetcher := fakePublicFetcher(server)
	rawURL := publicTestURL(t, server.URL, "/runes")

	first, err := fetcher.Fetch(context.Background(), rawURL, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "中🙂" || first.NextOffset != 3 {
		t.Fatalf("first page = %+v", first)
	}
	last, err := fetcher.Fetch(context.Background(), rawURL, first.NextOffset, 100)
	if err != nil {
		t.Fatal(err)
	}
	if last.Text != "BCD" || last.NextOffset != 0 {
		t.Fatalf("last page = %+v", last)
	}
	past, err := fetcher.Fetch(context.Background(), rawURL, 99, 10)
	if err != nil {
		t.Fatal(err)
	}
	if past.Text != "" || past.NextOffset != 0 {
		t.Fatalf("past-end page = %+v", past)
	}
	if _, err := fetcher.Fetch(context.Background(), rawURL, -1, 10); err == nil {
		t.Fatal("negative offset was accepted")
	}
}

func TestFetchMaxCharsIsCapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchChars+1)))
	}))
	defer server.Close()

	result, err := fakePublicFetcher(server).Fetch(context.Background(), publicTestURL(t, server.URL, "/cap"), 0, maxFetchChars+1000)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(result.Text)) != maxFetchChars || result.NextOffset != maxFetchChars {
		t.Fatalf("capped result has %d runes, next %d", len([]rune(result.Text)), result.NextOffset)
	}
}

func TestFetchBodyLimitContentTypeAndHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("a", maxBodyBytes+1)))
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7"))
		case "/missing":
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("<h1>Not here</h1>"))
		}
	}))
	defer server.Close()
	fetcher := fakePublicFetcher(server)

	for _, test := range []struct {
		path    string
		message string
	}{
		{"/large", "5 MiB"},
		{"/pdf", "application/pdf"},
		{"/missing", "404 Not Found: # Not here"},
	} {
		_, err := fetcher.Fetch(context.Background(), publicTestURL(t, server.URL, test.path), 0, 0)
		if err == nil || !strings.Contains(err.Error(), test.message) {
			t.Errorf("%s error = %v, want substring %q", test.path, err, test.message)
		}
	}
}

func TestFetchHonorsCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	_, err := fakePublicFetcher(server).Fetch(ctx, publicTestURL(t, server.URL, "/wait"), 0, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
