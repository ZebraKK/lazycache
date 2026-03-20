package lazycache

import "time"

// item represents a cached value with metadata
type item[V any] struct {
	value    V
	expireAt time.Time
	size     int64
	loading  bool
	loadChan chan struct{}
	isNull   bool
}

// isExpired checks if the item has expired
func (it *item[V]) isExpired() bool {
	return time.Now().After(it.expireAt)
}
