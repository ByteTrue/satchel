package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxResponseBytes      = 4 << 20
	maxErrorResponseBytes = 1 << 20
)

func post(ctx context.Context, provider Provider, request protocolRequest) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, request.endpoint, bytes.NewReader(request.body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range request.headers {
		req.Header.Set(key, value)
	}
	// User-configured headers are applied last, including protocol overrides.
	for key, value := range provider.Headers {
		req.Header.Set(key, value)
	}
	client := provider.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := clone.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%s request failed: %w", provider.API, err)
	}
	defer response.Body.Close()

	limit := int64(maxResponseBytes)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		limit = maxErrorResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", provider.API, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%s response exceeds %d bytes", provider.API, limit)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s%s", provider.API, response.Status, upstreamDetails(provider, body))
	}
	return body, nil
}

func vendorMessage(body []byte) string {
	var envelope struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Error) > 0 {
		var object struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(envelope.Error, &object) == nil && object.Message != "" {
			return object.Message
		}
		var message string
		if json.Unmarshal(envelope.Error, &message) == nil {
			return message
		}
	}
	return string(body)
}

func upstreamDetails(provider Provider, body []byte) string {
	message := strings.Join(strings.Fields(redactText(vendorMessage(body), provider)), " ")
	if message == "" {
		return ""
	}
	runes := []rune(message)
	if len(runes) > 300 {
		message = string(runes[:300]) + "…"
	}
	return ": " + message
}

func redactText(message string, provider Provider) string {
	secrets := []string{provider.APIKey}
	for key, value := range provider.Headers {
		secrets = append(secrets, value)
		if strings.EqualFold(key, "Authorization") {
			value = strings.TrimSpace(value)
			if i := strings.IndexAny(value, " \t"); i >= 0 {
				// Upstreams often echo only the token, without "Bearer".
				secrets = append(secrets, strings.TrimSpace(value[i+1:]))
			}
		}
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		message = strings.ReplaceAll(message, secret, "[REDACTED]")
		if escaped := url.QueryEscape(secret); escaped != secret {
			message = strings.ReplaceAll(message, escaped, "[REDACTED]")
		}
	}
	return message
}
