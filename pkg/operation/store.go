package operation

import (
	"context"
	"errors"
)

// ErrNotFound is returned when an operation does not exist.
var ErrNotFound = errors.New("operation: not found")

// Filter constrains which operations are returned by List.
type Filter struct {
	ResourceID string
	Kind       string
	Phase      Phase
	Limit      int
}

// Store persists and retrieves operations.
type Store interface {
	// Create persists a new operation. The ID must be set by the caller.
	Create(ctx context.Context, op Operation) (Operation, error)

	// Get returns a single operation by ID. Returns ErrNotFound if missing.
	Get(ctx context.Context, id string) (Operation, error)

	// Update replaces an existing operation. Returns ErrNotFound if missing.
	Update(ctx context.Context, op Operation) error

	// List returns operations matching the filter, ordered by CreatedAt then ID.
	// An empty filter returns all.
	List(ctx context.Context, filter Filter) ([]Operation, error)
}
