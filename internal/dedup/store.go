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
}

func NewStore() *Store {
	return &Store{states: map[cacheKey]*model.DedupState{}}
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

	state := &model.DedupState{
		RuleID:          ruleID,
		KeyHash:         keyHash,
		KeyRepr:         keyRepr,
		FirstEvent:      event,
		FirstSeenAt:     event.ReceivedAt,
		LastSeenAt:      event.ReceivedAt,
		TTLSeconds:      ttlSeconds,
		HoldUntilClear:  holdUntilClear,
		SuppressedCount: 0,
	}
	s.states[cacheKey{RuleID: ruleID, KeyHash: keyHash}] = state
	return state
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
