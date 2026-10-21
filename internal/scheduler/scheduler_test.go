package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimitsConcurrency(t *testing.T) {
	s := New(3)
	var cur, peak atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := s.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(2 * time.Millisecond)
			cur.Add(-1)
			release()
			release() // idempotent
		}()
	}
	wg.Wait()
	if peak.Load() != 3 {
		t.Fatalf("peak = %d, want 3", peak.Load())
	}
	if _, running, waiting := s.Stats(); running != 0 || waiting != 0 {
		t.Fatalf("running=%d waiting=%d", running, waiting)
	}
}

func TestAcquireCancel(t *testing.T) {
	s := New(1)
	release, _ := s.Acquire(context.Background())
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.Acquire(ctx); err == nil {
		t.Fatal("expected timeout")
	}
	if _, _, waiting := s.Stats(); waiting != 0 {
		t.Fatalf("waiting = %d", waiting)
	}
}

func TestSetSlotsWakesWaiters(t *testing.T) {
	s := New(1)
	r1, _ := s.Acquire(context.Background())
	defer r1()
	done := make(chan struct{})
	go func() {
		r2, err := s.Acquire(context.Background())
		if err == nil {
			r2()
		}
		close(done)
	}()
	time.Sleep(5 * time.Millisecond)
	s.SetSlots(2)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiter not woken")
	}
}
