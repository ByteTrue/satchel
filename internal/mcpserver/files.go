package mcpserver

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type OutputImage struct {
	Data     []byte
	MIMEType string
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// GenerationTarget applies Magpie Image MCP's path semantics: a file or
// folder, relative to the MCP process or absolute, with automatic numbering.
// The returned path does not exist at the time of the check.
func GenerationTarget(defaultDir, path, prompt, ext string, index, count int) (string, error) {
	dir := defaultDir
	name := ""
	if path != "" {
		p := path
		if strings.HasPrefix(p, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			p = filepath.Join(home, p[2:])
		} else if !filepath.IsAbs(p) {
			cwd, err := os.Getwd()
			if err != nil {
				return "", err
			}
			p = filepath.Join(cwd, p)
		}
		if st, err := os.Stat(p); (err == nil && st.IsDir()) || strings.HasSuffix(path, "/") || filepath.Ext(p) == "" {
			dir = p
		} else {
			dir, name = filepath.Dir(p), filepath.Base(p)
		}
	}
	if dir == "" {
		return "", errors.New("no output directory is configured; run satchel setup or provide path")
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("configured output directory must be absolute")
	}
	if name == "" {
		slug := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(prompt), "-"), "-")
		if len(slug) > 40 {
			slug = strings.TrimRight(slug[:40], "-")
		}
		if slug == "" {
			slug = "image"
		}
		name = slug + "-" + time.Now().Format("20060102-150405") + ext
	}
	base, extension := strings.TrimSuffix(name, filepath.Ext(name)), filepath.Ext(name)
	lower := strings.ToLower(extension)
	switch {
	case lower == ".jpeg" && ext == ".jpg", lower == ext:
	case lower == ".png", lower == ".jpg", lower == ".jpeg", lower == ".webp", lower == ".gif", lower == ".mp4", lower == ".mov", lower == ".webm", lower == ".m4v":
		extension = ext
	default:
		base, extension = name, ext
	}
	if count > 1 {
		base = fmt.Sprintf("%s-%d", base, index+1)
	}
	out := filepath.Join(dir, base+extension)
	for suffix := 2; ; suffix++ {
		if _, err := os.Lstat(out); errors.Is(err, os.ErrNotExist) {
			return out, nil
		} else if err != nil {
			return "", err
		}
		out = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, suffix, extension))
	}
}

func SaveGenerationImages(defaultDir, path, prompt string, images []OutputImage) ([]string, error) {
	var paths []string
	var errs []error
	for i, image := range images {
		ext, err := generatedImageExtension(image.Data)
		if err != nil {
			errs = append(errs, fmt.Errorf("image %d: %w", i+1, err))
			continue
		}
		for {
			target, err := GenerationTarget(defaultDir, path, prompt, ext, i, len(images))
			if err != nil {
				errs = append(errs, fmt.Errorf("image %d: %w", i+1, err))
				break
			}
			err = writeExclusiveFile(target, image.Data, 0o644)
			if errors.Is(err, os.ErrExist) {
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("image %d: %w", i+1, err))
			} else {
				paths = append(paths, target)
			}
			break
		}
	}
	return paths, errors.Join(errs...)
}

func writeExclusiveFile(path string, data []byte, mode os.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	ok = true
	return nil
}

func generatedImageExtension(data []byte) (string, error) {
	switch {
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return ".png", nil
	case len(data) >= 3 && bytes.Equal(data[:3], []byte("\xff\xd8\xff")):
		return ".jpg", nil
	case len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))):
		return ".gif", nil
	case len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return ".webp", nil
	default:
		return "", errors.New("provider returned an unsupported or invalid image (expected PNG, JPEG, GIF, or WebP)")
	}
}
