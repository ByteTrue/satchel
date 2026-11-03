package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestBochaContract(t *testing.T) {
	isolateKeys(t)
	const key = "bocha-test-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/web-search" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("Accept") != "application/json" {
			t.Errorf("headers = %+v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		want := map[string]any{"query": "latest go", "summary": true, "count": float64(6), "freshness": "noLimit"}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("body = %#v, want %#v", body, want)
		}
		io.WriteString(w, `{"data":{"webPages":{"value":[{"name":"Go downloads","url":"https://go.dev/dl/","summary":"Download Go","snippet":"ignored"}]}}}`)
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{{Name: "bocha", APIKey: key, BaseURL: server.URL, HTTPClient: server.Client()}}, "latest go", 6)
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	hit := result.Hits[0]
	if hit.Title != "Go downloads" || hit.URL != "https://go.dev/dl/" || hit.Text != "Download Go" {
		t.Errorf("hit = %+v", hit)
	}
}

func TestJinaContract(t *testing.T) {
	isolateKeys(t)
	const key = "jina-test-key"
	const query = "latest go / 中文?"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/"+url.PathEscape(query) {
			t.Errorf("request = %s %s, escaped path = %q", r.Method, r.URL.Path, r.URL.EscapedPath())
		}
		if r.URL.Query().Get("num") != "6" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("query = %v, headers = %+v", r.URL.Query(), r.Header)
		}
		io.WriteString(w, `{"data":{"results":[{"title":"Go downloads","url":"https://go.dev/dl/","description":"Download Go"}]}}`)
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{{Name: "jina", APIKey: key, BaseURL: server.URL, HTTPClient: server.Client()}}, query, 6)
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	hit := result.Hits[0]
	if hit.Title != "Go downloads" || hit.URL != "https://go.dev/dl/" || hit.Text != "Download Go" {
		t.Errorf("hit = %+v", hit)
	}
}
