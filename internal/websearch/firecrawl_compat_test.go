package websearch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFirecrawlV2NotFoundFallsBackToV1(t *testing.T) {
	isolateKeys(t)
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer fc-test-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/v2/search" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/v1/search" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"title":"v1 result","url":"https://example.test/v1","description":"compatible"}]}`)
	}))
	defer server.Close()
	result, err := Search(context.Background(), []Provider{{Name: "firecrawl", APIKey: "fc-test-key", BaseURL: server.URL}}, "go", 1)
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Title != "v1 result" {
		t.Fatalf("Search = %+v, %v", result, err)
	}
	if !reflect.DeepEqual(paths, []string{"/v2/search", "/v1/search"}) {
		t.Fatalf("request paths = %v", paths)
	}
}
