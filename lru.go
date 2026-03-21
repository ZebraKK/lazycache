package lazycache

import "sync"

// touchEveryN controls how often a read-path Touch actually moves the item to
// the front of the LRU list. Every N-th read acquires lruList.mu; the rest are
// handled with a lock-free atomic increment and an early return.
//
// Trade-off: smaller N → more accurate LRU, more lock contention on reads.
//
// --- Alternative: async Touch via buffered channel ---
// If profiling shows lruList.mu is still a bottleneck under very high read QPS
// (e.g. >100k/s), replace MaybeTouch with an async approach:
//
//   type Cache[V any] struct {
//       touchCh chan *item[V]  // e.g. buffered at 1024
//   }
//
//   // Hot path (non-blocking):
//   select {
//   case c.touchCh <- it:
//   default: // channel full: drop this touch → approximate LRU
//   }
//
//   // Background goroutine (started in NewCache, drained on Cache.Close()):
//   func (c *Cache[V]) touchWorker() {
//       for it := range c.touchCh {
//           c.lru.Touch(it)
//       }
//   }
//
// Caveats:
//   - Dropped touches degrade LRU accuracy (same as MaybeTouch under high load)
//   - Requires Cache.Close() to shut down the worker goroutine cleanly
//   - Eviction may race with a queued Touch, evicting a recently-read item
//   - Suitable when read QPS is very high and slight LRU inaccuracy is acceptable
// -----------------------------------------------------------------------
const touchEveryN = 8

// lruList is a generic doubly-linked list used for LRU tracking.
// Head is the most-recently-used end; tail is the least-recently-used end.
// The nodes map has been eliminated: LRU prev/next pointers live directly
// inside item[V], saving one allocation and one map lookup per operation.
type lruList[V any] struct {
	mu   sync.Mutex
	head *item[V] // most recently used
	tail *item[V] // least recently used (eviction candidate)
}

func newLRUList[V any]() *lruList[V] {
	return &lruList[V]{}
}

// MaybeTouch is used on the read path (Get). It increments the item's atomic
// read counter and acquires lruList.mu on:
//   - the first read after any write (n == 1): preserves exact LRU semantics
//     for the initial access burst.
//   - every touchEveryN reads thereafter: keeps LRU approximately fresh while
//     greatly reducing lock contention for hot items.
//
// The write-path Touch resets readCount to 0, so the "n==1" trigger fires
// again after each write, maintaining accurate LRU ordering across write cycles.
func (l *lruList[V]) MaybeTouch(it *item[V]) {
	n := it.readCount.Add(1)
	if n != 1 && n%touchEveryN != 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if it.inLRU {
		l.remove(it)
	}
	l.addToFront(it)
}

// Touch is used on the write path (Set, syncLoad, asyncRefresh). It always
// moves the item to the front and resets the read counter so that the next
// read will immediately update the LRU position (n==1 in MaybeTouch).
func (l *lruList[V]) Touch(it *item[V]) {
	it.readCount.Store(0)
	l.mu.Lock()
	defer l.mu.Unlock()
	if it.inLRU {
		l.remove(it)
	}
	l.addToFront(it)
}

// Remove removes an item from the list (used by Invalidate).
func (l *lruList[V]) Remove(it *item[V]) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if it.inLRU {
		l.remove(it)
	}
}

// RemoveLast removes and returns the least recently used item, or nil if empty.
func (l *lruList[V]) RemoveLast() *item[V] {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.tail == nil {
		return nil
	}

	victim := l.tail
	l.remove(victim)
	return victim
}

// remove unlinks a node from the list (not thread-safe, must hold l.mu).
// Caller must ensure it.inLRU == true before calling.
func (l *lruList[V]) remove(it *item[V]) {
	if it.lruPrev != nil {
		it.lruPrev.lruNext = it.lruNext
	} else {
		l.head = it.lruNext
	}

	if it.lruNext != nil {
		it.lruNext.lruPrev = it.lruPrev
	} else {
		l.tail = it.lruPrev
	}

	it.lruPrev = nil
	it.lruNext = nil
	it.inLRU = false
}

// addToFront inserts a node at the head of the list (not thread-safe, must hold l.mu).
func (l *lruList[V]) addToFront(it *item[V]) {
	it.lruNext = l.head
	it.lruPrev = nil

	if l.head != nil {
		l.head.lruPrev = it
	}
	l.head = it

	if l.tail == nil {
		l.tail = it
	}

	it.inLRU = true
}
