package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"satchel/internal/config"
	"satchel/internal/mcpserver"
	"satchel/internal/video"
)

func TestGenerateVideoRetriesEEXISTWithNextNumber(t *testing.T) {
	mp4 := []byte("\x00\x00\x00\x18ftypmp42new-video")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/videos":
			fmt.Fprint(w, `{"id":"vid-1"}`)
		case "/v1/videos/vid-1":
			fmt.Fprint(w, `{"status":"completed"}`)
		case "/v1/videos/vid-1/content":
			_, _ = w.Write(mp4)
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	root := t.TempDir()
	t.Setenv("SATCHEL_CONFIG", filepath.Join(root, "config.json"))
	cfg := config.Default()
	cfg.Video = config.VideoConfig{API: "openai-videos", BaseURL: provider.URL + "/v1", Model: "video-test", OutputDir: root}
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	requested := filepath.Join(root, "hero.mp4")
	calls := 0
	result, err := generateVideoWithGenerator(context.Background(), mcpserver.VideoInput{Prompt: "test", Seconds: 1, Path: requested}, func(ctx context.Context, p video.Provider, request video.Request, path string) (video.Result, error) {
		calls++
		if calls == 1 {
			if err := os.WriteFile(path, []byte("competitor"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return video.Generate(ctx, p, request, path)
	})
	if err != nil || len(result.Files) != 1 {
		t.Fatalf("generateVideoWithGenerator = %+v, %v", result, err)
	}
	if calls != 2 || filepath.Base(result.Files[0]) != "hero-2.mp4" {
		t.Fatalf("retry calls=%d, files=%v", calls, result.Files)
	}
	original, err := os.ReadFile(requested)
	if err != nil || string(original) != "competitor" {
		t.Fatalf("competing file was overwritten: %q, %v", original, err)
	}
	generated, err := os.ReadFile(result.Files[0])
	if err != nil || string(generated) != string(mp4) {
		t.Fatalf("generated video = %x, %v", generated, err)
	}
}
