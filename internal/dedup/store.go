package dedup

import (
	"sync"
	"time"

	"snmptrap-relay/internal/model"
)

type cacheKey struct {
	RuleID  string
	KeyHash string
}

type Store struct {
	mu     sync.Mutex
	states map[cacheKey]*model.DedupState
	limit  int
}

func NewStore(limit int) *Store {
	return &Store{states: map[cacheKey]*model.DedupState{}, limit: limit}
}

func (s *Store) Cleanup(now time.Time) []*model.DedupState {
	s.mu.Lock()
	defer s.mu.Unlock()

	var expired []*model.DedupState
	for k, state := range s.states {
		if state.HoldUntilClear {
			continue
		}
		if !state.ExpiresAt().After(now) {
			expired = append(expired, state)
			delete(s.states, k)
		}
	}
	return expired
}

func (s *Store) Get(ruleID, keyHash string, now time.Time) *model.DedupState {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey{RuleID: ruleID, KeyHash: keyHash}
	state := s.states[key]
	if state == nil {
		return nil
	}
	if !state.HoldUntilClear && !state.ExpiresAt().After(now) {
		delete(s.states, key)
		return nil
	}
	return state
}

func (s *Store) Put(ruleID, keyHash, keyRepr string, event *model.TrapEvent, ttlSeconds int, holdUntilClear bool) *model.DedupState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.limit > 0 && len(s.states) >= s.limit {
		s.evictOldestLocked()
	}

	state := &model.DedupState{
		RuleID:          ruleID,
		KeyHash:         keyHash,
		KeyRepr:         keyRepr,
		FirstEvent:      compactEvent(event),
		FirstSeenAt:     event.ReceivedAt,
		LastSeenAt:      event.ReceivedAt,
		TTLSeconds:      ttlSeconds,
		HoldUntilClear:  holdUntilClear,
		SuppressedCount: 0,
	}
	s.states[cacheKey{RuleID: ruleID, KeyHash: keyHash}] = state
	return state
}

func (s *Store) evictOldestLocked() {
	var oldestKey cacheKey
	var oldest *model.DedupState
	for key, state := range s.states {
		if oldest == nil || state.LastSeenAt.Before(oldest.LastSeenAt) {
			oldestKey = key
			oldest = state
		}
	}
	if oldest != nil {
		delete(s.states, oldestKey)
	}
}

func compactEvent(event *model.TrapEvent) *model.TrapEvent {
	if event == nil {
		return nil
	}
	return &model.TrapEvent{
		ReceivedAt: event.ReceivedAt,
		SourceIP:   event.SourceIP,
		SourcePort: event.SourcePort,
		TrapOID:    event.TrapOID,
	}
}

func (s *Store) Touch(ruleID, keyHash string, event *model.TrapEvent) *model.DedupState {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.states[cacheKey{RuleID: ruleID, KeyHash: keyHash}]
	if state == nil {
		return nil
	}
	state.LastSeenAt = event.ReceivedAt
	state.SuppressedCount++
	return state
}

func (s *Store) Clear(ruleID, keyHash string) *model.DedupState {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey{RuleID: ruleID, KeyHash: keyHash}
	state := s.states[key]
	delete(s.states, key)
	return state
}
