package server

import (
	"sync"
	"testing"
	"time"
)

func TestClaudePreparationFailureTrackerConcurrent(t *testing.T) {
	var tracker claudePreparationFailureTracker
	now := time.Now()
	const workers = 64
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			if got := tracker.record("client-hash", "structure-hash", "invalid_message_role", now); got < 1 {
				t.Errorf("count=%d", got)
			}
		}()
	}
	group.Wait()
	if got := tracker.record("client-hash", "structure-hash", "invalid_message_role", now); got != workers+1 {
		t.Fatalf("count=%d want=%d", got, workers+1)
	}
	if got := tracker.record("client-hash", "structure-hash", "invalid_message_role", now.Add(claudePreparationFailureWindow+time.Second)); got != 1 {
		t.Fatalf("expired entry count=%d want=1", got)
	}
}
