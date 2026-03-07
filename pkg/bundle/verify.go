package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CanAccept reports whether a runtime can load a bundle with the given
// schema version. Accepts current (N) and previous (N-1) versions,
// but always requires version >= 1.
func CanAccept(schemaVersion int) bool {
	return schemaVersion >= 1 && (schemaVersion == SchemaVersion || schemaVersion == SchemaVersion-1)
}

// VerifyChecksum verifies that the contents of bundleDir match the expected
// checksum. The checksum format is "sha256:<hex>". Files are hashed in
// sorted order for determinism. The manifest file itself is excluded.
// Only regular files are included — symlinks and special files are rejected.
func VerifyChecksum(bundleDir string, expected string) error {
	parts := strings.SplitN(expected, ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return fmt.Errorf("unsupported checksum format: %q", expected)
	}
	wantHex := parts[1]

	h := sha256.New()
	var files []string
	err := filepath.WalkDir(bundleDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("bundle contains non-regular file: %s", path)
		}
		rel, err := filepath.Rel(bundleDir, path)
		if err != nil {
			return err
		}
		// Skip the manifest file itself.
		if rel == "manifest.json" {
			return nil
		}
		// Normalize to forward slashes immediately for cross-platform sort order.
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return fmt.Errorf("walking bundle dir: %w", err)
	}

	sort.Strings(files)
	for _, rel := range files {
		// Use length-prefixed framing to prevent ambiguous concatenation:
		// <path-length>:<path><content-bytes>
		// This ensures different file trees cannot produce identical byte streams.
		if _, err := fmt.Fprintf(h, "%d:%s", len(rel), rel); err != nil {
			return fmt.Errorf("hashing path %s: %w", rel, err)
		}
		f, err := os.Open(filepath.Join(bundleDir, filepath.FromSlash(rel)))
		if err != nil {
			return fmt.Errorf("opening %s: %w", rel, err)
		}
		_, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if copyErr != nil {
			return fmt.Errorf("reading %s: %w", rel, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("closing %s: %w", rel, closeErr)
		}
	}

	gotHex := hex.EncodeToString(h.Sum(nil))
	if gotHex != wantHex {
		return fmt.Errorf("checksum mismatch: got sha256:%s, want sha256:%s", gotHex, wantHex)
	}
	return nil
}
