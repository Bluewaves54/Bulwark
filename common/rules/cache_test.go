// SPDX-License-Identifier: Apache-2.0

package rules_test

import (
	"testing"
	"time"

	"Bulwark/common/rules"
)

func TestCacheGetMiss(t *testing.T) {
	c := rules.NewCache(time.Minute)
	if e := c.Get("missing"); e != nil {
		t.Errorf("expected nil for cache miss, got %v", e)
	}
}

func TestCacheSetAndGet(t *testing.T) {
	c := rules.NewCache(time.Minute)
	entry := &rules.CacheEntry{Body: []byte("hello"), ContentType: "text/plain", StatusCode: 200}
	c.Set("key1", entry)

	got := c.Get("key1")
	if got == nil {
		t.Fatal("expected cache hit, got nil")
	}
	if string(got.Body) != "hello" {
		t.Errorf("body: want hello, got %q", string(got.Body))
	}
}

func TestCacheExpiry(t *testing.T) {
	c := rules.NewCache(50 * time.Millisecond)
	c.Set("k", &rules.CacheEntry{Body: []byte("v"), StatusCode: 200})
	time.Sleep(100 * time.Millisecond)
	if e := c.Get("k"); e != nil {
		t.Error("expected expired entry to return nil")
	}
}

func TestCacheDelete(t *testing.T) {
	c := rules.NewCache(time.Minute)
	c.Set("del", &rules.CacheEntry{Body: []byte("x"), StatusCode: 200})
	c.Delete("del")
	if e := c.Get("del"); e != nil {
		t.Error("expected deleted entry to return nil")
	}
}

func TestCachePurge(t *testing.T) {
	c := rules.NewCache(50 * time.Millisecond)
	c.Set("expired", &rules.CacheEntry{Body: []byte("old"), StatusCode: 200})
	time.Sleep(100 * time.Millisecond)
	c.Set("fresh", &rules.CacheEntry{Body: []byte("new"), StatusCode: 200})
	c.Purge()
	if e := c.Get("fresh"); e == nil {
		t.Error("fresh entry should still be present after purge")
	}
}
