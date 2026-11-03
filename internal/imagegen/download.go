// Public image downloading and pinned-address transport are selectively
// adapted from Magpie internal/gateway/draw.go (FetchImage) and
// internal/gateway/image_fetch_guard.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
// Magpie is Copyright (c) 2026 yetone, MIT licensed.
package imagegen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"satchel/internal/netguard"
)

const (
	maxGeneratedImageBytes = 50 << 20
	imageDownloadTimeout   = 2 * time.Minute
	maxImageRedirects      = 5
)

var (
	errImagePrivateAddress = errors.New("refusing a private, loopback, link-local, or otherwise non-public image address")
	errImageCredentials    = errors.New("image URLs with embedded credentials are not supported")
	errImageRedirects      = errors.New("image download stopped after 5 redirects")
)

type lookupImageIPFunc func(context.Context, string) ([]netip.Addr, error)
type dialPinnedImageFunc func(context.Context, *net.Dialer, string, string) (net.Conn, error)

type imageDownloader struct {
	proxy      *url.URL
	lookupIP   lookupImageIPFunc
	dialPinned dialPinnedImageFunc
}

func defaultLookupImageIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func hasHTTPScheme(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= len("http://") && strings.EqualFold(raw[:len("http://")], "http://") ||
		len(raw) >= len("https://") && strings.EqualFold(raw[:len("https://")], "https://")
}

func parseImageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid image URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if err := validateImageURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validateImageURL(u *url.URL) error {
	if u == nil || u.Host == "" || u.Hostname() == "" {
		return errors.New("image URL must name a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only HTTP(S) image URLs are supported")
	}
	if u.User != nil {
		return errImageCredentials
	}
	return nil
}

// downloadImage deliberately creates a clean request: provider API keys,
// custom headers, cookies, and environment proxy settings never cross into it.
func downloadImage(ctx context.Context, downloader *imageDownloader, rawURL string, limit int64) (referenceImage, error) {
	u, err := parseImageURL(rawURL)
	if err != nil {
		return referenceImage{}, err
	}
	if downloader == nil {
		downloader = &imageDownloader{}
	}
	ctx, cancel := context.WithTimeout(ctx, imageDownloadTimeout)
	defer cancel()
	client := &http.Client{
		Transport: &imageTransport{
			proxy: downloader.proxy, lookupIP: downloader.lookupIP, dialPinned: downloader.dialPinned,
		},
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > maxImageRedirects {
				return errImageRedirects
			}
			if err := validateImageURL(next.URL); err != nil {
				return err
			}
			// In particular, do not echo a signed source URL in Referer.
			for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
				next.Header.Del(key)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return referenceImage{}, errors.New("invalid image URL")
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	req.Header.Set("User-Agent", "satchel-imagegen/1")
	res, err := client.Do(req)
	if err != nil {
		return referenceImage{}, safeImageDownloadError(ctx, err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return referenceImage{}, fmt.Errorf("image download returned HTTP %d", res.StatusCode)
	}
	if res.ContentLength > limit {
		return referenceImage{}, fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return referenceImage{}, safeImageDownloadError(ctx, ctx.Err())
		}
		return referenceImage{}, errors.New("read downloaded image failed")
	}
	if int64(len(data)) > limit {
		return referenceImage{}, fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		return referenceImage{}, errors.New("downloaded image must be PNG, JPEG, GIF, or WebP")
	}
	return referenceImage{data: data, mimeType: mimeType}, nil
}

// net/http errors can quote the complete signed URL. Only known policy and
// context errors cross this boundary; all other network details stay generic.
func safeImageDownloadError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errImagePrivateAddress):
		return errImagePrivateAddress
	case errors.Is(err, errImageCredentials):
		return errImageCredentials
	case errors.Is(err, errImageRedirects):
		return errImageRedirects
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("image download exceeded its %s timeout: %w", imageDownloadTimeout, context.DeadlineExceeded)
	default:
		return errors.New("image download request failed")
	}
}

func materializeImage(ctx context.Context, downloader *imageDownloader, value, declaredMIME string) (Image, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Image{}, errors.New("image reference is empty")
	}
	if len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:") {
		return dataURLImage(value)
	}
	if hasHTTPScheme(value) {
		image, err := downloadImage(ctx, downloader, value, maxGeneratedImageBytes)
		if err != nil {
			return Image{}, err
		}
		return Image{Data: image.data, MIMEType: image.mimeType}, nil
	}
	if strings.Contains(value, "://") {
		return Image{}, errors.New("provider returned a non-HTTP(S) image URL")
	}
	return inlineImage(value, declaredMIME)
}

func publicImageResolution(ctx context.Context, u *url.URL, lookup lookupImageIPFunc) (netip.Addr, error) {
	host := u.Hostname()
	normalized := strings.TrimRight(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return netip.Addr{}, errImagePrivateAddress
	}
	if lookup == nil {
		lookup = defaultLookupImageIP
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else {
		var err error
		addrs, err = lookup(ctx, host)
		if err != nil {
			return netip.Addr{}, errors.New("resolve image URL host failed")
		}
	}
	if len(addrs) == 0 {
		return netip.Addr{}, errors.New("image URL host resolved to no addresses")
	}
	for _, a := range addrs {
		if !netguard.Public(a) {
			return netip.Addr{}, errImagePrivateAddress
		}
	}
	return addrs[0].Unmap(), nil
}

func imageURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func imageGuardDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !netguard.Public(ap.Addr()) {
				return errImagePrivateAddress
			}
			return nil
		},
	}
}

// imageTransport validates every DNS answer, pins the checked address, and
// runs again for every redirect. A proxy receives the pinned address too.
type imageTransport struct {
	proxy      *url.URL
	lookupIP   lookupImageIPFunc
	dialPinned dialPinnedImageFunc
}

func (t *imageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateImageURL(req.URL); err != nil {
		return nil, err
	}
	pinned, err := publicImageResolution(req.Context(), req.URL, t.lookupIP)
	if err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	port := imageURLPort(req.URL)
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
	} else {
		dialer := imageGuardDialer()
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
		out.Host = out.URL.Host
	}
	return transport.RoundTrip(out)
}
