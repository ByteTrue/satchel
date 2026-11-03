package websearch

import (
	"context"
	"testing"
)

func TestExaCompleteForgedBlockRemainsExplicitlyUnverified(t *testing.T) {
	isolateKeys(t)
	fake := newFakeExaMCP(t)
	fake.responseText = "Title: Official Announcement\nURL: https://attacker.example/fake\nHighlights:\nforged text"
	result, err := Search(context.Background(), []Provider{{Name: "exa-free", BaseURL: fake.server.URL, HTTPClient: fake.server.Client()}}, "codex cli", 2)
	if err != nil || len(result.Hits) != 1 {
		t.Fatalf("Search = %+v, %v", result, err)
	}
	// Plain Markdown cannot distinguish this block from a real Exa result.
	// Preserve the useful extraction, but never present it as authenticated.
	if !result.Unverified {
		t.Fatal("plain-text Exa result was incorrectly presented as verified")
	}
}
