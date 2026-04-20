package orders

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

// StoreState captures the bead-store accessors needed to resolve order history
// and run-state across city and rig scopes.
type StoreState interface {
	CityBeadStore() beads.Store
	BeadStore(rig string) beads.Store
}

// StoresForState returns the primary store for an order plus the city store
// fallback when available. Rig-scoped orders read rig first so the current
// store wins over any legacy city fallback.
func StoresForState(state StoreState, a Order) ([]beads.Store, error) {
	stores := make([]beads.Store, 0, 2)
	if strings.TrimSpace(a.Rig) == "" {
		store := state.CityBeadStore()
		if store == nil {
			return nil, fmt.Errorf("city bead store is unavailable")
		}
		return []beads.Store{store}, nil
	}

	rigStore := state.BeadStore(a.Rig)
	if rigStore == nil {
		return nil, fmt.Errorf("rig %q bead store is unavailable", a.Rig)
	}
	stores = append(stores, rigStore)

	if cityStore := state.CityBeadStore(); cityStore != nil {
		stores = append(stores, cityStore)
	}
	return stores, nil
}

// LastRunFuncForStore returns the latest order-run bead time for one store.
func LastRunFuncForStore(store beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		if store == nil {
			return time.Time{}, nil
		}
		label := "order-run:" + name
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         1,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
		})
		if err != nil {
			return time.Time{}, err
		}
		if len(results) == 0 {
			return time.Time{}, nil
		}
		return results[0].CreatedAt, nil
	}
}

// LastRunAcrossStores returns the most recent run time across a set of stores
// for a single order name.
func LastRunAcrossStores(stores ...beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		var latest time.Time
		for _, store := range stores {
			if store == nil {
				continue
			}
			last, err := LastRunFuncForStore(store)(name)
			if err != nil {
				return time.Time{}, err
			}
			if last.After(latest) {
				latest = last
			}
		}
		return latest, nil
	}
}

// CursorFuncForStore returns the max order-run seq for one store.
func CursorFuncForStore(store beads.Store) CursorFunc {
	return func(name string) uint64 {
		label := "order-run:" + name
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         10,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
		})
		if err != nil || len(results) == 0 {
			return 0
		}
		labelSets := make([][]string, 0, len(results))
		for _, b := range results {
			labelSets = append(labelSets, b.Labels)
		}
		return MaxSeqFromLabels(labelSets)
	}
}

// CursorAcrossStores merges seq cursors from multiple stores.
func CursorAcrossStores(stores ...beads.Store) CursorFunc {
	fns := make([]CursorFunc, 0, len(stores))
	for _, store := range stores {
		if store != nil {
			fns = append(fns, CursorFuncForStore(store))
		}
	}
	return func(name string) uint64 {
		var latest uint64
		for _, fn := range fns {
			if seq := fn(name); seq > latest {
				latest = seq
			}
		}
		return latest
	}
}

// HistoryBeadsAcrossStores merges order history rows from a primary store and
// its fallback stores while preserving recency ordering.
func HistoryBeadsAcrossStores(stores []beads.Store, scopedName string) ([]beads.Bead, error) {
	label := "order-run:" + scopedName
	seen := make(map[string]bool)
	results := make([]beads.Bead, 0)

	for i, store := range stores {
		if store == nil {
			continue
		}
		rows, err := store.List(beads.ListQuery{
			Label:         label,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
		})
		if err != nil {
			if i == 0 {
				return nil, err
			}
			continue
		}
		for _, row := range rows {
			if seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			results = append(results, row)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return results[i].CreatedAt.After(results[j].CreatedAt)
	})
	return results, nil
}

// HistoryBeadAcrossStores finds a bead by ID across a primary store and its
// fallbacks.
func HistoryBeadAcrossStores(stores []beads.Store, beadID string) (beads.Bead, error) {
	var lastErr error
	for _, store := range stores {
		if store == nil {
			continue
		}
		bead, err := store.Get(beadID)
		if err == nil {
			return bead, nil
		}
		if errors.Is(err, beads.ErrNotFound) {
			continue
		}
		lastErr = err
	}
	if lastErr != nil {
		return beads.Bead{}, lastErr
	}
	return beads.Bead{}, beads.ErrNotFound
}
