package data

import (
	"github.com/google/uuid"
	"sync"
)

// Each waiter has its own buffered signal; broadcasts may coalesce.
// Subscribe BEFORE reading PostgreSQL so a concurrent commit cannot be missed.
type JobChanges struct {
	mu       sync.Mutex
	watchers map[uuid.UUID]map[chan struct{}]struct{}
}

func NewJobChanges() *JobChanges {
	return &JobChanges{watchers: make(map[uuid.UUID]map[chan struct{}]struct{})}
}
func (c *JobChanges) Subscribe(id uuid.UUID) (<-chan struct{}, func()) {
	c.mu.Lock()
	ch := make(chan struct{}, 1)
	if c.watchers[id] == nil {
		c.watchers[id] = make(map[chan struct{}]struct{})
	}
	c.watchers[id][ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.watchers[id], ch)
		if len(c.watchers[id]) == 0 {
			delete(c.watchers, id)
		}
	}
}
func (c *JobChanges) Notify(id uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for ch := range c.watchers[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
