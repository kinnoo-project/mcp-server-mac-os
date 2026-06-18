// store_test.go verifies the token store's three guarantees: a token round-trips
// to exactly the payload it was minted for, a token is consumed one-shot, and an
// entry past its TTL is no longer retrievable. A concurrency test (run under
// -race) confirms the store is safe under parallel staging and consumption.
package transaction

import (
	"sync"
	"testing"
	"time"
)

// TestStore_RoundTrip confirms Put returns a prefixed token and Take returns the
// exact payload that was stashed.
func TestStore_RoundTrip(t *testing.T) {
	s := NewStore[string]("req_", time.Minute)
	token, err := s.Put("hello")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(token) <= len("req_") || token[:4] != "req_" {
		t.Fatalf("token %q should carry the req_ prefix and a random body", token)
	}
	got, ok := s.Take(token)
	if !ok {
		t.Fatal("Take should find a freshly stored token")
	}
	if got != "hello" {
		t.Errorf("Take returned %q, want %q", got, "hello")
	}
}

// TestStore_UniqueTokens confirms two Puts never collide, so one staged plan can
// never be mistaken for another.
func TestStore_UniqueTokens(t *testing.T) {
	s := NewStore[int]("req_", time.Minute)
	a, err := s.Put(1)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	b, err := s.Put(2)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if a == b {
		t.Fatalf("expected distinct tokens, both were %q", a)
	}
}

// TestStore_OneShotConsume confirms a token works exactly once: the second Take
// of the same token misses, which is what prevents a staged action from being
// committed twice.
func TestStore_OneShotConsume(t *testing.T) {
	s := NewStore[string]("req_", time.Minute)
	token, err := s.Put("payload")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, ok := s.Take(token); !ok {
		t.Fatal("first Take should succeed")
	}
	if _, ok := s.Take(token); ok {
		t.Fatal("second Take of a consumed token must fail")
	}
}

// TestStore_Expiry confirms an entry older than the TTL is no longer retrievable.
func TestStore_Expiry(t *testing.T) {
	s := NewStore[string]("req_", time.Millisecond)
	token, err := s.Put("stale")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.Take(token); ok {
		t.Fatal("Take of an expired token must fail")
	}
}

// TestStore_UnknownToken confirms a never-issued token reports a clean miss
// rather than panicking or returning a bogus payload.
func TestStore_UnknownToken(t *testing.T) {
	s := NewStore[string]("req_", time.Minute)
	if _, ok := s.Take("req_does-not-exist"); ok {
		t.Fatal("Take of an unknown token must fail")
	}
}

// TestStore_PutPurgesExpiredEntries confirms that staging a new token also
// sweeps away already-expired ones, so a workload that stages many tokens and
// never consumes them (abandoned plans nobody executes or undoes) doesn't
// grow the store's memory without bound.
func TestStore_PutPurgesExpiredEntries(t *testing.T) {
	s := NewStore[string]("req_", time.Millisecond)
	for i := 0; i < 5; i++ {
		if _, err := s.Put("stale"); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	time.Sleep(5 * time.Millisecond)

	fresh, err := s.Put("fresh")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	s.mu.Lock()
	n := len(s.entries)
	s.mu.Unlock()
	if n != 1 {
		t.Fatalf("expected the 5 stale entries to be purged by the next Put, leaving 1, got %d", n)
	}
	if _, ok := s.Take(fresh); !ok {
		t.Fatal("the fresh entry inserted by the purging Put should still be retrievable")
	}
}

// TestStore_Concurrent stages and consumes from many goroutines at once; under
// -race this asserts the locking is sound. Every successfully staged token must
// be consumable exactly once.
func TestStore_Concurrent(t *testing.T) {
	s := NewStore[int]("req_", time.Minute)
	const workers = 50

	tokens := make(chan string, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tok, err := s.Put(n)
			if err != nil {
				t.Errorf("Put: %v", err)
				return
			}
			tokens <- tok
		}(i)
	}
	wg.Wait()
	close(tokens)

	consumed := 0
	for tok := range tokens {
		if _, ok := s.Take(tok); ok {
			consumed++
		}
	}
	if consumed != workers {
		t.Fatalf("expected to consume all %d staged tokens, got %d", workers, consumed)
	}
}
