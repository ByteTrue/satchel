package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
)

func TestAnalyzeImageAdapter(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer vision-secret" {
			t.Errorf("unexpected vision request: %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "vision-model" {
			t.Errorf("model = %v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"a tiny test image"}}]}`)
	}))
	defer provider.Close()
	root := t.TempDir()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(root, "config.json"))
	cfg := config.Default()
	cfg.Vision = config.VisionConfig{API: "openai-completions", BaseURL: provider.URL + "/v1", APIKey: "vision-secret", Model: "vision-model"}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(root, "tiny.png")
	if err := os.WriteFile(imagePath, []byte("\x89PNG\r\n\x1a\n"+"tiny"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := analyzeImage(context.Background(), mcpserver.AnalyzeImageInput{Image: imagePath, Prompt: "describe it"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "a tiny test image" {
		t.Fatalf("answer = %q", result.Text)
	}
}
