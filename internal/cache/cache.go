package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"sync"
	"time"

	"github.com/payapi/x402-server/internal/pricing"
)

// Entry is a fully-computed LLM response held until the client pays for it.
type Entry struct {
	Body        []byte
	Status      int
	ContentType string
	Cost        *big.Int // USDC base units
	Usage       pricing.Usage
	Model       string
	CreatedAt   time.Time
}

// Cache is a TTL keyed store of staged responses.
type Cache struct {
	mu    sync.RWMutex
	store map[string]*Entry
	ttl   time.Duration
	stop  chan struct{}
}

// New creates a Cache with the given TTL and starts a background sweeper.
func New(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 3 * time.Minute
	}
	c := &Cache{
		store: make(map[string]*Entry),
		ttl:   ttl,
		stop:  make(chan struct{}),
	}
	go c.sweep()
	return c
}

// RequestID derives a stable idempotency key from the raw request body.
func RequestID(body []byte) string {
	sum := sha256.Sum256(body)
	return "req_" + hex.EncodeToString(sum[:16])
}

func (c *Cache) Store(id string, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.CreatedAt = time.Now()
	c.store[id] = e
}

func (c *Cache) Get(id string) *Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.store[id]
	if !ok || time.Since(e.CreatedAt) > c.ttl {
		return nil
	}
	return e
}

func (c *Cache) Delete(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.store, id)
}

func (c *Cache) sweep() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			c.mu.Lock()
			now := time.Now()
			for k, v := range c.store {
				if now.Sub(v.CreatedAt) > c.ttl {
					delete(c.store, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

func (c *Cache) Close() { close(c.stop) }
