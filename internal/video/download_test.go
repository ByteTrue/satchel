package video

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadVideoRejectsNonPublicTargetsBeforeDial(t *testing.T) {
	var dialed atomic.Bool
	downloader := &videoDownloader{
		lookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "internal.test":
				return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
			case "mixed.test":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("::ffff:10.0.0.1")}, nil
			default:
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
		},
		dialPinned: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("forbidden target was dialed")
		},
	}
	for _, rawURL := range []string{
		"http://127.0.0.1/video.mp4",
		"http://[::1]/video.mp4",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost/video.mp4",
		"http://service.localhost/video.mp4",
		"http://internal.test/video.mp4",
		"http://mixed.test/video.mp4",
	} {
		err := downloadVideo(context.Background(), downloader, rawURL, io.Discard)
		if !errors.Is(err, errPrivateAddress) {
			t.Errorf("%s error = %v, want errPrivateAddress", rawURL, err)
		}
	}
	if dialed.Load() {
		t.Fatal("a non-public target reached the dial hook")
	}
	for _, rawURL := range []string{"file:///etc/passwd", "ftp://public.example/video.mp4", "http://user:password@public.example/video.mp4"} {
		if err := downloadVideo(context.Background(), downloader, rawURL, io.Discard); err == nil {
			t.Errorf("%s was accepted", rawURL)
		}
	}
}

func TestDownloadVideoRechecksRedirectAndRedactsSignedURL(t *testing.T) {
	const signature = "redirect-signed-secret"
	var secretHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open":
			_, port, _ := net.SplitHostPort(r.Host)
			w.Header().Set("Location", "http://internal.example:"+port+"/secret?signature="+signature)
			w.WriteHeader(http.StatusFound)
		case "/secret":
			secretHits.Add(1)
			_, _ = w.Write(testMP4)
		}
	}))
	defer server.Close()
	downloader := fakePublicDownloader(server)
	downloader.lookupIP = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	err := downloadVideo(context.Background(), downloader, publicTestURL(t, server.URL, "/open"), io.Discard)
	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("redirect error = %v", err)
	}
	if secretHits.Load() != 0 {
		t.Fatal("private redirect target was downloaded")
	}
	if strings.Contains(err.Error(), signature) {
		t.Fatalf("signed URL leaked into error: %v", err)
	}
}

func TestDownloadVideoRedirectDropsRefererAndSensitiveHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/source":
			_, port, _ := net.SplitHostPort(r.Host)
			w.Header().Set("Location", "http://target.example:"+port+"/final")
			w.WriteHeader(http.StatusFound)
		case "/final":
			for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
				if got := r.Header.Get(key); got != "" {
					t.Errorf("redirect forwarded %s = %q", key, got)
				}
			}
			_, _ = w.Write(testMP4)
		}
	}))
	defer server.Close()
	var output bytes.Buffer
	rawURL := publicTestURL(t, server.URL, "/source") + "?signature=source-signed-secret"
	if err := downloadVideo(context.Background(), fakePublicDownloader(server), rawURL, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), testMP4) {
		t.Fatalf("downloaded %d bytes", output.Len())
	}
}

func TestOpenAIContentRedirectUsesCleanSafeDownload(t *testing.T) {
	const apiKey = "content-provider-secret"
	const customSecret = "content-custom-secret"
	const signature = "content-signed-secret"
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled.Store(true)
		if r.URL.Query().Get("signature") != signature {
			t.Errorf("content redirect query = %q", r.URL.RawQuery)
		}
		for _, key := range []string{"Authorization", "X-Custom", "Cookie", "Referer"} {
			if got := r.Header.Get(key); got != "" {
				t.Errorf("redirect target received %s = %q", key, got)
			}
		}
		_, _ = w.Write(testMP4)
	}))
	defer target.Close()
	location := publicTestURL(t, target.URL, "/video.mp4") + "?signature=" + signature
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/videos":
			_, _ = w.Write([]byte(`{"id":"redirected"}`))
		case "/v1/videos/redirected":
			_, _ = w.Write([]byte(`{"status":"completed"}`))
		case "/v1/videos/redirected/content":
			if r.Header.Get("Authorization") != "Bearer "+apiKey || r.Header.Get("X-Custom") != customSecret {
				t.Errorf("content endpoint did not receive provider credentials")
			}
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	output := videoOutputPath(t)
	_, err := generate(context.Background(), Provider{
		API: "openai-videos", BaseURL: provider.URL, APIKey: apiKey, Model: "m",
		Headers: map[string]string{"X-Custom": customSecret}, HTTPClient: provider.Client(),
	}, Request{Prompt: "x"}, output, fakePublicDownloader(target), time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !targetCalled.Load() {
		t.Fatal("content redirect target was not called")
	}
	data, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(data, testMP4) {
		t.Fatalf("redirected output = %d bytes, %v", len(data), err)
	}
}

func TestCopyVideoEnforcesDeclaredAndStreamedLimits(t *testing.T) {
	for _, test := range []struct {
		name          string
		body          string
		contentLength int64
	}{
		{name: "declared", body: "12345", contentLength: 5},
		{name: "streamed", body: "12345", contentLength: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := copyVideo(context.Background(), strings.NewReader(test.body), test.contentLength, &output, 4)
			if err == nil || !strings.Contains(err.Error(), "exceeds 4 bytes") {
				t.Fatalf("size error = %v", err)
			}
		})
	}
	if maxVideoBytes != 512<<20 {
		t.Fatalf("video budget = %d", maxVideoBytes)
	}
}

func TestDownloadVideoHonorsCancellation(t *testing.T) {
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
	err := downloadVideo(ctx, fakePublicDownloader(server), publicTestURL(t, server.URL, "/wait"), io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestRemoteReferenceUsesSafeDownloader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, key := range []string{"Authorization", "X-Custom", "Cookie"} {
			if got := r.Header.Get(key); got != "" {
				t.Errorf("reference download received %s = %q", key, got)
			}
		}
		_, _ = w.Write(testPNG)
	}))
	defer server.Close()
	ref, err := loadReference(context.Background(), fakePublicDownloader(server), publicTestURL(t, server.URL, "/reference.png")+"?signature=reference-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ref.data, testPNG) || ref.mimeType != "image/png" {
		t.Fatalf("reference = %d bytes, %s", len(ref.data), ref.mimeType)
	}
}

func TestMediaHTTPStatusDoesNotEchoSignedURL(t *testing.T) {
	const signature = "status-url-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(w, "signed URL was %s", r.URL.String())
	}))
	defer server.Close()
	err := downloadVideo(context.Background(), fakePublicDownloader(server), publicTestURL(t, server.URL, "/missing")+"?signature="+signature, io.Discard)
	if err == nil || strings.Contains(err.Error(), signature) {
		t.Fatalf("HTTP status error = %v", err)
	}
}
