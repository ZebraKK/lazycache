# LazyCache

**English | [中文](README_CN.md)**

A high-performance, lazy-loading cache middleware for Go with LRU eviction, anti-stampede protection, and zero-blocking queries.

## Features

- 🚀 **Zero-Blocking Queries** - When cache expires, immediately return stale value and refresh in background
- 🛡️ **Anti-Stampede** - Single-key level loading deduplication prevents cache stampede
- 🔒 **Anti-Penetration** - Null value caching prevents repeated queries for non-existent keys
- ⚡ **High Performance** - 16M+ ops/sec for Get operations, <100ns latency
- 🔧 **Hot Configuration** - Runtime updates of cache size, TTL, and other parameters
- 📊 **Smart Eviction** - LRU strategy with dual limits (item count + byte size)
- 🎯 **Type Safety** - Generic implementation for compile-time type safety
- 🔌 **Multiple Loaders** - Register and switch between different data sources
- 📈 **Built-in Metrics** - Track hits, misses, evictions, and refresh performance

## Installation

```bash
go get github.com/ZebraKK/lazycache
```

**Requirements**: Go 1.18+ (uses generics)

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/ZebraKK/lazycache"
)

type User struct {
    ID   string
    Name string
}

func main() {
    // Create cache with configuration
    cache := lazycache.New[*User](
        lazycache.WithMaxItems[*User](10000),
        lazycache.WithMaxBytes[*User](1<<30), // 1GB
        lazycache.WithTTL[*User](5*time.Minute),
    )

    // Register a data loader
    cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
        func(ctx context.Context, key string) (*User, error) {
            // Fetch from database
            return &User{ID: key, Name: "Alice"}, nil
        },
    ))

    ctx := context.Background()

    // Get with lazy loading (default async mode)
    // Returns stale value immediately if expired, refreshes in background
    user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"))
    if err != nil {
        panic(err)
    }
    fmt.Printf("User: %+v\n", user)

    // Get with synchronous loading
    // Waits for refresh to complete before returning
    user, err = cache.Get(ctx, "user:456",
        lazycache.WithLoader("db"),
        lazycache.WithSync(),
    )

    // Manual set
    cache.Set("user:789", &User{ID: "789", Name: "Bob"})

    // Invalidate cache
    cache.Invalidate("user:123")

    // Get statistics
    stats := cache.Stats()
    fmt.Printf("Hit Rate: %.2f%%\n", stats.HitRate*100)
    fmt.Printf("Hits: %d, Misses: %d\n", stats.Hits, stats.Misses)
}
```

## Core Concepts

### Lazy Loading (Async Mode)

The killer feature of LazyCache is **zero-blocking lazy loading**:

```go
// When cache expires:
// 1. Returns stale value immediately (no blocking!)
// 2. Triggers background refresh
// 3. Next request gets fresh data
user, _ := cache.Get(ctx, "key", lazycache.WithLoader("db"))
```

This provides:
- ✅ Consistent low latency (P99 < 1ms)
- ✅ No user-facing blocking on cache refresh
- ✅ Graceful handling of slow backends

### Anti-Stampede Protection

When multiple goroutines request the same uncached key:

```go
// Only ONE goroutine loads the data
// Others wait and share the result
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    go func() {
        defer wg.Done()
        // All 100 goroutines, only 1 database query
        cache.Get(ctx, "key", lazycache.WithLoader("db"), lazycache.WithSync())
    }()
}
wg.Wait()
```

### Null Value Caching

Prevents cache penetration attacks:

```go
cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
    func(ctx context.Context, key string) (*User, error) {
        // Key doesn't exist
        return nil, errors.New("not found")
    },
))

// First call: queries database, caches the error
_, err1 := cache.Get(ctx, "missing", lazycache.WithLoader("db"), lazycache.WithSync())

// Second call: returns cached error, NO database query
_, err2 := cache.Get(ctx, "missing", lazycache.WithLoader("db"), lazycache.WithSync())
```

### Dual Memory Limits

Cache respects BOTH limits simultaneously:

```go
cache := lazycache.New[string](
    lazycache.WithMaxItems[string](10000),  // Max 10k items
    lazycache.WithMaxBytes[string](1<<30),  // Max 1GB memory
)
// Evicts LRU items when EITHER limit is exceeded
```

## API Reference

### Creating a Cache

```go
func New[V any](opts ...Option[V]) *Cache[V]
```

**Options:**
- `WithMaxItems[V](n int)` - Maximum number of cached items (default: 10000)
- `WithMaxBytes[V](bytes int64)` - Maximum total size in bytes (default: 1GB)
- `WithTTL[V](duration time.Duration)` - Default TTL for items (default: 5 minutes)
- `WithSizeEstimator[V](fn SizeEstimator[V])` - Custom size calculation function

### Cache Operations

#### Get

```go
func (c *Cache[V]) Get(ctx context.Context, key string, opts ...GetOption) (V, error)
```

**Options:**
- `WithLoader(name string)` - Specify which loader to use
- `WithSync()` - Synchronous mode (wait for load/refresh)
- `WithAsync()` - Async mode (return stale, refresh in background) *default*
- `WithTTLOverride(duration time.Duration)` - Override default TTL for this Get

**Returns:**
- Value from cache or loaded from source
- `ErrNotFound` if loader returns error (null value cached)
- `ErrNoLoader` if no loader specified

#### Set

```go
func (c *Cache[V]) Set(key string, value V, opts ...SetOption)
```

Manually sets a cache value.

**Options:**
- `WithSetTTL(duration time.Duration)` - Custom TTL for this item
- `WithSetSize(bytes int64)` - Manually specify item size

#### Invalidate

```go
func (c *Cache[V]) Invalidate(key string)
```

Removes a key from cache.

#### RegisterLoader

```go
func (c *Cache[V]) RegisterLoader(name string, loader Loader[V])
```

Registers a data source.

**Loader Interface:**
```go
type Loader[V any] interface {
    Load(ctx context.Context, key string) (V, error)
}
```

**Helper:**
```go
lazycache.LoaderFunc[V](func(ctx context.Context, key string) (V, error) {
    // Your loading logic
})
```

#### UpdateConfig

```go
func (c *Cache[V]) UpdateConfig(opts ...Option[V])
```

Hot-reload configuration at runtime.

#### Stats

```go
func (c *Cache[V]) Stats() Snapshot
```

Returns cache statistics:
```go
type Snapshot struct {
    Hits           int64
    Misses         int64
    Evictions      int64
    RefreshSuccess int64
    RefreshFail    int64
    HitRate        float64
}
```

### Advanced Usage

#### Multiple Data Sources

```go
cache := lazycache.New[*User]()

// Register multiple loaders
cache.RegisterLoader("db", dbLoader)
cache.RegisterLoader("api", apiLoader)
cache.RegisterLoader("cache_fallback", fallbackLoader)

// Switch sources at runtime
user, _ := cache.Get(ctx, "key", lazycache.WithLoader("db"))
user, _ = cache.Get(ctx, "key", lazycache.WithLoader("api"))
```

#### Custom Size Estimation

```go
cache := lazycache.New[*User](
    lazycache.WithSizeEstimator(func(u *User) int64 {
        return int64(len(u.Name) + len(u.Email) + 100)
    }),
)
```

#### Refresh Failure Handling

When background refresh fails:
- Keeps the old/stale value in cache
- Extends expiration time by 50% of TTL
- Increments `RefreshFail` counter

```go
stats := cache.Stats()
if stats.RefreshFail > 0 {
    log.Printf("Background refreshes failing: %d", stats.RefreshFail)
}
```

#### Hot Configuration Updates

```go
// Start with small cache
cache := lazycache.New[string](
    lazycache.WithMaxItems[string](1000),
    lazycache.WithTTL[string](1*time.Minute),
)

// Later: scale up capacity
cache.UpdateConfig(
    lazycache.WithMaxItems[string](10000),
    lazycache.WithTTL[string](10*time.Minute),
)
```

## Performance

Benchmarked on Apple M3 Max:

```
BenchmarkCacheGet-16          19423137    61.14 ns/op    32 B/op    1 allocs/op
BenchmarkCacheSet-16          17817249    68.44 ns/op    16 B/op    1 allocs/op
BenchmarkCacheConcurrent-16    5973404   180.0  ns/op    32 B/op    1 allocs/op
```

**Throughput:**
- Single-threaded Get: **16M ops/sec**
- Single-threaded Set: **14M ops/sec**
- Concurrent Get: **5.5M ops/sec**

## Design Decisions

### Why String Keys Only?

While Go generics support `comparable` types, we restrict keys to `string` for:
1. **Simplicity** - Most cache use cases use string keys
2. **Performance** - String comparison is highly optimized in Go
3. **Serialization** - Easy integration with external caches (Redis, Memcached)
4. **Future-proof** - Easier to add distributed cache support

### Default Async Mode

Async (lazy loading) is the default because:
- **Better UX** - No blocking on cache expiration
- **Graceful degradation** - Slow backends don't impact latency
- **Production-ready** - Matches behavior of high-performance systems

Switch to sync mode when:
- You need guaranteed fresh data
- Loading is fast (<10ms)
- Stale data is unacceptable

### Refresh Failure Strategy

On background refresh failure, we **extend expiration by 50%** rather than deleting because:
- Stale data is better than no data (availability > consistency)
- Prevents cascading failures to backend
- Gives backend time to recover

## Testing

```bash
# Run tests
go test -v

# Run with race detector
go test -race -v

# Run benchmarks
go test -bench=. -benchmem

# Check coverage
go test -cover
```

**Test Coverage:** 88.1%

## Limitations

- Keys must be strings (not arbitrary comparable types)
- No distributed cache support (single-node only)
- No persistence (in-memory only)
- Size estimation is approximate
- TTL granularity is implementation-dependent

## Future Enhancements

Potential improvements (not currently implemented):

- Distributed cache support (Redis/Memcached backend)
- Metrics export (Prometheus, StatsD)
- Cache warming / preloading
- Compression support
- Tiered caching (L1/L2)
- Event hooks (onEvict, onLoad, etc.)

## Contributing

This is currently a personal project. Issues and suggestions welcome!

## License

MIT License - see LICENSE file for details

## Related Projects

- [groupcache](https://github.com/golang/groupcache) - Google's distributed cache
- [ristretto](https://github.com/dgraph-io/ristretto) - High-performance cache by Dgraph
- [bigcache](https://github.com/allegro/bigcache) - Fast, concurrent cache

## Why LazyCache?

Unlike other caches, LazyCache prioritizes **user-facing latency** over data freshness:

| Feature | LazyCache | Traditional Cache |
|---------|-----------|-------------------|
| Expired cache read | Returns stale instantly | Blocks on reload |
| Cache stampede | Single load per key | N simultaneous loads |
| Missing key attacks | Caches null values | Every request hits backend |
| Config updates | Hot reload | Requires restart |
| Concurrency | Lock-free reads* | Global lock or sharding |

*Lock-free for cache hits; uses fine-grained locking for misses.

**Best for:**
- Web APIs with SLA requirements
- High-QPS services
- Systems with slow backends
- Microservices with external dependencies

**Not ideal for:**
- Strong consistency requirements
- Financial/transactional systems
- Small datasets that fit in memory

---

Built with ❤️ and zero external dependencies.
