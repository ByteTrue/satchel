package vision

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func publicVisionTestURL(t *testing.T, serverURL, path string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort("public.example", u.Port())
	u.Path = path
	return u.String()
}

func fakePublicVisionDownloader(server *httptest.Server) *imageDownloader {
	address := server.Listener.Addr().String()
	return &imageDownloader{
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dialPinned: func(ctx context.Context, _ *net.Dialer, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, address)
		},
	}
}

func TestAnalyzeInputForms(t *testing.T) {
	const (
		apiKey       = "provider-secret"
		headerSecret = "custom-provider-secret"
		signature    = "signed-url-secret"
	)
	for _, form := range []string{"local", "data", "http"} {
		t.Run(form, func(t *testing.T) {
			var downloader *imageDownloader
			var input string
			switch form {
			case "local":
				input = filepath.Join(t.TempDir(), "image.bin")
				writeTestImage(t, input, testPNG)
			case "data":
				// The declared type deliberately disagrees with the magic bytes.
				input = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(testPNG)
			case "http":
				downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("signature") != signature {
						t.Errorf("signed query was not preserved: %q", r.URL.RawQuery)
					}
					for _, key := range []string{"Authorization", "X-Secret", "x-goog-api-key", "Cookie"} {
						if got := r.Header.Get(key); got != "" {
							t.Errorf("download received sensitive header %s = %q", key, got)
						}
					}
					w.Header().Set("Content-Type", "text/plain")
					_, _ = w.Write(testPNG)
				}))
				defer downloadServer.Close()
				downloader = fakePublicVisionDownloader(downloadServer)
				input = publicVisionTestURL(t, downloadServer.URL, "/image.png") + "?signature=" + signature
			}

			captured := make(chan capturedRequest, 1)
			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				captured <- capturedRequest{header: r.Header.Clone(), body: body, err: err}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"analyzed"}}]}`)
			}))
			defer providerServer.Close()

			prompt := "What is shown?"
			if form == "data" {
				prompt = " \t\n "
			}
			answer, err := analyzeWithDownloader(context.Background(), Provider{
				API: "openai-completions", BaseURL: providerServer.URL, APIKey: apiKey, Model: "vision-model",
				Headers: map[string]string{"X-Secret": headerSecret}, HTTPClient: providerServer.Client(),
			}, input, prompt, downloader)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			if answer.Text != "analyzed" {
				t.Fatalf("answer = %q", answer.Text)
			}

			got := <-captured
			if got.err != nil {
				t.Fatal(got.err)
			}
			if value := got.header.Get("Authorization"); value != "Bearer "+apiKey {
				t.Errorf("provider Authorization = %q", value)
			}
			if form == "http" && strings.Contains(string(got.body), signature) {
				t.Fatal("signed remote URL was forwarded to the provider")
			}
			expectedPrompt := prompt
			if strings.TrimSpace(expectedPrompt) == "" {
				expectedPrompt = defaultQuestion
			}
			encoded := base64.StdEncoding.EncodeToString(testPNG)
			want := map[string]any{
				"model": "vision-model", "stream": false,
				"messages": []any{map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64," + encoded}},
					map[string]any{"type": "text", "text": expectedPrompt},
				}}},
			}
			if body := jsonValue(t, got.body); !reflect.DeepEqual(body, jsonValue(t, mustMarshal(t, want))) {
				t.Errorf("payload mismatch\n got: %s\nwant: %s", mustMarshal(t, body), mustMarshal(t, want))
			}
		})
	}
}

func TestAnalyzeValidationFailsBeforeProvider(t *testing.T) {
	tests := []struct {
		name  string
		input func(*testing.T) string
		want  string
	}{
		{"empty", func(*testing.T) string { return " " }, "needs an image"},
		{"relative", func(*testing.T) string { return "image.png" }, "absolute"},
		{"file URL", func(*testing.T) string { return "file:///tmp/image.png" }, "only absolute"},
		{"embedded credentials", func(*testing.T) string { return "https://user:pass@public.example/image.png" }, "credentials"},
		{"data not base64", func(*testing.T) string { return "data:image/png,abc" }, "base64"},
		{"data not image media", func(*testing.T) string { return "data:text/plain;base64," + base64.StdEncoding.EncodeToString(testPNG) }, "image media type"},
		{"data invalid base64", func(*testing.T) string { return "data:image/png;base64,!!!" }, "invalid base64"},
		{"data bad magic", func(*testing.T) string {
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))
		}, "magic bytes"},
		{"data too large", func(*testing.T) string {
			data := make([]byte, maxImageBytes+1)
			copy(data, testPNG)
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
		}, "20 MiB"},
		{"local bad magic", func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "fake.png")
			writeTestImage(t, path, []byte("not an image"))
			return path
		}, "magic bytes"},
		{"local too large", func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "large.png")
			writeSizedTestImage(t, path, testPNG, maxImageBytes+1)
			return path
		}, "20 MiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.WriteString(w, `{}`)
			}))
			defer providerServer.Close()
			_, err := Analyze(context.Background(), Provider{
				API: "openai-completions", BaseURL: providerServer.URL, Model: "vision-model",
			}, test.input(t), "q")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if calls.Load() != 0 {
				t.Fatalf("provider received %d requests", calls.Load())
			}
		})
	}
}

func TestAnalyzeRemoteSizeTypeAndSignedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			data := make([]byte, maxImageBytes+1)
			copy(data, testPNG)
			_, _ = w.Write(data)
		case "/text":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not an image"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	downloader := fakePublicVisionDownloader(server)
	if _, err := downloadImage(context.Background(), downloader, publicVisionTestURL(t, server.URL, "/large"), maxImageBytes); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("size limit error = %v", err)
	}
	if _, err := downloadImage(context.Background(), downloader, publicVisionTestURL(t, server.URL, "/text"), maxImageBytes); err == nil || !strings.Contains(err.Error(), "magic bytes") {
		t.Fatalf("image type error = %v", err)
	}
	const signature = "status-url-secret"
	rawURL := publicVisionTestURL(t, server.URL, "/missing") + "?signature=" + signature
	if _, err := downloadImage(context.Background(), downloader, rawURL, maxImageBytes); err == nil || strings.Contains(err.Error(), signature) {
		t.Fatalf("HTTP status error = %v", err)
	}
}

func TestAnalyzeRejectsNonPublicTargets(t *testing.T) {
	var dialed atomic.Bool
	downloader := &imageDownloader{
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
		"http://127.0.0.1/image.png",
		"http://[::1]/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://localhost/image.png",
		"http://service.localhost/image.png",
		"http://internal.test/image.png",
		"http://mixed.test/image.png",
	} {
		_, err := downloadImage(context.Background(), downloader, rawURL, maxImageBytes)
		if !errors.Is(err, errImagePrivateAddress) {
			t.Errorf("%s error = %v, want errImagePrivateAddress", rawURL, err)
		}
	}
	if dialed.Load() {
		t.Fatal("a non-public target reached the dial hook")
	}
}

func TestAnalyzeRechecksRedirectAndRedactsSignedURL(t *testing.T) {
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
			_, _ = w.Write(testPNG)
		}
	}))
	defer server.Close()
	downloader := fakePublicVisionDownloader(server)
	downloader.lookupIP = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	_, err := downloadImage(context.Background(), downloader, publicVisionTestURL(t, server.URL, "/open"), maxImageBytes)
	if !errors.Is(err, errImagePrivateAddress) {
		t.Fatalf("redirect error = %v", err)
	}
	if secretHits.Load() != 0 {
		t.Fatal("private redirect target was downloaded")
	}
	if strings.Contains(err.Error(), signature) {
		t.Fatalf("signed URL leaked into error: %v", err)
	}
}

func TestAnalyzeRedirectDropsRefererAndSensitiveHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/source":
			_, port, _ := net.SplitHostPort(r.Host)
			w.Header().Set("Location", "http://target.example:"+port+"/final")
			w.WriteHeader(http.StatusFound)
		case "/final":
			if got := r.Header.Get("Referer"); got != "" {
				t.Errorf("redirect forwarded Referer %q", got)
			}
			for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie"} {
				if got := r.Header.Get(key); got != "" {
					t.Errorf("redirect forwarded %s", key)
				}
			}
			_, _ = w.Write(testPNG)
		}
	}))
	defer server.Close()
	rawURL := publicVisionTestURL(t, server.URL, "/source") + "?signature=source-signed-secret"
	image, err := downloadImage(context.Background(), fakePublicVisionDownloader(server), rawURL, maxImageBytes)
	if err != nil || image.data != base64.StdEncoding.EncodeToString(testPNG) {
		t.Fatalf("redirect image = %#v, err = %v", image, err)
	}
}

func TestAnalyzeDownloadPinsCheckedAddress(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if host := r.Host; !strings.HasPrefix(host, "public.example:") {
			t.Errorf("Host = %q", host)
		}
		_, _ = w.Write(testPNG)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	var dialedAddress string
	downloader := &imageDownloader{
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("8.8.8.8")}, nil
		},
		dialPinned: func(ctx context.Context, _ *net.Dialer, network, address string) (net.Conn, error) {
			dialedAddress = address
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	}
	if _, err := downloadImage(context.Background(), downloader, publicVisionTestURL(t, server.URL, "/image.png"), maxImageBytes); err != nil {
		t.Fatal(err)
	}
	if want := net.JoinHostPort("93.184.216.34", port); dialedAddress != want {
		t.Fatalf("dialed address = %q, want pinned %q", dialedAddress, want)
	}
}

func TestAnalyzeDownloadHonorsCancellation(t *testing.T) {
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
	_, err := downloadImage(ctx, fakePublicVisionDownloader(server), publicVisionTestURL(t, server.URL, "/wait"), maxImageBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestAnalyzeDownloadStopsExcessRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", r.URL.Path+"/next")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	_, err := downloadImage(context.Background(), fakePublicVisionDownloader(server), publicVisionTestURL(t, server.URL, "/redirect/0"), maxImageBytes)
	if !errors.Is(err, errImageRedirects) {
		t.Fatalf("redirect limit error = %v", err)
	}
}
