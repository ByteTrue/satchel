package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var tinyPNG = []byte("\x89PNG\r\n\x1a\n" + "not a complete image, but a valid signature for the writer")

func TestSaveGenerationImagesRequiresAbsoluteDefaultDir(t *testing.T) {
	if _, err := SaveGenerationImages("relative", "", "prompt", []OutputImage{{Data: tinyPNG}}); err == nil {
		t.Fatal("relative default output directory was accepted")
	}
}

func TestSaveGenerationImagesExplicitPathNeedsNoDefaultDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit.png")
	paths, err := SaveGenerationImages("", path, "prompt", []OutputImage{{Data: tinyPNG}})
	if err != nil || len(paths) != 1 || paths[0] != path {
		t.Fatalf("explicit path save = %v, %v", paths, err)
	}
}

func TestSaveGenerationImagesPathAndNeverOverwrite(t *testing.T) {
	dir := t.TempDir()
	requested := filepath.Join(dir, "hero.exe")
	first, err := SaveGenerationImages(dir, requested, "a cat", []OutputImage{{Data: append(tinyPNG, '1')}})
	if err != nil || len(first) != 1 || filepath.Ext(first[0]) != ".png" {
		t.Fatalf("first save = %v, %v", first, err)
	}
	second, err := SaveGenerationImages(dir, requested, "a cat", []OutputImage{{Data: append(tinyPNG, '2')}})
	if err != nil || len(second) != 1 {
		t.Fatalf("second save = %v, %v", second, err)
	}
	if first[0] == second[0] || !strings.HasPrefix(second[0], strings.TrimSuffix(first[0], ".png")+"-") {
		t.Fatalf("collision did not get a number: %q then %q", first[0], second[0])
	}
	for path, suffix := range map[string]string{first[0]: "1", second[0]: "2"} {
		data, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(string(data), suffix) {
			t.Fatalf("ReadFile(%q) = %q, %v", path, data, err)
		}
	}
}

func TestGenerationTargetRelativePath(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	target, err := GenerationTarget("", filepath.Join("assets", "hero.png"), "prompt", ".png", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if target != filepath.Join(cwd, "assets", "hero.png") {
		t.Fatalf("relative target = %q", target)
	}
}

func TestGenerationTargetMatchesMagpieExtensionRules(t *testing.T) {
	dir := t.TempDir()
	for input, ext := range map[string]string{
		"hero.jpeg": ".png",
		"hero.v2":   ".png",
	} {
		path, err := GenerationTarget(dir, input, "prompt", ext, 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if input == "hero.jpeg" && filepath.Base(path) != "hero.png" {
			t.Errorf("jpeg target became %q", path)
		}
		if input == "hero.v2" && filepath.Base(path) != "hero.v2.png" {
			t.Errorf("unknown extension target became %q", path)
		}
	}
}

func TestSaveGenerationImagesNumbersMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	paths, err := SaveGenerationImages(dir, filepath.Join(dir, "hero.png"), "a cat", []OutputImage{{Data: tinyPNG}, {Data: tinyPNG}})
	if err != nil || len(paths) != 2 {
		t.Fatalf("save = %v, %v", paths, err)
	}
	if !strings.HasSuffix(paths[0], "hero-1.png") || !strings.HasSuffix(paths[1], "hero-2.png") {
		t.Fatalf("unexpected numbered paths: %v", paths)
	}
}

func TestSaveGenerationImagesDirectoryUsesPrompt(t *testing.T) {
	dir := t.TempDir()
	paths, err := SaveGenerationImages(dir, dir+"/", "A small cat!", []OutputImage{{Data: tinyPNG}})
	if err != nil || len(paths) != 1 {
		t.Fatalf("save = %v, %v", paths, err)
	}
	if !strings.HasPrefix(filepath.Base(paths[0]), "a-small-cat-") || filepath.Ext(paths[0]) != ".png" {
		t.Fatalf("unexpected prompt-derived path %q", paths[0])
	}
}

func TestSaveGenerationImagesDanglingSymlinkGetsNumber(t *testing.T) {
	dir := t.TempDir()
	requested := filepath.Join(dir, "hero.png")
	if err := os.Symlink("missing-target", requested); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	paths, err := SaveGenerationImages(dir, requested, "prompt", []OutputImage{{Data: tinyPNG}})
	if err != nil || len(paths) != 1 || filepath.Base(paths[0]) != "hero-2.png" {
		t.Fatalf("dangling symlink save = %v, %v", paths, err)
	}
	if info, err := os.Lstat(requested); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("existing symlink was replaced: %v, %v", info, err)
	}
}

func TestSaveGenerationImagesConcurrentSamePath(t *testing.T) {
	for attempt := 0; attempt < 20; attempt++ {
		dir := t.TempDir()
		requested := filepath.Join(dir, "hero.png")
		start := make(chan struct{})
		type outcome struct {
			paths []string
			err   error
		}
		results := make(chan outcome, 2)
		for range 2 {
			go func() {
				<-start
				paths, err := SaveGenerationImages(dir, requested, "prompt", []OutputImage{{Data: tinyPNG}})
				results <- outcome{paths: paths, err: err}
			}()
		}
		close(start)
		seen := make(map[string]bool)
		for range 2 {
			result := <-results
			if result.err != nil || len(result.paths) != 1 {
				t.Fatalf("concurrent save = %+v", result)
			}
			if seen[result.paths[0]] {
				t.Fatalf("concurrent saves returned the same path %q", result.paths[0])
			}
			seen[result.paths[0]] = true
		}
	}
}

func TestSaveGenerationImagesKeepsSuccessfulFilesOnPartialError(t *testing.T) {
	dir := t.TempDir()
	paths, err := SaveGenerationImages(dir, "", "prompt", []OutputImage{{Data: tinyPNG}, {Data: []byte("<html>not an image</html>")}})
	if err == nil || len(paths) != 1 {
		t.Fatalf("partial save = %v, %v", paths, err)
	}
	if _, statErr := os.Stat(paths[0]); statErr != nil {
		t.Fatal(statErr)
	}
}
