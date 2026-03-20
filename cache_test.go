package lazycache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Test basic Get/Set operations
func TestBasicGetSet(t *testing.T) {
	cache := New[string]()

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
	cache := New[string]()

	loadCount := 0
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[string](WithTTL[string](100 * time.Millisecond))

	loadCount := atomic.Int32{}
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		loadCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return "loaded_v" + key[len(key)-1:], nil
	}))

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
	cache := New[string]()

	loadCount := atomic.Int32{}
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[string]()

	loadCount := 0
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[string](WithMaxItems[string](3))

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// Access key1 to make it more recently used
	cache.Get(context.Background(), "key1")

	// Add key4, should evict key2 (least recently used)
	cache.Set("key4", "value4")

	// key2 should be evicted
	val, err := cache.Get(context.Background(), "key2")
	if err == nil || val != "" {
		t.Fatal("key2 should have been evicted")
	}

	// key1 should still exist
	val, err = cache.Get(context.Background(), "key1")
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
	cache := New[string]()
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
	cache := New[string](WithMaxItems[string](10))

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
	cache := New[string](WithTTL[string](1 * time.Second))

	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "loaded", nil
	}))

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
	cache := New[string](WithTTL[string](100 * time.Millisecond))

	attemptCount := atomic.Int32{}
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		count := attemptCount.Add(1)
		if count == 1 {
			return "initial", nil
		}
		return "", errors.New("refresh failed")
	}))

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
	cache := New[string]()

	cache.RegisterLoader("db", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[string]()

	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[int]()

	cache.RegisterLoader("test", LoaderFunc[int](func(ctx context.Context, key string) (int, error) {
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
	cache := New[string]()
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
	cache := New[string]()

	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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

// TestLoggerNoLoader verifies an Error "no loader" is emitted when loader is missing.
func TestLoggerNoLoader(t *testing.T) {
	cache := New[string]()

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
	cache := New[string]()

	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
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
	cache := New[string](WithTTL[string](50 * time.Millisecond))

	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		return "refreshed", nil
	}))

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
	cache := New[string](WithTTL[string](50 * time.Millisecond))

	attempt := atomic.Int32{}
	cache.RegisterLoader("test", LoaderFunc[string](func(ctx context.Context, key string) (string, error) {
		if attempt.Add(1) == 1 {
			return "initial", nil
		}
		return "", errors.New("refresh error")
	}))

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
	cache := New[string]()
	cache.Set("key1", "value1")

	// No logger in context → noopLogger, must not panic.
	_, err := cache.Get(context.Background(), "key1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Benchmark Get operations
func BenchmarkCacheGet(b *testing.B) {
	cache := New[string]()
	cache.Set("key", "value")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(context.Background(), "key")
	}
}

// Benchmark Set operations
func BenchmarkCacheSet(b *testing.B) {
	cache := New[string]()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set("key", "value")
	}
}

// Benchmark concurrent access
func BenchmarkCacheConcurrent(b *testing.B) {
	cache := New[string]()
	cache.Set("key", "value")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cache.Get(context.Background(), "key")
		}
	})
}
