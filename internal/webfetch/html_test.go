package webfetch

import (
	"net/url"
	"strings"
	"testing"
)

func extractedHTML(body string) [2]string {
	base, _ := url.Parse("https://public.example/base/page")
	return fetchText([]byte(body), "text/html; charset=utf-8", "html", base)
}

func TestHTMLFidelityRegressionCases(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		want     string
		unwanted string
	}{
		{
			name: "entities decode only once",
			body: "<p>x &amp;lt; y &amp;#60; tag</p>",
			want: "x &lt; y &#60; tag",
		},
		{
			name:     "greater-than inside attributes",
			body:     `<p data-x="a>b">visible</p><span class="x>y">also</span>`,
			want:     "visible\n\nalso",
			unwanted: "data-x",
		},
		{
			name:     "unterminated script does not leak",
			body:     "<p>before</p><script>alert(document.cookie)",
			want:     "before",
			unwanted: "cookie",
		},
		{
			name:     "comments including conditional comments are dropped",
			body:     "<!-- hidden --><p>shown</p><!--[if IE]><p>legacy</p><![endif]-->",
			want:     "shown",
			unwanted: "legacy",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := extractedHTML(test.body)[1]
			if got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
			if test.unwanted != "" && strings.Contains(got, test.unwanted) {
				t.Fatalf("text leaked %q: %q", test.unwanted, got)
			}
		})
	}

	if got := extractedHTML("<title>A &amp; B &#39;quoted&#39;</title>")[0]; got != "A & B 'quoted'" {
		t.Fatalf("decoded title = %q", got)
	}
}

func TestTextContentTypeWhitelist(t *testing.T) {
	for _, mediaType := range []string{
		"text/plain", "text/html", "text/markdown", "application/json",
		"application/xml", "application/xhtml+xml", "application/ld+json",
		"application/rss+xml", "application/javascript", "application/x-ndjson",
	} {
		if fetchKind(mediaType) == "" {
			t.Errorf("%s was rejected", mediaType)
		}
	}
	for _, mediaType := range []string{"", "application/pdf", "application/zip", "application/octet-stream", "image/png"} {
		if fetchKind(mediaType) != "" {
			t.Errorf("%s was accepted", mediaType)
		}
	}
}
