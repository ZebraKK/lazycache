package lazycache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
	"unsafe"
)

// Cache is a generic lazy-loading cache with LRU eviction
type Cache[V any] struct {
	mu            sync.RWMutex // protects items, lru, currentSize
	loadersMu     sync.RWMutex // protects loaders (read-heavy, rarely written)
	items         map[string]*item[V]
	maxItems      int
	maxBytes      int64
	currentSize   int64
	ttl           time.Duration
	loaderTimeout time.Duration // 0 = no timeout
	lru           *lruList[V]
	loaders       map[string]Loader[V]
	sizeEstimator SizeEstimator[V]
	stats         Statistics
}

// isTransientError reports whether err represents a transient infrastructure
// failure (loader panic or context timeout) rather than a business logic error.
func isTransientError(err error) bool {
	return errors.Is(err, ErrLoaderPanic) || errors.Is(err, context.DeadlineExceeded)
}

// safeLoad calls loader.Load and converts any panic into an ErrLoaderPanic-wrapped error.
func safeLoad[V any](ctx context.Context, loader Loader[V], key string) (v V, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrLoaderPanic, r)
		}
	}()
	return loader.Load(ctx, key)
}

// callLoader wraps safeLoad with an optional timeout derived from the cache config.
func (c *Cache[V]) callLoader(ctx context.Context, loader Loader[V], key string) (V, error) {
	if c.loaderTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.loaderTimeout)
		defer cancel()
	}
	return safeLoad(ctx, loader, key)
}

// New creates a new Cache instance with a mandatory default loader.
// Additional loaders can be registered later via RegisterLoader.
func New[V any](name string, loader Loader[V], opts ...Option[V]) *Cache[V] {
	if loader == nil {
		panic("lazycache: loader must not be nil")
	}
	c := &Cache[V]{
		items:         make(map[string]*item[V]),
		maxItems:      10000,            // default max items
		maxBytes:      1 << 30,          // default 1GB
		ttl:           5 * time.Minute,  // default 5 minutes
		loaderTimeout: 30 * time.Second, // default 30 seconds
		lru:           newLRUList[V](),
		loaders:       make(map[string]Loader[V]),
		sizeEstimator: defaultSizeEstimator[V],
	}

	c.loaders[name] = loader

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// RegisterLoader registers a data loader with the given name
func (c *Cache[V]) RegisterLoader(name string, loader Loader[V]) {
	if loader == nil {
		panic("lazycache: loader must not be nil")
	}
	c.loadersMu.Lock()
	defer c.loadersMu.Unlock()
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

	// Copy all fields we may need while holding the lock to avoid races with syncLoad/asyncRefresh.
	c.mu.RLock()
	it, exists := c.items[key]
	var (
		itValue   V
		itIsNull  bool
		itExpired bool
	)
	if exists {
		itValue = it.value
		itIsNull = it.isNull
		itExpired = it.isExpired()
	}
	c.mu.RUnlock()

	// Case 1: Cache hit and not expired
	if exists && !itExpired {
		c.lru.MaybeTouch(it)
		c.stats.Hit()
		l.Debug("cache hit", "key", key)
		if itIsNull {
			return zero[V](), ErrNotFound
		}
		return itValue, nil
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
		l.Debug("async refresh", "key", key, "loader", options.loaderName)
		go c.asyncRefresh(context.Background(), key, loader, options.ttlOverride, l)
		if itIsNull {
			return zero[V](), ErrNotFound
		}
		return itValue, nil
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
		it = &item[V]{key: key}
		c.items[key] = it
	}

	it.value = value
	it.expireAt = time.Now().Add(ttl)
	it.size = size
	it.isNull = false
	it.loading = false

	c.currentSize += size
	c.lru.Touch(it)
	c.evictIfNeeded()
}

// Invalidate removes a key from the cache
func (c *Cache[V]) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if it, exists := c.items[key]; exists {
		c.currentSize -= it.size
		delete(c.items, key)
		c.lru.Remove(it)
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

/*
多个 goroutine 并发请求同一个 key（缓存未命中）
         │
         ▼
    c.mu.Lock()  ← 所有 goroutine 争抢锁
         │
    ┌────┴─────────────────────┐
    │                          │
 第 1 个 goroutine            第 2、3、N 个 goroutine
 抢到锁，发现 loading=false   抢到锁，发现 loading=true
    │                          │
 it.loading = true             ch := it.loadChan
 it.loadChan = make(chan struct{})
 c.mu.Unlock()                 c.mu.Unlock()
    │                          │
 执行 loader.Load(...)          select { case <-ch: ... }
    │                          │       ← 阻塞等待
 成功/失败                      │
    │                          │
 c.mu.Lock()                   │
 更新 it.value                 │
 it.loading = false            │
 close(it.loadChan) ──────────►│ channel 关闭，所有等待者同时解除阻塞
 c.mu.Unlock()                 │
                               │
                          重新读取 c.items[key]
                          返回已加载好的值
*/
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
		// vs sync.WaitGroup or sync.Cond:
		// - channel close is more efficient than broadcasting to all waiters via Cond
		// - avoids potential goroutine leaks if waiters time out or are canceled while waiting
		// - allows waiters to select on ctx.Done() for cancellation, which Cond does not support
		// - avoids the thundering herd problem on Cond.Broadcast if many waiters are present
		// - Note: this means that if the loader goroutine panics or gets stuck, all waiters will also be affected.
		// 		This is a trade-off for simplicity and performance.
		//   Alternative designs could involve more complex coordination
		// 	 (e.g. separate "loading" state with a sync.Cond or a wait group),
		//   but the channel approach is simple and efficient for the common case where loaders succeed quickly.
		select {
		case <-ch:
			c.mu.RLock()
			defer c.mu.RUnlock()
			loadedItem := c.items[key]
			if loadedItem.isNull {
				return zero[V](), ErrNotFound
			}
			return loadedItem.value, nil
		case <-ctx.Done(): // context 超时或取消
			return zero[V](), ctx.Err()
		}
	}

	// This goroutine is responsible for loading
	if !exists {
		it = &item[V]{key: key}
		c.items[key] = it
	}
	it.loading = true
	it.loadChan = make(chan struct{})
	c.mu.Unlock()

	// Release lock and perform expensive load operation
	value, err := c.callLoader(ctx, loader, key)

	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.ttl
	if ttlOverride != nil {
		ttl = *ttlOverride
	}
	//在 `Lock` 保护下先写值、再 `close`"来保证内存可见性和数据一致性
	if err != nil && isTransientError(err) {
		if exists && !it.isNull {
			// Stale real value available: extend TTL and return it silently.
			l.Warn("transient load error, using stale", "key", key, "error", err)
			it.expireAt = time.Now().Add(ttl)
			it.loading = false
			close(it.loadChan)
			c.lru.Touch(it)
			c.evictIfNeeded()
			return it.value, nil
		}
		// No usable stale value: null-cache with a short TTL so callers can retry quickly.
		l.Error("transient load error, no stale value", "key", key, "error", err)
		it.isNull = true
		shortTTL := ttl / 10
		if shortTTL > 30*time.Second {
			shortTTL = 30 * time.Second
		}
		it.expireAt = time.Now().Add(shortTTL)
		it.size = 0
		it.loading = false
		close(it.loadChan)
		c.lru.Touch(it)
		c.evictIfNeeded()
		return zero[V](), fmt.Errorf("%w: %v", ErrUpdateFailed, err)
	}

	if err != nil {
		// Non-transient error: null-cache to prevent cache penetration.
		l.Error("load error", "key", key, "error", err)
		it.isNull = true
		it.expireAt = time.Now().Add(ttl)
		it.size = 0
	} else {
		// Load succeeded.
		l.Debug("load ok", "key", key)
		it.value = value
		it.isNull = false
		it.expireAt = time.Now().Add(ttl)
		it.size = c.sizeEstimator(value)
		c.currentSize += it.size
	}

	it.loading = false
	close(it.loadChan)

	c.lru.Touch(it)
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
	value, err := c.callLoader(ctx, loader, key)

	c.mu.Lock()
	defer c.mu.Unlock()

	ttl := c.ttl
	if ttlOverride != nil {
		ttl = *ttlOverride
	}

	if err != nil {
		if isTransientError(err) {
			// Transient failure: stale value is always present in async path; extend to full TTL.
			l.Warn("transient refresh error, keeping stale", "key", key, "error", err)
			it.expireAt = time.Now().Add(ttl)
		} else {
			// Non-transient (business logic) error: extend by half TTL as before.
			l.Warn("refresh failed", "key", key, "error", err)
			it.expireAt = time.Now().Add(ttl / 2)
		}
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
		if victim == nil {
			break
		}

		c.currentSize -= victim.size
		delete(c.items, victim.key)
		c.stats.Evict()
	}
}

// pickLoader randomly selects a loader from the registered loaders.
// Must be called with c.loadersMu.RLock held.
func (c *Cache[V]) pickLoader() Loader[V] {
	for _, l := range c.loaders {
		return l
	}
	return nil
}

// getLoader retrieves a loader by name, or auto-selects one when name is empty.
func (c *Cache[V]) getLoader(name string) Loader[V] {
	c.loadersMu.RLock()
	defer c.loadersMu.RUnlock()

	if name == "" {
		return c.pickLoader()
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
