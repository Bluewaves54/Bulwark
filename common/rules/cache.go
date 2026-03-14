// SPDX-License-Identifier: Apache-2.0

// Package rules — in-memory TTL response cache.

package rules

import (
	"sync"
	"time"
)

// CacheEntry holds a single cached response.
type CacheEntry struct {
	Body        []byte
	ContentType string
	StatusCode  int
	StoredAt    time.Time
}

// Cache is a thread-safe in-memory TTL cache.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*CacheEntry
	ttl     time.Duration
}

// NewCache creates a Cache with the given TTL duration.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*CacheEntry),
		ttl:     ttl,
	}
}

// Get returns the cached entry for key, or nil if absent or expired.
func (c *Cache) Get(key string) *CacheEntry {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		return nil
	}
	if time.Since(e.StoredAt) > c.ttl {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil
	}
	return e
}

// Set stores a cache entry under key.
func (c *Cache) Set(key string, entry *CacheEntry) {
	entry.StoredAt = time.Now()
	c.mu.Lock()
	c.entries[key] = entry
	c.mu.Unlock()
}

// Delete removes the entry for key.
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// Purge removes all expired entries.
func (c *Cache) Purge() {
	now := time.Now()
	c.mu.Lock()
	for k, e := range c.entries {
		if now.Sub(e.StoredAt) > c.ttl {
			delete(c.entries, k)
		}
	}
	c.mu.Unlock()
}
