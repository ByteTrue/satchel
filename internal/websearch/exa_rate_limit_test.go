package websearch

import (
	"context"
	"strings"
	"testing"
)

func TestExaFreeToolLevelRateLimitHasActionableError(t *testing.T) {
	isolateKeys(t)
	fake := newFakeExaMCP(t)
	fake.toolRateLimit = true
	_, err := Search(context.Background(), []Provider{{Name: "exa-free", BaseURL: fake.server.URL, HTTPClient: fake.server.Client()}}, "codex cli", 2)
	if err == nil || !strings.Contains(err.Error(), exaRateLimitHint) {
		t.Fatalf("tool-level rate limit error = %v", err)
	}
}
