package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
)

func TestWebSearchAdapter(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.Header.Get("Authorization") != "Bearer search-secret" {
			t.Errorf("unexpected search request: %s, %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"title":"Satchel result","url":"https://example.test/page","content":"useful snippet"}]}`)
	}))
	defer provider.Close()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Default()
	cfg.Web.Providers = []string{"tavily"}
	cfg.Web.APIKeys["tavily"] = "search-secret"
	cfg.Web.BaseURLs["tavily"] = provider.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	result, err := webSearch(context.Background(), mcpserver.WebSearchInput{Query: "satchel"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider != "tavily" || !strings.Contains(result.Text, "Satchel result") || !strings.Contains(result.Text, "useful snippet") {
		t.Fatalf("search result = %+v", result)
	}
}

func TestWebFetchAdapterThroughExplicitProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RequestURI, "http://93.184.216.34:80/") {
			t.Errorf("proxy request URI = %q", r.RequestURI)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><title>Proxy page</title></head><body><a href="/next">Next page</a><p>Safe fetched text.</p></body></html>`)
	}))
	defer proxy.Close()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Default()
	cfg.Web.Proxy = proxy.URL
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	result, err := webFetch(context.Background(), mcpserver.WebFetchInput{URL: "http://93.184.216.34/start", MaxChars: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Proxy page", "Safe fetched text.", "http://93.184.216.34/next"} {
		if !strings.Contains(result.Text, want) {
			t.Errorf("fetch result %q does not contain %q", result.Text, want)
		}
	}
}

func TestWebFetchAdapterRejectsPrivateTarget(t *testing.T) {
	t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
	if err := config.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	_, err := webFetch(context.Background(), mcpserver.WebFetchInput{URL: "http://127.0.0.1/private", MaxChars: 100})
	if err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("private target error = %v", err)
	}
}
