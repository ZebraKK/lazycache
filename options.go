package lazycache

import "time"

// SizeEstimator is a function that estimates the size of a value in bytes
type SizeEstimator[V any] func(V) int64

// Option is a function that configures a Cache
type Option[V any] func(*Cache[V])

// WithMaxItems sets the maximum number of items in the cache
func WithMaxItems[V any](n int) Option[V] {
	return func(c *Cache[V]) {
		c.maxItems = n
	}
}

// WithMaxBytes sets the maximum total size of cached items in bytes
func WithMaxBytes[V any](bytes int64) Option[V] {
	return func(c *Cache[V]) {
		c.maxBytes = bytes
	}
}

// WithTTL sets the default time-to-live for cache items
func WithTTL[V any](d time.Duration) Option[V] {
	return func(c *Cache[V]) {
		c.ttl = d
	}
}

// WithSizeEstimator sets a custom size estimator function
func WithSizeEstimator[V any](fn SizeEstimator[V]) Option[V] {
	return func(c *Cache[V]) {
		c.sizeEstimator = fn
	}
}

// WithLoaderTimeout sets a timeout for loader calls. Zero means no timeout.
func WithLoaderTimeout[V any](d time.Duration) Option[V] {
	return func(c *Cache[V]) {
		c.loaderTimeout = d
	}
}

// LoadMode determines how cache loading behaves
type LoadMode int

const (
	// AsyncMode returns stale value immediately and refreshes in background (default)
	AsyncMode LoadMode = iota
	// SyncMode waits for refresh to complete before returning
	SyncMode
)

// getOptions holds options for Get operations
type getOptions struct {
	loaderName string
	mode       LoadMode
	ttlOverride *time.Duration
}

// GetOption is a function that configures a Get operation
type GetOption func(*getOptions)

// WithLoader specifies which loader to use for fetching data
func WithLoader(name string) GetOption {
	return func(o *getOptions) {
		o.loaderName = name
	}
}

// WithSync enables synchronous mode (wait for load to complete)
func WithSync() GetOption {
	return func(o *getOptions) {
		o.mode = SyncMode
	}
}

// WithAsync enables async mode (return stale value, refresh in background)
func WithAsync() GetOption {
	return func(o *getOptions) {
		o.mode = AsyncMode
	}
}

// WithTTLOverride overrides the default TTL for this specific Get
func WithTTLOverride(d time.Duration) GetOption {
	return func(o *getOptions) {
		o.ttlOverride = &d
	}
}

// setOptions holds options for Set operations
type setOptions struct {
	ttl  *time.Duration
	size *int64
}

// SetOption is a function that configures a Set operation
type SetOption func(*setOptions)

// WithSetTTL overrides the default TTL for this Set operation
func WithSetTTL(d time.Duration) SetOption {
	return func(o *setOptions) {
		o.ttl = &d
	}
}

// WithSetSize manually specifies the size of the value being set
func WithSetSize(size int64) SetOption {
	return func(o *setOptions) {
		o.size = &size
	}
}
