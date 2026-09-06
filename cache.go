package gobridge

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

type cacheItem[K comparable, V any] struct {
	key     K
	value   V
	expires time.Time
}
type flight[V any] struct {
	done    chan struct{}
	value   V
	err     error
	waiters int
	cancel  context.CancelFunc
}

// Memo is a bounded, concurrency-safe TTL/LRU cache with per-key request
// coalescing. Values must be immutable or copied by the caller. Only use it
// for pure operations; keys must include every input and identity boundary.
type Memo[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	lru      *list.List
	items    map[K]*list.Element
	flights  map[K]*flight[V]
}

func NewMemo[K comparable, V any](capacity int, ttl time.Duration) *Memo[K, V] {
	if capacity < 1 || ttl <= 0 {
		panic("cache capacity and TTL must be positive")
	}
	return &Memo[K, V]{capacity: capacity, ttl: ttl, lru: list.New(), items: map[K]*list.Element{}, flights: map[K]*flight[V]{}}
}
func (m *Memo[K, V]) Get(ctx context.Context, key K, load func(context.Context) (V, error)) (V, error) {
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	if e := m.items[key]; e != nil {
		item := e.Value.(cacheItem[K, V])
		if time.Now().Before(item.expires) {
			m.lru.MoveToFront(e)
			m.mu.Unlock()
			return item.value, nil
		}
		m.lru.Remove(e)
		delete(m.items, key)
	}
	f := m.flights[key]
	if f == nil {
		// A caller cancelling does not cancel other waiters. The loader is
		// cancelled when the last waiter leaves, not when the first leaves.
		work, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f = &flight[V]{done: make(chan struct{}), cancel: cancel}
		m.flights[key] = f
		go func() {
			func() {
				defer func() {
					if recover() != nil {
						f.err = fmt.Errorf("cache loader panicked")
					}
				}()
				f.value, f.err = load(work)
			}()
			m.mu.Lock()
			defer m.mu.Unlock()
			defer cancel()
			if m.flights[key] == f {
				delete(m.flights, key)
				if f.err == nil && work.Err() == nil {
					e := m.lru.PushFront(cacheItem[K, V]{key, f.value, time.Now().Add(m.ttl)})
					m.items[key] = e
					if m.lru.Len() > m.capacity {
						last := m.lru.Back()
						delete(m.items, last.Value.(cacheItem[K, V]).key)
						m.lru.Remove(last)
					}
				}
			}
			close(f.done)
		}()
	}
	f.waiters++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		f.waiters--
		if f.waiters == 0 {
			f.cancel()
			if m.flights[key] == f {
				delete(m.flights, key)
			}
		}
	}()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-f.done:
		return f.value, f.err
	}
}

// Delete invalidates a key and detaches any current loader. Existing waiters
// may finish, but that loader cannot repopulate the cache after invalidation.
func (m *Memo[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.items[key]; e != nil {
		m.lru.Remove(e)
		delete(m.items, key)
	}
	delete(m.flights, key)
}

// Clear invalidates all values and detaches in-flight loads from future reads.
func (m *Memo[K, V]) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lru.Init()
	clear(m.items)
	clear(m.flights)
}
