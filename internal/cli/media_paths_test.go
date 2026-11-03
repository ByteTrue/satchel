package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMediaPaths(t *testing.T) {
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := resolveMediaPaths([]string{"assets/a.png", "https://example.test/a.png", "data:image/png;base64,AA==", "~/a.png"})
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(cwd, "assets", "a.png"), "https://example.test/a.png", "data:image/png;base64,AA==", filepath.Join(home, "a.png")}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path %d = %q, want %q", i, got[i], want[i])
		}
	}
}
