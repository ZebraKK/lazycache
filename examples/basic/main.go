package main

import (
	"context"
	"fmt"
	"time"

	"github.com/ZebraKK/lazycache"
)

type User struct {
	ID    string
	Name  string
	Email string
}

func main() {
	fmt.Println("=== LazyCache Basic Example ===\n")

	// Create cache with configuration
	cache := lazycache.New[*User](
		lazycache.WithMaxItems[*User](100),
		lazycache.WithMaxBytes[*User](1<<20), // 1MB
		lazycache.WithTTL[*User](2*time.Second),
	)

	// Register a mock database loader
	loadCount := 0
	cache.RegisterLoader("db", lazycache.LoaderFunc[*User](
		func(ctx context.Context, key string) (*User, error) {
			loadCount++
			fmt.Printf("📦 Loading from DB (call #%d): %s\n", loadCount, key)
			time.Sleep(100 * time.Millisecond) // Simulate slow DB query
			return &User{
				ID:    key,
				Name:  "User " + key,
				Email: key + "@example.com",
			}, nil
		},
	))

	// Inject StdLogger so cache events are visible in the output
	ctx := lazycache.NewContext(context.Background(), lazycache.StdLogger("[cache] "))

	// Example 1: Initial load (cache miss)
	fmt.Println("1️⃣  First Get (cache miss, sync load):")
	start := time.Now()
	user, err := cache.Get(ctx, "user:123", lazycache.WithLoader("db"), lazycache.WithSync())
	if err != nil {
		panic(err)
	}
	fmt.Printf("   ✅ Got: %+v (took %v)\n\n", user, time.Since(start))

	// Example 2: Cache hit
	fmt.Println("2️⃣  Second Get (cache hit):")
	start = time.Now()
	user, _ = cache.Get(ctx, "user:123", lazycache.WithLoader("db"))
	fmt.Printf("   ✅ Got: %+v (took %v)\n\n", user, time.Since(start))

	// Example 3: Wait for expiration
	fmt.Println("3️⃣  Waiting for cache to expire (2 seconds)...")
	time.Sleep(2100 * time.Millisecond)

	// Example 4: Lazy loading (async mode)
	fmt.Println("4️⃣  Get after expiration (lazy loading mode):")
	start = time.Now()
	user, _ = cache.Get(ctx, "user:123", lazycache.WithLoader("db"))
	elapsed := time.Since(start)
	fmt.Printf("   ✅ Got stale value instantly: %+v (took %v)\n", user, elapsed)
	fmt.Println("   🔄 Background refresh triggered...")
	time.Sleep(150 * time.Millisecond) // Wait for refresh
	fmt.Println()

	// Example 5: Manual set
	fmt.Println("5️⃣  Manual Set:")
	cache.Set("user:456", &User{ID: "456", Name: "Alice", Email: "alice@example.com"})
	user, _ = cache.Get(ctx, "user:456")
	fmt.Printf("   ✅ Got: %+v\n\n", user)

	// Example 6: Invalidation
	fmt.Println("6️⃣  Invalidation:")
	cache.Invalidate("user:456")
	user, _ = cache.Get(ctx, "user:456")
	if user == nil {
		fmt.Println("   ✅ Cache invalidated successfully\n")
	}

	// Example 7: Statistics
	fmt.Println("7️⃣  Cache Statistics:")
	stats := cache.Stats()
	fmt.Printf("   Hits: %d\n", stats.Hits)
	fmt.Printf("   Misses: %d\n", stats.Misses)
	fmt.Printf("   Hit Rate: %.2f%%\n", stats.HitRate*100)
	fmt.Printf("   Refresh Success: %d\n", stats.RefreshSuccess)
	fmt.Printf("   Total DB Loads: %d\n\n", loadCount)

	// Example 8: Concurrent access (anti-stampede)
	fmt.Println("8️⃣  Concurrent Access (10 goroutines, same key):")
	cache.Invalidate("user:999")
	loadCountBefore := loadCount
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			cache.Get(ctx, "user:999", lazycache.WithLoader("db"), lazycache.WithSync())
			done <- true
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	fmt.Printf("   ✅ All 10 requests completed\n")
	fmt.Printf("   ℹ️  DB was called %d time(s) (anti-stampede protection)\n\n", loadCount-loadCountBefore)

	fmt.Println("=== Example Complete ===")
}
