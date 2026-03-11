package cache

import (
	"math/rand/v2"
	"sync"
	"time"
)

// TTLCache is a generic TTL cache with random jitter to prevent thundering herd.
type TTLCache[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]ttlEntry[V]
	maxTTL  time.Duration
	rand    time.Duration
}

type ttlEntry[V any] struct {
	value     V
	expiresAt time.Time
}

func NewTTLCache[K comparable, V any](maxTTL, randRange time.Duration) *TTLCache[K, V] {
	return &TTLCache[K, V]{
		entries: make(map[K]ttlEntry[V]),
		maxTTL:  maxTTL,
		rand:    randRange,
	}
}

func (c *TTLCache[K, V]) jitteredTTL() time.Duration {
	jitter := time.Duration(rand.Float64()*2*float64(c.rand)) - c.rand
	return c.maxTTL + jitter
}

func (c *TTLCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		if ok {
			delete(c.entries, key)
		}
		var zero V
		return zero, false
	}
	return e.value, true
}

func (c *TTLCache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = ttlEntry[V]{
		value:     value,
		expiresAt: time.Now().Add(c.jitteredTTL()),
	}
}

func (c *TTLCache[K, V]) Invalidate(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}
