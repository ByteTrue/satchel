package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProviderRedirectsNeverReceiveCredentials(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses", "anthropic-messages", "google-generative-ai"} {
		t.Run(api, func(t *testing.T) {
			const apiKey = "sk-test/secret"
			const headerSecret = "custom secret/abc"
			var targetCalled atomic.Bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetCalled.Store(true)
				w.WriteHeader(http.StatusOK)
			}))
			defer target.Close()
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				location := target.URL + "/stolen?key=" + url.QueryEscape(apiKey) + "&header=" + url.QueryEscape(headerSecret)
				http.Redirect(w, r, location, http.StatusTemporaryRedirect)
			}))
			defer source.Close()

			path := filepath.Join(t.TempDir(), "image.png")
			writeTestImage(t, path, testPNG)
			_, err := Ask(context.Background(), Provider{
				API: api, BaseURL: source.URL, APIKey: apiKey, Model: "vision-model",
				Headers: map[string]string{"X-Secret": headerSecret},
				// Even an explicitly permissive client policy must not enable redirects.
				HTTPClient: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return nil }},
			}, []string{path}, "q")
			if err == nil {
				t.Fatal("redirect response was treated as success")
			}
			if targetCalled.Load() {
				t.Fatal("provider redirect was followed")
			}
			for _, secret := range []string{apiKey, headerSecret, url.QueryEscape(apiKey), url.QueryEscape(headerSecret)} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestUpstreamErrorRedactsCredentials(t *testing.T) {
	const apiKey = "sk-test/secret"
	const headerSecret = "custom secret/abc"
	const authToken = "auth-token/secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		message := "rejected " + apiKey + " " + url.QueryEscape(apiKey) + " " + headerSecret + " " + url.QueryEscape(headerSecret) + " " + authToken
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message}})
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "image.png")
	writeTestImage(t, path, testPNG)

	_, err := Ask(context.Background(), Provider{
		API: "openai-completions", BaseURL: server.URL, APIKey: apiKey, Model: "vision-model",
		Headers: map[string]string{"X-Secret": headerSecret, "Authorization": "Bearer " + authToken},
	}, []string{path}, "q")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, secret := range []string{apiKey, headerSecret, authToken, url.QueryEscape(apiKey), url.QueryEscape(headerSecret)} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked %q: %v", secret, err)
		}
	}
}

func TestResponseSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxResponseBytes+1))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "image.png")
	writeTestImage(t, path, testPNG)

	_, err := Ask(context.Background(), Provider{
		API: "openai-completions", BaseURL: server.URL, Model: "vision-model",
	}, []string{path}, "q")
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %v, want response size failure", err)
	}
}

func TestContextCancellationPropagates(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		close(cancelled)
		return nil, request.Context().Err()
	})}
	path := filepath.Join(t.TempDir(), "image.png")
	writeTestImage(t, path, testPNG)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := Ask(ctx, Provider{
			API: "openai-completions", BaseURL: "http://provider.test", Model: "vision-model", HTTPClient: client,
			Headers: map[string]string{"X-Env": "context"}, // Redaction must not break errors.Is.
		}, []string{path}, "q")
		errCh <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return after cancellation")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request context was not cancelled")
	}
}

func TestTransportErrorRedactsCredentials(t *testing.T) {
	const secret = "transport-secret"
	path := filepath.Join(t.TempDir(), "image.png")
	writeTestImage(t, path, testPNG)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("connection failed for " + secret)
	})}
	_, err := Ask(context.Background(), Provider{
		API: "openai-completions", BaseURL: "http://provider.test", APIKey: secret, Model: "vision-model", HTTPClient: client,
	}, []string{path}, "q")
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("unexpected transport error: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
