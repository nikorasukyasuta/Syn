package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxImageSizeBytes = 8 * 1024 * 1024 // 8 MB Discord standard upload limit

var (
	ErrPathTraversal   = errors.New("security violation: invalid filename or path traversal attempt")
	ErrAssetNotFound   = errors.New("image file not found in EC2 assets directory")
	ErrUnsupportedMIME = errors.New("file is not a valid PNG, JPEG, WEBP, or GIF image")
	ErrFileTooLarge    = errors.New("image file exceeds 8MB limit")
)

// AssetFile represents a validated local image ready to stream to Discord.
type AssetFile struct {
	Name        string
	ContentType string
	Reader      io.Reader
}

// LocalAssetStore provides chroot-like confinement to the EC2 image directory using os.Root.
type LocalAssetStore struct {
	baseDir string
}

// NewLocalAssetStore initializes the local asset store and verifies the directory exists.
func NewLocalAssetStore(baseDir string) (*LocalAssetStore, error) {
	info, err := os.Stat(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			if mkdirErr := os.MkdirAll(baseDir, 0755); mkdirErr == nil {
				return &LocalAssetStore{baseDir: baseDir}, nil
			}
		}
		return nil, fmt.Errorf("checking assets directory %q: %w", baseDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("assets path %q is not a directory", baseDir)
	}
	return &LocalAssetStore{baseDir: baseDir}, nil
}

// LoadImage safely reads and validates an image file from the confined EC2 directory.
func (s *LocalAssetStore) LoadImage(filename string) (*AssetFile, error) {
	cleaned := strings.TrimSpace(filename)
	// Reject empty names, directory separators, or hidden dotfiles immediately
	if cleaned == "" || cleaned != filepath.Base(cleaned) || strings.HasPrefix(cleaned, ".") {
		return nil, ErrPathTraversal
	}

	ext := strings.ToLower(filepath.Ext(cleaned))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif":
	default:
		return nil, ErrUnsupportedMIME
	}

	// OpenRoot confines all file operations to s.baseDir at the OS syscall level (preventing symlink/.. escapes)
	root, err := os.OpenRoot(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("opening asset root: %w", err)
	}
	defer root.Close()

	f, err := root.Open(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrAssetNotFound
		}
		return nil, ErrPathTraversal
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		return nil, ErrAssetNotFound
	}
	if stat.Size() > maxImageSizeBytes {
		return nil, ErrFileTooLarge
	}

	data, err := io.ReadAll(io.LimitReader(f, maxImageSizeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading image file: %w", err)
	}

	// Verify actual file magic bytes (first 512 bytes), not just the file extension
	mimeType := http.DetectContentType(data)
	if !strings.HasPrefix(mimeType, "image/") {
		return nil, ErrUnsupportedMIME
	}

	return &AssetFile{
		Name:        cleaned,
		ContentType: mimeType,
		Reader:      bytes.NewReader(data),
	}, nil
}

// ListAvailableImages returns all valid image filenames currently on the EC2 disk.
// Used for Discord Slash Command Autocomplete so Staff don't have to memorize filenames!
func (s *LocalAssetStore) ListAvailableImages() ([]string, error) {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		switch ext {
		case ".png", ".jpg", ".jpeg", ".webp", ".gif":
			files = append(files, entry.Name())
		}
	}
	return files, nil
}
