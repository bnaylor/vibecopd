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

func TestFormatActivityCellsNoTruncation(t *testing.T) {
	// 200-char input that previously got ellipsised at 57 + "..." in
	// the List-based renderer. With Table + horizontal scroll the full
	// body cell must hold the entire input.
	longInput := strings.Repeat("x", 200)
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     longInput,
		Verdict:   "approve",
		Timestamp: "2026-05-08T20:13:01Z",
	}
	// UTC mode: substring-truncated timestamp, no zone shift.
	ts, _, _, body, _ := formatActivityCells(evt, false)
	if !strings.Contains(body, longInput) {
		t.Fatalf("expected full input preserved (no ellipsis); got: %q", body)
	}
	if strings.Contains(body, "...") {
		t.Errorf("ellipsis must not appear; got: %q", body)
	}
	if ts != "20:13:01" {
		t.Errorf("expected HH:MM:SS timestamp, got %q", ts)
	}
}

func TestFormatActivityCellsWithReason(t *testing.T) {
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     "rm -rf /etc/passwd",
		Verdict:   "deny",
		Reason:    "Critical system file",
		Timestamp: "2026-05-08T20:13:01Z",
	}
	_, verdict, tool, body, _ := formatActivityCells(evt, false)
	if !strings.Contains(body, "rm -rf /etc/passwd") {
		t.Errorf("expected input in body; got: %q", body)
	}
	if !strings.Contains(body, "Critical system file") {
		t.Errorf("expected reason in body; got: %q", body)
	}
	if verdict != "DENIED" {
		t.Errorf("expected verdict label DENIED, got %q", verdict)
	}
	if tool != "Bash" {
		t.Errorf("expected tool Bash, got %q", tool)
	}
}

func TestFormatDetailContentRendersAllFields(t *testing.T) {
	evt := daemon.Event{
		Tool:      "Bash",
		Input:     "rm -rf /etc/passwd",
		Verdict:   "deny",
		Reason:    "Critical system file. Would brick the host.",
		LatencyMs: 345,
		Timestamp: "2026-05-08T20:13:01Z",
		Level:     "warn",
		Message:   "synthetic test message",
	}
	got := formatDetailContent(evt, false) // UTC mode for stable assertions
	for _, want := range []string{
		"2026-05-08 20:13:01 UTC", // formatted absolute timestamp on detail sheet
		"Bash",
		"DENIED",
		"345 ms",
		"warn",
		"rm -rf /etc/passwd",
		"Critical system file",
		"synthetic test message",
		"Esc / Enter / d to close",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail content missing %q; got: %s", want, got)
		}
	}
}

func TestFormatDetailContentHandlesEmptyFields(t *testing.T) {
	// approve verdicts often arrive with no reason/message — the
	// formatter must not render empty bullets in that case.
	evt := daemon.Event{
		Tool:      "Read",
		Input:     "/tmp/x",
		Verdict:   "approve",
		Timestamp: "2026-05-08T20:13:01Z",
	}
	got := formatDetailContent(evt, false)
	if strings.Contains(got, "Reason:") {
		t.Errorf("empty reason should be omitted; got: %s", got)
	}
	if strings.Contains(got, "Message:") {
		t.Errorf("empty message should be omitted; got: %s", got)
	}
	if !strings.Contains(got, "APPROVED") {
		t.Errorf("approve verdict should render label APPROVED; got: %s", got)
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

func TestFormatHHMMSS(t *testing.T) {
	// UTC mode: no zone shift, HH:MM:SS substring.
	if got := formatHHMMSS("2026-05-08T20:13:01Z", false); got != "20:13:01" {
		t.Errorf("UTC mode: expected 20:13:01, got %q", got)
	}
	if got := formatHHMMSS("", false); got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
	// Local mode with a fixed offset RFC3339 input. We can't predict the
	// machine's local zone, so assert format shape (HH:MM:SS) and that
	// it's a valid time-of-day, not a specific value.
	got := formatHHMMSS("2026-05-08T20:13:01Z", true)
	if len(got) != 8 || got[2] != ':' || got[5] != ':' {
		t.Errorf("local-mode output should be HH:MM:SS, got %q", got)
	}
	// Unparseable input falls back to substring slicing rather than
	// erroring out.
	if got := formatHHMMSS("not-a-timestamp", true); got != "not-a-ti" {
		t.Errorf("malformed input should fall back to 8-char substring, got %q", got)
	}
}

func TestFormatTimestampForDisplayUTC(t *testing.T) {
	got := formatTimestampForDisplay("2026-05-08T20:13:01Z", false)
	if got != "2026-05-08 20:13:01 UTC" {
		t.Errorf("UTC mode: expected stable format, got %q", got)
	}
	if got := formatTimestampForDisplay("", false); got != "" {
		t.Errorf("empty input should produce empty output, got %q", got)
	}
	// Unparseable input round-trips so we never silently drop data on
	// the detail sheet.
	if got := formatTimestampForDisplay("garbage", true); got != "garbage" {
		t.Errorf("malformed input should round-trip, got %q", got)
	}
}

func TestFormatLogTimestamp(t *testing.T) {
	if got := formatLogTimestamp("2026-05-08T20:13:01Z", false); got != "2026-05-08T20:13:01" {
		t.Errorf("UTC mode: expected substring of input, got %q", got)
	}
	got := formatLogTimestamp("2026-05-08T20:13:01Z", true)
	if len(got) != 19 || got[10] != ' ' {
		t.Errorf("local mode: expected 'YYYY-MM-DD HH:MM:SS', got %q", got)
	}
}

func TestFormatEscalDetailContentRendersAllFields(t *testing.T) {
	p := daemon.PendingEntry{
		Key:         "Bash|2026-05-09T12:00:00Z|1",
		ProjectHash: "deadbeefcafebabe1234",
		Tool:        "Bash",
		Input:       "rm -rf /var/log",
		Verdict:     "escalate",
		Reason:      "destructive command pattern",
		Timestamp:   "2026-05-09T12:00:00Z",
	}
	got := formatEscalDetailContent(p, false)
	for _, want := range []string{
		"Bash",
		"rm -rf /var/log",
		"ESCALATED",
		"destructive command pattern",
		"deadbeefcafe", // shortened project hash (12-char prefix)
		"a[gray]:approve",
		"d[gray]:deny",
		"Esc[gray]:back",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("escal detail missing %q; got: %s", want, got)
		}
	}
}

func TestFormatEscalDetailEmptyMentionsEsc(t *testing.T) {
	got := formatEscalDetailEmpty()
	if !strings.Contains(got, "Esc") {
		t.Errorf("empty banner should reference Esc to back out, got: %q", got)
	}
	if !strings.Contains(got, "no pending") {
		t.Errorf("empty banner should explain why, got: %q", got)
	}
}

func TestRenderHeaderLineIncludesCounters(t *testing.T) {
	got := renderHeaderLine(42, 7, 3, "12:34:56", 200)
	for _, want := range []string{
		"events: 42",
		"[red]denied: 7[white]",
		"[orange]escalations: 3[white]",
		"12:34:56",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("header missing %q in output: %q", want, got)
		}
	}
}

func TestRenderHeaderLineDropsClockOnNarrowTerminal(t *testing.T) {
	// Width 30 is too narrow to fit the body + clock with min gap; the
	// renderer must drop the right-aligned clock rather than overflow
	// or overlap counters.
	got := renderHeaderLine(1, 0, 0, "12:34:56", 30)
	if strings.Contains(got, "12:34:56") {
		t.Errorf("expected clock to be dropped on narrow terminal, got: %q", got)
	}
	if !strings.Contains(got, "events: 1") {
		t.Errorf("counters must always render; got: %q", got)
	}
}

func TestRenderHeaderLineWidthZeroDropsClock(t *testing.T) {
	// Before the first draw GetInnerRect returns 0; we must still
	// render a usable line without trying to right-align the clock.
	got := renderHeaderLine(0, 0, 0, "12:34:56", 0)
	if strings.Contains(got, "12:34:56") {
		t.Errorf("expected clock to be omitted at width=0, got: %q", got)
	}
}

func TestVisibleLengthStripsTags(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"[red]abc[white]", 3},
		{"[red]a[white]bc", 3},
		{"", 0},
	}
	for _, c := range cases {
		if got := visibleLength(c.in); got != c.want {
			t.Errorf("visibleLength(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestCurrentClockShape(t *testing.T) {
	fixed := time.Date(2026, 5, 9, 12, 34, 56, 0, time.UTC)
	if got := currentClock(fixed, false); got != "12:34:56 UTC" {
		t.Errorf("UTC clock: expected '12:34:56 UTC', got %q", got)
	}
	got := currentClock(fixed, true)
	// Local zone varies by host — just assert HH:MM:SS shape.
	if len(got) != 8 || got[2] != ':' || got[5] != ':' {
		t.Errorf("local clock: expected HH:MM:SS, got %q", got)
	}
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
