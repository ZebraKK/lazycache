package lazycache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noopLoader is a loader that always returns ErrNotFound.
// Used in tests where we only rely on pre-Set values and want cache misses to return errors.
func noopLoader[V any]() Loader[V] {
	return LoaderFunc[V](func(_ context.Context, _ string) (V, error) {
		var z V
		return z, ErrNotFound
	})
}

// Test basic Get/Set operations
func TestBasicGetSet(t *testing.T) {
	cache := New[string]("noop", noopLoader[string]())

	// Set a value
	cache.Set("key1", "value1")

	// Get should return the value
	val, err := cache.Get(context.Background(), "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "value1" {
		t.Fatalf("expected 'value1', got '%s'", val)
	}

	// Stats should show 1 hit
	stats := cache.Stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
}

// Test cache miss with loader
func TestCacheMissWithLoader(t *testing.T) {
	loadCount := 0
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		loadCount++
		return "loaded_" + key, nil
	}))

	// First get should trigger load
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "loaded_key1" {
		t.Fatalf("expected 'loaded_key1', got '%s'", val)
	}
	if loadCount != 1 {
		t.Fatalf("expected load to be called once, got %d", loadCount)
	}

	// Second get should hit cache
	val, err = cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "loaded_key1" {
		t.Fatalf("expected 'loaded_key1', got '%s'", val)
	}
	if loadCount != 1 {
		t.Fatalf("expected load to still be called once, got %d", loadCount)
	}

	stats := cache.Stats()
	if stats.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", stats.Misses)
	}
}

// Test lazy loading (async mode)
func TestLazyLoading(t *testing.T) {
	loadCount := atomic.Int32{}
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		loadCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return "loaded_v" + key[len(key)-1:], nil
	}), WithTTL[string](100*time.Millisecond))

	// Initial load (sync)
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "loaded_v1" {
		t.Fatalf("expected 'loaded_v1', got '%s'", val)
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Get expired value (async mode) - should return stale value immediately
	start := time.Now()
	val, err = cache.Get(context.Background(), "key1", WithLoader("test"))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "loaded_v1" {
		t.Fatalf("expected stale 'loaded_v1', got '%s'", val)
	}
	// Should return immediately (async mode)
	if elapsed > 30*time.Millisecond {
		t.Fatalf("async get took too long: %v", elapsed)
	}

	// Wait for background refresh to complete
	time.Sleep(100 * time.Millisecond)

	// Verify the value was refreshed
	if loadCount.Load() != 2 {
		t.Fatalf("expected 2 loads (initial + refresh), got %d", loadCount.Load())
	}
}

// Test concurrent access with anti-stampede
func TestAntiStampede(t *testing.T) {
	loadCount := atomic.Int32{}
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		loadCount.Add(1)
		time.Sleep(100 * time.Millisecond)
		return "loaded", nil
	}))

	// Launch 10 concurrent requests for the same key
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()

	// Load should only be called once (anti-stampede)
	if loadCount.Load() != 1 {
		t.Fatalf("expected load to be called once, got %d", loadCount.Load())
	}
}

// Test null value caching (anti-penetration)
func TestNullValueCaching(t *testing.T) {
	loadCount := 0
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		loadCount++
		return "", errors.New("not found")
	}))

	// First attempt should trigger load
	_, err := cache.Get(context.Background(), "missing", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if loadCount != 1 {
		t.Fatalf("expected 1 load, got %d", loadCount)
	}

	// Second attempt should use cached null value
	_, err = cache.Get(context.Background(), "missing", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if loadCount != 1 {
		t.Fatalf("expected load count to stay at 1, got %d", loadCount)
	}
}

// Test LRU eviction by item count
func TestLRUEvictionByCount(t *testing.T) {
	cache := New[string]("noop", noopLoader[string](), WithMaxItems[string](3))

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// Access key1 to make it more recently used
	cache.Get(context.Background(), "key1")

	// Add key4, should evict key2 (least recently used)
	cache.Set("key4", "value4")

	// key2 should be evicted; use a non-registered loader to avoid null-caching side effects
	val, err := cache.Get(context.Background(), "key2", WithLoader("_absent_"))
	if err == nil || val != "" {
		t.Fatal("key2 should have been evicted")
	}

	// key1 should still exist (cache hit, loader not invoked)
	val, err = cache.Get(context.Background(), "key1", WithLoader("_absent_"))
	if err != nil || val != "value1" {
		t.Fatal("key1 should still be in cache")
	}

	stats := cache.Stats()
	if stats.Evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", stats.Evictions)
	}
}

// Test LRU eviction by byte size
func TestLRUEvictionBySize(t *testing.T) {
	// Custom size estimator: each value is 100 bytes
	sizeEstimator := func(v string) int64 {
		return 100
	}

	cache := New[string](
		"noop", noopLoader[string](),
		WithMaxItems[string](10),
		WithMaxBytes[string](250), // Can fit 2.5 items
		WithSizeEstimator[string](sizeEstimator),
	)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3") // Should trigger eviction

	// Should have evicted oldest item
	if cache.Len() > 2 {
		t.Fatalf("expected at most 2 items, got %d", cache.Len())
	}

	stats := cache.Stats()
	if stats.Evictions == 0 {
		t.Fatal("expected at least one eviction")
	}
}

// Test invalidation
func TestInvalidate(t *testing.T) {
	cache := New[string]("noop", noopLoader[string]())
	cache.Set("key1", "value1")

	// Verify key exists
	val, _ := cache.Get(context.Background(), "key1")
	if val != "value1" {
		t.Fatal("key1 should exist")
	}

	// Invalidate
	cache.Invalidate("key1")

	// Key should no longer exist
	val, err := cache.Get(context.Background(), "key1")
	if err == nil || val != "" {
		t.Fatal("key1 should be invalidated")
	}
}

// Test hot config update
func TestConfigUpdate(t *testing.T) {
	cache := New[string]("noop", noopLoader[string](), WithMaxItems[string](10))

	// Add some items
	for i := 0; i < 5; i++ {
		cache.Set(string(rune('a'+i)), "value")
	}

	if cache.Len() != 5 {
		t.Fatalf("expected 5 items, got %d", cache.Len())
	}

	// Update config to smaller size
	cache.UpdateConfig(WithMaxItems[string](3))

	// Should have evicted 2 items
	if cache.Len() != 3 {
		t.Fatalf("expected 3 items after config update, got %d", cache.Len())
	}
}

// Test TTL override
func TestTTLOverride(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "loaded", nil
	}), WithTTL[string](1*time.Second))

	// Load with custom TTL
	_, err := cache.Get(context.Background(), "key1",
		WithLoader("test"),
		WithSync(),
		WithTTLOverride(50*time.Millisecond))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Wait for custom TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	cache.mu.RLock()
	it := cache.items["key1"]
	expired := it.isExpired()
	cache.mu.RUnlock()

	if !expired {
		t.Fatal("item should have expired with custom TTL")
	}
}

// Test background refresh failure handling
func TestRefreshFailure(t *testing.T) {
	attemptCount := atomic.Int32{}
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		count := attemptCount.Add(1)
		if count == 1 {
			return "initial", nil
		}
		return "", errors.New("refresh failed")
	}), WithTTL[string](100*time.Millisecond))

	// Initial load
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "initial" {
		t.Fatalf("expected 'initial', got '%s'", val)
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Async refresh should fail but keep old value
	val, err = cache.Get(context.Background(), "key1", WithLoader("test"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "initial" {
		t.Fatalf("expected stale 'initial' value, got '%s'", val)
	}

	// Wait for background refresh
	time.Sleep(100 * time.Millisecond)

	stats := cache.Stats()
	if stats.RefreshFail == 0 {
		t.Fatal("expected at least one refresh failure")
	}
}

// Test multiple loaders
func TestMultipleLoaders(t *testing.T) {
	cache := New[string]("db", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "from_db", nil
	}))

	cache.RegisterLoader("api", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "from_api", nil
	}))

	// Load from db
	val, err := cache.Get(context.Background(), "key1", WithLoader("db"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "from_db" {
		t.Fatalf("expected 'from_db', got '%s'", val)
	}

	// Switch to api loader for same key (invalidate first)
	cache.Invalidate("key1")
	val, err = cache.Get(context.Background(), "key1", WithLoader("api"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "from_api" {
		t.Fatalf("expected 'from_api', got '%s'", val)
	}
}

// Test context cancellation
func TestContextCancellation(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			return "loaded", nil
		}
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := cache.Get(ctx, "key1", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
}

// Test race conditions with -race flag
func TestConcurrentAccess(t *testing.T) {
	cache := New[int]("test", LoaderFunc[int](func(ctx context.Context, key string) (int, error) {
		return 42, nil
	}))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)

		// Concurrent reads
		go func(id int) {
			defer wg.Done()
			cache.Get(context.Background(), "key", WithLoader("test"), WithSync())
		}(i)

		// Concurrent writes
		go func(id int) {
			defer wg.Done()
			cache.Set("key"+string(rune(id%10)), id)
		}(i)

		// Concurrent invalidations
		go func(id int) {
			defer wg.Done()
			cache.Invalidate("key" + string(rune(id%10)))
		}(i)
	}

	wg.Wait()
}

// TestAutoSelectLoader verifies that Get without WithLoader auto-selects a registered loader.
func TestAutoSelectLoader(t *testing.T) {
	cache := New[string]("loader1", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "from_loader1", nil
	}))
	cache.RegisterLoader("loader2", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "from_loader2", nil
	}))

	val, err := cache.Get(context.Background(), "key1", WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "from_loader1" && val != "from_loader2" {
		t.Fatalf("expected value from one of the loaders, got %q", val)
	}
}

// TestNewRequiresLoader verifies that a cache created with New works correctly via the default loader.
func TestNewRequiresLoader(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "loaded_" + key, nil
	}))

	val, err := cache.Get(context.Background(), "key1", WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != "loaded_key1" {
		t.Fatalf("expected 'loaded_key1', got '%s'", val)
	}
}

// mockLogger records log calls for assertions.
type mockLogger struct {
	mu     sync.Mutex
	debug  []string
	warns  []string
	errors []string
}

func (m *mockLogger) Debug(msg string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.debug = append(m.debug, msg)
}

func (m *mockLogger) Warn(msg string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warns = append(m.warns, msg)
}

func (m *mockLogger) Error(msg string, args ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors = append(m.errors, msg)
}

func (m *mockLogger) hasDebug(msg string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.debug {
		if s == msg {
			return true
		}
	}
	return false
}

func (m *mockLogger) hasWarn(msg string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.warns {
		if s == msg {
			return true
		}
	}
	return false
}

func (m *mockLogger) hasError(msg string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.errors {
		if s == msg {
			return true
		}
	}
	return false
}

// TestLoggerCacheHit verifies a Debug "cache hit" is emitted on a cache hit.
func TestLoggerCacheHit(t *testing.T) {
	cache := New[string]("noop", noopLoader[string]())
	cache.Set("key1", "value1")

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	_, err := cache.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ml.hasDebug("cache hit") {
		t.Fatal("expected 'cache hit' debug log")
	}
}

// TestLoggerCacheMiss verifies a Debug "cache miss" is emitted on a cache miss.
func TestLoggerCacheMiss(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "loaded", nil
	}))

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	_, err := cache.Get(ctx, "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ml.hasDebug("cache miss") {
		t.Fatal("expected 'cache miss' debug log")
	}
	if !ml.hasDebug("sync load") {
		t.Fatal("expected 'sync load' debug log")
	}
	if !ml.hasDebug("load ok") {
		t.Fatal("expected 'load ok' debug log")
	}
}

// TestLoggerNoLoader verifies an Error "no loader" is emitted when a named loader is missing.
func TestLoggerNoLoader(t *testing.T) {
	cache := New[string]("noop", noopLoader[string]())

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	_, err := cache.Get(ctx, "key1", WithLoader("missing"))
	if err == nil {
		t.Fatal("expected ErrNoLoader")
	}
	if !ml.hasError("no loader") {
		t.Fatal("expected 'no loader' error log")
	}
}

// TestLoggerLoadError verifies an Error "load error" is emitted when loader fails.
func TestLoggerLoadError(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "", errors.New("db down")
	}))

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	_, _ = cache.Get(ctx, "key1", WithLoader("test"), WithSync())
	if !ml.hasError("load error") {
		t.Fatal("expected 'load error' error log")
	}
}

// TestLoggerAsyncRefresh verifies Debug "async refresh" and "refresh ok" on background refresh.
func TestLoggerAsyncRefresh(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "refreshed", nil
	}), WithTTL[string](50*time.Millisecond))

	// Prime the cache synchronously.
	cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())

	// Wait for TTL to expire.
	time.Sleep(80 * time.Millisecond)

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	// Async get on expired key.
	cache.Get(ctx, "key1", WithLoader("test"))

	if !ml.hasDebug("async refresh") {
		t.Fatal("expected 'async refresh' debug log")
	}

	// Wait for background goroutine to finish.
	time.Sleep(50 * time.Millisecond)

	if !ml.hasDebug("refresh ok") {
		t.Fatal("expected 'refresh ok' debug log")
	}
}

// TestLoggerRefreshFailed verifies Warn "refresh failed" when background refresh errors.
func TestLoggerRefreshFailed(t *testing.T) {
	attempt := atomic.Int32{}
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		return "", errors.New("refresh error")
	}), WithTTL[string](50*time.Millisecond))

	// Prime the cache.
	cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())

	time.Sleep(80 * time.Millisecond)

	ml := &mockLogger{}
	ctx := NewContext(context.Background(), ml)

	cache.Get(ctx, "key1", WithLoader("test"))

	// Wait for background goroutine.
	time.Sleep(50 * time.Millisecond)

	if !ml.hasWarn("refresh failed") {
		t.Fatal("expected 'refresh failed' warn log")
	}
}

// TestLoggerNoop verifies no-op logger (default) causes no panic and no overhead path.
func TestLoggerNoop(t *testing.T) {
	cache := New[string]("noop", noopLoader[string]())
	cache.Set("key1", "value1")

	// No logger in context → noopLogger, must not panic.
	_, err := cache.Get(context.Background(), "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestLoaderPanicSync verifies that a loader panic in syncLoad is recovered, returns
// ErrUpdateFailed (no stale value), and does not leave waiting goroutines permanently blocked.
func TestLoaderPanicSync(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		panic("boom")
	}))

	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error from panicking loader")
	}
	if !errors.Is(err, ErrUpdateFailed) {
		t.Fatalf("expected ErrUpdateFailed, got: %v", err)
	}

	// A second Get must not hang — loadChan was closed despite the panic.
	done := make(chan struct{})
	go func() {
		cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("second Get after loader panic hung indefinitely")
	}
}

// TestLoaderPanicAsync verifies that a loader panic during asyncRefresh is recovered,
// increments RefreshFail, and clears it.loading so the key can refresh again.
func TestLoaderPanicAsync(t *testing.T) {
	attempt := atomic.Int32{}
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		n := attempt.Add(1)
		if n == 1 {
			return "initial", nil
		}
		panic("async boom")
	}), WithTTL[string](50*time.Millisecond))

	// Prime the cache.
	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("unexpected error on initial load: %v", err)
	}

	// Wait for TTL to expire.
	time.Sleep(80 * time.Millisecond)

	// Async get — triggers background refresh that will panic.
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"))
	if err != nil {
		t.Fatalf("unexpected error on async get: %v", err)
	}
	if val != "initial" {
		t.Fatalf("expected stale 'initial', got %q", val)
	}

	// Wait for background goroutine to finish.
	time.Sleep(100 * time.Millisecond)

	stats := cache.Stats()
	if stats.RefreshFail == 0 {
		t.Fatal("expected RefreshFail to be incremented after async loader panic")
	}

	// it.loading must be cleared — a new Get must not hang.
	cache.mu.RLock()
	it := cache.items["key1"]
	loading := it.loading
	cache.mu.RUnlock()
	if loading {
		t.Fatal("it.loading should be false after async loader panic")
	}
}

// TestLoaderTimeout verifies that WithLoaderTimeout causes Get to return
// ErrUpdateFailed (wrapping the timeout) when the loader exceeds the configured timeout
// and no stale value exists.
func TestLoaderTimeout(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return "loaded", nil
		}
	}), WithLoaderTimeout[string](50*time.Millisecond))

	start := time.Now()
	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, ErrUpdateFailed) {
		t.Fatalf("expected ErrUpdateFailed, got: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("Get took too long (%v), expected ~50ms", elapsed)
	}
}

// TestNilLoaderPanic verifies that passing a nil loader to New or RegisterLoader panics.
func TestNilLoaderPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from New with nil loader")
		}
	}()
	New[string]("test", nil)
}

func TestNilRegisterLoaderPanic(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "ok", nil
	}))
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic from RegisterLoader with nil loader")
		}
	}()
	cache.RegisterLoader("other", nil)
}

// TestSyncLoadPanicWithStale verifies that when a stale value exists and the loader
// panics, the stale value is returned with nil error and its TTL is extended.
func TestSyncLoadPanicWithStale(t *testing.T) {
	attempt := atomic.Int32{}
	ttl := 100 * time.Millisecond
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		panic("boom")
	}), WithTTL[string](ttl))

	// Prime the cache.
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil || val != "initial" {
		t.Fatalf("unexpected: val=%q err=%v", val, err)
	}

	// Wait for expiration.
	time.Sleep(150 * time.Millisecond)

	// Sync load with panicking loader — should return stale value.
	val, err = cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("expected nil error (stale fallback), got: %v", err)
	}
	if val != "initial" {
		t.Fatalf("expected stale 'initial', got %q", val)
	}

	// TTL should be extended (item should not be expired yet).
	cache.mu.RLock()
	it := cache.items["key1"]
	expired := it.isExpired()
	cache.mu.RUnlock()
	if expired {
		t.Fatal("expected TTL to be extended after transient error with stale value")
	}
}

// TestSyncLoadTimeoutWithStale verifies that when a stale value exists and the loader
// times out, the stale value is returned with nil error and its TTL is extended.
func TestSyncLoadTimeoutWithStale(t *testing.T) {
	attempt := atomic.Int32{}
	ttl := 100 * time.Millisecond
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			return "late", nil
		}
	}), WithTTL[string](ttl), WithLoaderTimeout[string](50*time.Millisecond))

	// Prime the cache.
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil || val != "initial" {
		t.Fatalf("unexpected: val=%q err=%v", val, err)
	}

	// Wait for expiration.
	time.Sleep(150 * time.Millisecond)

	// Sync load with timing-out loader — should return stale value.
	val, err = cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err != nil {
		t.Fatalf("expected nil error (stale fallback), got: %v", err)
	}
	if val != "initial" {
		t.Fatalf("expected stale 'initial', got %q", val)
	}

	// TTL should be extended.
	cache.mu.RLock()
	it := cache.items["key1"]
	expired := it.isExpired()
	cache.mu.RUnlock()
	if expired {
		t.Fatal("expected TTL to be extended after transient timeout with stale value")
	}
}

// TestSyncLoadPanicNoStale verifies that when no stale value exists and the loader
// panics, ErrUpdateFailed is returned and the item is null-cached with a short TTL.
func TestSyncLoadPanicNoStale(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		panic("infra down")
	}), WithTTL[string](5*time.Minute))

	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUpdateFailed) {
		t.Fatalf("expected ErrUpdateFailed, got: %v", err)
	}

	// Item should be null-cached with a short TTL (≤30s, not the full 5 minutes).
	cache.mu.RLock()
	it := cache.items["key1"]
	ttlRemaining := time.Until(it.expireAt)
	isNull := it.isNull
	cache.mu.RUnlock()

	if !isNull {
		t.Fatal("expected item to be null-cached")
	}
	if ttlRemaining > 31*time.Second {
		t.Fatalf("expected short null-cache TTL (≤30s), got %v remaining", ttlRemaining)
	}
}

// TestSyncLoadTimeoutNoStale verifies that when no stale value exists and the loader
// times out, ErrUpdateFailed is returned with a short null-cache TTL.
func TestSyncLoadTimeoutNoStale(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			return "late", nil
		}
	}), WithTTL[string](5*time.Minute), WithLoaderTimeout[string](50*time.Millisecond))

	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUpdateFailed) {
		t.Fatalf("expected ErrUpdateFailed, got: %v", err)
	}

	cache.mu.RLock()
	it := cache.items["key1"]
	ttlRemaining := time.Until(it.expireAt)
	isNull := it.isNull
	cache.mu.RUnlock()

	if !isNull {
		t.Fatal("expected item to be null-cached")
	}
	if ttlRemaining > 31*time.Second {
		t.Fatalf("expected short null-cache TTL (≤30s), got %v remaining", ttlRemaining)
	}
}

// TestAsyncRefreshPanic verifies that a panic during asyncRefresh extends the TTL to
// the full value (not ttl/2) since the stale value is kept.
func TestAsyncRefreshPanic(t *testing.T) {
	attempt := atomic.Int32{}
	ttl := 2 * time.Second
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		panic("async boom")
	}), WithTTL[string](ttl))

	// Prime the cache.
	cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())

	// Force expiry by back-dating the item.
	cache.mu.Lock()
	cache.items["key1"].expireAt = time.Now().Add(-1 * time.Millisecond)
	cache.mu.Unlock()

	// Trigger async refresh (panicking loader).
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"))
	if err != nil || val != "initial" {
		t.Fatalf("expected stale value, got val=%q err=%v", val, err)
	}

	// Wait for background goroutine to finish.
	time.Sleep(100 * time.Millisecond)

	// TTL should be extended to full ttl (≈2s remaining), not ttl/2 (≈1s).
	cache.mu.RLock()
	it := cache.items["key1"]
	ttlRemaining := time.Until(it.expireAt)
	cache.mu.RUnlock()

	if ttlRemaining < ttl/2 {
		t.Fatalf("expected TTL extended to full %v, got only %v remaining", ttl, ttlRemaining)
	}

	stats := cache.Stats()
	if stats.RefreshFail == 0 {
		t.Fatal("expected RefreshFail to be incremented")
	}
}

// TestAsyncRefreshTimeout verifies that a timeout during asyncRefresh extends TTL to
// full value (not ttl/2).
func TestAsyncRefreshTimeout(t *testing.T) {
	attempt := atomic.Int32{}
	ttl := 2 * time.Second
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(1 * time.Second):
			return "late", nil
		}
	}), WithTTL[string](ttl), WithLoaderTimeout[string](50*time.Millisecond))

	// Prime the cache.
	cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())

	// Force expiry by back-dating the item.
	cache.mu.Lock()
	cache.items["key1"].expireAt = time.Now().Add(-1 * time.Millisecond)
	cache.mu.Unlock()

	// Trigger async refresh (timing-out loader).
	val, err := cache.Get(context.Background(), "key1", WithLoader("test"))
	if err != nil || val != "initial" {
		t.Fatalf("expected stale value, got val=%q err=%v", val, err)
	}

	// Wait for background goroutine to finish.
	time.Sleep(150 * time.Millisecond)

	// TTL should be extended to full ttl (≈2s remaining), not ttl/2 (≈1s).
	cache.mu.RLock()
	it := cache.items["key1"]
	ttlRemaining := time.Until(it.expireAt)
	cache.mu.RUnlock()

	if ttlRemaining < ttl/2 {
		t.Fatalf("expected TTL extended to full %v, got only %v remaining", ttl, ttlRemaining)
	}

	stats := cache.Stats()
	if stats.RefreshFail == 0 {
		t.Fatal("expected RefreshFail to be incremented")
	}
}

// TestNonTransientErrorUnchanged verifies that non-transient errors still null-cache
// with the full TTL (existing behavior).
func TestNonTransientErrorUnchanged(t *testing.T) {
	cache := New[string]("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "", errors.New("not found")
	}), WithTTL[string](5*time.Minute))

	_, err := cache.Get(context.Background(), "key1", WithLoader("test"), WithSync())
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, ErrUpdateFailed) {
		t.Fatal("non-transient error should not be wrapped in ErrUpdateFailed")
	}

	// Null-cached with full TTL (~5 minutes).
	cache.mu.RLock()
	it := cache.items["key1"]
	ttlRemaining := time.Until(it.expireAt)
	isNull := it.isNull
	cache.mu.RUnlock()

	if !isNull {
		t.Fatal("expected item to be null-cached")
	}
	// Full TTL: should be close to 5 minutes (well over 30s).
	if ttlRemaining < 4*time.Minute {
		t.Fatalf("expected full TTL (~5min) for non-transient error, got %v", ttlRemaining)
	}
}

// TestMaybeTouchLRUPrecision verifies the MaybeTouch read-throttle semantics:
//   - The first read after a write (n==1) immediately updates LRU order.
//   - Subsequent reads within the same burst are throttled (no lock acquired).
//   - After every touchEveryN reads, LRU order is updated again.
func TestMaybeTouchLRUPrecision(t *testing.T) {
	// maxItems=2 so the 3rd Set triggers eviction of the LRU tail.
	cache := New[string]("noop", noopLoader[string](), WithMaxItems[string](2))

	cache.Set("key1", "v1")
	cache.Set("key2", "v2")
	// LRU order: [key2(MRU), key1(LRU)]

	// First read of key1 after its Set → n==1 → Touch → key1 becomes MRU.
	cache.Get(context.Background(), "key1")
	// LRU order should now be: [key1(MRU), key2(LRU)]

	// Adding key3 must evict key2 (LRU), not key1.
	cache.Set("key3", "v3")

	_, err := cache.Get(context.Background(), "key2", WithLoader("_absent_"))
	if err == nil {
		t.Fatal("key2 should have been evicted after first read of key1 moved it to MRU")
	}
	_, err = cache.Get(context.Background(), "key1", WithLoader("_absent_"))
	if err != nil {
		t.Fatal("key1 should still be in cache")
	}

	// Verify throttling: reads 2..touchEveryN-1 do NOT update LRU order.
	//
	// Setup:
	//   Set keyA, Set keyB → LRU: [keyB(MRU), keyA(LRU)]
	//   Read keyA once  (n=1 → Touch) → LRU: [keyA(MRU), keyB(LRU)]
	//   Read keyB once  (n=1 → Touch) → LRU: [keyB(MRU), keyA(LRU)]
	//   Now read keyA (touchEveryN-2) more times (reads 2..touchEveryN-1):
	//     all throttled → keyA stays as LRU.
	//   Set keyC → evict keyA (still LRU despite multiple reads).
	cache2 := New[string]("noop", noopLoader[string](), WithMaxItems[string](2))
	cache2.Set("keyA", "vA")
	cache2.Set("keyB", "vB")
	// LRU: [keyB(MRU), keyA(LRU)]

	cache2.Get(context.Background(), "keyA") // n=1 → Touch → LRU: [keyA, keyB]
	cache2.Get(context.Background(), "keyB") // n=1 → Touch → LRU: [keyB, keyA]

	// Reads 2..touchEveryN-1 of keyA: all throttled, no Touch, keyA stays LRU.
	for i := 0; i < touchEveryN-2; i++ {
		cache2.Get(context.Background(), "keyA")
	}
	// keyA.readCount == touchEveryN-1; next read would be touchEveryN (Touch fires).
	// But we stop here: keyA is still LRU.
	cache2.Set("keyC", "vC") // triggers eviction of keyA (LRU tail)

	_, err = cache2.Get(context.Background(), "keyA", WithLoader("_absent_"))
	if err == nil {
		t.Fatal("keyA should have been evicted; reads 2..touchEveryN-1 are throttled")
	}
	_, err = cache2.Get(context.Background(), "keyB", WithLoader("_absent_"))
	if err != nil {
		t.Fatal("keyB should still be in cache")
	}
}

// TestConcurrentRegisterLoader verifies that RegisterLoader and Get can be called
// concurrently without data races, exercising the dedicated loadersMu lock.
func TestConcurrentRegisterLoader(t *testing.T) {
	cache := New[string]("loader0", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "v0", nil
	}))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		name := fmt.Sprintf("loader%d", i+1)
		go func(n string) {
			defer wg.Done()
			cache.RegisterLoader(n, LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
				return "v_" + n, nil
			}))
		}(name)
		go func() {
			defer wg.Done()
			cache.Get(context.Background(), "key", WithSync())
		}()
	}
	wg.Wait()
}

// Benchmark Get operations
func BenchmarkCacheGet(b *testing.B) {
	cache := New[string]("noop", noopLoader[string]())
	cache.Set("key", "value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(context.Background(), "key")
	}
}

// Benchmark Set operations
func BenchmarkCacheSet(b *testing.B) {
	cache := New[string]("noop", noopLoader[string]())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("key", "value")
	}
}

// Benchmark concurrent access
func BenchmarkCacheConcurrent(b *testing.B) {
	cache := New[string]("noop", noopLoader[string]())
	cache.Set("key", "value")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Get(context.Background(), "key")
		}
	})
}
