package main

import (
	"context"
	"testing"
)

// Stop reaches fresh windows through the freshCancels registry from the
// moment they open (even while they wait for a manual login, before any
// live-worker handover exists). This pins the registry StopBot drains.
func TestFreshCancelRegistryReachesWaitingWindow(t *testing.T) {
	a := &App{}
	ctx, cancel := context.WithCancel(context.Background())
	a.registerFreshCancel(42, cancel)

	a.freshMu.Lock()
	registered, ok := a.freshCancels[42]
	a.freshMu.Unlock()
	if !ok || registered == nil {
		t.Fatal("fresh window cancel was not registered")
	}

	// Exactly what StopBot does with every registered fresh cancel.
	registered()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stopping via the registry did not cancel the fresh window context")
	}

	a.unregisterFreshCancel(42)
	a.freshMu.Lock()
	remaining := len(a.freshCancels)
	a.freshMu.Unlock()
	if remaining != 0 {
		t.Fatalf("registry should be empty after unregister, has %d", remaining)
	}
}
