package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/rivo/tview"
)

func TestLatencyStats(t *testing.T) {
	s := &latencyStats{}
	if s.count() != 0 {
		t.Error("expected 0 samples initially")
	}
	if s.avg() != 0 {
		t.Error("expected 0 avg initially")
	}

	s.add(100)
	s.add(200)
	s.add(300)

	if s.count() != 3 {
		t.Errorf("expected 3 samples, got %d", s.count())
	}
	if s.avg() != 200 {
		t.Errorf("expected avg 200, got %f", s.avg())
	}
	if s.min() != 100 {
		t.Errorf("expected min 100, got %d", s.min())
	}
	if s.max() != 300 {
		t.Errorf("expected max 300, got %d", s.max())
	}
}

func TestLatencyStatsWindow(t *testing.T) {
	s := &latencyStats{}
	for i := 0; i < 100; i++ {
		s.add(int64(i))
	}
	if s.count() != maxLatencySamples {
		t.Errorf("expected %d samples, got %d", maxLatencySamples, s.count())
	}
	// Oldest values should have been dropped.
	if s.min() != 50 {
		t.Errorf("expected min 50 after window trim, got %d", s.min())
	}
}

func TestVerdictColor(t *testing.T) {
	if verdictColor("approve").String() != "green" {
		t.Error("approve should be green")
	}
	if verdictColor("deny").String() != "red" {
		t.Error("deny should be red")
	}
	if verdictColor("escalate").String() != "yellow" {
		t.Error("escalate should be yellow")
	}
}

func TestVerdictLabel(t *testing.T) {
	if verdictLabel("approve") != "APPROVED" {
		t.Error("expected APPROVED")
	}
	if verdictLabel("deny") != "DENIED" {
		t.Error("expected DENIED")
	}
	if verdictLabel("escalate") != "ESCALATED" {
		t.Error("expected ESCALATED")
	}
	if verdictLabel("error") != "ERROR" {
		t.Error("expected ERROR")
	}
	if verdictLabel("unknown") != "UNKNOWN" {
		t.Error("expected UNKNOWN for unrecognized verdicts")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "ab..."},
		{"abcdefgh", 4, "a..."},
		{"abcdef", 2, "ab"},
	}
	for _, c := range cases {
		got := truncate(c.in, c.n)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestEscalationLabels(t *testing.T) {
	p := daemon.PendingEntry{
		ProjectHash: "1234567890abcdef",
		Tool:        "Bash",
		Input:       "rm -rf /",
		Verdict:     "escalate",
		Reason:      "destructive",
		Timestamp:   "2026-05-07T10:00:00Z",
	}
	main, secondary, _, _ := escalationLabels(p)
	if !strings.Contains(main, "Bash") {
		t.Errorf("main should contain tool, got %q", main)
	}
	if !strings.Contains(main, "rm -rf /") {
		t.Errorf("main should contain input, got %q", main)
	}
	if !strings.Contains(secondary, "ESCALATE") {
		t.Errorf("secondary should contain uppercase verdict, got %q", secondary)
	}
	if !strings.Contains(secondary, "proj:1234567890ab") {
		t.Errorf("secondary should contain shortened project hash, got %q", secondary)
	}
	if !strings.Contains(secondary, "destructive") {
		t.Errorf("secondary should contain reason, got %q", secondary)
	}
}

func TestEscalationLabelsTruncates(t *testing.T) {
	long := strings.Repeat("x", 200)
	p := daemon.PendingEntry{Tool: "Bash", Input: long, Verdict: "escalate", Reason: long}
	main, secondary, _, _ := escalationLabels(p)
	if strings.Contains(main, long) {
		t.Error("main should truncate long input")
	}
	if strings.Contains(secondary, long) {
		t.Error("secondary should truncate long reason")
	}
}

func TestHelpTextSections(t *testing.T) {
	got := helpText()
	for _, section := range []string{"Global", "Activity page", "Escalations page"} {
		if !strings.Contains(got, section) {
			t.Errorf("help text missing section %q", section)
		}
	}
	for _, key := range []string{"q", "?", "e", "a", "d", "Tab"} {
		if !strings.Contains(got, "[white]"+key+"[gray]") {
			t.Errorf("help text missing key %q", key)
		}
	}
}

func TestToggleFullscreenRoundTrip(t *testing.T) {
	a := newTestApp()
	pane1 := tview.NewBox()
	pane2 := tview.NewBox()
	a.activityFocusables = []tview.Primitive{pane1, pane2}
	a.activityFocusableNames = []string{"pane1", "pane2"}
	a.fullscreenContainer = tview.NewFlex()
	a.pages.AddPage(pageFullscreen, a.fullscreenContainer, true, false)

	// Focus pane2 before toggling so we verify the toggle uses the
	// current focus index, not always pane1.
	a.activityFocusIdx = 1
	a.toggleFullscreen()
	if a.currentPage != pageFullscreen {
		t.Fatalf("expected currentPage=fullscreen, got %s", a.currentPage)
	}
	if a.fullscreenContainer.GetItemCount() != 1 {
		t.Fatalf("expected 1 item in fullscreen container, got %d", a.fullscreenContainer.GetItemCount())
	}
	if a.fullscreenContainer.GetItem(0) != pane2 {
		t.Errorf("expected fullscreen container to host pane2 (idx 1)")
	}

	// Toggle off — should land back on activity, container drained.
	a.toggleFullscreen()
	if a.currentPage != pageActivity {
		t.Fatalf("expected currentPage=activity after exit, got %s", a.currentPage)
	}
	if a.fullscreenContainer.GetItemCount() != 0 {
		t.Errorf("expected fullscreen container to be cleared on exit, got %d items", a.fullscreenContainer.GetItemCount())
	}
	// Focus index is preserved across the round trip so the user
	// returns to the pane they were inspecting.
	if a.activityFocusIdx != 1 {
		t.Errorf("expected focus idx preserved across toggle (1), got %d", a.activityFocusIdx)
	}
}

func TestToggleFullscreenIgnoredOnNonActivityPages(t *testing.T) {
	a := newTestApp()
	a.fullscreenContainer = tview.NewFlex()
	a.pages.AddPage(pageFullscreen, a.fullscreenContainer, true, false)
	a.activityFocusables = []tview.Primitive{tview.NewBox()}

	a.currentPage = pageEscalations
	a.toggleFullscreen()
	if a.currentPage != pageEscalations {
		t.Errorf("toggleFullscreen should be a no-op outside activity/fullscreen pages, got %s", a.currentPage)
	}
}

func TestFormatActivityLineNoTruncation(t *testing.T) {
	// 200-char input that previously got ellipsised at 57 + "..." in
	// the List-based renderer. With the TextView + horizontal scroll
	// design the full text must round-trip into the rendered line.
	longInput := strings.Repeat("x", 200)
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     longInput,
		Verdict:   "approve",
		Timestamp: "2026-05-08T20:13:01Z",
	}
	got := formatActivityLine(evt)
	if !strings.Contains(got, longInput) {
		t.Fatalf("expected full input preserved (no ellipsis); got: %q", got)
	}
	if strings.Contains(got, "...") {
		t.Errorf("ellipsis must not appear in formatted line; got: %q", got)
	}
	// Timestamp should be truncated to HH:MM:SS for the embedded view.
	if !strings.Contains(got, "20:13:01") || strings.Contains(got, "2026-05-08") {
		t.Errorf("expected HH:MM:SS timestamp without date prefix; got: %q", got)
	}
}

func TestFormatActivityLineWithReason(t *testing.T) {
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     "rm -rf /etc/passwd",
		Verdict:   "deny",
		Reason:    "Critical system file",
		Timestamp: "2026-05-08T20:13:01Z",
	}
	got := formatActivityLine(evt)
	if !strings.Contains(got, "rm -rf /etc/passwd") {
		t.Errorf("expected input in line; got: %q", got)
	}
	if !strings.Contains(got, "Critical system file") {
		t.Errorf("expected reason in line; got: %q", got)
	}
	if !strings.Contains(got, "DENIED") {
		t.Errorf("expected verdict label DENIED; got: %q", got)
	}
}

func TestCycleActivityFocusWraps(t *testing.T) {
	a := &App{
		app: tview.NewApplication(),
		activityFocusables: []tview.Primitive{
			tview.NewBox(),
			tview.NewBox(),
			tview.NewBox(),
		},
	}
	if a.activityFocusIdx != 0 {
		t.Fatalf("expected initial idx 0, got %d", a.activityFocusIdx)
	}
	a.cycleActivityFocus(+1)
	if a.activityFocusIdx != 1 {
		t.Errorf("after +1, expected idx 1, got %d", a.activityFocusIdx)
	}
	a.cycleActivityFocus(+1)
	a.cycleActivityFocus(+1)
	if a.activityFocusIdx != 0 {
		t.Errorf("expected wrap to 0 after 3 forward steps, got %d", a.activityFocusIdx)
	}
	a.cycleActivityFocus(-1)
	if a.activityFocusIdx != 2 {
		t.Errorf("expected wrap to 2 after backward step, got %d", a.activityFocusIdx)
	}
}

func TestFindPendingIndex(t *testing.T) {
	pending := []daemon.PendingEntry{
		{ProjectHash: "h1", Key: "k1"},
		{ProjectHash: "h2", Key: "k2"},
	}

	if got := findPendingIndex(pending, "h2", "k2"); got != 1 {
		t.Fatalf("expected index 1, got %d", got)
	}
	if got := findPendingIndex(pending, "h3", "k3"); got != -1 {
		t.Fatalf("expected -1 for missing entry, got %d", got)
	}
}

func TestRebuildEscalationListPreservesSelectionByKey(t *testing.T) {
	a := &App{
		escalations: tview.NewList(),
		escalEmpty:  tview.NewTextView(),
	}
	initial := []daemon.PendingEntry{
		{ProjectHash: "h1", Key: "k1", Tool: "Bash", Verdict: "escalate"},
		{ProjectHash: "h2", Key: "k2", Tool: "Read", Verdict: "error"},
	}
	a.rebuildEscalationList(initial, true)
	a.escalations.SetCurrentItem(1)

	refreshed := []daemon.PendingEntry{
		{ProjectHash: "h2", Key: "k2", Tool: "Read", Verdict: "error"},
		{ProjectHash: "h1", Key: "k1", Tool: "Bash", Verdict: "escalate"},
	}
	a.rebuildEscalationList(refreshed, true)

	if got := a.escalations.GetCurrentItem(); got != 0 {
		t.Fatalf("expected selection to follow h2/k2 to index 0, got %d", got)
	}
}

func TestEmptyBannerFor(t *testing.T) {
	off := emptyBannerFor(false, 0)
	if !strings.Contains(off, "audit_enabled = false") {
		t.Errorf("audit-off banner should call out the disabled config, got %q", off)
	}
	on0 := emptyBannerFor(true, 0)
	if strings.Contains(on0, "audit_enabled") || !strings.Contains(on0, "no pending") {
		t.Errorf("audit-on empty banner should not mention audit_enabled, got %q", on0)
	}
	on3 := emptyBannerFor(true, 3)
	if !strings.Contains(on3, "3 pending") {
		t.Errorf("audit-on populated banner should show count, got %q", on3)
	}
}

// TestInputHandlerHelpersDoNotDeadlock guards against the tview gotcha where
// QueueUpdate{,Draw} called from inside an input handler deadlocks: the call
// blocks waiting for the main event loop to drain the update channel, but
// the main loop is busy executing the handler. With Application.Run not
// running here the channel never drains, so a buggy handler hangs forever.
// A 500ms budget is plenty for a direct primitive mutation; if it trips,
// somebody re-introduced QueueUpdate inside switchTo or refreshConfig.
func TestInputHandlerHelpersDoNotDeadlock(t *testing.T) {
	cases := []struct {
		name string
		run  func(a *App)
	}{
		{"refreshConfig", func(a *App) { a.refreshConfig() }},
		{"switchTo other page", func(a *App) { a.switchTo(pageHelp) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			done := make(chan struct{})
			go func() {
				tc.run(a)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(500 * time.Millisecond):
				t.Fatalf("%s blocked — likely QueueUpdate{,Draw} called from input-handler context", tc.name)
			}
		})
	}
}

func newTestApp() *App {
	a := &App{
		app:         tview.NewApplication(),
		pages:       tview.NewPages(),
		statusBar:   tview.NewTextView(),
		configView:  tview.NewTextView(),
		currentPage: pageActivity,
	}
	a.pages.AddPage(pageActivity, tview.NewBox(), true, true)
	a.pages.AddPage(pageEscalations, tview.NewBox(), true, false)
	a.pages.AddPage(pageHelp, tview.NewBox(), true, false)
	return a
}

func TestMinMax(t *testing.T) {
	if minOf([]int64{3, 1, 4, 1, 5}) != 1 {
		t.Error("minOf failed")
	}
	if maxOf([]int64{3, 1, 4, 1, 5}) != 5 {
		t.Error("maxOf failed")
	}
	if minOf(nil) != 0 {
		t.Error("min of nil should be 0")
	}
	if maxOf(nil) != 0 {
		t.Error("max of nil should be 0")
	}
}
