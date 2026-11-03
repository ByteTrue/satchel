// Public media downloading and pinned-address transport are selectively
// adapted from Magpie internal/gateway/draw.go (FetchImage) and
// internal/gateway/image_fetch_guard.go at commit
// 4534561f6bb8f8353a67801fbe864a6502517022.
package video

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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"satchel/internal/netguard"
)

const maxMediaRedirects = 5

var (
	errPrivateAddress   = errors.New("refusing a private, loopback, link-local, or otherwise non-public media address")
	errMediaCredentials = errors.New("media URLs with embedded credentials are not supported")
	errMediaRedirects   = errors.New("media download stopped after 5 redirects")
)

type referenceImage struct {
	data     []byte
	mimeType string
}

type lookupIPFunc func(context.Context, string) ([]netip.Addr, error)
type dialPinnedFunc func(context.Context, *net.Dialer, string, string) (net.Conn, error)

type videoDownloader struct {
	proxy      *url.URL
	lookupIP   lookupIPFunc
	dialPinned dialPinnedFunc
}

func loadReference(ctx context.Context, downloader *videoDownloader, value string) (referenceImage, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return referenceImage{}, errors.New("image reference is empty")
	}
	if len(value) >= len("data:") && strings.EqualFold(value[:len("data:")], "data:") {
		return dataURLReference(value)
	}
	if hasHTTPScheme(value) {
		var ref referenceImage
		err := fetchMedia(ctx, downloader, value, referenceTimeout, "image/png,image/jpeg,image/gif,image/webp", func(ctx context.Context, res *http.Response) error {
			if res.ContentLength > maxReferenceBytes {
				return fmt.Errorf("reference image exceeds %d bytes", maxReferenceBytes)
			}
			data, err := io.ReadAll(io.LimitReader(res.Body, maxReferenceBytes+1))
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return errors.New("read reference image failed")
			}
			if int64(len(data)) > maxReferenceBytes {
				return fmt.Errorf("reference image exceeds %d bytes", maxReferenceBytes)
			}
			mimeType := sniffImage(data)
			if mimeType == "" {
				return errors.New("reference image must be PNG, JPEG, GIF, or WebP")
			}
			ref = referenceImage{data: data, mimeType: mimeType}
			return nil
		})
		return ref, err
	}
	if strings.Contains(value, "://") {
		return referenceImage{}, errors.New("only absolute local paths, HTTP(S) URLs, and data URLs are supported for images")
	}
	if !filepath.IsAbs(value) {
		return referenceImage{}, errors.New("local image reference must be an absolute path")
	}
	f, err := os.Open(value)
	if err != nil {
		return referenceImage{}, fmt.Errorf("open image reference: %w", err)
	}
	data, err := readReferenceFile(f)
	closeErr := f.Close()
	if err != nil {
		return referenceImage{}, fmt.Errorf("read image reference: %w", err)
	}
	if closeErr != nil {
		return referenceImage{}, fmt.Errorf("close image reference: %w", closeErr)
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		return referenceImage{}, errors.New("reference image must be PNG, JPEG, GIF, or WebP")
	}
	return referenceImage{data: data, mimeType: mimeType}, nil
}

func readReferenceFile(f *os.File) ([]byte, error) {
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if st.Size() > maxReferenceBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxReferenceBytes)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxReferenceBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxReferenceBytes {
		return nil, fmt.Errorf("image exceeds %d bytes", maxReferenceBytes)
	}
	return data, nil
}

func dataURLReference(value string) (referenceImage, error) {
	meta, encoded, ok := strings.Cut(value[len("data:"):], ",")
	if !ok || !strings.HasSuffix(strings.ToLower(meta), ";base64") {
		return referenceImage{}, errors.New("image data URL must contain base64 data")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(int(maxReferenceBytes))+4 {
		return referenceImage{}, fmt.Errorf("reference image exceeds %d bytes", maxReferenceBytes)
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return referenceImage{}, errors.New("image data URL is not valid base64")
	}
	if len(data) == 0 {
		return referenceImage{}, errors.New("image data URL decodes to no bytes")
	}
	if int64(len(data)) > maxReferenceBytes {
		return referenceImage{}, fmt.Errorf("reference image exceeds %d bytes", maxReferenceBytes)
	}
	mimeType := sniffImage(data)
	if mimeType == "" {
		return referenceImage{}, errors.New("image data URL must contain PNG, JPEG, GIF, or WebP bytes")
	}
	return referenceImage{data: data, mimeType: mimeType}, nil
}

func sniffImage(data []byte) string {
	switch mimeType := http.DetectContentType(data); mimeType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return mimeType
	default:
		return ""
	}
}

func hasHTTPScheme(raw string) bool {
	raw = strings.TrimSpace(raw)
	return len(raw) >= len("http://") && strings.EqualFold(raw[:len("http://")], "http://") ||
		len(raw) >= len("https://") && strings.EqualFold(raw[:len("https://")], "https://")
}

func parseMediaURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, errors.New("invalid media URL")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if err := validateMediaURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

func validateMediaURL(u *url.URL) error {
	if u == nil || u.Host == "" || u.Hostname() == "" {
		return errors.New("media URL must name a host")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("only HTTP(S) media URLs are supported")
	}
	if u.User != nil {
		return errMediaCredentials
	}
	return nil
}

// fetchMedia deliberately creates a clean request: provider API keys, custom
// headers, cookies, and environment proxy settings never cross into it.
func fetchMedia(ctx context.Context, downloader *videoDownloader, rawURL string, timeout time.Duration, accept string, consume func(context.Context, *http.Response) error) error {
	u, err := parseMediaURL(rawURL)
	if err != nil {
		return err
	}
	if downloader == nil {
		downloader = &videoDownloader{}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := &http.Client{
		Transport: &mediaTransport{
			proxy: downloader.proxy, lookupIP: downloader.lookupIP, dialPinned: downloader.dialPinned,
		},
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > maxMediaRedirects {
				return errMediaRedirects
			}
			if err := validateMediaURL(next.URL); err != nil {
				return err
			}
			for _, key := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Referer"} {
				next.Header.Del(key)
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid media URL")
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "satchel-video/1")
	res, err := client.Do(req)
	if err != nil {
		return safeMediaError(ctx, timeout, err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("media download returned HTTP %d", res.StatusCode)
	}
	return consume(ctx, res)
}

func downloadVideo(ctx context.Context, downloader *videoDownloader, rawURL string, output io.Writer) error {
	return fetchMedia(ctx, downloader, rawURL, videoDownloadTimeout, "video/mp4,application/octet-stream", func(ctx context.Context, res *http.Response) error {
		return copyVideo(ctx, res.Body, res.ContentLength, output, maxVideoBytes)
	})
}

func copyVideo(ctx context.Context, body io.Reader, contentLength int64, output io.Writer, limit int64) error {
	if contentLength > limit {
		return fmt.Errorf("downloaded video exceeds %d bytes", limit)
	}
	written, err := io.Copy(output, io.LimitReader(body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("read or write video download failed")
	}
	if written > limit {
		return fmt.Errorf("downloaded video exceeds %d bytes", limit)
	}
	if written == 0 {
		return errors.New("downloaded video is empty")
	}
	return nil
}

// net/http errors can quote a complete signed URL. Only known policy and
// context errors cross this boundary; other network details stay generic.
func safeMediaError(ctx context.Context, timeout time.Duration, err error) error {
	switch {
	case errors.Is(err, errPrivateAddress):
		return errPrivateAddress
	case errors.Is(err, errMediaCredentials):
		return errMediaCredentials
	case errors.Is(err, errMediaRedirects):
		return errMediaRedirects
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("media download exceeded its %s timeout: %w", timeout, context.DeadlineExceeded)
	default:
		return errors.New("media download request failed")
	}
}

func defaultLookupIP(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
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
			return netip.Addr{}, errors.New("resolve media URL host failed")
		}
	}
	if len(addrs) == 0 {
		return netip.Addr{}, errors.New("media URL host resolved to no addresses")
	}
	for _, addr := range addrs {
		if !netguard.Public(addr) {
			return netip.Addr{}, errPrivateAddress
		}
	}
	return addrs[0].Unmap(), nil
}

func mediaURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func mediaGuardDialer() *net.Dialer {
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

// mediaTransport validates every DNS answer, pins the checked address, and
// runs again for every redirect. A proxy receives the pinned address too.
type mediaTransport struct {
	proxy      *url.URL
	lookupIP   lookupIPFunc
	dialPinned dialPinnedFunc
}

func (t *mediaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := validateMediaURL(req.URL); err != nil {
		return nil, err
	}
	pinned, err := publicResolution(req.Context(), req.URL, t.lookupIP)
	if err != nil {
		return nil, err
	}
	host := req.URL.Hostname()
	port := mediaURLPort(req.URL)
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
		dialer := mediaGuardDialer()
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
