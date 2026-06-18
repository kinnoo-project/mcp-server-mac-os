// Package transaction provides the two-phase staging substrate for mutating
// operations: a thread-safe, expiring registry that maps an opaque random token
// to a stashed payload.
//
// # Architecture role
//
// MCP tools run on stateless request loops, but a safe mutation needs two
// separate calls — one to *stage* a validated, fully-resolved plan and a later
// one to *commit* (or to *undo*) it. The gap between those calls is bridged by a
// token: staging stashes the plan here and hands the caller a token; committing
// presents the token to retrieve and run exactly what was staged. Routing the
// executable payload through this server-side store (never through the model's
// context) is what guarantees the model cannot alter what actually runs between
// the two phases — the integrity property required by
// .claude/rules/transactional-state.md.
//
// This package is deliberately payload-agnostic: it is parameterised over the
// payload type and knows only about tokens, locking, and expiry — not about
// commands, the engine, or MCP. Keeping it free of any internal dependency lets
// the server reuse the same primitive for both the staged-plan registry and the
// undo journal, and keeps the dependency graph acyclic.
package transaction

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// tokenBytes is the number of random bytes behind each token. 16 bytes (128
// bits) is far beyond guessable, so a token doubles as an unforgeable capability
// to act on the staged payload.
const tokenBytes = 16

// entry pairs a stashed payload with the instant it was created, so reads can
// lazily discard anything older than the store's TTL.
type entry[T any] struct {
	payload T
	created time.Time
}

// Store is a thread-safe registry mapping freshly minted, prefixed random tokens
// to payloads of type T. Entries expire after a fixed TTL and are consumed
// one-shot: a successful Take both returns and removes the entry, so a given
// token can drive its action exactly once.
//
// A single Store is safe for concurrent use by many request handlers.
type Store[T any] struct {
	mu      sync.Mutex
	entries map[string]entry[T]
	ttl     time.Duration
	prefix  string
}

// NewStore constructs an empty Store whose tokens carry the given prefix (for
// example "req_" for staged plans or "undo_" for the undo journal) and whose
// entries expire after ttl. The prefix is purely a human-readable aid for
// telling the two token namespaces apart in logs and messages; expiry is what
// bounds how long a stashed payload remains actionable.
func NewStore[T any](prefix string, ttl time.Duration) *Store[T] {
	return &Store[T]{
		entries: make(map[string]entry[T]),
		ttl:     ttl,
		prefix:  prefix,
	}
}

// Put stashes payload under a newly generated, unguessable token and returns the
// token. The only error path is a failure of the system's secure random source,
// which is treated as fatal to the operation rather than papered over with a
// weak token.
func (s *Store[T]) Put(payload T) (string, error) {
	token, err := s.mintToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[token] = entry[T]{payload: payload, created: time.Now()}
	return token, nil
}

// Take looks up the payload for a token and, on a hit, removes it before
// returning so the token cannot be reused. A token that is unknown, already
// consumed, or older than the TTL reports ok=false (and the expired entry is
// purged). The zero value of T is returned alongside ok=false.
func (s *Store[T]) Take(token string) (payload T, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, found := s.entries[token]
	if !found {
		var zero T
		return zero, false
	}
	// A consumed-or-expired token is always removed: expiry should not leave
	// dangling state, and consumption is one-shot.
	delete(s.entries, token)
	if time.Since(e.created) > s.ttl {
		var zero T
		return zero, false
	}
	return e.payload, true
}

// mintToken returns prefix + hex(random bytes). It is unexported because callers
// only ever obtain tokens via Put, which also stores the payload — there is no
// legitimate reason to mint a token that maps to nothing.
func (s *Store[T]) mintToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("transaction: could not generate secure token: %w", err)
	}
	return s.prefix + hex.EncodeToString(buf), nil
}
