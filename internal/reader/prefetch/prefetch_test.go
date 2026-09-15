// SPDX-FileCopyrightText: Copyright The Miniflux Authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package prefetch // import "miniflux.app/v2/internal/reader/prefetch"

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"miniflux.app/v2/internal/config"
	"miniflux.app/v2/internal/model"
)

func setConfig(t *testing.T) {
	t.Helper()
	t.Setenv("FETCH_THIN_CONTENT_THRESHOLD", "100")
	opts, err := config.NewConfigParser().ParseEnvironmentVariables()
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	config.Opts = opts
}

const (
	thin = "<p>teaser</p>"
)

var full = "<p>" + strings.Repeat("a", 500) + "</p>"

func install(t *testing.T, p *Prefetcher) {
	t.Helper()
	instance.Store(p)
	t.Cleanup(func() { instance.Store(nil) })
}

func TestQueueIsBoundedAndSkipsWhenFull(t *testing.T) {
	p := newPrefetcher(2, 0)
	for id := int64(1); id <= 2; id++ {
		if !p.enqueue(job{userID: 1, entryID: id}, false) {
			t.Fatalf("entry %d should fit in a queue of two", id)
		}
	}
	if p.enqueue(job{userID: 1, entryID: 3}, false) {
		t.Fatal("a full queue must skip the entry rather than block the poll")
	}
	if state := p.state(3); state != "" {
		t.Fatalf("a skipped entry is not fetching, got %q", state)
	}
	if len(p.queue) != 2 {
		t.Fatalf("expected two queued jobs, got %d", len(p.queue))
	}
}

func TestQueueDoesNotDuplicateAPendingEntry(t *testing.T) {
	p := newPrefetcher(10, 0)
	p.enqueue(job{userID: 1, entryID: 7}, false)
	p.enqueue(job{userID: 1, entryID: 7}, false)
	if len(p.queue) != 1 {
		t.Fatalf("expected one job for one entry, got %d", len(p.queue))
	}
}

func TestStateMovesFromFetchingToFailedOrNothing(t *testing.T) {
	p := newPrefetcher(10, 0)
	results := map[int64]bool{1: true, 2: false}
	done := make(chan struct{}, 2)
	p.work = func(j job) bool {
		defer func() { done <- struct{}{} }()
		return results[j.entryID]
	}
	install(t, p)

	release := make(chan struct{})
	work := p.work
	p.work = func(j job) bool { <-release; return work(j) }

	p.enqueue(job{userID: 1, entryID: 1}, false)
	p.enqueue(job{userID: 1, entryID: 2}, false)
	if State(1) != StateFetching || State(2) != StateFetching {
		t.Fatalf("queued entries must read as fetching, got %q and %q", State(1), State(2))
	}

	go p.drain()
	close(release)
	<-done
	<-done
	close(p.queue)

	deadline := time.Now().Add(time.Second)
	for (State(1) != "" || State(2) != StateFailed) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if State(1) != "" {
		t.Fatalf("an entry whose content arrived shows neither state, got %q", State(1))
	}
	if State(2) != StateFailed {
		t.Fatalf("an entry whose chain found nothing must read as failed, got %q", State(2))
	}
}

func TestFailedSetIsBounded(t *testing.T) {
	p := newPrefetcher(failedEntriesKept+10, 0)
	p.work = func(job) bool { return false }
	for id := int64(1); id <= failedEntriesKept+5; id++ {
		p.enqueue(job{userID: 1, entryID: id}, false)
	}
	close(p.queue)
	p.drain()

	if len(p.failed) != failedEntriesKept || len(p.failedOrder) != failedEntriesKept {
		t.Fatalf("expected the failed set capped at %d, got %d/%d", failedEntriesKept, len(p.failed), len(p.failedOrder))
	}
	if p.state(1) != "" || p.state(failedEntriesKept+5) != StateFailed {
		t.Fatal("the oldest failures are forgotten first")
	}
}

func TestEnqueueOnlyQueuesThinEntriesOfCrawlerFeeds(t *testing.T) {
	setConfig(t)
	p := newPrefetcher(10, 0)
	install(t, p)

	entries := model.Entries{{ID: 1, Content: thin}, {ID: 2, Content: full}, {ID: 0, Content: thin}}
	Enqueue(&model.Feed{UserID: 1, Crawler: false}, entries)
	if len(p.queue) != 0 {
		t.Fatalf("a feed with the crawler off must enqueue nothing, got %d", len(p.queue))
	}

	Enqueue(&model.Feed{UserID: 1, Crawler: true}, entries)
	if len(p.queue) != 1 || State(1) != StateFetching {
		t.Fatalf("expected only the stored thin entry queued, got %d jobs", len(p.queue))
	}
}

func TestEnqueueAndStateAreInertWhenPrefetchIsOff(t *testing.T) {
	setConfig(t)
	instance.Store(nil)
	Enqueue(&model.Feed{UserID: 1, Crawler: true}, model.Entries{{ID: 1, Content: thin}})
	if State(1) != "" {
		t.Fatal("with PREFETCH_WORKERS=0 there is no prefetcher and no state")
	}
}

func TestRecoveryRequeuesRecentThinCrawlerEntries(t *testing.T) {
	setConfig(t)
	p := newPrefetcher(10, 0)

	p.recoverRecent(func() ([]*model.Entry, error) {
		return []*model.Entry{
			{ID: 1, UserID: 2, Content: thin, Feed: &model.Feed{Crawler: true}},
			{ID: 2, UserID: 2, Content: full, Feed: &model.Feed{Crawler: true}},
			{ID: 3, UserID: 2, Content: thin, Feed: &model.Feed{Crawler: false}},
			{ID: 4, UserID: 3, Content: thin, Feed: nil},
		}, nil
	})

	if len(p.queue) != 1 {
		t.Fatalf("expected one recovered job, got %d", len(p.queue))
	}
	if j := <-p.queue; j.entryID != 1 || j.userID != 2 {
		t.Fatalf("expected entry 1 for user 2, got %+v", j)
	}

	p.recoverRecent(func() ([]*model.Entry, error) { return nil, errors.New("db down") })
}

func TestHostGateSerialisesOneHostAndSpacesRequests(t *testing.T) {
	gate := newHostGate(50 * time.Millisecond)

	var mu sync.Mutex
	active, maxActive := 0, 0
	var starts []time.Time
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.acquire("www.nytimes.com")
			mu.Lock()
			active++
			maxActive = max(maxActive, active)
			starts = append(starts, time.Now())
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			active--
			mu.Unlock()
			gate.release("www.nytimes.com")
		}()
	}
	wg.Wait()

	if maxActive != 1 {
		t.Fatalf("expected per-host concurrency of 1, saw %d at once", maxActive)
	}
	for i := 1; i < len(starts); i++ {
		if gap := starts[i].Sub(starts[i-1]); gap < 50*time.Millisecond {
			t.Fatalf("requests to one host must be at least the delay apart, got %s", gap)
		}
	}
}

func TestHostGateDoesNotHoldBackOtherHosts(t *testing.T) {
	gate := newHostGate(time.Hour)
	gate.acquire("a.example")
	gate.release("a.example")

	done := make(chan struct{})
	go func() {
		gate.acquire("b.example")
		gate.release("b.example")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a delay on one host must not block another")
	}
}

func TestAForcedRefreshRequeuesEveryEntryNotOnlyNewOnes(t *testing.T) {
	setConfig(t)
	p := newPrefetcher(10, 0)
	install(t, p)

	all := model.Entries{{ID: 1, Content: thin}, {ID: 2, Content: thin}, {ID: 3, Content: thin}}
	fresh := model.Entries{all[2]}

	EnqueueRefreshed(&model.Feed{UserID: 1, Crawler: true}, all, fresh, false)
	if len(p.queue) != 1 {
		t.Fatalf("a normal poll queues only new entries, got %d", len(p.queue))
	}

	// A forced refresh overwrote entries 1 and 2 with the feed's teaser; they must be fetched again.
	EnqueueRefreshed(&model.Feed{UserID: 1, Crawler: true}, all, fresh, true)
	if len(p.queue) != 3 || State(1) != StateFetching || State(2) != StateFetching {
		t.Fatalf("a forced refresh must queue every rewritten entry, got %d jobs", len(p.queue))
	}
}

func TestAnAutomaticFetchOnlyStoresLongerContent(t *testing.T) {
	if outcomeOf(errors.New("403"), 100, 0) != outcomeError {
		t.Error("a failed fetch stores nothing")
	}
	if outcomeOf(nil, 300, 300) != outcomeNotLonger || outcomeOf(nil, 300, 120) != outcomeNotLonger {
		t.Error("content no longer than the feed's must not replace it (D7)")
	}
	if outcomeOf(nil, 120, 4000) != outcomeStore {
		t.Error("longer content is stored")
	}
}

func TestFilterRulesRunOnPrefetchedContent(t *testing.T) {
	user := &model.User{BlockFilterEntryRules: "EntryContent=(?i)sponsored content"}
	feed := &model.Feed{}
	if !blockedAfterFetch(user, feed, &model.Entry{Title: "A deal", Content: "<p>This is sponsored content from a brand.</p>"}) {
		t.Error("a block rule on the fetched content must match, as it does after an inline scrape")
	}
	if blockedAfterFetch(user, feed, &model.Entry{Title: "A story", Content: "<p>An ordinary article.</p>"}) {
		t.Error("an ordinary article must not match")
	}

	keep := &model.User{KeepFilterEntryRules: "EntryContent=(?i)golang"}
	if !blockedAfterFetch(keep, feed, &model.Entry{Content: "<p>Nothing relevant.</p>"}) {
		t.Error("a keep rule the fetched content doesn't match blocks it")
	}
}

func TestRecoveryLogsOnceWhenTheQueueIsFull(t *testing.T) {
	setConfig(t)
	p := newPrefetcher(1, 0)
	p.recoverRecent(func() ([]*model.Entry, error) {
		return []*model.Entry{
			{ID: 1, UserID: 2, Content: thin, Feed: &model.Feed{Crawler: true}},
			{ID: 2, UserID: 2, Content: thin, Feed: &model.Feed{Crawler: true}},
			{ID: 3, UserID: 2, Content: thin, Feed: &model.Feed{Crawler: true}},
		}, nil
	})
	if len(p.queue) != 1 {
		t.Fatalf("recovery respects the queue bound, got %d", len(p.queue))
	}
}
