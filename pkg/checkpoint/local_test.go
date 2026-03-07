package checkpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/pkg/checkpoint"
	"github.com/gastownhall/gascity/pkg/checkpoint/checkpointtest"
)

func TestLocalStoreConformance(t *testing.T) {
	checkpointtest.RunStoreTests(t, func() checkpoint.Store {
		return checkpoint.NewLocalStore(t.TempDir())
	})
}

func TestLocalStoreDeletedEpochReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	s := checkpoint.NewLocalStore(dir)
	ctx := context.Background()

	m := checkpoint.RecoveryManifest{
		ManifestVersion: 1, WorkspaceID: "ws-1", Epoch: 1,
		SnapshotID: "snap-1", CreatedAt: time.Now().Truncate(time.Second),
	}
	if err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}

	// Remove the epoch file — Load scans the directory and finds nothing.
	if err := os.Remove(filepath.Join(dir, "ws-1", "1.json")); err != nil {
		t.Fatal(err)
	}

	_, err := s.Load(ctx, "ws-1")
	if err == nil {
		t.Fatal("Load with deleted epoch should return error")
	}
}

func TestLocalStorePathTraversalOnRead(t *testing.T) {
	dir := t.TempDir()
	s := checkpoint.NewLocalStore(dir)
	ctx := context.Background()

	// Load and List should reject workspace IDs with path traversal.
	for _, badID := range []string{"../escape", "foo/../../etc", "latest", "."} {
		_, err := s.Load(ctx, badID)
		if err == nil {
			t.Errorf("Load(%q) should return error", badID)
		}
		_, err = s.List(ctx, badID)
		if err == nil {
			t.Errorf("List(%q) should return error", badID)
		}
	}
}
