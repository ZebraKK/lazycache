package lazycache

import (
	"context"
	"reflect"
	"sync"
	"time"
	"unsafe"
)

// Cache is a generic lazy-loading cache with LRU eviction
type Cache[V any] struct {
	mu            sync.RWMutex
	items         map[string]*item[V]
	maxItems      int
	maxBytes      int64
	currentSize   int64
	ttl           time.Duration
	lru           *lruList
	loaders       map[string]Loader[V]
	sizeEstimator SizeEstimator[V]
	stats         Statistics
}

// New creates a new Cache instance
func New[V any](opts ...Option[V]) *Cache[V] {
	c := &Cache[V]{
		items:         make(map[string]*item[V]),
		maxItems:      10000,              // default max items
		maxBytes:      1 << 30,            // default 1GB
		ttl:           5 * time.Minute,    // default 5 minutes
		lru:           newLRUList(),
		loaders:       make(map[string]Loader[V]),
		sizeEstimator: defaultSizeEstimator[V],
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// RegisterLoader registers a data loader with the given name
func (c *Cache[V]) RegisterLoader(name string, loader Loader[V]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loaders[name] = loader
}

// Get retrieves a value from cache or loads it using the specified loader
func (c *Cache[V]) Get(ctx context.Context, key string, opts ...GetOption) (V, error) {
	options := &getOptions{
		mode: AsyncMode, // default to async (lazy loading)
	}
	for _, opt := range opts {
		opt(options)
	}

	l := fromContext(ctx)

	c.mu.RLock()
	it, exists := c.items[key]
	c.mu.RUnlock()

	// Case 1: Cache hit and not expired
	if exists && !it.isExpired() {
		c.lru.Touch(key)
		c.stats.Hit()
		l.Debug("cache hit", "key", key)
		if it.isNull {
			return zero[V](), ErrNotFound
		}
		return it.value, nil
	}

	l.Debug("cache miss", "key", key)
	c.stats.Miss()

	// Case 2: Need to load
	loader := c.getLoader(options.loaderName)
	if loader == nil {
		l.Error("no loader", "key", key)
		return zero[V](), ErrNoLoader
	}

	// Case 2a: Has stale value and async mode (lazy loading core)
	if exists && options.mode == AsyncMode {
		// Capture the current state before launching async refresh
		isNull := it.isNull
		value := it.value
		l.Debug("async refresh", "key", key, "loader", options.loaderName)
		go c.asyncRefresh(context.Background(), key, loader, options.ttlOverride, l)
		if isNull {
			return zero[V](), ErrNotFound
		}
		return value, nil
	}

	// Case 2b: Sync mode or no stale value
	return c.syncLoad(ctx, key, options.loaderName, loader, options.ttlOverride)
}

// Set manually sets a cache value
func (c *Cache[V]) Set(key string, value V, opts ...SetOption) {
	options := &setOptions{}
	for _, opt := range opts {
		opt(options)
	}

	ttl := c.ttl
	if options.ttl != nil {
		ttl = *options.ttl
	}

	size := c.sizeEstimator(value)
	if options.size != nil {
		size = *options.size
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	it, exists := c.items[key]
	if exists {
		c.currentSize -= it.size
	} else {
		it = &item[V]{}
		c.items[key] = it
	}

	it.value = value
	it.expireAt = time.Now().Add(ttl)
	it.size = size
	it.isNull = false
	it.loading = false

	c.currentSize += size
	c.lru.Touch(key)
	c.evictIfNeeded()
}

// Invalidate removes a key from the cache
func (c *Cache[V]) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, exists := c.items[key]; exists {
		c.currentSize -= it.size
		delete(c.items, key)
		c.lru.Remove(key)
	}
}

// UpdateConfig updates cache configuration at runtime
func (c *Cache[V]) UpdateConfig(opts ...Option[V]) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, opt := range opts {
		opt(c)
	}

	c.evictIfNeeded()
}

// Stats returns a snapshot of cache statistics
func (c *Cache[V]) Stats() Snapshot {
	return c.stats.GetSnapshot()
}

// Len returns the current number of items in the cache
func (c *Cache[V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Size returns the current total size of cached items in bytes
func (c *Cache[V]) Size() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentSize
}

// syncLoad loads a value synchronously with anti-stampede protection
func (c *Cache[V]) syncLoad(ctx context.Context, key string, loaderName string, loader Loader[V], ttlOverride *time.Duration) (V, error) {
	l := fromContext(ctx)
	l.Debug("sync load", "key", key, "loader", loaderName)

	c.mu.Lock()

	it, exists := c.items[key]

	// Anti-stampede: check if another goroutine is already loading
	if exists && it.loading {
		ch := it.loadChan
		c.mu.Unlock()

		// Wait for the other goroutine to finish loading
		select {
		case <-ch:
			c.mu.RLock()
			defer c.mu.RUnlock()
			loadedItem := c.items[key]
			if loadedItem.isNull {
				return zero[V](), ErrNotFound
			}
			return loadedItem.value, nil
		case <-ctx.Done():
			return zero[V](), ctx.Err()
		}
	}

	// This goroutine is responsible for loading
	if !exists {
		it = &item[V]{}
		c.items[key] = it
	}
	it.loading = true
	it.loadChan = make(chan struct{})
	c.mu.Unlock()

	// Release lock and perform expensive load operation
	value, err := loader.Load(ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.ttl
	if ttlOverride != nil {
		ttl = *ttlOverride
	}

	if err != nil {
		// Load failed: cache as null to prevent cache penetration
		l.Error("load error", "key", key, "error", err)
		it.isNull = true
		it.expireAt = time.Now().Add(ttl)
		it.size = 0
	} else {
		// Load succeeded
		l.Debug("load ok", "key", key)
		it.value = value
		it.isNull = false
		it.expireAt = time.Now().Add(ttl)
		it.size = c.sizeEstimator(value)
		c.currentSize += it.size
	}

	it.loading = false
	close(it.loadChan)

	c.lru.Touch(key)
	c.evictIfNeeded()

	if it.isNull {
		return zero[V](), err
	}
	return it.value, nil
}

// asyncRefresh refreshes a cache entry in the background
func (c *Cache[V]) asyncRefresh(ctx context.Context, key string, loader Loader[V], ttlOverride *time.Duration, l Logger) {
	c.mu.Lock()
	it, exists := c.items[key]
	if !exists || it.loading {
		c.mu.Unlock()
		return
	}

	it.loading = true
	oldSize := it.size
	c.mu.Unlock()

	// Perform load without holding the lock
	value, err := loader.Load(ctx, key)

	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.ttl
	if ttlOverride != nil {
		ttl = *ttlOverride
	}

	if err != nil {
		// Refresh failed: keep old value and extend expiration by 50%
		l.Warn("refresh failed", "key", key, "error", err)
		it.expireAt = time.Now().Add(ttl / 2)
		c.stats.RefreshFail()
	} else {
		// Refresh succeeded
		l.Debug("refresh ok", "key", key)
		it.value = value
		it.isNull = false
		it.expireAt = time.Now().Add(ttl)
		c.currentSize -= oldSize
		it.size = c.sizeEstimator(value)
		c.currentSize += it.size
		c.stats.RefreshSuccess()
	}

	it.loading = false
	c.evictIfNeeded()
}

// evictIfNeeded performs LRU eviction if limits are exceeded
func (c *Cache[V]) evictIfNeeded() {
	// Evict until both limits are satisfied
	for len(c.items) > c.maxItems || c.currentSize > c.maxBytes {
		victim := c.lru.RemoveLast()
		if victim == "" {
			break
		}

		if it, ok := c.items[victim]; ok {
			c.currentSize -= it.size
			delete(c.items, victim)
			c.stats.Evict()
		}
	}
}

// getLoader retrieves a loader by name
func (c *Cache[V]) getLoader(name string) Loader[V] {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if name == "" {
		return nil
	}

	loader, exists := c.loaders[name]
	if !exists {
		return nil
	}
	return loader
}

// zero returns the zero value of type V
func zero[V any]() V {
	var z V
	return z
}

// defaultSizeEstimator provides a basic size estimation using reflection
func defaultSizeEstimator[V any](v V) int64 {
	// For pointer types, try to estimate the size of the pointed-to value
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr && !rv.IsNil() {
		return int64(rv.Elem().Type().Size())
	}
	// For non-pointer types, use the size of the type itself
	return int64(unsafe.Sizeof(v))
}
