package video

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestGenerateCancellationAlsoCancelsProviderTask(t *testing.T) {
	for _, tc := range []struct {
		api       string
		basePath  string
		createURI string
		taskURI   string
	}{
		{api: "openai-videos", basePath: "/v1", createURI: "/v1/videos", taskURI: "/v1/videos/vid-1"},
		{api: "ark", basePath: "/api/v3", createURI: "/api/v3/contents/generations/tasks", taskURI: "/api/v3/contents/generations/tasks/vid-1"},
	} {
		t.Run(tc.api, func(t *testing.T) {
			firstStatus := make(chan struct{})
			cancelSeen := make(chan string, 1)
			var statusOnce sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == tc.createURI:
					_, _ = w.Write([]byte(`{"id":"vid-1"}`))
				case r.Method == http.MethodGet && r.URL.Path == tc.taskURI:
					statusOnce.Do(func() { close(firstStatus) })
					_, _ = w.Write([]byte(`{"status":"running"}`))
				case r.Method == http.MethodDelete && r.URL.Path == tc.taskURI:
					cancelSeen <- r.Method + " " + r.URL.Path
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			output := videoOutputPath(t)
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				_, err := generate(ctx, Provider{API: tc.api, BaseURL: server.URL + tc.basePath, Model: "video-test", HTTPClient: server.Client()}, Request{Prompt: "test"}, output, nil, 5*time.Millisecond)
				result <- err
			}()
			select {
			case <-firstStatus:
				cancel()
			case <-time.After(2 * time.Second):
				t.Fatal("provider task did not start polling")
			}
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("Generate error = %v", err)
			}
			select {
			case got := <-cancelSeen:
				if got != "DELETE "+tc.taskURI {
					t.Fatalf("cancel request = %q", got)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("provider task was not cancelled")
			}
			if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("cancelled output still exists: %v", err)
			}
		})
	}
}

func TestGenerateDeadlineAlsoCancelsProviderTask(t *testing.T) {
	cancelSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/videos":
			_, _ = w.Write([]byte(`{"id":"vid-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/videos/vid-1":
			_, _ = w.Write([]byte(`{"status":"running"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/videos/vid-1":
			cancelSeen <- struct{}{}
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	output := videoOutputPath(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := generate(ctx, Provider{API: "openai-videos", BaseURL: server.URL + "/v1", Model: "video-test", HTTPClient: server.Client()}, Request{Prompt: "test"}, output, nil, 5*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Generate error = %v", err)
	}
	select {
	case <-cancelSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("provider task was not cancelled after deadline")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deadline output still exists: %v", err)
	}
}

func TestDataURLReferenceRejectsNonImageBytes(t *testing.T) {
	value := "data:image/png;base64,bm90LWEtcG5n"
	if _, err := dataURLReference(value); err == nil {
		t.Fatal("non-image data URL was accepted as a declared image")
	}
}
