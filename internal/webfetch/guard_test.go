package webfetch

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRejectsNonPublicTargets(t *testing.T) {
	var dialed atomic.Bool
	fetcher := Fetcher{
		lookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "intranet.test":
				return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
			case "mixed.test":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("::ffff:10.0.0.1")}, nil
			default:
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
		},
		dialPinned: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
			dialed.Store(true)
			return nil, errors.New("a forbidden target was dialed")
		},
	}
	for _, rawURL := range []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data",
		"http://10.0.0.8/",
		"http://[::ffff:127.0.0.1]/",
		"http://[64:ff9b::7f00:1]/",
		"http://localhost/",
		"http://localhost./",
		"http://localhost../",
		"http://service.localhost/",
		"http://intranet.test/",
		"http://mixed.test/",
	} {
		_, err := fetcher.Fetch(context.Background(), rawURL, 0, 0)
		if !errors.Is(err, errPrivateAddress) {
			t.Errorf("%s error = %v, want errPrivateAddress", rawURL, err)
		}
	}
	if dialed.Load() {
		t.Fatal("a non-public target reached the dial hook")
	}
}

func TestFetchRejectsInvalidURLAndCredentials(t *testing.T) {
	for _, rawURL := range []string{"", "example.com", "file:///etc/passwd", "ftp://example.com", "://bad"} {
		if _, err := (Fetcher{}).Fetch(context.Background(), rawURL, 0, 0); err == nil {
			t.Errorf("%q was accepted", rawURL)
		}
	}
	const secret = "secret"
	_, err := (Fetcher{}).Fetch(context.Background(), "http://user:"+secret+"@example.com/", 0, 0)
	if err == nil || !strings.Contains(err.Error(), "embedded credentials") {
		t.Fatalf("credential URL error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("credential URL error leaked its password: %v", err)
	}
}

func TestFetchRechecksDNSAfterRedirect(t *testing.T) {
	var opened, secret atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open":
			opened.Add(1)
			_, port, err := net.SplitHostPort(r.Host)
			if err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Location", "http://internal.example:"+port+"/secret")
			w.WriteHeader(http.StatusFound)
		case "/secret":
			secret.Add(1)
			_, _ = w.Write([]byte("secret"))
		}
	}))
	defer server.Close()
	fetcher := fakePublicFetcher(server)
	fetcher.lookupIP = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "internal.example" {
			return []netip.Addr{netip.MustParseAddr("10.1.2.3")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}

	_, err := fetcher.Fetch(context.Background(), publicTestURL(t, server.URL, "/open"), 0, 0)
	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("redirect error = %v", err)
	}
	if opened.Load() != 1 || secret.Load() != 0 {
		t.Fatalf("opened %d times, private redirect reached %d times", opened.Load(), secret.Load())
	}
}

func TestFetchRejectsRedirectCredentialsWithoutLeak(t *testing.T) {
	const secret = "secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, port, _ := net.SplitHostPort(r.Host)
		w.Header().Set("Location", "http://user:"+secret+"@public.example:"+port+"/final")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	_, err := fakePublicFetcher(server).Fetch(context.Background(), publicTestURL(t, server.URL, "/open"), 0, 0)
	if err == nil || !strings.Contains(err.Error(), "embedded credentials") {
		t.Fatalf("redirect credential error = %v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("redirect credential error leaked its password: %v", err)
	}
}

func TestExplicitProxyReceivesOnlyCheckedAddress(t *testing.T) {
	seenURLHost := make(chan string, 1)
	seenHost := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURLHost <- r.URL.Hostname()
		seenHost <- r.Host
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()
	fetcher := Fetcher{
		Proxy: proxy.URL,
		lookupIP: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		dialPinned: func(context.Context, *net.Dialer, string, string) (net.Conn, error) {
			return nil, errors.New("direct dial used despite explicit proxy")
		},
	}

	result, err := fetcher.Fetch(context.Background(), "http://public.example/resource", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "proxied" || result.URL != "http://public.example/resource" {
		t.Fatalf("proxy result = %+v", result)
	}
	if host := <-seenURLHost; host != "93.184.216.34" {
		t.Fatalf("proxy URL host = %q, want pinned public IP", host)
	}
	if host := <-seenHost; host != "93.184.216.34:80" {
		t.Fatalf("proxy Host = %q, want pinned public IP", host)
	}
}

func TestHTTPSProxyClientHelloUsesProxyHost(t *testing.T) {
	for _, test := range []struct {
		name    string
		host    string
		wantSNI string
	}{
		{name: "domain", host: "localhost", wantSNI: "localhost"},
		{name: "IP", host: "127.0.0.1", wantSNI: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			clientHello := make(chan string, 1)
			proxy := httptest.NewUnstartedServer(nil)
			proxy.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				clientHello <- hello.ServerName
				return nil, nil
			}}
			proxy.StartTLS()
			defer proxy.Close()
			proxyURL, err := url.Parse(proxy.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxyURL.Host = net.JoinHostPort(test.host, proxyURL.Port())

			fetcher := Fetcher{
				Proxy: proxyURL.String(),
				lookupIP: func(context.Context, string) ([]netip.Addr, error) {
					return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
				},
			}
			if _, err := fetcher.Fetch(context.Background(), "http://public.example/", 0, 0); err == nil {
				t.Fatal("fetch unexpectedly trusted the test proxy certificate")
			}
			select {
			case got := <-clientHello:
				if got != test.wantSNI {
					t.Fatalf("proxy ClientHello SNI = %q, want %q", got, test.wantSNI)
				}
			case <-time.After(time.Second):
				t.Fatal("proxy did not receive a ClientHello")
			}
		})
	}
}

func TestExplicitProxyCannotBypassPolicy(t *testing.T) {
	var hits atomic.Int32
	var redirect atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if redirect.Load() {
			w.Header().Set("Location", "http://127.0.0.1/private")
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("should not be reached"))
	}))
	defer proxy.Close()
	fetcher := Fetcher{
		Proxy: proxy.URL,
		lookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host == "private.test" {
				return []netip.Addr{netip.MustParseAddr("10.0.0.1")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}

	if _, err := fetcher.Fetch(context.Background(), "http://private.test/", 0, 0); !errors.Is(err, errPrivateAddress) {
		t.Fatalf("private target through proxy error = %v", err)
	}
	if hits.Load() != 0 {
		t.Fatal("proxy was contacted for a private target")
	}

	redirect.Store(true)
	if _, err := fetcher.Fetch(context.Background(), "http://public.example/open", 0, 0); !errors.Is(err, errPrivateAddress) {
		t.Fatalf("private redirect through proxy error = %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("proxy received %d requests; private redirect was not stopped", hits.Load())
	}
}

func TestProxyErrorDoesNotLeakCredentials(t *testing.T) {
	const secret = "secret"
	_, err := (Fetcher{Proxy: "http://user:" + secret + "@"}).Fetch(context.Background(), "http://public.example/", 0, 0)
	if err == nil {
		t.Fatal("invalid proxy was accepted")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("proxy error leaked its password: %v", err)
	}
}
