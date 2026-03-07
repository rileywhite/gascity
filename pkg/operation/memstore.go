package operation

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// MemStore is an in-memory Store implementation for testing and development.
type MemStore struct {
	mu  sync.Mutex
	ops map[string]Operation
}

// NewMemStore creates a new in-memory operation store.
func NewMemStore() *MemStore {
	return &MemStore{ops: make(map[string]Operation)}
}

// Create stores a new operation.
func (s *MemStore) Create(_ context.Context, op Operation) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if op.ID == "" {
		return Operation{}, fmt.Errorf("operation: id is required")
	}
	if _, exists := s.ops[op.ID]; exists {
		return Operation{}, fmt.Errorf("operation %q already exists", op.ID)
	}
	s.ops[op.ID] = cloneOp(op)
	return cloneOp(op), nil
}

// Get retrieves an operation by ID.
func (s *MemStore) Get(_ context.Context, id string) (Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	op, ok := s.ops[id]
	if !ok {
		return Operation{}, ErrNotFound
	}
	return cloneOp(op), nil
}

// Update replaces an existing operation.
func (s *MemStore) Update(_ context.Context, op Operation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ops[op.ID]; !ok {
		return ErrNotFound
	}
	s.ops[op.ID] = cloneOp(op)
	return nil
}

// List returns operations matching the filter.
func (s *MemStore) List(_ context.Context, filter Filter) ([]Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []Operation
	for _, op := range s.ops {
		if filter.ResourceID != "" && op.ResourceID != filter.ResourceID {
			continue
		}
		if filter.Kind != "" && op.Kind != filter.Kind {
			continue
		}
		if filter.Phase != "" && op.Phase != filter.Phase {
			continue
		}
		result = append(result, cloneOp(op))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func cloneOp(op Operation) Operation {
	if op.Metadata != nil {
		cp := make(map[string]string, len(op.Metadata))
		for k, v := range op.Metadata {
			cp[k] = v
		}
		op.Metadata = cp
	}
	return op
}
