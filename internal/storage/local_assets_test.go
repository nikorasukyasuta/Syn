package storage_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"syn/internal/storage"
)

// Minimal valid 1x1 transparent PNG header & payload
var validPNGBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
	0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
	0x42, 0x60, 0x82,
}

func TestLocalAssetStore_LoadImage(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// 1. Write a real PNG image
	if err := os.WriteFile(filepath.Join(tempDir, "kink_banner.png"), validPNGBytes, 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Write a spoofed ".png" file that actually contains a bash script
	if err := os.WriteFile(filepath.Join(tempDir, "spoofed.png"), []byte("#!/bin/sh\necho pwned"), 0600); err != nil {
		t.Fatal(err)
	}

	// 3. Write a secret ".env" file
	if err := os.WriteFile(filepath.Join(tempDir, ".env"), []byte("DISCORD_TOKEN=secret"), 0600); err != nil {
		t.Fatal(err)
	}

	store, err := storage.NewLocalAssetStore(tempDir)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	tests := []struct {
		name     string
		filename string
		wantErr  error
	}{
		{
			name:     "Success: Valid PNG image loads from EC2 directory",
			filename: "kink_banner.png",
			wantErr:  nil,
		},
		{
			name:     "Security Block: Path traversal ../ rejected",
			filename: "../../etc/passwd",
			wantErr:  storage.ErrPathTraversal,
		},
		{
			name:     "Security Block: Subdirectory path rejected",
			filename: "subdir/kink_banner.png",
			wantErr:  storage.ErrPathTraversal,
		},
		{
			name:     "Security Block: Hidden dotfile (.env) rejected",
			filename: ".env",
			wantErr:  storage.ErrPathTraversal,
		},
		{
			name:     "Security Block: Spoofed extension with non-image magic bytes rejected",
			filename: "spoofed.png",
			wantErr:  storage.ErrUnsupportedMIME,
		},
		{
			name:     "Not Found: Non-existent image returns ErrAssetNotFound",
			filename: "missing_banner.png",
			wantErr:  storage.ErrAssetNotFound,
		},
		{
			name:     "Security Block: Empty filename rejected",
			filename: "",
			wantErr:  storage.ErrPathTraversal,
		},
		{
			name:     "Security Block: Non-image extension rejected",
			filename: "document.pdf",
			wantErr:  storage.ErrUnsupportedMIME,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			asset, err := store.LoadImage(tc.filename)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LoadImage(%q) error = %v, want %v", tc.filename, err, tc.wantErr)
			}
			if tc.wantErr == nil && asset.ContentType != "image/png" {
				t.Fatalf("expected content-type image/png, got %s", asset.ContentType)
			}
		})
	}
}

func TestLocalAssetStore_NewLocalAssetStore(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// 1. Existing directory succeeds
	store, err := storage.NewLocalAssetStore(tempDir)
	if err != nil {
		t.Fatalf("expected success for existing dir, got: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store")
	}

	// 2. Non-existent path is automatically created
	autoCreatedPath := filepath.Join(tempDir, "auto_created", "panels")
	storeAuto, err := storage.NewLocalAssetStore(autoCreatedPath)
	if err != nil {
		t.Fatalf("expected NewLocalAssetStore to auto-create directory: %v", err)
	}
	if storeAuto == nil {
		t.Fatal("expected non-nil storeAuto")
	}

	// 3. File path returns error
	filePath := filepath.Join(tempDir, "file.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = storage.NewLocalAssetStore(filePath)
	if err == nil {
		t.Fatal("expected error when path is a file, got nil")
	}
}

func TestLocalAssetStore_ListAvailableImages(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Add images
	_ = os.WriteFile(filepath.Join(tempDir, "banner.png"), validPNGBytes, 0600)
	_ = os.WriteFile(filepath.Join(tempDir, "photo.jpg"), []byte("test"), 0600)
	_ = os.WriteFile(filepath.Join(tempDir, "crest.webp"), []byte("test"), 0600)

	// Add non-images and directories
	_ = os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("text"), 0600)
	_ = os.WriteFile(filepath.Join(tempDir, ".hidden.png"), validPNGBytes, 0600)
	_ = os.Mkdir(filepath.Join(tempDir, "subdir.png"), 0755)

	store, err := storage.NewLocalAssetStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	files, err := store.ListAvailableImages()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	fileSet := make(map[string]bool)
	for _, f := range files {
		fileSet[f] = true
	}

	if !fileSet["banner.png"] || !fileSet["photo.jpg"] || !fileSet["crest.webp"] {
		t.Errorf("expected images missing from list: %v", files)
	}
	if fileSet["notes.txt"] || fileSet[".hidden.png"] || fileSet["subdir.png"] {
		t.Errorf("unexpected files included in list: %v", files)
	}
}

func TestLocalAssetStore_FileTooLarge(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create 8MB + 1 byte file with .png extension
	largeFile, err := os.Create(filepath.Join(tempDir, "large.png"))
	if err != nil {
		t.Fatal(err)
	}
	// Truncate creates a sparse file without consuming 8MB of disk
	if err := largeFile.Truncate(8*1024*1024 + 10); err != nil {
		_ = largeFile.Close()
		t.Fatal(err)
	}
	_ = largeFile.Close()

	store, err := storage.NewLocalAssetStore(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = store.LoadImage("large.png")
	if !errors.Is(err, storage.ErrFileTooLarge) {
		t.Fatalf("expected ErrFileTooLarge, got %v", err)
	}
}
