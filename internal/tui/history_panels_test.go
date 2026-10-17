package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trestle/internal/config"
	"trestle/internal/runlog"
)

// panelsWithHistory primes a console with three on-disk runlog records.
func panelsWithHistory(t *testing.T) (dashboardModel, string) {
	t.Helper()
	path := configPath(t, config.Default("demo"))
	if run, err := runlog.Start(path, "Build"); err != nil {
		t.Fatal(err)
	} else {
		run.Write([]byte("alpha line\nprogress\n"))
		if err := run.Finish(nil); err != nil {
			t.Fatal(err)
		}
	}
	if run, err := runlog.Start(path, "Doctor"); err != nil {
		t.Fatal(err)
	} else {
		run.Write([]byte("beta line\n"))
		if err := run.Finish(errors.New("doctor failed")); err != nil {
			t.Fatal(err)
		}
	}
	// Failure echoes the recorded operation failure back; the write matters.
	_ = runlog.Failure(path, "Install", errors.New(" boom"))
	model, _ := freshConsole(t)
	model.path = path
	model.width, model.height = 120, 40
	return model, path
}

// The panel function keys toggle their overlays and keep scroll positions.
func TestPanelsToggleAndRememberScroll(t *testing.T) {
	model, _ := panelsWithHistory(t)

	toggled, _, handled := model.handlePanels("f2")
	pane := toggled.(dashboardModel)
	if !handled || !pane.fullLog || pane.focus != focusLog {
		t.Fatalf("f2 ignored: handled=%v full=%v", handled, pane.fullLog)
	}
	toggled, _, handled = pane.handlePanels("f2")
	if !handled || toggled.(dashboardModel).fullLog {
		t.Fatal("second f2 must close the full log")
	}

	collapsed, _, handled := model.handlePanels("f5")
	if !handled || !collapsed.(dashboardModel).logsCollapsed {
		t.Fatal("f5 did not collapse the logs")
	}
	collapsed, _, _ = collapsed.(dashboardModel).handlePanels("f5")
	if collapsed.(dashboardModel).logsCollapsed {
		t.Fatal("second f5 must reopen the logs")
	}

	details, _, handled := model.handlePanels("f3")
	pane = details.(dashboardModel)
	if !handled || !pane.details {
		t.Fatal("f3 did not open details")
	}
	pane.overlayScroll = 7
	closed, _, _ := pane.handlePanels("f3")
	pane = closed.(dashboardModel)
	if pane.details || pane.detailsScroll != 7 {
		t.Fatalf("f3 must close details and remember the scroll, scroll=%d", pane.detailsScroll)
	}
	pane.overlayScroll = 0
	reopened, _, _ := pane.handlePanels("f3")
	pane = reopened.(dashboardModel)
	if !pane.details || pane.overlayScroll != 7 {
		t.Fatalf("reopened details must restore the scroll, scroll=%d", pane.overlayScroll)
	}
}

// The history overlay lists disk runs, moves the cursor with the viewport,
// and enter attaches the selected record to the log panel.
func TestHistoryOverlayReadsDiskRuns(t *testing.T) {
	model, _ := panelsWithHistory(t)

	opened, _, handled := model.handlePanels("f4")
	pane := opened.(dashboardModel)
	if !handled || !pane.history || len(pane.runHistory) != 3 {
		t.Fatalf("history overlay wrong: handled=%v records=%d", handled, len(pane.runHistory))
	}

	pane.overlayScroll = 0
	down, _, handled := pane.handlePanels("down")
	pane = down.(dashboardModel)
	if !handled || pane.historyCursor != 1 {
		t.Fatalf("history down = cursor %d", pane.historyCursor)
	}
	if pane.overlayScroll != pane.historyOffset(1) {
		t.Fatalf("scroll %d must track the offset %d", pane.overlayScroll, pane.historyOffset(1))
	}
	up, _, _ := pane.handlePanels("up")
	pane = up.(dashboardModel)
	if pane.historyCursor != 0 || pane.overlayScroll != 0 {
		t.Fatalf("history up = cursor %d scroll %d", pane.historyCursor, pane.overlayScroll)
	}

	pane.historyCursor = 1
	committed, _, _ := pane.handlePanels("enter")
	pane = committed.(dashboardModel)
	record := pane.runHistory[1]
	if pane.history || !pane.fullLog || pane.diskLog == nil || pane.logFollow {
		t.Fatalf("enter did not attach the record: %+v", pane)
	}
	if pane.diskLog.reader.Path != record.LogPath {
		t.Fatalf("attached log path = %q", pane.diskLog.reader.Path)
	}
	if !strings.Contains(pane.message, record.Status+" · "+record.Operation) {
		t.Fatalf("banner = %q", pane.message)
	}
	if _, err := os.Stat(record.LogPath); err != nil {
		t.Fatalf("record log file missing: %v", err)
	}

	closed, _, handled := pane.handlePanels("esc")
	if !handled || closed.(dashboardModel).details || closed.(dashboardModel).help {
		t.Fatal("esc must close the current overlay")
	}
}

// Empty logs list nothing and esc still closes; up/down scroll other overlays.
func TestHistoryOverlayEdgeCases(t *testing.T) {
	model, _ := freshConsole(t)
	dir := t.TempDir()
	model.path = filepath.Join(dir, "trestle.toml")
	opened, _, _ := model.handlePanels("f4")
	pane := opened.(dashboardModel)
	if !pane.history || len(pane.runHistory) != 0 {
		t.Fatalf("empty history = %d records", len(pane.runHistory))
	}
	pane.overlayScroll = 2
	scrolled, _, handled := pane.handlePanels("down")
	pane = scrolled.(dashboardModel)
	if !handled || pane.overlayScroll != pane.historyOffset(pane.historyCursor) || pane.historyCursor != 0 {
		t.Fatalf("empty history scroll = %d cursor=%d", pane.overlayScroll, pane.historyCursor)
	}
	if len(pane.runHistory) != 0 {
		t.Fatal("empty history granted a cursor row")
	}

	closed, _, handled := pane.handlePanels("q")
	pane = closed.(dashboardModel)
	if !handled || pane.history {
		t.Fatal("q must close the overlay too")
	}
}
