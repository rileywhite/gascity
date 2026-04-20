package molecule

import (
	"strings"

	"github.com/gastownhall/gascity/internal/beads"
)

// ListSubtree returns the root bead and all transitive parent-child
// descendants, including already-closed beads so nested open descendants are
// still reachable through a closed intermediate node.
func ListSubtree(store beads.Store, rootID string) ([]beads.Bead, error) {
	rootID = strings.TrimSpace(rootID)
	if store == nil || rootID == "" {
		return nil, nil
	}
	root, err := store.Get(rootID)
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{root.ID: {}}
	out := []beads.Bead{root}
	queue := []string{root.ID}
	for len(queue) > 0 {
		parentID := queue[0]
		queue = queue[1:]

		children, err := store.Children(parentID, beads.IncludeClosed)
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if child.ID == "" {
				continue
			}
			if _, ok := seen[child.ID]; ok {
				continue
			}
			seen[child.ID] = struct{}{}
			out = append(out, child)
			queue = append(queue, child.ID)
		}
	}
	return out, nil
}

// CloseSubtree closes the root bead and every open descendant. Descendants are
// closed before the root so stores with stricter parent/child close rules can
// still accept the operation.
func CloseSubtree(store beads.Store, rootID string) (int, error) {
	matched, err := ListSubtree(store, rootID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(matched))
	for i := len(matched) - 1; i >= 0; i-- {
		bead := matched[i]
		if bead.ID == "" || bead.Status == "closed" {
			continue
		}
		ids = append(ids, bead.ID)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	return store.CloseAll(ids, nil)
}
