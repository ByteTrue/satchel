package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveVideoAndRedaction(t *testing.T) {
	t.Setenv("SATCHEL_VIDEO_KEY", "resolved-video-key")
	cfg := Default()
	cfg.Video = VideoConfig{API: "ark", BaseURL: "https://video.example.test/api/v3", APIKey: "$SATCHEL_VIDEO_KEY", Model: "video-model", OutputDir: "/tmp/videos", Headers: map[string]string{"X-Key": "header-secret"}}
	resolved, err := ResolveVideo(cfg)
	if err != nil || resolved.APIKey != "resolved-video-key" || resolved.Model != "video-model" || resolved.OutputDir != "/tmp/videos" {
		t.Fatalf("resolved video = %+v, %v", resolved, err)
	}
	redacted := Redacted(cfg)
	data, _ := json.Marshal(redacted)
	for _, secret := range []string{"resolved-video-key", "header-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("redacted video contains %q: %s", secret, data)
		}
	}
}

func TestVideoValidation(t *testing.T) {
	for name, video := range map[string]VideoConfig{
		"api":       {API: "unknown", Model: "m"},
		"base URL":  {API: "ark", BaseURL: "https://user:secret@example.test", Model: "m"},
		"outputDir": {API: "ark", Model: "m", OutputDir: "relative"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SATCHEL_CONFIG", filepath.Join(t.TempDir(), "config.json"))
			cfg := Default()
			cfg.Video = video
			if err := Save(cfg); err == nil {
				t.Fatal("invalid video config was saved")
			}
		})
	}
}
