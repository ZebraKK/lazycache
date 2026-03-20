package lazycache

import "sync"

// Statistics holds cache performance metrics
type Statistics struct {
	mu             sync.RWMutex
	hits           int64
	misses         int64
	evictions      int64
	refreshSuccess int64
	refreshFail    int64
}

// Hit increments the hit counter
func (s *Statistics) Hit() {
	s.mu.Lock()
	s.hits++
	s.mu.Unlock()
}

// Miss increments the miss counter
func (s *Statistics) Miss() {
	s.mu.Lock()
	s.misses++
	s.mu.Unlock()
}

// Evict increments the eviction counter
func (s *Statistics) Evict() {
	s.mu.Lock()
	s.evictions++
	s.mu.Unlock()
}

// RefreshSuccess increments the successful refresh counter
func (s *Statistics) RefreshSuccess() {
	s.mu.Lock()
	s.refreshSuccess++
	s.mu.Unlock()
}

// RefreshFail increments the failed refresh counter
func (s *Statistics) RefreshFail() {
	s.mu.Lock()
	s.refreshFail++
	s.mu.Unlock()
}

// HitRate returns the cache hit rate
func (s *Statistics) HitRate() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := s.hits + s.misses
	if total == 0 {
		return 0
	}
	return float64(s.hits) / float64(total)
}

// Snapshot returns a copy of the current statistics
type Snapshot struct {
	Hits           int64
	Misses         int64
	Evictions      int64
	RefreshSuccess int64
	RefreshFail    int64
	HitRate        float64
}

// GetSnapshot returns a snapshot of current statistics
func (s *Statistics) GetSnapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return Snapshot{
		Hits:           s.hits,
		Misses:         s.misses,
		Evictions:      s.evictions,
		RefreshSuccess: s.refreshSuccess,
		RefreshFail:    s.refreshFail,
		HitRate:        s.HitRate(),
	}
}

// Reset resets all statistics to zero
func (s *Statistics) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.hits = 0
	s.misses = 0
	s.evictions = 0
	s.refreshSuccess = 0
	s.refreshFail = 0
}
