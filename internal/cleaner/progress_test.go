package cleaner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmptyOperationsReportCompletion(t *testing.T) {
	root := fixtureRoot(t)
	var events []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	plan, err := Scan(ctx, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertProgressEnd(t, events, "scan", 0, 0, "done")
	events = nil
	if _, err := Execute(ctx, plan, Quarantine); err != nil {
		t.Fatal(err)
	}
	assertProgressEnd(t, events, "clean", 0, 0, "done")
}

func TestProgressCountsCandidatesAndSkippedRestore(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("first"))
	putFixture(t, root, "nested/.DS_Store", []byte("second"))
	var events []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	plan, err := Scan(ctx, root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertProgressEnd(t, events, "scan", 3, 0, "done")
	if events[len(events)-1].Matches != 2 {
		t.Fatalf("scan matches = %d", events[len(events)-1].Matches)
	}
	events = nil
	result, err := Execute(ctx, plan, Quarantine)
	if err != nil || len(result.Completed) != 2 {
		t.Fatalf("quarantine = %#v, %v", result, err)
	}
	assertProgressEnd(t, events, "clean", 2, 2, "done")
	putFixture(t, root, ".DS_Store", []byte("conflict data"))
	events = nil
	result, err = Restore(ctx, result.Session)
	if err != nil || len(result.Completed) != 1 || len(result.Issues) != 1 {
		t.Fatalf("restore = %#v, %v", result, err)
	}
	assertProgressEnd(t, events, "restore", 2, 2, "done")
	assertFixture(t, root, ".DS_Store", []byte("conflict data"))
}

func TestSnapshotChunksDoNotInflateVisitedCount(t *testing.T) {
	root := fixtureRoot(t)
	data := bytes.Repeat([]byte("metadata"), 64*1024)
	putFixture(t, root, ".DS_Store", data)
	var events []Progress
	ctx := WithProgress(context.Background(), func(p Progress) { events = append(events, p) })
	if _, err := Scan(ctx, root, Options{}); err != nil {
		t.Fatal(err)
	}
	assertProgressEnd(t, events, "scan", 1, 0, "done")
	last := events[len(events)-1]
	if last.BytesDone != int64(len(data)) || last.Matches != 1 {
		t.Fatalf("final verification progress = %#v", last)
	}
}

func TestProgressCancellationKeepsActualCounters(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("metadata"))
	plan := scanFixture(t, root, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	var events []Progress
	ctx = WithProgress(ctx, func(p Progress) {
		events = append(events, p)
		if p.Phase == "starting" {
			cancel()
		}
	})
	result, err := Execute(ctx, plan, Quarantine)
	if !errors.Is(err, context.Canceled) || !result.Cancelled || len(result.Completed) != 0 {
		t.Fatalf("cancelled cleanup = %#v, %v", result, err)
	}
	assertProgressEnd(t, events, "clean", 0, 1, "cancelled")
	assertFixture(t, root, ".DS_Store", []byte("metadata"))

	root, session := quarantinedFixture(t)
	ctx, cancel = context.WithCancel(context.Background())
	events = nil
	ctx = WithProgress(ctx, func(p Progress) {
		events = append(events, p)
		if p.Phase == "restoring" {
			cancel()
		}
	})
	result, err = Restore(ctx, session)
	if !errors.Is(err, context.Canceled) || !result.Cancelled || len(result.Completed) != 0 {
		t.Fatalf("cancelled restore = %#v, %v", result, err)
	}
	assertProgressEnd(t, events, "restore", 0, 1, "cancelled")
	assertFixture(t, session, "data/.DS_Store", []byte("original metadata"))
	if _, err := os.Stat(filepath.Join(root, ".DS_Store")); !os.IsNotExist(err) {
		t.Fatalf("cancelled restore changed destination: %v", err)
	}
}

func TestScanCancellationDuringByteVerification(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", bytes.Repeat([]byte("metadata"), 64*1024))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []Progress
	ctx = WithProgress(ctx, func(p Progress) {
		events = append(events, p)
		if p.Phase == "verifying" && p.BytesDone > 0 {
			cancel()
			return
		}
		if p.Phase == "starting" || p.Phase == "walking" || p.Phase == "verifying" {
			// Simulate a slow progress consumer, ensuring the next read crosses
			// the reporting interval without requiring a huge fixture file.
			time.Sleep(110 * time.Millisecond)
		}
	})
	plan, err := Scan(ctx, root, Options{})
	if !errors.Is(err, context.Canceled) || plan.ready {
		t.Fatalf("cancelled snapshot scan remained executable: %#v, %v", plan, err)
	}
	assertProgressEnd(t, events, "scan", 1, 0, "cancelled")
	if events[len(events)-1].BytesDone <= 0 || len(plan.Items) != 0 {
		t.Fatalf("cancelled scan counters = %#v", events[len(events)-1])
	}
}

func TestLastScanVisitCancellationRejectsPlan(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, "ordinary.txt", []byte("ordinary file"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = WithProgress(ctx, func(p Progress) {
		if p.Phase == "starting" {
			time.Sleep(110 * time.Millisecond)
		}
		if p.Phase == "walking" {
			cancel()
		}
	})
	plan, err := Scan(ctx, root, Options{})
	if !errors.Is(err, context.Canceled) || plan.ready {
		t.Fatalf("last-visit cancellation produced an executable plan: %#v, %v", plan, err)
	}
}

func TestLastItemProgressCancellationKeepsRecovery(t *testing.T) {
	root := fixtureRoot(t)
	putFixture(t, root, ".DS_Store", []byte("metadata"))
	plan := scanFixture(t, root, Options{})
	cancelAtLastItem := func() (context.Context, *[]Progress) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		events := []Progress{}
		ctx = WithProgress(ctx, func(p Progress) {
			events = append(events, p)
			if p.Total > 0 && p.Done == p.Total && p.Phase != "done" && p.Phase != "cancelled" {
				cancel()
			} else if p.Phase != "done" && p.Phase != "cancelled" {
				time.Sleep(110 * time.Millisecond)
			}
		})
		return ctx, &events
	}
	ctx, events := cancelAtLastItem()
	result, err := Execute(ctx, plan, Quarantine)
	if !errors.Is(err, context.Canceled) || !result.Cancelled || len(result.Completed) != 1 {
		t.Fatalf("last-item cleanup cancellation = %#v, %v", result, err)
	}
	assertProgressEnd(t, *events, "clean", 1, 1, "cancelled")
	ctx, events = cancelAtLastItem()
	restored, err := Restore(ctx, result.Session)
	if !errors.Is(err, context.Canceled) || !restored.Cancelled || len(restored.Completed) != 1 {
		t.Fatalf("last-item restore cancellation = %#v, %v", restored, err)
	}
	assertProgressEnd(t, *events, "restore", 1, 1, "cancelled")
	assertFixture(t, root, ".DS_Store", []byte("metadata"))
	if retried, err := Restore(context.Background(), result.Session); err != nil || len(retried.Issues) != 0 {
		t.Fatalf("cancelled journal recovery = %#v, %v", retried, err)
	}
}

func assertProgressEnd(t *testing.T, events []Progress, operation string, done, total int64, phase string) {
	t.Helper()
	if len(events) < 2 || events[0].Phase != "starting" {
		t.Fatalf("missing initial/final progress: %#v", events)
	}
	var previous int64
	for _, event := range events {
		if event.Operation != operation || event.Done < previous || event.Done < 0 ||
			(event.Total > 0 && event.Done > event.Total) || event.BytesDone < 0 ||
			(event.BytesTotal > 0 && event.BytesDone > event.BytesTotal) {
			t.Fatalf("invalid progress counters: %#v", events)
		}
		previous = event.Done
	}
	last := events[len(events)-1]
	if last.Phase != phase || last.Done != done || last.Total != total {
		t.Fatalf("final progress = %#v; want %s %d/%d", last, phase, done, total)
	}
}
