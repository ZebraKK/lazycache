package lazycache

import (
	"sync/atomic"
	"time"
)

// item represents a cached value with metadata
type item[V any] struct {
	key      string    // stored for eviction (delete from items map via RemoveLast)
	value    V
	expireAt time.Time
	size     int64
	loading  bool
	loadChan chan struct{}
	isNull   bool
	// embedded LRU doubly-linked list pointers (replaces separate lruNode allocation)
	lruPrev *item[V]
	lruNext *item[V]
	inLRU   bool // true when the item is currently linked in lruList
	// read Touch throttle: Touch is skipped unless readCount % touchEveryN == 0
	readCount atomic.Int64
}

// isExpired checks if the item has expired
func (it *item[V]) isExpired() bool {
	return time.Now().After(it.expireAt)
}
