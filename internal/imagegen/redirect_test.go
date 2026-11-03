package imagegen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProviderRedirectsNeverReceiveCredentials(t *testing.T) {
	for _, api := range []string{"openai", "gemini", "dashscope", "ark", "openrouter"} {
		t.Run(api, func(t *testing.T) {
			var targetCalled atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalled.Store(true)
				if r.Header.Get("Authorization") != "" || r.Header.Get("x-goog-api-key") != "" {
					t.Error("redirect target received provider credentials")
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
			}))
			defer source.Close()

			_, err := Generate(context.Background(), Provider{
				API: api, BaseURL: source.URL, APIKey: "redirect-secret", Model: "image-model",
			}, Request{Prompt: "test", N: 1})
			if err == nil {
				t.Fatal("redirect response was treated as a successful generation")
			}
			if targetCalled.Load() {
				t.Fatal("provider redirect was followed")
			}
			if strings.Contains(err.Error(), "redirect-secret") {
				t.Fatalf("error leaked credential: %v", err)
			}
		})
	}
}
