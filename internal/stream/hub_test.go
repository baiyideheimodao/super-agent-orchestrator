package stream

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPublishIncrementsSequenceAndReplays(t *testing.T) {
	h := NewHub()
	first := h.Publish("run.created", "run-1", "", map[string]int{"n": 1})
	second := h.Publish("agent.started", "run-1", "agent-1", nil)
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("sequence = %d,%d; want 1,2", first.Seq, second.Seq)
	}

	ch, replay, cancel := h.Subscribe(1)
	defer cancel()
	if len(replay) != 1 || replay[0].Seq != 2 {
		t.Fatalf("replay = %+v, want only seq 2", replay)
	}
	third := h.Publish("tick", "", "", nil)
	select {
	case got := <-ch:
		if got.Seq != third.Seq {
			t.Fatalf("live seq = %d, want %d", got.Seq, third.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event")
	}
}

func TestTickerEmitsIncreasingSequence(t *testing.T) {
	h := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Start(ctx, 20*time.Millisecond)

	ch, _, unsubscribe := h.Subscribe(0)
	defer unsubscribe()

	deadline := time.After(2 * time.Second)
	var last int64
	for got := 0; got < 2; {
		select {
		case event := <-ch:
			if event.Type != "tick" {
				continue
			}
			if event.Seq <= last {
				t.Fatalf("seq did not increase: %d then %d", last, event.Seq)
			}
			last = event.Seq
			got++
		case <-deadline:
			t.Fatal("timed out waiting for ticks")
		}
	}
}

func TestConcurrentPublishIsUnique(t *testing.T) {
	h := NewHub()
	const workers, perWorker = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				h.Publish("test", "", "", nil)
			}
		}()
	}
	wg.Wait()
	if h.CurrentSeq() != workers*perWorker {
		t.Fatalf("seq = %d, want %d", h.CurrentSeq(), workers*perWorker)
	}
}
