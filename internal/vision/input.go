// Single-image input handling is selectively adapted from Magpie
// internal/gateway/vision_mcp.go (mcpImageSrc) and public-address downloading
// from internal/gateway/image_fetch_guard.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022. The transport also follows
// Satchel internal/imagegen/download.go, adapted from the same Magpie baseline.
// Magpie is Copyright (c) 2026 yetone, MIT licensed; see THIRD_PARTY_NOTICES.md.
package vision

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"satchel/internal/netguard"
)

const maxImageRedirects = 5

var (
	errImagePrivateAddress = errors.New("refusing a private, loopback, link-local, or otherwise non-public image address")
	errImageCredentials    = errors.New("image URLs with embedded credentials are not supported")
	errImageRedirects      = errors.New("image download stopped after 5 redirects")
)

type lookupImageIPFunc func(context.Context, string) ([]netip.Addr, error)
type dialPinnedImageFunc func(context.Context, *net.Dialer, string, string) (net.Conn, error)

type imageDownloader struct {
	lookupIP   lookupImageIPFunc
	dialPinned dialPinnedImageFunc
}

func defaultLookupImageIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func hasHTTPScheme(raw string) bool {
	return len(raw) >= len("http://") && strings.EqualFold(raw[:len("http://")], "http://") ||
		len(raw) >= len("https://") && strings.EqualFold(raw[:len("https://")], "https://")
}

func parseImageURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
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
	if u == nil || u.Host == "" || u.Hostname() == "" || u.Opaque != "" {
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

func loadImage(ctx context.Context, downloader *imageDownloader, value string) ([]image, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("analyze_image needs an image: an absolute file path, an HTTP(S) URL, or a data URL")
	}
	low := strings.ToLower(value)
	var loaded image
	switch {
	case strings.HasPrefix(low, "data:"):
		var err error
		loaded, err = dataURLImage(value)
		if err != nil {
			return nil, err
		}
	case hasHTTPScheme(value):
		var err error
		loaded, err = downloadImage(ctx, downloader, value, maxImageBytes)
		if err != nil {
			return nil, err
		}
	default:
		if strings.Contains(value, "://") {
			return nil, errors.New("only absolute local paths, HTTP(S) URLs, and data URLs are supported")
		}
		if !filepath.IsAbs(value) {
			return nil, errors.New("analyze_image local image path must be absolute")
		}
		data, err := readImage(value, maxImageBytes)
		if err != nil {
			return nil, err
		}
		mimeType := sniffImage(data)
		if mimeType == "" {
			return nil, fmt.Errorf("image %q must be PNG, JPEG, GIF, or WebP (checked by magic bytes)", value)
		}
		loaded = image{data: base64.StdEncoding.EncodeToString(data), mimeType: mimeType}
	}
	return []image{loaded}, nil
}

func dataURLImage(value string) (image, error) {
	meta, encoded, ok := strings.Cut(value[len("data:"):], ",")
	lowerMeta := strings.ToLower(meta)
	if !ok || !strings.HasPrefix(lowerMeta, "image/") || !strings.HasSuffix(lowerMeta, ";base64") {
		return image{}, errors.New("image data URL must use an image media type and base64 encoding")
	}
	// Reject an oversized encoding before DecodeString can allocate for it.
	if len(encoded) > base64.StdEncoding.EncodedLen(maxImageBytes) {
		return image{}, fmt.Errorf("data URL image exceeds the %d MiB budget", maxImageBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return image{}, errors.New("image data URL contains invalid base64")
	}
	if len(data) == 0 {
		return image{}, errors.New("image data URL decodes to no bytes")
	}
	if len(data) > maxImageBytes {
		return image{}, fmt.Errorf("data URL image exceeds the %d MiB budget", maxImageBytes>>20)
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		return image{}, errors.New("data URL image must be PNG, JPEG, GIF, or WebP (checked by magic bytes)")
	}
	return image{data: base64.StdEncoding.EncodeToString(data), mimeType: mimeType}, nil
}

// downloadImage deliberately creates a clean request and transport: provider
// API keys, custom headers, cookies, clients, and environment proxies never
// cross into an agent-supplied image URL.
func downloadImage(ctx context.Context, downloader *imageDownloader, rawURL string, limit int64) (image, error) {
	u, err := parseImageURL(rawURL)
	if err != nil {
		return image{}, err
	}
	if downloader == nil {
		downloader = &imageDownloader{}
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	client := &http.Client{
		Transport: &imageTransport{lookupIP: downloader.lookupIP, dialPinned: downloader.dialPinned},
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
		return image{}, errors.New("invalid image URL")
	}
	req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
	req.Header.Set("User-Agent", "satchel-vision/1")
	response, err := client.Do(req)
	if err != nil {
		return image{}, safeImageDownloadError(ctx, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return image{}, fmt.Errorf("image download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return image{}, fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return image{}, safeImageDownloadError(ctx, ctx.Err())
		}
		return image{}, errors.New("read downloaded image failed")
	}
	if int64(len(data)) > limit {
		return image{}, fmt.Errorf("downloaded image exceeds %d bytes", limit)
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		return image{}, errors.New("downloaded image must be PNG, JPEG, GIF, or WebP (checked by magic bytes)")
	}
	return image{data: base64.StdEncoding.EncodeToString(data), mimeType: mimeType}, nil
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
		return fmt.Errorf("image download exceeded its %s timeout: %w", requestTimeout, context.DeadlineExceeded)
	default:
		return errors.New("image download request failed")
	}
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
	// Every answer must be safe, not just the one selected for dialing.
	for _, addr := range addrs {
		if !netguard.Public(addr) {
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
			addr, err := netip.ParseAddrPort(address)
			if err != nil || !netguard.Public(addr.Addr()) {
				return errImagePrivateAddress
			}
			return nil
		},
	}
}

// imageTransport validates every DNS answer, pins the checked address, and
// runs again for every redirect. TLS and Host keep the original hostname.
type imageTransport struct {
	lookupIP   lookupImageIPFunc
	dialPinned dialPinnedImageFunc
}

func (transport *imageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateImageURL(req.URL); err != nil {
		return nil, err
	}
	pinned, err := publicImageResolution(req.Context(), req.URL, transport.lookupIP)
	if err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	port := imageURLPort(req.URL)
	dialer := imageGuardDialer()
	address := net.JoinHostPort(pinned.String(), port)
	upstream := &http.Transport{
		TLSClientConfig:       &tls.Config{ServerName: host},
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	upstream.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		if transport.dialPinned != nil {
			return transport.dialPinned(ctx, dialer, network, address)
		}
		return dialer.DialContext(ctx, network, address)
	}

	out := req.Clone(req.Context())
	out.URL.Host = address
	out.Host = req.Host
	if out.Host == "" {
		out.Host = req.URL.Host
	}
	return upstream.RoundTrip(out)
}
