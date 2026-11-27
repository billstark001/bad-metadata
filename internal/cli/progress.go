package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/billstark001/bad-metadata/internal/cleaner"
)

func progressContext(ctx context.Context, mode string, stderr io.Writer) context.Context {
	if mode == "never" || (mode == "auto" && !progressTerminal(stderr)) {
		return ctx
	}
	renderer := &progressRenderer{writer: stderr}
	return cleaner.WithProgress(ctx, renderer.render)
}

func progressTerminal(writer io.Writer) bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type progressRenderer struct {
	writer io.Writer
	width  int
	frame  int
	failed bool
}

func (r *progressRenderer) render(progress cleaner.Progress) {
	if r.failed {
		return // A progress display is best-effort; operation output is separate.
	}
	const barWidth = 20
	const lineWidth = 79
	final := progress.Phase == "done" || progress.Phase == "cancelled" || progress.Phase == "failed"
	var bar, counts string
	if progress.Total > 0 {
		done := min(max(progress.Done, 0), progress.Total)
		percent := int(float64(done) / float64(progress.Total) * 100)
		if progress.Phase != "done" {
			percent = min(percent, 99)
		}
		filled := percent * barWidth / 100
		bar = strings.Repeat("=", filled) + strings.Repeat(" ", barWidth-filled)
		counts = fmt.Sprintf("%d/%d (%d%%)", done, progress.Total, percent)
	} else {
		bar = strings.Repeat(" ", barWidth)
		if progress.Phase == "done" {
			bar = strings.Repeat("=", barWidth)
		} else if !final {
			position := r.frame % (barWidth - 2)
			bar = bar[:position] + "==>" + bar[position+3:]
			r.frame++
		}
		if progress.Operation == "scan" {
			counts = fmt.Sprintf("%d visited, %d matches", progress.Done, progress.Matches)
		} else {
			counts = fmt.Sprintf("%d processed", progress.Done)
		}
	}
	line := fmt.Sprintf("[%s] %s: %s | %s", bar, progress.Operation, progress.Phase, counts)
	byteText := ""
	if progress.BytesTotal > 0 {
		byteText = progressBytes(progress.BytesDone) + "/" + progressBytes(progress.BytesTotal)
	} else if progress.BytesDone > 0 {
		byteText = progressBytes(progress.BytesDone) + " read"
	}
	if byteText != "" && len(line)+len(byteText)+3 <= lineWidth-8 {
		line += " | " + byteText
	}
	if progress.Path != "" {
		// Quoting also escapes CR, LF, ESC, and other control characters. Keep
		// the complete display ASCII for Windows terminals with legacy encodings.
		path := strconv.QuoteToASCII(progress.Path)
		budget := min(60, lineWidth-len(line)-3)
		if budget >= 4 {
			if len(path) > budget {
				path = "..." + path[len(path)-(budget-3):]
			}
			line += " | " + path
		}
	}
	if len(line) > lineWidth {
		line = line[:lineWidth-3] + "..."
	}
	padding := strings.Repeat(" ", max(r.width-len(line), 0))
	ending := ""
	if final {
		ending = "\n"
	}
	if _, err := fmt.Fprintf(r.writer, "\r%s%s%s", line, padding, ending); err != nil {
		r.failed = true
	}
	r.width = len(line)
	if final {
		r.width = 0
	}
}

func progressBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%dB", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value := float64(n) / 1024
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f%s", value, units[unit])
}
