package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveVision(t *testing.T) {
	t.Setenv("SATCHEL_VISION_KEY", "resolved-vision-key")
	t.Setenv("SATCHEL_VISION_HEADER", "resolved-header")
	cfg := Default()
	cfg.Vision = VisionConfig{
		API: "openai-completions", BaseURL: "https://vision.example.test/v1",
		APIKey: "$SATCHEL_VISION_KEY", Model: "vision-model",
		Headers: map[string]string{"X-Test": "$SATCHEL_VISION_HEADER"},
	}
	resolved, err := ResolveVision(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.API != cfg.Vision.API || resolved.BaseURL != cfg.Vision.BaseURL || resolved.APIKey != "resolved-vision-key" || resolved.Model != "vision-model" || resolved.Headers["X-Test"] != "resolved-header" {
		t.Fatalf("resolved vision = %+v", resolved)
	}
}

func TestResolveVisionRequiresConfiguration(t *testing.T) {
	if _, err := ResolveVision(Default()); err == nil || !strings.Contains(err.Error(), "satchel setup") {
		t.Fatalf("unconfigured vision error = %v", err)
	}
}

func TestVisionValidationAndRedaction(t *testing.T) {
	cfg := Default()
	cfg.Vision = VisionConfig{API: "anthropic-messages", BaseURL: "https://user:secret@example.test?key=secret", APIKey: "vision-secret", Headers: map[string]string{"X-Key": "header-secret"}}
	if err := validate(cfg); err == nil {
		t.Fatal("credential-bearing vision baseUrl passed validation")
	}
	redacted := Redacted(cfg)
	data, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user:password", "key=secret", "vision-secret", "header-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("redacted vision contains %q: %s", secret, data)
		}
	}
	cfg.Vision.API = "unsupported"
	if err := validate(cfg); err == nil {
		t.Fatal("unsupported vision API passed validation")
	}
}
