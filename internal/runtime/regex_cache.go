package runtime

import (
	"container/list"
	"regexp"
	"sync"
)

const compiledRegexCacheCapacity = 64

var compiledRegexps = newRegexCache(compiledRegexCacheCapacity)

type regexCache struct {
	mu       sync.Mutex
	capacity int
	lru      *list.List
	entries  map[string]*list.Element
}

type regexCacheEntry struct {
	pattern string
	re      *regexp.Regexp
}

func newRegexCache(capacity int) *regexCache {
	if capacity < 1 {
		capacity = 1
	}
	return &regexCache{
		capacity: capacity,
		lru:      list.New(),
		entries:  make(map[string]*list.Element, capacity),
	}
}

func compileCachedRegex(pattern string) (*regexp.Regexp, error) {
	return compiledRegexps.compile(pattern)
}

func (c *regexCache) compile(pattern string) (*regexp.Regexp, error) {
	c.mu.Lock()
	if elem := c.entries[pattern]; elem != nil {
		c.lru.MoveToFront(elem)
		entry := elem.Value.(regexCacheEntry)
		c.mu.Unlock()
		return entry.re, nil
	}
	c.mu.Unlock()

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if elem := c.entries[pattern]; elem != nil {
		c.lru.MoveToFront(elem)
		return elem.Value.(regexCacheEntry).re, nil
	}

	elem := c.lru.PushFront(regexCacheEntry{pattern: pattern, re: re})
	c.entries[pattern] = elem
	if c.lru.Len() > c.capacity {
		evicted := c.lru.Back()
		if evicted != nil {
			c.lru.Remove(evicted)
			entry := evicted.Value.(regexCacheEntry)
			delete(c.entries, entry.pattern)
		}
	}
	return re, nil
}
