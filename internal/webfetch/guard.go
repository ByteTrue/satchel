// SSRF-safe HTTP transport selectively adapted from Magpie
// internal/gateway/image_fetch_guard.go (publicTransport/publicClient) at
// commit 4534561f6bb8f8353a67801fbe864a6502517022. Magpie's settings and
// netproxy dependencies are replaced by Satchel's explicit proxy string.
package webfetch

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"satchel/internal/netguard"
)

var (
	errPrivateAddress      = errors.New("refusing a private, loopback, link-local, or otherwise non-public address")
	errEmbeddedCredentials = errors.New("URLs with embedded credentials are not supported")
)

type lookupIPFunc func(context.Context, string) ([]netip.Addr, error)
type dialPinnedFunc func(context.Context, *net.Dialer, string, string) (net.Conn, error)

func defaultLookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		// url.Error can quote the complete input, including userinfo.
		return nil, errors.New("invalid URL")
	}
	if err := validateHTTPURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validateHTTPURL(u *url.URL) error {
	if u == nil || u.Host == "" || u.Hostname() == "" {
		return errors.New("URL must name a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only http:// and https:// URLs are supported")
	}
	if u.User != nil {
		return errEmbeddedCredentials
	}
	return nil
}

func publicResolution(ctx context.Context, u *url.URL, lookup lookupIPFunc) (netip.Addr, error) {
	host := u.Hostname()
	normalized := strings.TrimRight(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return netip.Addr{}, errPrivateAddress
	}
	if lookup == nil {
		lookup = defaultLookupIP
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		var err error
		addrs, err = lookup(ctx, host)
		if err != nil {
			return netip.Addr{}, fmt.Errorf("resolve URL host: %w", err)
		}
	}
	if len(addrs) == 0 {
		return netip.Addr{}, errors.New("URL host resolved to no addresses")
	}
	// Every answer must be safe, not just the one selected for dialing.
	for _, a := range addrs {
		if !netguard.Public(a) {
			return netip.Addr{}, errPrivateAddress
		}
	}
	return addrs[0].Unmap(), nil
}

func urlPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// guardDialer rechecks the actual socket address immediately before dialing.
func guardDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !netguard.Public(ap.Addr()) {
				return errPrivateAddress
			}
			return nil
		},
	}
}

// guardedTransport resolves and validates each request, including every
// redirect. It pins the checked address for both direct connections and proxy
// requests, so neither a second DNS lookup nor a proxy can bypass the policy.
type guardedTransport struct {
	proxy      *url.URL
	lookupIP   lookupIPFunc
	dialPinned dialPinnedFunc // package-private test hook; production dials the pinned IP
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateHTTPURL(req.URL); err != nil {
		return nil, err
	}
	pinned, err := publicResolution(req.Context(), req.URL, t.lookupIP)
	if err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	port := urlPort(req.URL)
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{ServerName: host},
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	if t.proxy != nil {
		proxy := t.proxy
		transport.Proxy = func(*http.Request) (*url.URL, error) { return proxy, nil }
		if proxy.Scheme == "https" {
			// TLSClientConfig belongs to the origin; the proxy's outer TLS
			// handshake must authenticate and name the proxy instead.
			tlsDialer := &tls.Dialer{
				NetDialer: &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second},
				Config:    &tls.Config{ServerName: proxy.Hostname()},
			}
			transport.DialTLSContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return tlsDialer.DialContext(ctx, network, address)
			}
		}
	} else {
		dialer := guardDialer()
		address := net.JoinHostPort(pinned.String(), port)
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			if t.dialPinned != nil {
				return t.dialPinned(ctx, dialer, network, address)
			}
			return dialer.DialContext(ctx, network, address)
		}
	}

	out := req.Clone(req.Context())
	out.URL.Host = net.JoinHostPort(pinned.String(), port)
	out.Host = req.Host
	if out.Host == "" {
		out.Host = req.URL.Host
	}
	if t.proxy != nil && req.URL.Scheme == "http" && (t.proxy.Scheme == "http" || t.proxy.Scheme == "https") {
		// A plain HTTP proxy otherwise resolves the Host name itself.
		out.Host = out.URL.Host
	}
	return transport.RoundTrip(out)
}
