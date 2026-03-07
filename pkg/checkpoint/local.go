package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LocalStore is a filesystem-backed checkpoint store. Each manifest is stored
// as <dir>/<workspaceID>/<epoch>.json with a "latest" symlink pointing to the
// most recent epoch file.
type LocalStore struct {
	dir string
}

// NewLocalStore creates a LocalStore rooted at dir.
func NewLocalStore(dir string) *LocalStore {
	return &LocalStore{dir: dir}
}

// Save persists a recovery manifest to disk. Validates before writing.
func (s *LocalStore) Save(_ context.Context, m RecoveryManifest) error {
	if err := m.Validate(); err != nil {
		return err
	}

	wsDir := filepath.Join(s.dir, m.WorkspaceID)
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		return fmt.Errorf("creating checkpoint dir: %w", err)
	}

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling manifest: %w", err)
	}

	filename := fmt.Sprintf("%d.json", m.Epoch)
	filePath := filepath.Join(wsDir, filename)

	// Atomic write: unique temp file then rename.
	tmpFile, err := os.CreateTemp(wsDir, fmt.Sprintf("%d.json.tmp.*", m.Epoch))
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmp := tmpFile.Name()
	defer func() {
		// Clean up temp file if still present (rename succeeded = no-op).
		_ = os.Remove(tmp)
	}()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing checkpoint: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmp, filePath); err != nil {
		return fmt.Errorf("renaming checkpoint: %w", err)
	}

	// Update latest symlink only if this epoch >= the currently published epoch.
	// This prevents out-of-order writes from rolling back the latest pointer.
	latestPath := filepath.Join(wsDir, "latest")
	shouldUpdate := true
	if target, err := os.Readlink(latestPath); err == nil {
		currentEpochStr := strings.TrimSuffix(target, ".json")
		if currentEpoch, err := strconv.ParseInt(currentEpochStr, 10, 64); err == nil {
			shouldUpdate = m.Epoch >= currentEpoch
		}
	}

	if shouldUpdate {
		// Symlink update is best-effort: if symlinks are unsupported
		// (e.g., unprivileged Windows), Load falls back to scanning.
		tmpLink := fmt.Sprintf("%s.tmp.%d", latestPath, time.Now().UnixNano())
		if err := os.Symlink(filename, tmpLink); err == nil {
			if err := os.Rename(tmpLink, latestPath); err != nil {
				_ = os.Remove(tmpLink)
			}
		}
	}

	return nil
}

// Load returns the latest recovery manifest for a workspace by following
// the "latest" symlink. Falls back to scanning epoch files if the symlink
// is missing.
func (s *LocalStore) Load(_ context.Context, workspaceID string) (RecoveryManifest, error) {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return RecoveryManifest{}, err
	}
	wsDir := filepath.Join(s.dir, workspaceID)

	// Always scan epoch files and return the highest. This avoids TOCTOU
	// races on the "latest" symlink when concurrent saves are in flight.
	manifests, err := s.listManifests(wsDir)
	if err != nil {
		return RecoveryManifest{}, err
	}
	if len(manifests) == 0 {
		return RecoveryManifest{}, ErrNotFound
	}
	return manifests[len(manifests)-1], nil
}

// List returns all recovery manifests for a workspace ordered by epoch.
func (s *LocalStore) List(_ context.Context, workspaceID string) ([]RecoveryManifest, error) {
	if err := validateWorkspaceID(workspaceID); err != nil {
		return nil, err
	}
	wsDir := filepath.Join(s.dir, workspaceID)
	return s.listManifests(wsDir)
}

func (s *LocalStore) readManifest(path string) (RecoveryManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return RecoveryManifest{}, fmt.Errorf("reading manifest: %w", err)
	}
	var m RecoveryManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return RecoveryManifest{}, fmt.Errorf("parsing manifest: %w", err)
	}
	if err := m.Validate(); err != nil {
		return RecoveryManifest{}, fmt.Errorf("invalid manifest at %s: %w", filepath.Base(path), err)
	}
	return m, nil
}

func validateWorkspaceID(id string) error {
	if id == "" {
		return fmt.Errorf("checkpoint: empty workspace ID")
	}
	if strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return fmt.Errorf("checkpoint: invalid workspace ID %q", id)
	}
	if id == "latest" || id == "." {
		return fmt.Errorf("checkpoint: reserved workspace ID %q", id)
	}
	return nil
}

func (s *LocalStore) listManifests(wsDir string) ([]RecoveryManifest, error) {
	entries, err := os.ReadDir(wsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading checkpoint dir: %w", err)
	}

	var manifests []RecoveryManifest
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		epochStr := strings.TrimSuffix(name, ".json")
		if _, err := strconv.ParseInt(epochStr, 10, 64); err != nil {
			continue // skip non-epoch files
		}
		m, err := s.readManifest(filepath.Join(wsDir, name))
		if err != nil {
			return nil, fmt.Errorf("corrupt checkpoint %s: %w", name, err)
		}
		manifests = append(manifests, m)
	}

	sort.Slice(manifests, func(i, j int) bool {
		return manifests[i].Epoch < manifests[j].Epoch
	})
	return manifests, nil
}
