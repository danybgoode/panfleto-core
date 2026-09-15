// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

// Package prefetch takes article scraping off the poll path: the poller enqueues new entries of
// crawler feeds, and a bounded pool of workers drains the queue one request per host at a time.
// Its state is in memory only - "fetching" and "found nothing" are derived, never stored (A3), and a
// restart re-derives the queue from recent unread thin entries. Roadmap/01-reading-experience/article-autofetch.
package prefetch // import "miniflux.app/v2/internal/reader/prefetch"

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
	"miniflux.app/v2/internal/reader/autofetch"
	"miniflux.app/v2/internal/reader/filter"
	"miniflux.app/v2/internal/reader/processor"
	"miniflux.app/v2/internal/storage"
	"miniflux.app/v2/internal/urllib"
)

// Entry states a template can show.
const (
	StateFetching = "fetching"
	StateFailed   = "failed"
)

const failedEntriesKept = 10000

type job struct {
	userID, entryID int64
}

// Prefetcher is the queue, its workers and the in-memory state they share.
type Prefetcher struct {
	queue chan job
	hosts *hostGate
	work  func(job) bool

	mu          sync.Mutex
	pending     map[int64]struct{}
	failed      map[int64]struct{}
	failedOrder []int64
}

var instance atomic.Pointer[Prefetcher]

func newPrefetcher(queueSize int, hostDelay time.Duration) *Prefetcher {
	return &Prefetcher{
		queue:   make(chan job, queueSize),
		hosts:   newHostGate(hostDelay),
		pending: make(map[int64]struct{}),
		failed:  make(map[int64]struct{}),
	}
}

// Start launches PREFETCH_WORKERS workers. With zero workers - the upstream default and the kill
// switch - nothing starts, and the processor scrapes inline exactly as Miniflux does.
func Start(store *storage.Storage) {
	workers := config.Opts.PrefetchWorkers()
	if workers == 0 {
		return
	}

	p := newPrefetcher(config.Opts.PrefetchQueueSize(), config.Opts.PrefetchHostDelay())
	p.work = func(j job) bool { return fetchEntry(store, p.hosts, j) }
	instance.Store(p)

	for range workers {
		go p.drain()
	}

	slog.Info("Prefetch started",
		slog.Int("workers", workers),
		slog.Int("queue_size", cap(p.queue)),
		slog.Duration("host_delay", config.Opts.PrefetchHostDelay()),
	)

	go p.recoverRecent(func() ([]*model.Entry, error) { return recentEntries(store, config.Opts.PrefetchRecoveryWindow()) })
}

// Enqueue queues a feed's newly stored entries. It never blocks: a full queue skips the entry, which
// stays one Download press away.
func Enqueue(feed *model.Feed, entries model.Entries) {
	p := instance.Load()
	if p == nil || feed == nil || !feed.Crawler {
		return
	}
	for _, entry := range entries {
		if entry.ID > 0 && autofetch.IsThin(entry.Content) {
			p.enqueue(job{userID: feed.UserID, entryID: entry.ID}, false)
		}
	}
}

// EnqueueRefreshed is Enqueue for a feed refresh. A forced refresh rewrites every existing entry with
// the feed's own content, which upstream re-scrapes inline; with the scrape moved off the poll path,
// those entries have to be queued again or their fetched articles revert to teasers.
func EnqueueRefreshed(feed *model.Feed, allEntries, newEntries model.Entries, forceRefresh bool) {
	Enqueue(feed, entriesToFetch(allEntries, newEntries, forceRefresh))
}

func entriesToFetch(allEntries, newEntries model.Entries, forceRefresh bool) model.Entries {
	if forceRefresh {
		return allEntries
	}
	return newEntries
}

// State reports an entry's derived fetch state: StateFetching, StateFailed, or "".
func State(entryID int64) string {
	p := instance.Load()
	if p == nil {
		return ""
	}
	return p.state(entryID)
}

func (p *Prefetcher) enqueue(j job, quiet bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, queued := p.pending[j.entryID]; queued {
		return true
	}

	select {
	case p.queue <- j:
		p.pending[j.entryID] = struct{}{}
		return true
	default:
		if !quiet {
			slog.Warn("Prefetch queue is full, skipping entry", slog.Int64("entry_id", j.entryID), slog.Int("queue_size", cap(p.queue)))
		}
		return false
	}
}

func (p *Prefetcher) state(entryID int64) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, queued := p.pending[entryID]; queued {
		return StateFetching
	}
	if _, failed := p.failed[entryID]; failed {
		return StateFailed
	}
	return ""
}

func (p *Prefetcher) drain() {
	for j := range p.queue {
		found := p.work(j)

		p.mu.Lock()
		delete(p.pending, j.entryID)
		if found {
			delete(p.failed, j.entryID)
		} else if _, known := p.failed[j.entryID]; !known {
			p.failed[j.entryID] = struct{}{}
			p.failedOrder = append(p.failedOrder, j.entryID)
			if len(p.failedOrder) > failedEntriesKept {
				delete(p.failed, p.failedOrder[0])
				p.failedOrder = p.failedOrder[1:]
			}
		}
		p.mu.Unlock()
	}
}

func (p *Prefetcher) recoverRecent(list func() ([]*model.Entry, error)) {
	entries, err := list()
	if err != nil {
		slog.Error("Prefetch recovery failed", slog.Any("error", err))
		return
	}

	queued, skipped := 0, 0
	for _, entry := range entries {
		if entry.Feed == nil || !entry.Feed.Crawler || !autofetch.IsThin(entry.Content) {
			continue
		}
		if p.enqueue(job{userID: entry.UserID, entryID: entry.ID}, true) {
			queued++
		} else {
			skipped++
		}
	}
	slog.Info("Prefetch recovered recent thin entries", slog.Int("queued", queued), slog.Int("skipped_queue_full", skipped), slog.Int("scanned", len(entries)))
}

func recentEntries(store *storage.Storage, window time.Duration) ([]*model.Entry, error) {
	if window <= 0 {
		return nil, nil
	}
	users, err := store.Users()
	if err != nil {
		return nil, err
	}

	var all []*model.Entry
	since := time.Now().Add(-window)
	for _, user := range users {
		entries, err := store.NewEntryQueryBuilder(user.ID).
			AfterChangedDate(since).
			WithStatuses(model.EntryStatusUnread).
			WithSorting("changed_at", "desc").
			WithLimit(config.Opts.PrefetchQueueSize()).
			GetEntries()
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	return all, nil
}

// fetchEntry runs one entry through the same path as the Download button, and only keeps the result
// if it is longer than what the feed delivered (D7). It reports whether anything was found.
func fetchEntry(store *storage.Storage, hosts *hostGate, j job) bool {
	entry, err := store.NewEntryQueryBuilder(j.userID).WithEntryIDs(j.entryID).GetEntry()
	if err != nil || entry == nil {
		return true
	}
	feed, err := store.NewFeedQueryBuilder(j.userID).WithFeedID(entry.FeedID).GetFeed()
	if err != nil || feed == nil || !feed.Crawler || !autofetch.IsThin(entry.Content) {
		return true
	}
	user, err := store.UserByID(j.userID)
	if err != nil || user == nil {
		return true
	}

	host := urllib.Domain(entry.URL)
	waited := hosts.acquire(host)
	defer hosts.release(host)

	originalLength := autofetch.TextLength(entry.Content)
	fetchErr := processor.ProcessEntryWebPage(feed, entry, user)
	fetchedLength := autofetch.TextLength(entry.Content)

	switch outcomeOf(fetchErr, originalLength, fetchedLength) {
	case outcomeError:
		slog.Info("Prefetch found nothing", slog.Int64("entry_id", entry.ID), slog.String("host", host), slog.Duration("host_wait", waited), slog.Any("error", fetchErr))
		return false
	case outcomeNotLonger:
		slog.Info("Prefetch found nothing longer", slog.Int64("entry_id", entry.ID), slog.String("host", host), slog.Duration("host_wait", waited), slog.Int("text_length", fetchedLength))
		return false
	}

	if err := store.UpdateEntryTitleAndContent(entry); err != nil {
		slog.Error("Prefetch could not store content", slog.Int64("entry_id", entry.ID), slog.Any("error", err))
		return false
	}

	// Inline, the processor re-runs the filters on scraped content (filter_stage after_scrape) and drops
	// a match. The entry is already stored here, so a match is marked read: out of Unread, like a block.
	if blockedAfterFetch(user, feed, entry) {
		if err := store.SetEntriesStatus(user.ID, []int64{entry.ID}, model.EntryStatusRead); err != nil {
			slog.Error("Prefetch could not apply a filter rule", slog.Int64("entry_id", entry.ID), slog.Any("error", err))
		}
		slog.Info("Prefetch content matched a filter rule", slog.Int64("entry_id", entry.ID), slog.String("filter_stage", "after_prefetch"))
	}

	slog.Info("Prefetch stored content", slog.Int64("entry_id", entry.ID), slog.String("host", host), slog.Duration("host_wait", waited), slog.Int("text_length", fetchedLength))
	return true
}

type outcome int

const (
	outcomeStore outcome = iota
	outcomeError
	outcomeNotLonger
)

// outcomeOf is D7: an automatic fetch only replaces the feed's content with something longer.
func outcomeOf(fetchErr error, originalLength, fetchedLength int) outcome {
	switch {
	case fetchErr != nil:
		return outcomeError
	case fetchedLength <= originalLength:
		return outcomeNotLonger
	default:
		return outcomeStore
	}
}

func blockedAfterFetch(user *model.User, feed *model.Feed, entry *model.Entry) bool {
	blockRules := filter.ParseRules(user.BlockFilterEntryRules, feed.BlockFilterEntryRules)
	allowRules := filter.ParseRules(user.KeepFilterEntryRules, feed.KeepFilterEntryRules)
	return filter.IsBlockedEntry(blockRules, allowRules, feed, entry)
}

// hostGate allows one request per host at a time, spaced by PREFETCH_HOST_DELAY.
type hostGate struct {
	delay time.Duration
	now   func() time.Time
	sleep func(time.Duration)

	mu    sync.Mutex
	slots map[string]*hostSlot
}

type hostSlot struct {
	mu    sync.Mutex
	users int
	last  time.Time
}

func newHostGate(delay time.Duration) *hostGate {
	return &hostGate{delay: delay, now: time.Now, sleep: time.Sleep, slots: make(map[string]*hostSlot)}
}

// acquire blocks until host is free and its delay has passed, and returns how long that took.
//
// It returns with the host's slot mutex HELD; the caller owns it until it calls release, which unlocks
// it. Always pair them with `defer release(host)` straight after acquire.
func (g *hostGate) acquire(host string) time.Duration {
	start := g.now()

	g.mu.Lock()
	slot, found := g.slots[host]
	if !found {
		slot = &hostSlot{}
		g.slots[host] = slot
	}
	slot.users++
	g.mu.Unlock()

	slot.mu.Lock()
	if !slot.last.IsZero() {
		if wait := slot.last.Add(g.delay).Sub(g.now()); wait > 0 {
			g.sleep(wait)
		}
	}
	return g.now().Sub(start)
}

// release unlocks the slot mutex acquire left held (see acquire).
func (g *hostGate) release(host string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	slot := g.slots[host]
	slot.last = g.now()
	slot.users--
	slot.mu.Unlock()

	// Forget idle hosts whose delay has passed, so a year of distinct hosts doesn't pile up.
	if slot.users == 0 && len(g.slots) > 1000 {
		for name, idle := range g.slots {
			if idle.users == 0 && g.now().Sub(idle.last) > g.delay {
				delete(g.slots, name)
			}
		}
	}
}
