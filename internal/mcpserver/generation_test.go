package mcpserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestGenerationManagerImmediateAndPending(t *testing.T) {
	immediate := newGenerationManager(context.Background(), time.Second)
	defer immediate.Close()
	start, err := immediate.Start(context.Background(), func(context.Context) (generationResult, error) {
		return generationResult{Text: "done", Files: []string{"/tmp/a.png"}}, nil
	})
	if err != nil || start.Pending || start.ID != "" || start.Result.Text != "done" {
		t.Fatalf("immediate start = %+v, %v", start, err)
	}

	m := newGenerationManager(context.Background(), 15*time.Millisecond)
	defer m.Close()
	release := make(chan struct{})
	start, err = m.Start(context.Background(), func(ctx context.Context) (generationResult, error) {
		select {
		case <-release:
			return generationResult{Text: "finished"}, nil
		case <-ctx.Done():
			return generationResult{}, ctx.Err()
		}
	})
	if err != nil || !start.Pending || start.ID == "" {
		t.Fatalf("pending start = %+v, %v", start, err)
	}
	close(release)

	got, pending, err := m.Wait(context.Background(), start.ID)
	if err != nil || pending || got.Text != "finished" {
		t.Fatalf("Wait = %+v, %v, %v", got, pending, err)
	}
}

func TestGenerationManagerInitialCancellationCancelsJob(t *testing.T) {
	m := newGenerationManager(context.Background(), time.Second)
	defer m.Close()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-started
		cancel()
	}()
	_, err := m.Start(ctx, func(ctx context.Context) (generationResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return generationResult{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("job was not cancelled")
	}
}

func TestGenerationManagerWaitCancellationKeepsJob(t *testing.T) {
	m := newGenerationManager(context.Background(), 20*time.Millisecond)
	defer m.Close()
	release := make(chan struct{})
	var cancelled atomic.Bool
	start, err := m.Start(context.Background(), func(ctx context.Context) (generationResult, error) {
		select {
		case <-release:
			return generationResult{Text: "ok"}, nil
		case <-ctx.Done():
			cancelled.Store(true)
			return generationResult{}, ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	// Start returned immediately, so use a fresh long-running job for the cancellation check.
	if !start.Pending {
		t.Fatal("expected a pending job")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = m.Wait(ctx, start.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v", err)
	}
	close(release)
	got, pending, err := m.Wait(context.Background(), start.ID)
	if err != nil || pending || got.Text != "ok" || cancelled.Load() {
		t.Fatalf("job after caller cancellation = %+v, %v, %v; cancelled=%v", got, pending, err, cancelled.Load())
	}
}

func TestGenerationManagerCloseCancelsJobs(t *testing.T) {
	m := newGenerationManager(context.Background(), time.Millisecond)
	cancelled := make(chan struct{})
	start, err := m.Start(context.Background(), func(ctx context.Context) (generationResult, error) {
		<-ctx.Done()
		close(cancelled)
		return generationResult{}, ctx.Err()
	})
	if err != nil || !start.Pending {
		t.Fatalf("Start = %+v, %v", start, err)
	}
	m.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the job")
	}
}
