// Governing: ADR-0018 (graceful shutdown), ADR-0019 (structured metrics), issue #344
package handlers

import (
	"context"
	"sync"
	"time"
)

const (
	// navidromeHealthTTL is how long a reachability result is trusted before the
	// next render asks for a refresh.
	navidromeHealthTTL = 60 * time.Second
	// navidromePingTimeout bounds one ping of the Navidrome server.
	navidromePingTimeout = 5 * time.Second
)

// navidromeHealthEntry is the last known reachability result for one user.
type navidromeHealthEntry struct {
	online     bool
	checkedAt  time.Time
	refreshing bool
}

// navidromeHealthCache holds the last Navidrome reachability result per user so
// the home page reports what was last known instead of waiting for the probe: a
// stale entry triggers one background refresh, and a Navidrome server that does
// not answer can no longer hold up a render.
//
// The zero value works once configure has run, so it needs no wiring in New.
type navidromeHealthCache struct {
	mu      sync.Mutex
	entries map[int]navidromeHealthEntry
	ttl     time.Duration
	now     func() time.Time
	probe   func(ctx context.Context, username, password string) bool
	start   func(fn func(context.Context)) bool
}

// configure fills in whatever the caller has not already overridden. It runs
// under the cache's own lock, so the first render on any handler wires it once.
func (c *navidromeHealthCache) configure(h *Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ttl == 0 {
		c.ttl = navidromeHealthTTL
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.probe == nil {
		c.probe = func(ctx context.Context, username, password string) bool {
			return h.checkNavidromeOnline(ctx, username, password)
		}
	}
	if c.start == nil && h.Jobs != nil {
		c.start = h.Jobs.Start
	}
}

// navidromeOnline reports Navidrome reachability for a user without letting the
// probe hold up the render. A user who has never been probed reads as offline on
// the render that starts the first probe, and correctly from the next one on.
func (h *Handler) navidromeOnline(userID int, username, password string) bool {
	c := &h.navidromeHealth
	c.configure(h)

	c.mu.Lock()
	entry := c.entries[userID]
	stale := !entry.refreshing && c.now().Sub(entry.checkedAt) >= c.ttl
	if stale {
		if c.entries == nil {
			c.entries = make(map[int]navidromeHealthEntry)
		}
		entry.refreshing = true
		c.entries[userID] = entry
	}
	start := c.start
	c.mu.Unlock()

	if !stale {
		return entry.online
	}

	run := func(ctx context.Context) { c.refresh(ctx, userID, username, password) }
	if start != nil {
		if start(run) {
			return entry.online
		}
		// The runner is closed, which means shutdown: leave no untracked
		// goroutine behind and let the next render try again.
		c.clearRefreshing(userID)
		return entry.online
	}
	go run(context.Background())

	return entry.online
}

// refresh runs one probe and records its answer. A probe cancelled before it
// answered leaves the entry stale, so the next render schedules another one
// rather than caching an outage that never happened.
func (c *navidromeHealthCache) refresh(ctx context.Context, userID int, username, password string) {
	online := c.probe(ctx, username, password)
	if ctx.Err() != nil {
		c.clearRefreshing(userID)
		return
	}

	c.mu.Lock()
	if c.entries == nil {
		c.entries = make(map[int]navidromeHealthEntry)
	}
	c.entries[userID] = navidromeHealthEntry{online: online, checkedAt: c.now()}
	c.mu.Unlock()
}

func (c *navidromeHealthCache) clearRefreshing(userID int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.entries[userID]; ok {
		entry.refreshing = false
		c.entries[userID] = entry
	}
}
