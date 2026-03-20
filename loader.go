package lazycache

import "context"

// Loader defines the interface for loading data from external sources
type Loader[V any] interface {
	Load(ctx context.Context, key string) (V, error)
}

// LoaderFunc is a function type that implements the Loader interface
type LoaderFunc[V any] func(ctx context.Context, key string) (V, error)

// Load implements the Loader interface
func (f LoaderFunc[V]) Load(ctx context.Context, key string) (V, error) {
	return f(ctx, key)
}
