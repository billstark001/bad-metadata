package gui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (a *app) progressView(c *ui.Context) {
	ui.Column(c.Key("operation-progress")).FillWidth().Gap(4).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, a.text("phase_"+a.progress.Phase)).FontSize(12).Bold().Grow(1)
			ui.Text(c, a.progressCount()).FontSize(12).TextColor(c.Theme().TextMuted)
		})
		ui.Progress(c, a.progressValue()).FillWidth().Height(8).Label(a.text("progress"))
		if a.progress.Path != "" {
			ui.Text(c, a.progress.Path).SingleLine().FontSize(11).TextColor(c.Theme().TextMuted).Tooltip(a.progress.Path)
		}
		if a.progress.Phase == "verifying" && a.progress.BytesDone > 0 {
			label := fmt.Sprintf(a.text("progressBytes"), formatBytes(a.progress.BytesDone))
			if a.progress.BytesTotal > 0 {
				label = fmt.Sprintf(a.text("progressBytesTotal"), formatBytes(a.progress.BytesDone), formatBytes(a.progress.BytesTotal))
			}
			ui.Text(c, label).FontSize(11).TextColor(c.Theme().TextMuted)
		}
	})
}

func (a *app) progressValue() float64 {
	if a.progressComplete {
		return 1
	}
	if a.progress.Total > 0 {
		// Finishing the item loop is followed by journal persistence. Only a
		// successful return from the complete operation may show 100%.
		return min(max(float64(a.progress.Done)/float64(a.progress.Total), 0), 0.99)
	}
	if a.cancel != nil {
		return -1
	}
	return 0
}

func (a *app) progressCount() string {
	if a.progress.Operation == "scan" {
		label := fmt.Sprintf(a.text("progressScan"), a.progress.Done, a.progress.Matches)
		if a.progressComplete {
			label += " · 100%"
		}
		return label
	}
	if a.progress.Total > 0 {
		return fmt.Sprintf(a.text("progressItems"), a.progress.Done, a.progress.Total, int(a.progressValue()*100))
	}
	if a.progressComplete {
		return "100%"
	}
	return a.text("progressUnknown")
}
