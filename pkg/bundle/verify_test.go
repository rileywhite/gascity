package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestCanAccept(t *testing.T) {
	cases := []struct {
		version int
		want    bool
	}{
		{SchemaVersion, true},
		{SchemaVersion + 1, false},
		{SchemaVersion - 2, false},
		{0, false}, // version 0 is always invalid
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("v%d", tc.version), func(t *testing.T) {
			if got := CanAccept(tc.version); got != tc.want {
				t.Errorf("CanAccept(%d) = %v, want %v", tc.version, got, tc.want)
			}
		})
	}
}

func TestVerifyChecksumSuccess(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "hello")
	writeFile(t, dir, "template.md", "world")

	checksum := computeChecksum(t, dir)
	if err := VerifyChecksum(dir, checksum); err != nil {
		t.Errorf("valid checksum should not error: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "hello")

	err := VerifyChecksum(dir, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("mismatched checksum should return error")
	}
}

func TestVerifyChecksumExcludesManifest(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "hello")
	checksum := computeChecksum(t, dir)

	// Adding manifest.json should not change the checksum.
	writeFile(t, dir, "manifest.json", `{"version":1}`)
	if err := VerifyChecksum(dir, checksum); err != nil {
		t.Errorf("manifest.json should be excluded: %v", err)
	}
}

func TestVerifyChecksumBadFormat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.toml", "hello")
	if err := VerifyChecksum(dir, "md5:abc"); err == nil {
		t.Error("unsupported format should return error")
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// computeChecksum mirrors the VerifyChecksum algorithm to produce the expected value.
func computeChecksum(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "manifest.json" {
			return nil
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(files)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(h, "%d:%s%d:", len(rel), rel, len(data))
		h.Write(data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
