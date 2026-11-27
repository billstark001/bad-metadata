package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/billstark001/bad-metadata/internal/cleaner"
)

func TestProgressNeverPollutesJSONOutput(t *testing.T) {
	root := tempRoot(t)
	writeFile(t, root+"/.DS_Store", "metadata")
	for _, mode := range []string{"auto", "always", "never"} {
		t.Run(mode, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := Run(context.Background(), []string{"scan", root, "--json", "--progress", mode}, &out, &diagnostics); code != 0 {
				t.Fatalf("code = %d; diagnostics = %s", code, &diagnostics)
			}
			var plan cleaner.Plan
			if err := json.Unmarshal(out.Bytes(), &plan); err != nil || len(plan.Items) != 1 {
				t.Fatalf("invalid JSON output: %v: %s", err, &out)
			}
			if mode == "always" {
				if !strings.Contains(diagnostics.String(), "scan: done") || !strings.Contains(diagnostics.String(), "1 matches") {
					t.Fatalf("missing forced progress: %q", diagnostics.String())
				}
			} else if diagnostics.Len() != 0 {
				t.Fatalf("unexpected redirected progress: %q", diagnostics.String())
			}
		})
	}
}

func TestProgressModeValidation(t *testing.T) {
	for _, args := range [][]string{
		{"scan", "path", "--progress"},
		{"scan", "path", "--progress=invalid"},
		{"scan", "path", "--progress=auto", "--progress=always"},
	} {
		if code := Run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Fatalf("args = %v; code = %d", args, code)
		}
	}
}

func TestProgressRendererFitsTerminalAndEscapesPaths(t *testing.T) {
	var output bytes.Buffer
	renderer := progressRenderer{writer: &output}
	renderer.render(cleaner.Progress{
		Operation: "restore", Phase: "verifying", Done: 123, Total: 1234,
		BytesDone: 100000000, BytesTotal: 999999999,
		Path: strings.Repeat("long-directory/", 20) + "\n\r\x1b[2J中文",
	})
	text := output.String()
	if !strings.HasPrefix(text, "\r[") || strings.ContainsAny(text[1:], "\r\n\x1b") {
		t.Fatalf("unsafe progress control characters: %q", text)
	}
	if len(text)-1 > 79 {
		t.Fatalf("progress wraps an80-columnterminal (%d): %q", len(text)-1, text)
	}
	for _, char := range text {
		if char > 127 {
			t.Fatalf("non-ASCII terminal output: %q", text)
		}
	}
	if !strings.Contains(text, "restore: verifying") || !strings.Contains(text, "123/1234") {
		t.Fatalf("progress lost operation/counters: %q", text)
	}
}

func TestProgressRendererReserves100PercentForCompletion(t *testing.T) {
	for _, phase := range []string{"quarantine", "restoring", "failed", "cancelled", "done"} {
		var output bytes.Buffer
		renderer := progressRenderer{writer: &output}
		renderer.render(cleaner.Progress{Operation: "clean", Phase: phase, Done: 5, Total: 5})
		if got := strings.Contains(output.String(), "100%"); got != (phase == "done") {
			t.Fatalf("phase %s has misleading percentage: %q", phase, output.String())
		}
	}
	var output bytes.Buffer
	renderer := progressRenderer{writer: &output}
	renderer.render(cleaner.Progress{Operation: "scan", Phase: "done", Done: 12, Matches: 3})
	if !strings.Contains(output.String(), "[====================]") || strings.Contains(output.String(), "%") {
		t.Fatalf("unknown total completion = %q", output.String())
	}
}
