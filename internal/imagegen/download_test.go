package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func publicImageTestURL(t *testing.T, serverURL, path string) string {
	t.Helper()
	u, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Host = net.JoinHostPort("public.example", u.Port())
	u.Path = path
	return u.String()
}

func fakePublicImageDownloader(server *httptest.Server) *imageDownloader {
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

func TestOpenAIMixedBase64AndURLResultsAreAllPreserved(t *testing.T) {
	const apiKey = "provider-download-secret"
	const customSecret = "custom-download-secret"
	const signature = "signed-url-token"
	var downloads atomic.Int32
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		if r.URL.Query().Get("signature") != signature {
			t.Errorf("signed download query was not preserved: %q", r.URL.RawQuery)
		}
		for _, key := range []string{"Authorization", "X-Custom", "x-goog-api-key", "Cookie"} {
			if got := r.Header.Get(key); got != "" {
				t.Errorf("download received sensitive header %s = %q", key, got)
			}
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(testPNG)
	}))
	defer downloadServer.Close()
	imageURL := publicImageTestURL(t, downloadServer.URL, "/result.png") + "?signature=" + signature
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiKey || r.Header.Get("X-Custom") != customSecret {
			t.Errorf("provider request did not receive its configured credentials")
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q,"revised_prompt":"inline"},{"url":%q,"revised_prompt":"remote"}]}`,
			base64.StdEncoding.EncodeToString(testPNG), imageURL)
	}))
	defer providerServer.Close()

	images, err := generate(context.Background(), Provider{
		API: "openai", BaseURL: providerServer.URL, APIKey: apiKey, Model: "m",
		Headers: map[string]string{"X-Custom": customSecret}, HTTPClient: providerServer.Client(),
	}, Request{Prompt: "draw"}, fakePublicImageDownloader(downloadServer))
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || downloads.Load() != 1 {
		t.Fatalf("images = %d, downloads = %d", len(images), downloads.Load())
	}
	for i, image := range images {
		if !bytes.Equal(image.Data, testPNG) || image.MIMEType != "image/png" {
			t.Errorf("image %d = %d bytes, %s", i, len(image.Data), image.MIMEType)
		}
	}
	if images[0].RevisedPrompt != "inline" || images[1].RevisedPrompt != "remote" {
		t.Errorf("revised prompts = %q, %q", images[0].RevisedPrompt, images[1].RevisedPrompt)
	}
}

func TestOpenAIPartialSuccessWhenOneURLCannotBeDownloaded(t *testing.T) {
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"data":[{"url":"http://127.0.0.1/private.png"},{"b64_json":%q}]}`,
			base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer providerServer.Close()
	images, err := Generate(context.Background(), Provider{
		API: "openai", BaseURL: providerServer.URL, Model: "m", HTTPClient: providerServer.Client(),
	}, Request{Prompt: "draw"})
	if err != nil || len(images) != 1 || !bytes.Equal(images[0].Data, testPNG) {
		t.Fatalf("partial result images = %#v, err = %v", images, err)
	}
}

func TestRemoteReferenceImageUsesSafeDownloader(t *testing.T) {
	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(testPNG)
	}))
	defer downloadServer.Close()
	referenceURL := publicImageTestURL(t, downloadServer.URL, "/reference.png")
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		files := r.MultipartForm.File["image"]
		if len(files) != 1 {
			t.Fatalf("reference files = %d", len(files))
		}
		part, err := files[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		_ = part.Close()
		if err != nil || !bytes.Equal(data, testPNG) {
			t.Errorf("downloaded reference = %d bytes, err = %v", len(data), err)
		}
		fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString(testPNG))
	}))
	defer providerServer.Close()

	images, err := generate(context.Background(), Provider{
		API: "openai", BaseURL: providerServer.URL, Model: "m", HTTPClient: providerServer.Client(),
	}, Request{Prompt: "edit", Images: []string{referenceURL}}, fakePublicImageDownloader(downloadServer))
	if err != nil || len(images) != 1 {
		t.Fatalf("images = %#v, err = %v", images, err)
	}
}

func TestDownloadImageRejectsNonPublicTargets(t *testing.T) {
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
		_, err := downloadImage(context.Background(), downloader, rawURL, maxGeneratedImageBytes)
		if !errors.Is(err, errImagePrivateAddress) {
			t.Errorf("%s error = %v, want errImagePrivateAddress", rawURL, err)
		}
	}
	if dialed.Load() {
		t.Fatal("a non-public target reached the dial hook")
	}
	if _, err := downloadImage(context.Background(), downloader, "file:///etc/passwd", maxGeneratedImageBytes); err == nil {
		t.Fatal("file URL was accepted")
	}
}

func TestDownloadImageRechecksRedirectAndRedactsSignedURL(t *testing.T) {
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
	downloader := fakePublicImageDownloader(server)
	downloader.lookupIP = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.example" {
			return []netip.Addr{netip.MustParseAddr("10.0.0.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	_, err := downloadImage(context.Background(), downloader, publicImageTestURL(t, server.URL, "/open"), maxGeneratedImageBytes)
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

func TestDownloadRedirectDropsRefererAndSensitiveHeaders(t *testing.T) {
	const signature = "source-signed-secret"
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
	rawURL := publicImageTestURL(t, server.URL, "/source") + "?signature=" + signature
	image, err := downloadImage(context.Background(), fakePublicImageDownloader(server), rawURL, maxGeneratedImageBytes)
	if err != nil || !bytes.Equal(image.data, testPNG) {
		t.Fatalf("image = %d bytes, err = %v", len(image.data), err)
	}
}

func TestDownloadImageLimitTypeAndHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = w.Write(append(testPNG, make([]byte, 32)...))
		case "/text":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("not an image"))
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	downloader := fakePublicImageDownloader(server)
	if _, err := downloadImage(context.Background(), downloader, publicImageTestURL(t, server.URL, "/large"), int64(len(testPNG))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("size limit error = %v", err)
	}
	if _, err := downloadImage(context.Background(), downloader, publicImageTestURL(t, server.URL, "/text"), maxGeneratedImageBytes); err == nil || !strings.Contains(err.Error(), "PNG, JPEG, GIF, or WebP") {
		t.Fatalf("image type error = %v", err)
	}
	const signature = "status-url-secret"
	rawURL := publicImageTestURL(t, server.URL, "/missing") + "?signature=" + signature
	if _, err := downloadImage(context.Background(), downloader, rawURL, maxGeneratedImageBytes); err == nil || strings.Contains(err.Error(), signature) {
		t.Fatalf("HTTP status error = %v", err)
	}
}

func TestDownloadImageHonorsCancellation(t *testing.T) {
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
	_, err := downloadImage(ctx, fakePublicImageDownloader(server), publicImageTestURL(t, server.URL, "/wait"), maxGeneratedImageBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestDownloadImageProxyReceivesPinnedPublicAddress(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Hostname(); got != "93.184.216.34" {
			t.Errorf("proxy URL hostname = %q", got)
		}
		if got := r.Host; got != "93.184.216.34:80" {
			t.Errorf("proxy Host = %q", got)
		}
		_, _ = w.Write(testPNG)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	downloader := &imageDownloader{
		proxy: proxyURL,
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dialPinned: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
			return nil, errors.New("direct dial used despite proxy")
		},
	}
	image, err := downloadImage(context.Background(), downloader, "http://public.example/image.png", maxGeneratedImageBytes)
	if err != nil || !bytes.Equal(image.data, testPNG) {
		t.Fatalf("proxy image = %d bytes, err = %v", len(image.data), err)
	}
}
