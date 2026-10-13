package browser

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

// TestHelperProcess is not a real test: TestRodCloseKillsOrphanProcess
// re-executes this test binary as a child that just stays alive, standing in
// for a Chrome process whose CDP connection already died.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("CHITCHAT_HELPER_PROCESS") != "1" {
		return
	}
	select {
	case <-time.After(60 * time.Second):
	}
}

func TestRodCloseIdempotentAndBlocksRecovery(t *testing.T) {
	b := &RodBackend{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on never-started backend: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close must be a no-op (worker + fresh path both close): %v", err)
	}
	err := b.withPageRetry(context.Background(), func(page *rod.Page) error { return nil })
	if !errors.Is(err, ErrPageUnavailable) {
		t.Fatalf("closed backend must refuse page work instead of recovering a page, got %v", err)
	}
	if _, err := b.recoverPage(context.Background()); !errors.Is(err, ErrPageUnavailable) {
		t.Fatalf("closed backend must not recreate a page, got %v", err)
	}
}

func TestRodCloseKillsOrphanProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "-test.v")
	cmd.Env = append(os.Environ(), "CHITCHAT_HELPER_PROCESS=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper process: %v", err)
	}
	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()

	b := &RodBackend{proc: cmd, exitCh: exitCh}
	start := time.Now()
	if err := b.Close(); err != nil {
		t.Fatalf("Close must kill the orphaned browser process, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Close took too long: %v", elapsed)
	}
	// ensureProcessExited only returns nil after the process really exited,
	// so reaching here means the "browser" process is gone, not just muted.
	if err := b.Close(); err != nil {
		t.Fatalf("second Close after kill: %v", err)
	}
}

// Stop pressed while Chrome is still launching must abort the launch at
// once (the worker context reaches Browser.Start now). Whether this sandbox
// has a Chrome binary or not, a cancelled context must come back with an
// error quickly — never with a started browser.
func TestRodStartWithCancelledContextFailsFast(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &RodBackend{}
	start := time.Now()
	err := b.Start(ctx, Options{StartURL: "https://app.chitchat.gg/", UserDataDir: t.TempDir()})
	if err == nil {
		_ = b.Close()
		t.Fatal("Start with a cancelled context must fail")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancelled Start took too long to abort: %v", elapsed)
	}
	if cerr := b.Close(); cerr != nil {
		t.Fatalf("Close after aborted Start: %v", cerr)
	}
}

func TestChromeDPCloseIdempotentAndBlocksUse(t *testing.T) {
	b := &ChromeDPBackend{}
	if err := b.Close(); err != nil {
		t.Fatalf("Close on never-started backend: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	var out struct{}
	if err := b.eval(context.Background(), "1", &out); !errors.Is(err, ErrPageUnavailable) {
		t.Fatalf("closed chromedp backend must refuse page work, got %v", err)
	}
}
