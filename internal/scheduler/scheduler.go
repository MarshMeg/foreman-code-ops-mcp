// Package scheduler limits concurrent generations to the generator's slots.
package scheduler

import (
	"context"
	"sync"
)

// Scheduler is a counting semaphore with queue statistics.
type Scheduler struct {
	mu      sync.Mutex
	slots   int
	running int
	waiting int
	wake    chan struct{}
}

// New creates a scheduler with n slots (at least 1).
func New(n int) *Scheduler {
	if n < 1 {
		n = 1
	}
	return &Scheduler{slots: n, wake: make(chan struct{})}
}

// Acquire blocks until a slot is free or ctx is done. The returned function releases the slot.
func (s *Scheduler) Acquire(ctx context.Context) (func(), error) {
	s.mu.Lock()
	s.waiting++
	for s.running >= s.slots {
		wake := s.wake
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.waiting--
			s.mu.Unlock()
			return nil, ctx.Err()
		case <-wake:
		}
		s.mu.Lock()
	}
	s.waiting--
	s.running++
	s.mu.Unlock()

	var once sync.Once
	return func() { once.Do(s.release) }, nil
}

func (s *Scheduler) release() {
	s.mu.Lock()
	s.running--
	s.broadcast()
	s.mu.Unlock()
}

// broadcast wakes all waiters; the caller holds s.mu.
func (s *Scheduler) broadcast() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// SetSlots changes capacity (e.g. after reconciling with llama-server).
func (s *Scheduler) SetSlots(n int) {
	if n < 1 {
		n = 1
	}
	s.mu.Lock()
	s.slots = n
	s.broadcast()
	s.mu.Unlock()
}

// Stats reports slots, running and waiting tasks.
func (s *Scheduler) Stats() (slots, running, waiting int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slots, s.running, s.waiting
}
