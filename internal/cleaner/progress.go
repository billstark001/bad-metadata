package cleaner

import (
	"context"
	"errors"
	"time"
)

// Progress describes work performed so far. A zero Total means the total is
// unknown, as when scanning a directory tree. For clean and restore, Done counts
// processed candidates, including candidates skipped with an issue. Byte counts
// describe verification of the current candidate, rather than all candidates.
type Progress struct {
	Operation  string
	Phase      string
	Path       string
	Done       int64
	Total      int64
	BytesDone  int64
	BytesTotal int64
	Matches    int64
}

type progressCallbackKey struct{}
type progressReporterKey struct{}

// WithProgress returns a context carrying an optional progress callback. The
// callback runs synchronously on the operation's goroutine and must return
// promptly. Initial and final events are always emitted; intermediate events are
// limited to about ten per second. Separate operations have separate counters.
func WithProgress(ctx context.Context, callback func(Progress)) context.Context {
	return context.WithValue(ctx, progressCallbackKey{}, callback)
}

type progressReporter struct {
	ctx      context.Context
	callback func(Progress)
	current  Progress
	last     time.Time
}

func beginProgress(ctx context.Context, operation string, total int64) (context.Context, *progressReporter) {
	callback, _ := ctx.Value(progressCallbackKey{}).(func(Progress))
	reporter := &progressReporter{
		ctx:      ctx,
		callback: callback,
		current:  Progress{Operation: operation, Phase: "starting", Total: total},
	}
	reporter.emit(true)
	return context.WithValue(ctx, progressReporterKey{}, reporter), reporter
}

func (p *progressReporter) emit(force bool) {
	if p.callback == nil {
		return
	}
	now := time.Now()
	if !force && !p.last.IsZero() && now.Sub(p.last) < 100*time.Millisecond {
		return
	}
	p.last = now
	p.callback(p.current)
}

func (p *progressReporter) finish(err error) {
	p.current.Phase = "done"
	if p.ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		p.current.Phase = "cancelled"
	} else if err != nil {
		p.current.Phase = "failed"
	}
	p.emit(true)
}

func (p *progressReporter) visit(path string) {
	p.current.Phase = "walking"
	p.current.Path = path
	p.current.Done++
	p.emit(false)
}

func (p *progressReporter) candidate(path string, bytes int64) {
	p.current.Phase = "verifying"
	p.current.Path = path
	p.current.BytesDone = 0
	p.current.BytesTotal = bytes
	p.emit(false)
}

func (p *progressReporter) advance(done int64) {
	p.current.Done = done
	if p.current.Total > 0 && p.current.Done > p.current.Total {
		p.current.Done = p.current.Total
	}
	p.emit(false)
}

func (p *progressReporter) phase(phase string) {
	p.current.Phase = phase
	p.emit(false)
}

func snapshotProgress(ctx context.Context, name string, size int64, nested bool) {
	p, _ := ctx.Value(progressReporterKey{}).(*progressReporter)
	if p == nil {
		return
	}
	if p.current.Operation == "scan" && nested {
		p.current.Done++
	}
	p.current.Phase = "verifying"
	p.current.Path = name
	p.current.BytesDone = size
	if p.current.BytesTotal > 0 && p.current.BytesDone > p.current.BytesTotal {
		p.current.BytesDone = p.current.BytesTotal
	}
	p.emit(false)
}
