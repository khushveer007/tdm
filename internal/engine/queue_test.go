package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestQueueProcessor_PriorityAndBlocking(t *testing.T) {
	startCh := make(chan uuid.UUID, 10)

	var doneMu sync.Mutex
	completeSignals := make(map[uuid.UUID]chan struct{})

	startFn := func(id uuid.UUID) error {
		startCh <- id

		doneMu.Lock()
		done := make(chan struct{})
		completeSignals[id] = done
		doneMu.Unlock()

		<-done
		return nil
	}

	qp := NewQueueProcessor(1, startFn)

	idLow := uuid.New()
	qp.Enqueue(idLow, 1)

	var firstStarted uuid.UUID
	select {
	case firstStarted = <-startCh:
		if firstStarted != idLow {
			t.Fatalf("Expected first download to be %v, got %v", idLow, firstStarted)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("First download didn't start in time")
	}

	idHigh := uuid.New()
	qp.Enqueue(idHigh, 2)

	select {
	case unexpected := <-startCh:
		t.Fatalf("Unexpected download started before first completed: %v", unexpected)
	case <-time.After(50 * time.Millisecond):
	}

	doneMu.Lock()
	close(completeSignals[firstStarted])
	delete(completeSignals, firstStarted)
	doneMu.Unlock()

	select {
	case secondStarted := <-startCh:
		if secondStarted != idHigh {
			t.Fatalf("Expected high priority download %v to start, got %v", idHigh, secondStarted)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Second download didn't start after first completed")
	}

	doneMu.Lock()
	for id, ch := range completeSignals {
		close(ch)
		delete(completeSignals, id)
	}
	doneMu.Unlock()
}

// TestQueueProcessor_MultipleConcurrent tests multiple concurrent downloads
func TestQueueProcessor_MultipleConcurrent(t *testing.T) {
	startCh := make(chan uuid.UUID, 10)
	var doneMu sync.Mutex
	completeSignals := make(map[uuid.UUID]chan struct{})

	startFn := func(id uuid.UUID) error {
		startCh <- id

		doneMu.Lock()
		done := make(chan struct{})
		completeSignals[id] = done
		doneMu.Unlock()

		<-done
		return nil
	}

	qp := NewQueueProcessor(2, startFn)

	idLow := uuid.New()
	idMed := uuid.New()
	idHigh := uuid.New()

	qp.Enqueue(idLow, 1)
	qp.Enqueue(idMed, 2)
	qp.Enqueue(idHigh, 3)

	started := make(map[uuid.UUID]bool)
	for i := 0; i < 2; i++ {
		select {
		case id := <-startCh:
			started[id] = true
		case <-time.After(100 * time.Millisecond):
			t.Fatalf("Didn't get %d started downloads in time", 2)
		}
	}

	// Verify which ones started
	if !started[idMed] || !started[idHigh] {
		t.Logf("Started downloads: %v", started)
		t.Errorf("Expected medium and high priority downloads to start")
	}

	select {
	case id := <-startCh:
		t.Fatalf("Unexpected third download started: %v", id)
	case <-time.After(50 * time.Millisecond):
		// This is expected
	}

	doneMu.Lock()
	if ch, ok := completeSignals[idHigh]; ok {
		close(ch)
		delete(completeSignals, idHigh)
	}
	doneMu.Unlock()

	select {
	case id := <-startCh:
		if id != idLow {
			t.Fatalf("Expected low priority download to start, got %v", id)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Low priority download didn't start after slot freed")
	}

	// Clean up
	doneMu.Lock()
	for id, ch := range completeSignals {
		close(ch)
		delete(completeSignals, id)
	}
	doneMu.Unlock()
}
