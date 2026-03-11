package cache

import (
	"sync"
	"time"
)

// MtimeCache caches a value and invalidates when a file's mtime changes.
type MtimeCache[V any] struct {
	mu        sync.Mutex
	value     V
	lastMtime time.Time
	valid     bool
	stat      func(path string) (time.Time, error)
	path      string
}

// StatFunc returns the mtime of a file. Allows injection for testing.
type StatFunc func(path string) (time.Time, error)

func NewMtimeCache[V any](path string, stat StatFunc) *MtimeCache[V] {
	return &MtimeCache[V]{
		path: path,
		stat: stat,
	}
}

// Get returns the cached value if the file mtime hasn't changed.
// Returns (value, true) on cache hit, (zero, false) on miss.
func (c *MtimeCache[V]) Get() (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.valid {
		var zero V
		return zero, false
	}
	mtime, err := c.stat(c.path)
	if err != nil {
		var zero V
		return zero, false
	}
	if !mtime.Equal(c.lastMtime) {
		var zero V
		c.valid = false
		return zero, false
	}
	return c.value, true
}

// Set stores the value with the current file mtime.
func (c *MtimeCache[V]) Set(value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	mtime, err := c.stat(c.path)
	if err != nil {
		return
	}
	c.value = value
	c.lastMtime = mtime
	c.valid = true
}
