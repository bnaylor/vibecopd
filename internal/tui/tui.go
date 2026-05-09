package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const maxLatencySamples = 50
const maxActivityItems = 200
const maxLogLines = 100
const daemonRequestTimeout = 2 * time.Second

type latencyStats struct {
	mu      sync.Mutex
	samples []int64
}

func (s *latencyStats) add(ms int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, ms)
	if len(s.samples) > maxLatencySamples {
		s.samples = s.samples[len(s.samples)-maxLatencySamples:]
	}
}

func (s *latencyStats) avg() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		return 0
	}
	var sum int64
	for _, v := range s.samples {
		sum += v
	}
	return float64(sum) / float64(len(s.samples))
}

func (s *latencyStats) min() int64 { s.mu.Lock(); defer s.mu.Unlock(); return minOf(s.samples) }
func (s *latencyStats) max() int64 { s.mu.Lock(); defer s.mu.Unlock(); return maxOf(s.samples) }
func (s *latencyStats) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.samples) }

func minOf(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxOf(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// formatHHMMSS returns the HH:MM:SS slice of an RFC3339 timestamp,
// shifted into the local zone when `local` is true. Falls back to
// substring slicing when the timestamp doesn't parse — keeps the
// renderer robust against legacy or malformed inputs without erroring
// out the whole row. Pure for testability.
func formatHHMMSS(rfc3339 string, local bool) string {
	if rfc3339 == "" {
		return ""
	}
	if local {
		if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
			return t.Local().Format("15:04:05")
		}
	}
	// UTC path or unparseable: take the substring after the T marker.
	ts := rfc3339
	if i := strings.Index(ts, "T"); i >= 0 && len(ts) >= i+9 {
		return ts[i+1 : i+9]
	}
	if len(ts) > 8 {
		return ts[:8]
	}
	return ts
}

// formatLogTimestamp returns a compact YYYY-MM-DD HH:MM:SS slice of an
// RFC3339 timestamp, shifted to local zone when `local` is true. Falls
// back to the existing 19-char substring trick when parse fails. Pure
// for testability.
func formatLogTimestamp(rfc3339 string, local bool) string {
	if rfc3339 == "" {
		return ""
	}
	if local {
		if t, err := time.Parse(time.RFC3339, rfc3339); err == nil {
			return t.Local().Format("2006-01-02 15:04:05")
		}
	}
	if len(rfc3339) > 19 {
		return rfc3339[:19]
	}
	return rfc3339
}

// formatTimestampForDisplay returns a human-readable absolute timestamp
// suitable for the detail sheet (no timezone abbreviations are appended;
// the field label tells the reader which zone is in play). Pure.
func formatTimestampForDisplay(rfc3339 string, local bool) string {
	if rfc3339 == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	if local {
		return t.Local().Format("2006-01-02 15:04:05 MST")
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

// verdictColor returns the tcell color for a verdict badge.
func verdictColor(verdict string) tcell.Color {
	switch verdict {
	case "approve":
		return tcell.ColorGreen
	case "deny":
		return tcell.ColorRed
	case "escalate":
		return tcell.ColorYellow
	default:
		return tcell.ColorWhite
	}
}

func verdictLabel(verdict string) string {
	switch verdict {
	case "approve":
		return "APPROVED"
	case "deny":
		return "DENIED"
	case "escalate":
		return "ESCALATED"
	case "error":
		return "ERROR"
	default:
		return strings.ToUpper(verdict)
	}
}

// Page identifiers used with tview.Pages.
const (
	pageActivity     = "activity"
	pageEscalations  = "escalations"
	pageEscalDetail  = "escal-detail"
	pageHelp         = "help"
	pageFullscreen   = "fullscreen"
	pageDetail       = "detail"
	pageConfigView   = "config-view"
	defaultPage      = pageActivity
	refreshDebounce  = 250 * time.Millisecond
	emptyEscalations = "[gray](no pending escalations)"
)

// Activity table column indices. col 0's Reference holds the full
// daemon.Event so the detail sheet can render every field without
// having to maintain a parallel events slice.
const (
	colTime    = 0
	colVerdict = 1
	colTool    = 2
	colBody    = 3
)

// App is the tview-based TUI.
type App struct {
	app        *tview.Application
	conn       net.Conn
	scanner    *bufio.Scanner
	socketPath string

	// displayLocalTime controls whether render-time formatting converts
	// daemon-supplied UTC RFC3339 timestamps to the local zone before
	// truncation. Defaults to true; the daemon honors the
	// `daemon.display_local_time` config knob and ships the resolved
	// value down via get_config. Reads and writes both happen inside
	// QueueUpdateDraw closures so they share a happens-before edge —
	// any read on a background goroutine is a data race.
	displayLocalTime bool

	// configPath is the absolute path of the live config.toml as
	// reported by the daemon. Empty until the first get_config response
	// lands. Used by the config view/edit surface (VCOP-16.6) — the TUI
	// must edit the live file, not a guessed default.
	configPath string

	headerView      *tview.TextView
	activity        *tview.Table
	latencyView     *tview.TextView
	configView      *tview.TextView
	logView         *tview.TextView
	escalations     *tview.List
	escalEmpty      *tview.TextView
	escalDetailView *tview.TextView
	configFileView  *tview.TextView
	helpView        *tview.TextView
	detailView      *tview.TextView
	statusBar       *tview.TextView
	pages           *tview.Pages

	// Snapshot of the currently rendered escalation list. Accessed only on
	// the application goroutine so it stays aligned with the visible rows.
	pending []daemon.PendingEntry

	currentPage string
	prevPage    string
	lastRefresh time.Time
	refreshing  bool

	// Tab-cycle order of focusable primitives on the activity page.
	// Tab advances through this list; Shift-Tab reverses. Only used
	// while currentPage == pageActivity.
	activityFocusables     []tview.Primitive
	activityFocusableNames []string
	activityFocusIdx       int

	// Container for the currently full-screened pane. The same
	// primitive can be referenced by both the activity layout and this
	// container — only the visible Pages page draws, so there's no
	// overlap conflict. Empty when no pane is full-screened.
	fullscreenContainer *tview.Flex

	latency       *latencyStats
	events        int
	denied        int
	logHasEntries bool

	// Stop signal for the header clock goroutine. Closed in Close() to
	// terminate the ticker without leaking once the TUI exits.
	clockDone chan struct{}
	closeOnce sync.Once

	// escalInFlight is set to true while completeSelectedAndAdvance has
	// a goroutine in flight, and reset to false inside the terminal
	// QueueUpdateDraw closure. Both reads and writes happen exclusively
	// on the tview main goroutine (the input capture that sets it, and
	// the QueueUpdateDraw closure that clears it) so no mutex is
	// required.
	escalInFlight bool

	mu sync.Mutex
}

// focusedBorder is the border color of the currently focused panel.
// blurredBorder is the default. Wired via SetFocusFunc / SetBlurFunc on
// each focusable primitive in buildActivityPage.
var (
	focusedBorder = tcell.ColorYellow
	blurredBorder = tcell.ColorWhite
)

// Run connects to the daemon and starts the TUI. Blocks until the user quits.
func Run(socketPath string) error {
	conn, err := net.DialTimeout("unix", socketPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}

	// Subscribe to events.
	sub := daemon.Request{Type: daemon.TypeTUISubscribe}
	if err := json.NewEncoder(conn).Encode(sub); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe: %w", err)
	}

	a := &App{
		conn:             conn,
		scanner:          bufio.NewScanner(conn),
		socketPath:       socketPath,
		latency:          &latencyStats{},
		currentPage:      defaultPage,
		displayLocalTime: true,
		clockDone:        make(chan struct{}),
	}

	err = a.runUI()
	a.Close()
	return err
}

func (a *App) runUI() error {
	a.app = tview.NewApplication()

	root := tview.NewFlex().SetDirection(tview.FlexRow)

	// Header.
	a.headerView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.headerView.SetText("[green]vibecop[white] ● running  |  connect to TUI")
	a.headerView.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	root.AddItem(a.headerView, 3, 0, false)

	// Pages — activity, escalations, help, fullscreen overlay, and the
	// activity detail sheet (Enter on a selected row).
	a.pages = tview.NewPages()
	a.pages.AddPage(pageActivity, a.buildActivityPage(), true, true)
	a.pages.AddPage(pageEscalations, a.buildEscalationsPage(), true, false)
	a.pages.AddPage(pageEscalDetail, a.buildEscalDetailPage(), true, false)
	a.pages.AddPage(pageHelp, a.buildHelpPage(), true, false)
	a.fullscreenContainer = tview.NewFlex().SetDirection(tview.FlexRow)
	a.pages.AddPage(pageFullscreen, a.fullscreenContainer, true, false)
	a.pages.AddPage(pageDetail, a.buildDetailPage(), true, false)
	a.pages.AddPage(pageConfigView, a.buildConfigViewPage(), true, false)
	root.AddItem(a.pages, 0, 1, true)

	// Status bar (context-aware).
	a.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.statusBar.SetBorder(true).SetBorderPadding(0, 0, 1, 1)
	root.AddItem(a.statusBar, 3, 0, false)
	a.updateStatusBar()

	// Global keyboard shortcuts. Page-specific keys are attached on the
	// per-page primitives (escalations List handles a/d itself).
	root.SetInputCapture(a.globalInput)

	// Start reading events in background.
	go a.readEvents()

	// Initial config fetch. Runs in a goroutine so it doesn't race the
	// app loop startup; QueueUpdateDraw is buffered, so the goroutine's
	// first SetText queues against the loop without blocking even if it
	// finishes before Run() begins draining updates.
	go a.fetchAndRenderConfig()

	// Header clock — re-renders once per second so the right-aligned
	// time stays current. Stops on a.clockDone (closed in Close()).
	go a.runClock()

	a.app.SetRoot(root, true)
	return a.app.Run()
}

func (a *App) buildActivityPage() tview.Primitive {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	rightPanel := tview.NewFlex().SetDirection(tview.FlexRow)

	a.latencyView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(true)
	a.latencyView.SetTitle("latency").SetBorder(true)
	a.latencyView.SetText("waiting for data...")
	// Latency renders 3 content lines (avg / min / max); fix the slot
	// to 5 rows (border + 3 + border) so it stops claiming half the
	// sidebar by weight when config has more to show.
	rightPanel.AddItem(a.latencyView, 5, 0, false)

	a.configView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(true)
	a.configView.SetTitle("config").SetBorder(true)
	a.configView.SetText("waiting for data...")
	// Enter opens a read-only fullscreen view of the actual config.toml
	// file. `e` launches $EDITOR on a temp copy with TOML validation
	// before atomic replacement (VCOP-16.6). Other keys fall through to
	// scrolling and global handlers.
	a.configView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if a.currentPage != pageActivity {
			return event
		}
		if event.Key() == tcell.KeyEnter {
			a.openConfigView()
			return nil
		}
		if event.Rune() == 'e' {
			a.editConfigFile()
			return nil
		}
		return event
	})
	// Config takes whatever vertical space the sidebar has left after
	// the fixed-size latency and log slots.
	rightPanel.AddItem(a.configView, 0, 1, false)

	// Log slot lives at the bottom of the right sidebar — keeps it
	// compact and out of the activity feed's row budget. The borders
	// of the config (above) and log (below) sit on adjacent rows so
	// they read as a continuous boundary even though tview draws each
	// box's frame independently. 3 rows total: border + 1 content row
	// + border. Press `f` while focused to expand to a multi-row
	// scrollback view.
	a.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(true).
		SetWrap(false)
	a.logView.SetTitle("log").SetBorder(true)
	a.logView.SetText("[gray]idle  ([white]Enter[gray] / [white]f[gray] to expand)[white]")
	// Enter on the focused log pane mirrors the global `f` shortcut so
	// keyboard-only users discover expansion via the idle hint without
	// having to memorise a separate hotkey. Other keys fall through to
	// the TextView's default (scrolling), then the global capture.
	a.logView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEnter && a.currentPage == pageActivity {
			a.toggleFullscreen()
			return nil
		}
		return event
	})
	rightPanel.AddItem(a.logView, 3, 0, false)

	// Activity feed is a tview.Table so we get all three behaviors at
	// once: row selection (highlight bar), horizontal scroll across
	// long inputs, and an Enter handler for the detail sheet. Each
	// row's first cell carries a Reference to the full daemon.Event,
	// so the detail sheet can render every field without a parallel
	// events slice.
	//
	// SetSelectable(true, false): rows selectable, columns not. ↑/↓
	// moves the highlight; ←/→ scrolls horizontally when content
	// extends past the viewport; Enter fires SetSelectedFunc.
	//
	// SetFixed(0, 1): keeps the time column locked when scrolling
	// horizontally so the user always sees which event each row is.
	a.activity = tview.NewTable().
		SetSelectable(true, false).
		SetFixed(0, 1)
	a.activity.SetTitle("activity").SetBorder(true)
	a.activity.SetSelectedFunc(func(row, _ int) { a.openDetail(row) })

	// Mark the activity List as the focused item inside its Flex chain.
	// Without focus=true here, focus dead-ends at the middle Flex (which
	// has no input handler for ↑/↓), so the List never receives arrow
	// keys. Pages.SwitchToPage re-runs Focus() on the visible page and
	// delegates down through Flexes via the per-item Focus flag — so the
	// chain has to be marked end-to-end (middle: true, activity: true).
	// Activity now spans the full page height (no separate bottom log
	// row stealing rows) so this is the only horizontal split.
	middle := tview.NewFlex().
		AddItem(a.activity, 0, 3, true).
		AddItem(rightPanel, 0, 2, false)
	flex.AddItem(middle, 0, 1, true)

	// Cycle order: activity → config → log. Latency is intentionally
	// excluded — it's three lines of stats, fullscreening it is just
	// whitespace, and there's nothing inside it to scroll. Cycling
	// through it would just be noise. The pane still renders normally;
	// it's just not part of the focus rotation.
	a.activityFocusables = []tview.Primitive{
		a.activity,
		a.configView,
		a.logView,
	}
	a.activityFocusableNames = []string{"activity", "config", "log"}
	a.wireFocusHighlight(a.activity.Box)
	a.wireFocusHighlight(a.configView.Box)
	a.wireFocusHighlight(a.logView.Box)

	return flex
}

// wireFocusHighlight installs focus/blur callbacks that color the
// border yellow on focus and white on blur. The Box is what owns the
// border — for List/TextView we grab the embedded *Box to register
// callbacks via SetFocusFunc / SetBlurFunc.
func (a *App) wireFocusHighlight(box *tview.Box) {
	box.SetFocusFunc(func() {
		box.SetBorderColor(focusedBorder)
		a.updateStatusBar()
	}).SetBlurFunc(func() {
		box.SetBorderColor(blurredBorder)
	})
}

func (a *App) buildEscalationsPage() tview.Primitive {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	a.escalations = tview.NewList().ShowSecondaryText(true)
	a.escalations.SetTitle("escalations — pending").SetBorder(true)
	a.escalations.SetInputCapture(a.escalationsInput)

	a.escalEmpty = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	a.escalEmpty.SetText(emptyEscalations)
	a.escalEmpty.SetBorder(false)

	flex.AddItem(a.escalations, 0, 1, true)
	flex.AddItem(a.escalEmpty, 1, 0, false)
	return flex
}

func (a *App) buildHelpPage() tview.Primitive {
	a.helpView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.helpView.SetTitle("help — keyboard shortcuts").SetBorder(true)
	a.helpView.SetText(helpText())
	a.helpView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Any key closes the help modal.
		a.closeHelp()
		return nil
	})
	return a.helpView
}

// buildDetailPage builds the activity-detail sheet — a fullscreen
// scrollable TextView that renders every field of the selected event.
// Esc / Enter / d close it (handled in the view's input capture). The
// view's content is set lazily via openDetail; the page itself is
// constructed once at startup.
func (a *App) buildDetailPage() tview.Primitive {
	a.detailView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(true).
		SetWordWrap(true).
		SetScrollable(true)
	a.detailView.SetTitle("activity detail").SetBorder(true)
	a.detailView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc, tcell.KeyEnter:
			a.closeDetail()
			return nil
		}
		// Single-key shortcut to close so the user can dismiss without
		// reaching for Esc.
		if event.Rune() == 'd' {
			a.closeDetail()
			return nil
		}
		// Everything else (↑/↓/PgUp/PgDn) falls through to the
		// TextView's default scrolling.
		return event
	})
	return a.detailView
}

// buildEscalDetailPage builds the full-size escalation detail sheet —
// pressing Enter on the escalations List opens it. Renders every field
// of the highlighted PendingEntry. `a` and `d` complete-and-advance
// (approve/deny → next pending entry); the page only auto-closes when
// the user presses Esc, so an empty queue still displays a banner.
func (a *App) buildEscalDetailPage() tview.Primitive {
	a.escalDetailView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetWrap(true).
		SetWordWrap(true).
		SetScrollable(true)
	a.escalDetailView.SetTitle("escalation detail").SetBorder(true)
	a.escalDetailView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEsc:
			a.closeEscalDetail()
			return nil
		}
		switch event.Rune() {
		case 'a':
			a.completeSelectedAndAdvance("approved")
			return nil
		case 'd':
			a.completeSelectedAndAdvance("blocked")
			return nil
		}
		return event
	})
	return a.escalDetailView
}

// openEscalDetail renders the highlighted pending entry into the
// detail page and switches to it. If the queue is empty, the page
// still opens but shows the empty banner so the user has a consistent
// surface to act from.
func (a *App) openEscalDetail() {
	a.currentPage = pageEscalDetail
	a.pages.SwitchToPage(pageEscalDetail)
	a.app.SetFocus(a.escalDetailView)
	a.renderEscalDetail()
	a.updateStatusBar()
}

// closeEscalDetail returns from the detail page back to the escalations
// list, restoring focus so the highlight cursor lives where the user
// expects.
func (a *App) closeEscalDetail() {
	a.currentPage = pageEscalations
	a.pages.SwitchToPage(pageEscalations)
	if a.escalations != nil {
		a.app.SetFocus(a.escalations)
	}
	a.updateStatusBar()
}

// renderEscalDetail repaints the detail view from the currently-
// highlighted entry. Idempotent — safe to call after every queue
// mutation. When no entries remain, renders an explicit empty banner
// so the user knows their last action cleared the queue.
func (a *App) renderEscalDetail() {
	if a.escalDetailView == nil {
		return
	}
	idx := -1
	if a.escalations != nil {
		idx = a.escalations.GetCurrentItem()
	}
	if idx < 0 || idx >= len(a.pending) {
		a.escalDetailView.SetText(formatEscalDetailEmpty())
		return
	}
	a.escalDetailView.SetText(formatEscalDetailContent(a.pending[idx], a.displayLocalTime))
	a.escalDetailView.ScrollToBeginning()
}

// formatEscalDetailContent renders every field of a PendingEntry into
// the rich detail view. Pure for testability.
func formatEscalDetailContent(p daemon.PendingEntry, local bool) string {
	var sb strings.Builder
	colorName := verdictColor(p.Verdict).String()

	sb.WriteString("\n")
	writeField := func(label, value string) {
		sb.WriteString(fmt.Sprintf("  [yellow]%-12s[white] %s\n", label+":", value))
	}
	writeField("Timestamp", emptyOrValue(formatTimestampForDisplay(p.Timestamp, local)))
	writeField("Project", emptyOrValue(shortProjectHash(p.ProjectHash)))
	writeField("Tool", emptyOrValue(p.Tool))
	sb.WriteString(fmt.Sprintf("  [yellow]%-12s[white] [%s]%s[white]\n",
		"Verdict:", colorName, verdictLabel(p.Verdict)))

	if p.Input != "" {
		sb.WriteString("\n  [yellow]Input:[white]\n")
		sb.WriteString(indentBlock(p.Input, "    ") + "\n")
	}
	if p.Reason != "" {
		sb.WriteString("\n  [yellow]Reason:[white]\n")
		sb.WriteString(indentBlock(p.Reason, "    ") + "\n")
	}

	sb.WriteString("\n  [gray]── [white]a[gray]:approve  [white]d[gray]:deny  [white]Esc[gray]:back ──[white]\n")
	return sb.String()
}

// formatEscalDetailEmpty is the banner shown when the queue is empty
// but the user is still on the detail page (e.g. they just cleared
// the last entry). Pure for testability.
func formatEscalDetailEmpty() string {
	return "\n  [gray](no pending escalations)[white]\n\n  [gray]Press [white]Esc[gray] to return to the queue.[white]\n"
}

// completeSelectedAndAdvance is the detail-page sibling of
// completeSelected: after the daemon acknowledges the human decision,
// the queue is refreshed and the detail view repaints with whichever
// entry now sits at the prior index (clamped). Empty queue → the
// empty banner.
//
// The refresh-then-advance flow is one synchronous goroutine that
// owns the whole sequence: complete → fetchPending → queue a single
// QueueUpdateDraw closure that does both rebuild and advance. An
// earlier version split rebuild and advance into two QueueUpdateDraw
// calls and assumed they'd land in order; they didn't (the rebuild
// goroutine queued its draw *after* the advance closure had already
// been queued), causing rapid `a`/`d` keystrokes to act on stale
// indices and surface "pending record not found" errors. This shape
// is the fix for that race.
//
// escalInFlight prevents a second goroutine from launching while the
// first is still in flight. Both the set (here, on the main goroutine)
// and the clear (inside every terminal QueueUpdateDraw closure) happen
// exclusively on the tview main goroutine.
func (a *App) completeSelectedAndAdvance(humanDecision string) {
	if a.escalInFlight {
		return
	}
	if a.escalations == nil || a.escalations.GetItemCount() == 0 {
		return
	}
	idx := a.escalations.GetCurrentItem()
	if idx < 0 || idx >= len(a.pending) {
		return
	}
	target := a.pending[idx]
	a.escalInFlight = true

	go func() {
		if err := a.completePending(target.ProjectHash, target.Key, humanDecision); err != nil {
			// On daemon error, repaint the detail view with the
			// error banner *plus* the original entry below, so the
			// user has context — just an error string with no
			// surrounding entry leaves them stuck unable to tell
			// what they were acting on.
			a.app.QueueUpdateDraw(func() {
				a.escalInFlight = false
				banner := fmt.Sprintf("\n  [red]complete_pending failed: %v[white]\n\n", err)
				a.escalDetailView.SetText(banner + formatEscalDetailContent(target, a.displayLocalTime))
			})
			return
		}

		// Synchronously fetch the new pending list before queueing
		// any draw — this is the load-bearing ordering. By the time
		// the closure below runs, fresh pending data is already in
		// the local var, so rebuild + advance happen atomically on
		// the main goroutine.
		pending, auditEnabled, fetchErr := a.fetchPending()
		if fetchErr != nil {
			a.app.QueueUpdateDraw(func() {
				a.escalInFlight = false
				a.escalEmpty.SetText(fmt.Sprintf("[red]list_pending failed: %v[white]", fetchErr))
			})
			return
		}

		a.app.QueueUpdateDraw(func() {
			a.escalInFlight = false
			a.rebuildEscalationList(pending, auditEnabled)
			a.advanceAfterCompletion(idx)
		})
	}()
}

// advanceAfterCompletion picks the new selected index after a
// completion: same idx if still in bounds, else last entry. Then
// repaints. Runs on the tview main goroutine.
func (a *App) advanceAfterCompletion(prevIdx int) {
	if a.escalations == nil {
		return
	}
	count := a.escalations.GetItemCount()
	if count == 0 {
		a.renderEscalDetail()
		return
	}
	next := prevIdx
	if next >= count {
		next = count - 1
	}
	if next < 0 {
		next = 0
	}
	a.escalations.SetCurrentItem(next)
	a.renderEscalDetail()
}

// buildConfigViewPage builds the fullscreen read-only view of the
// live config.toml file. Enter on the focused config sidebar opens it;
// `e` from anywhere on this page jumps into $EDITOR with TOML
// validation (configedit.go).
func (a *App) buildConfigViewPage() tview.Primitive {
	a.configFileView = tview.NewTextView().
		SetDynamicColors(false).
		SetTextAlign(tview.AlignLeft).
		SetWrap(false).
		SetScrollable(true)
	a.configFileView.SetTitle("config.toml — read-only").SetBorder(true)
	a.configFileView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc || event.Key() == tcell.KeyEnter {
			a.closeConfigView()
			return nil
		}
		if event.Rune() == 'e' {
			a.editConfigFile()
			return nil
		}
		return event
	})
	return a.configFileView
}

// openConfigView reads the current config.toml off disk and renders it
// into the fullscreen read-only view. We always re-read at open time so
// a previous edit pass is reflected without restarting the TUI. On read
// failure the view shows the error rather than refusing to switch
// pages — the user can still hit Esc to return.
func (a *App) openConfigView() {
	body := a.readConfigFileForView()
	a.configFileView.SetText(body)
	a.configFileView.ScrollToBeginning()
	a.currentPage = pageConfigView
	a.pages.SwitchToPage(pageConfigView)
	a.app.SetFocus(a.configFileView)
	a.updateStatusBar()
}

// closeConfigView returns from the fullscreen view back to the
// activity page, restoring focus to whichever activity-page pane was
// last active so the user lands where they expect.
func (a *App) closeConfigView() {
	a.currentPage = pageActivity
	a.pages.SwitchToPage(pageActivity)
	if len(a.activityFocusables) > 0 {
		a.app.SetFocus(a.activityFocusables[a.activityFocusIdx])
	}
	a.updateStatusBar()
}

// readConfigFileForView returns the current config.toml content as a
// scrollable string, or a friendly error banner. Pure logic separated
// from the view so it's straightforward to unit test the failure
// paths via t.TempDir.
func (a *App) readConfigFileForView() string {
	if a.configPath == "" {
		return "(no config_path advertised by daemon — try restarting the TUI after the daemon's first get_config response)"
	}
	return readConfigFileBody(a.configPath)
}

// openDetail renders the event at activity table row `row` into the
// detail sheet and switches to the detail page. Lookup is via the
// Reference attached to col 0; if missing, the page just shows an
// empty banner instead of erroring.
func (a *App) openDetail(row int) {
	if a.activity == nil || row < 0 {
		return
	}
	cell := a.activity.GetCell(row, colTime)
	if cell == nil {
		return
	}
	evt, ok := cell.GetReference().(daemon.Event)
	if !ok {
		return
	}
	a.detailView.SetText(formatDetailContent(evt, a.displayLocalTime))
	a.detailView.ScrollToBeginning()
	a.currentPage = pageDetail
	a.pages.SwitchToPage(pageDetail)
	a.app.SetFocus(a.detailView)
	a.updateStatusBar()
}

// closeDetail returns from the detail sheet to the activity page,
// restoring focus to the activity table so the user resumes from the
// same selected row they came in on.
func (a *App) closeDetail() {
	a.currentPage = pageActivity
	a.pages.SwitchToPage(pageActivity)
	if len(a.activityFocusables) > 0 {
		a.app.SetFocus(a.activityFocusables[a.activityFocusIdx])
	}
	a.updateStatusBar()
}

// formatDetailContent renders all fields we have for an event into a
// human-readable rich view. Pure for testability. `local` shifts the
// displayed timestamp into the user's zone when true.
func formatDetailContent(evt daemon.Event, local bool) string {
	var sb strings.Builder
	colorName := verdictColor(evt.Verdict).String()

	sb.WriteString("\n")
	writeField := func(label, value string) {
		sb.WriteString(fmt.Sprintf("  [yellow]%-11s[white] %s\n", label+":", value))
	}
	writeField("Timestamp", emptyOrValue(formatTimestampForDisplay(evt.Timestamp, local)))
	writeField("Tool", emptyOrValue(evt.Tool))
	sb.WriteString(fmt.Sprintf("  [yellow]%-11s[white] [%s]%s[white]\n",
		"Verdict:", colorName, verdictLabel(evt.Verdict)))
	if evt.LatencyMs > 0 {
		writeField("Latency", fmt.Sprintf("%d ms", evt.LatencyMs))
	}
	if evt.Level != "" {
		writeField("Level", evt.Level)
	}

	if evt.Input != "" {
		sb.WriteString("\n  [yellow]Input:[white]\n")
		sb.WriteString(indentBlock(evt.Input, "    ") + "\n")
	}
	if evt.Reason != "" {
		sb.WriteString("\n  [yellow]Reason:[white]\n")
		sb.WriteString(indentBlock(evt.Reason, "    ") + "\n")
	}
	if evt.Message != "" {
		sb.WriteString("\n  [yellow]Message:[white]\n")
		sb.WriteString(indentBlock(evt.Message, "    ") + "\n")
	}

	sb.WriteString("\n  [gray]── Esc / Enter / d to close ──[white]\n")
	return sb.String()
}

func emptyOrValue(s string) string {
	if s == "" {
		return "[gray](none)[white]"
	}
	return s
}

func indentBlock(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func helpText() string {
	return strings.Join([]string{
		"",
		"  [yellow]Global[white]",
		"    [white]q[gray]            quit",
		"    [white]?[gray] / [white]h[gray]        toggle this help",
		"    [white]e[gray]            switch to escalations",
		"    [white]Esc[gray]          back to activity",
		"",
		"  [yellow]Activity page[white]",
		"    [white]Tab[gray] / [white]Shift-Tab[gray]  cycle focus across panes (yellow border)",
		"    [white]↑/↓[gray]          move highlight (activity) / scroll (other panes)",
		"    [white]←/→[gray]          horizontal scroll of long entries",
		"    [white]Enter[gray]        open detail sheet for highlighted event",
		"    [white]f[gray]            expand focused pane to fullscreen ([white]Esc[gray] / [white]f[gray] to collapse)",
		"    [white]Enter[gray]        on the log pane: same as [white]f[gray] (expand to fullscreen)",
		"    [white]r[gray]            refresh config",
		"",
		"  [yellow]Escalations page[white]",
		"    [white]↑/↓[gray]          scroll pending list",
		"    [white]Enter[gray]        open full-size detail with approve/deny controls",
		"    [white]a[gray]            approve selected (audit only — agent already saw harness prompt)",
		"    [white]d[gray]            deny selected (audit only)",
		"    [white]R[gray]            refresh queue",
		"",
		"  [yellow]Config pane (sidebar)[white]",
		"    [white]Enter[gray]        open read-only fullscreen view of the live config.toml",
		"    [white]e[gray]            edit config.toml in [white]$EDITOR[gray] (TOML-validated, atomic save)",
		"",
		"  [gray]Press any key to close help.",
	}, "\n")
}

// globalInput handles keys that work from any page. The active page's
// own primitive (e.g. the escalations List) gets first crack at the
// event; this fires after for unhandled keys.
func (a *App) globalInput(event *tcell.EventKey) *tcell.EventKey {
	// `?` / `h` toggles help from anywhere except inside help (the help
	// view's own input capture handles "any key closes").
	if a.currentPage != pageHelp {
		switch event.Rune() {
		case '?', 'h':
			a.openHelp()
			return nil
		}
	}

	switch event.Rune() {
	case 'q':
		a.app.Stop()
		return nil
	case 'e':
		a.switchTo(pageEscalations)
		return nil
	case 'f':
		// `f` only meaningful from the activity page (entering fullscreen)
		// or from fullscreen itself (exiting). Other pages already fill
		// the available space.
		if a.currentPage == pageActivity || a.currentPage == pageFullscreen {
			a.toggleFullscreen()
			return nil
		}
	case 'r':
		if a.currentPage == pageActivity {
			a.refreshConfig()
			return nil
		}
	case 'R':
		if a.currentPage == pageEscalations {
			a.requestEscalationRefresh(true)
			return nil
		}
	}

	if event.Key() == tcell.KeyEsc {
		switch a.currentPage {
		case pageEscalations, pageHelp:
			a.switchTo(pageActivity)
			return nil
		case pageFullscreen:
			a.toggleFullscreen()
			return nil
		case pageDetail:
			a.closeDetail()
			return nil
		case pageEscalDetail:
			a.closeEscalDetail()
			return nil
		case pageConfigView:
			a.closeConfigView()
			return nil
		}
	}

	// Tab / Shift-Tab: cycle focus between activity-page panels. Only
	// active on the activity page — other pages have at most one
	// focusable primitive so cycling is a no-op there.
	if a.currentPage == pageActivity && len(a.activityFocusables) > 0 {
		switch event.Key() {
		case tcell.KeyTab:
			a.cycleActivityFocus(+1)
			return nil
		case tcell.KeyBacktab:
			a.cycleActivityFocus(-1)
			return nil
		}
	}
	return event
}

// cycleActivityFocus advances the focused panel on the activity page by
// `step` (+1 for Tab, -1 for Shift-Tab). The focus/blur callbacks wired
// in buildActivityPage repaint the borders and refresh the status bar.
func (a *App) cycleActivityFocus(step int) {
	n := len(a.activityFocusables)
	if n == 0 {
		return
	}
	a.activityFocusIdx = (a.activityFocusIdx + step + n) % n
	a.app.SetFocus(a.activityFocusables[a.activityFocusIdx])
}

// toggleFullscreen swaps the focused activity-page pane between its
// embedded slot and a dedicated fullscreen page. The same primitive is
// referenced by both the activity Flex and the fullscreen container —
// safe because Pages only draws the visible page, so there's never a
// layout collision. On exit, the primitive lives back in its embedded
// slot and the activity Flex re-lays it out via SetRect on next draw.
func (a *App) toggleFullscreen() {
	if a.currentPage == pageFullscreen {
		// Exit fullscreen: clear the container so the embedded copy is
		// the only place the primitive renders, then return to activity.
		a.fullscreenContainer.Clear()
		a.currentPage = pageActivity
		a.pages.SwitchToPage(pageActivity)
		if len(a.activityFocusables) > 0 {
			a.app.SetFocus(a.activityFocusables[a.activityFocusIdx])
		}
		a.updateStatusBar()
		return
	}

	if a.currentPage != pageActivity || len(a.activityFocusables) == 0 {
		return
	}
	pane := a.activityFocusables[a.activityFocusIdx]
	a.fullscreenContainer.Clear()
	a.fullscreenContainer.AddItem(pane, 0, 1, true)
	a.currentPage = pageFullscreen
	a.pages.SwitchToPage(pageFullscreen)
	a.updateStatusBar()
}

// escalationsInput handles keys when the escalations List is focused.
// The List itself absorbs ↑/↓; we intercept `a` and `d` for verdicts,
// `Enter` to open the full-size detail page, and let everything else
// fall through to globalInput.
func (a *App) escalationsInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyEnter {
		a.openEscalDetail()
		return nil
	}
	switch event.Rune() {
	case 'a':
		a.completeSelected("approved")
		return nil
	case 'd':
		a.completeSelected("blocked")
		return nil
	}
	return event
}

// switchTo runs on the tview main goroutine (called from input handlers).
// It must NOT call QueueUpdate{,Draw} — those block waiting for the main
// loop to drain the update channel, which deadlocks since the main loop
// is currently executing this handler. Direct primitive mutation is safe
// here; tview redraws automatically after the input handler returns.
func (a *App) switchTo(name string) {
	if name == a.currentPage {
		return
	}
	a.currentPage = name
	a.pages.SwitchToPage(name)
	if name == pageEscalations {
		a.requestEscalationRefresh(true)
	}
	if name == pageActivity {
		// Reset the focus cursor so a fresh visit starts at the activity
		// List rather than at whichever pane the user was last on. Then
		// align tview's actual focus to match the cursor.
		a.activityFocusIdx = 0
		if len(a.activityFocusables) > 0 {
			a.app.SetFocus(a.activityFocusables[0])
		}
	}
	a.updateStatusBar()
}

func (a *App) openHelp() {
	a.prevPage = a.currentPage
	a.currentPage = pageHelp
	a.pages.SwitchToPage(pageHelp)
	a.updateStatusBar()
}

func (a *App) closeHelp() {
	target := a.prevPage
	if target == "" {
		target = defaultPage
	}
	a.currentPage = target
	a.pages.SwitchToPage(target)
	a.updateStatusBar()
}

func (a *App) updateStatusBar() {
	var hint string
	switch a.currentPage {
	case pageActivity:
		hint = "[white]q[gray]:quit  [white]e[gray]:escalations  [white]↑/↓[gray]:select  [white]Enter[gray]:detail  [white]Tab[gray]:next pane  [white]f[gray]:fullscreen  [white]r[gray]:refresh"
	case pageEscalations:
		hint = "[white]q[gray]:quit  [white]Enter[gray]:detail  [white]a[gray]:approve  [white]d[gray]:deny  [white]R[gray]:refresh  [white]Esc[gray]:back"
	case pageEscalDetail:
		hint = "[white]q[gray]:quit  [white]a[gray]:approve+next  [white]d[gray]:deny+next  [white]Esc[gray]:back to list"
	case pageConfigView:
		hint = "[white]q[gray]:quit  [white]e[gray]:edit  [white]Esc[gray] / [white]Enter[gray]:back"
	case pageHelp:
		hint = "[gray]press any key to close help"
	case pageFullscreen:
		hint = "[white]q[gray]:quit  [white]Esc[gray] / [white]f[gray]:collapse"
	case pageDetail:
		hint = "[white]q[gray]:quit  [white]↑/↓[gray]:scroll  [white]Esc[gray] / [white]Enter[gray] / [white]d[gray]:close"
	default:
		hint = "[white]q[gray]:quit"
	}
	if a.statusBar == nil {
		return
	}
	label := a.currentPage
	if a.currentPage == pageFullscreen {
		// Show which pane is being viewed so the label is informative
		// rather than just "fullscreen".
		if a.activityFocusIdx < len(a.activityFocusableNames) {
			label = fmt.Sprintf("fullscreen: %s", a.activityFocusableNames[a.activityFocusIdx])
		}
	}
	a.statusBar.SetText(fmt.Sprintf("[yellow]%s[gray]   %s   [white]?[gray]:help", label, hint))
}

func (a *App) readEvents() {
	a.scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for a.scanner.Scan() {
		line := a.scanner.Bytes()
		var evt daemon.Event
		if err := json.Unmarshal(line, &evt); err != nil {
			continue
		}
		a.handleEvent(evt)
	}
}

func (a *App) handleEvent(evt daemon.Event) {
	a.mu.Lock()
	a.events++
	if evt.Verdict == "deny" {
		a.denied++
	}
	a.mu.Unlock()

	// Log-level events go to the log tail.
	if evt.Level != "" || evt.Message != "" {
		a.addLogLine(evt)
	}

	// Tool verdict events — always show in the activity feed even when they
	// also carry a log level (e.g. pass-through / suspension events).
	if evt.Tool != "" {
		a.addActivity(evt)
		if evt.LatencyMs > 0 {
			a.latency.add(evt.LatencyMs)
		}
		a.updateLatencyDisplay()
		a.maybeRefreshOnEvent(evt.Verdict)
	}

	// Update header on each event as a heartbeat.
	a.updateHeader(evt)
}

// addActivity inserts a new event at the top of the activity Table
// (newest-first). The hot subtlety is the highlight: when the user has
// the activity pane focused, the highlighted row must stay visually
// pinned regardless of how many events arrive — the cursor should
// never slide out from under the user. We achieve that by reading the
// current selection + scroll offset, inserting at row 0, then bumping
// both by 1 (the count we just prepended). When the user has Tab'd
// away to another pane, no compensation is needed and the table
// scrolls naturally.
func (a *App) addActivity(evt daemon.Event) {
	a.app.QueueUpdateDraw(func() {
		// formatActivityCells reads a.displayLocalTime — keep it
		// inside QueueUpdateDraw so the read shares a happens-before
		// edge with UpdateConfig's writes (also QueueUpdateDraw'd).
		// Reading on the readEvents goroutine before the closure
		// would be a data race.
		ts, verdict, tool, body, vColor := formatActivityCells(evt, a.displayLocalTime)

		focused := a.app.GetFocus() == a.activity
		var prevSelRow, prevOffRow int
		if focused {
			prevSelRow, _ = a.activity.GetSelection()
			prevOffRow, _ = a.activity.GetOffset()
		}

		a.activity.InsertRow(0)
		// First cell carries the full event for the detail sheet.
		a.activity.SetCell(0, colTime,
			tview.NewTableCell(ts).
				SetTextColor(tcell.ColorDarkGray).
				SetReference(evt))
		a.activity.SetCell(0, colVerdict,
			tview.NewTableCell(verdict).
				SetTextColor(vColor))
		a.activity.SetCell(0, colTool,
			tview.NewTableCell(tool).
				SetTextColor(tcell.ColorYellow))
		a.activity.SetCell(0, colBody,
			tview.NewTableCell(body).
				SetTextColor(tcell.ColorWhite).
				SetExpansion(1))

		// Trim oldest rows to cap memory.
		for a.activity.GetRowCount() > maxActivityItems {
			a.activity.RemoveRow(a.activity.GetRowCount() - 1)
		}

		if focused {
			// Pin: the row the user was on is now at prevSelRow+1.
			// Shifting both selection and offset by 1 keeps the
			// highlighted row in the same screen position.
			a.activity.Select(prevSelRow+1, 0)
			a.activity.SetOffset(prevOffRow+1, 0)
		}
	})
}

// formatActivityCells renders the per-column strings for one event.
// Pure (no tview state) so it can be unit-tested. The right-most cell
// (body) holds input + optional reason at full length — nothing is
// truncated; horizontal scroll reveals everything off-screen.
//
// `local` controls whether the HH:MM:SS slice is taken in the user's
// local zone or the daemon's UTC zone (see App.displayLocalTime).
func formatActivityCells(evt daemon.Event, local bool) (ts, verdict, tool, body string, vColor tcell.Color) {
	ts = formatHHMMSS(evt.Timestamp, local)

	verdict = verdictLabel(evt.Verdict)
	vColor = verdictColor(evt.Verdict)

	tool = evt.Tool
	if tool == "" {
		tool = "-"
	}

	body = evt.Input
	if evt.Reason != "" {
		if body != "" {
			body += "  · " + evt.Reason
		} else {
			body = "· " + evt.Reason
		}
	}
	return
}

func (a *App) addLogLine(evt daemon.Event) {
	levelColor := "white"
	switch evt.Level {
	case "error":
		levelColor = "red"
	case "warn":
		levelColor = "yellow"
	case "info":
		levelColor = "green"
	}

	a.app.QueueUpdateDraw(func() {
		// Read a.displayLocalTime inside the closure (main goroutine)
		// to avoid a race against UpdateConfig's writes.
		ts := formatLogTimestamp(evt.Timestamp, a.displayLocalTime)
		line := fmt.Sprintf("[%s]%s[white] [gray]%s[white] %s", levelColor, strings.ToUpper(evt.Level), ts, evt.Message)

		// First real log entry replaces the "log: idle" placeholder so
		// it doesn't sit at the top of the scrollback forever.
		if !a.logHasEntries {
			a.logView.SetText("")
			a.logHasEntries = true
		}
		fmt.Fprintln(a.logView, line)
		text := a.logView.GetText(true)
		lineSlice := strings.Split(text, "\n")
		if len(lineSlice) > maxLogLines+1 {
			a.logView.SetText(strings.Join(lineSlice[len(lineSlice)-maxLogLines-1:], "\n"))
		}
		// In the embedded 1-line slot we want the latest line visible
		// without manual scrolling. ScrollToEnd is a no-op when the
		// view is full-screened and the user has scrolled up.
		a.logView.ScrollToEnd()
	})
}

func (a *App) updateLatencyDisplay() {
	c := a.latency.count()
	if c == 0 {
		return
	}

	avg := a.latency.avg()
	min := a.latency.min()
	max := a.latency.max()

	var color string
	switch {
	case avg < 1000:
		color = "green"
	case avg < 3000:
		color = "yellow"
	default:
		color = "red"
	}

	text := fmt.Sprintf("[green]avg:[white] [%s]%.0f ms[white]  (%d samples)\n", color, avg, c)
	text += fmt.Sprintf("[green]min:[white] %d ms\n", min)
	text += fmt.Sprintf("[green]max:[white] %d ms", max)

	a.app.QueueUpdateDraw(func() {
		a.latencyView.SetText(text)
	})
}

// updateHeader renders the top bar. Three logical segments:
//
//   - Left:   "vibecop ● running  events: N"
//   - Centre: "denied: N  escalations: N" (red / orange counters)
//   - Right:  local clock "HH:MM:SS" (or UTC if displayLocalTime=false)
//
// When the terminal is too narrow for all three, the clock is dropped
// first; the counters are kept because they're load-bearing signal.
// Pure rendering happens in renderHeaderLine so we can unit-test the
// layout decisions without standing up a tview.Application.
func (a *App) updateHeader(_ daemon.Event) {
	a.app.QueueUpdateDraw(func() {
		a.redrawHeader()
	})
}

// redrawHeader runs on the tview goroutine. Reads counters under the
// mutex, computes the available inner width, and writes the formatted
// line.
func (a *App) redrawHeader() {
	if a.headerView == nil {
		return
	}
	a.mu.Lock()
	events := a.events
	denied := a.denied
	pending := len(a.pending)
	a.mu.Unlock()

	_, _, width, _ := a.headerView.GetInnerRect()
	clock := currentClock(time.Now(), a.displayLocalTime)
	line := renderHeaderLine(events, denied, pending, clock, width)
	a.headerView.SetText(line)
}

// currentClock formats the wall clock for the header. Wraps time.Now
// behind a parameter so tests can pass a fixed instant.
func currentClock(now time.Time, local bool) string {
	if local {
		return now.Local().Format("15:04:05")
	}
	return now.UTC().Format("15:04:05") + " UTC"
}

// renderHeaderLine composes the formatted header. width is the
// header's inner content width (columns); when ≤0 the right-aligned
// segment is omitted (e.g. before the first draw, where GetInnerRect
// returns 0). Pure for testability.
//
// The trailing-time placement uses tview.AlignRight semantics achieved
// by padding with spaces against the *visible* width — colour escape
// codes don't render, so we measure them out before computing pad.
func renderHeaderLine(events, denied, pending int, clock string, width int) string {
	const (
		leftFmt   = "[green]vibecop[white] ● running  events: %d"
		centerFmt = "  |  [red]denied: %d[white]  [orange]escalations: %d[white]"
	)
	left := fmt.Sprintf(leftFmt, events)
	centre := fmt.Sprintf(centerFmt, denied, pending)
	body := left + centre

	if width <= 0 || clock == "" {
		return body
	}
	visible := visibleLength(body)
	clockLen := len(clock)
	// Need at least 2 spaces of separation between the centre and the
	// right-aligned clock; otherwise the clock would butt into the
	// counters and read as a single line.
	const minGap = 2
	if visible+minGap+clockLen > width {
		return body
	}
	pad := width - visible - clockLen
	return body + strings.Repeat(" ", pad) + "[gray]" + clock + "[white]"
}

// visibleLength returns the number of terminal columns a tview-coloured
// string occupies, by stripping `[…]` colour tags. tview's tag syntax
// is `[<fg>[:<bg>[:<flags>]]]`; tags that aren't the empty `[]` literal
// are removed entirely. Approximate but sufficient for header layout —
// we use a conservative gap when unsure.
func visibleLength(s string) int {
	n := 0
	inTag := false
	for _, r := range s {
		switch {
		case r == '[':
			inTag = true
		case r == ']':
			inTag = false
		case inTag:
			// Skip tag contents.
		default:
			n++
		}
	}
	return n
}

// runClock pumps a header redraw once per second so the right-aligned
// time advances. Exits when Close() closes a.clockDone.
//
// QueueUpdateDraw blocks waiting for the main loop to drain the
// update; if the Application has already been Stop'd (e.g. user hit
// `q`), the loop is gone and the call hangs forever. To keep the
// outer ticker loop responsive to clockDone, we dispatch each redraw
// on a detached goroutine and race against clockDone. The detached
// goroutine may leak in the post-Stop window but it's bounded — the
// process is about to exit when the user quits — and the outer loop
// returns cleanly on the close.
func (a *App) runClock() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.clockDone:
			return
		case <-t.C:
			if a.app == nil || a.headerView == nil {
				continue
			}
			// Non-blocking dispatch: if QueueUpdateDraw blocks
			// because the app stopped, it leaks here, not in the
			// ticker loop.
			go a.app.QueueUpdateDraw(a.redrawHeader)
		}
	}
}

// refreshConfig runs on the tview main goroutine (input handler). The
// actual fetch must happen on a goroutine to avoid the deadlock that
// killed the original implementation: synchronous QueueUpdateDraw from
// inside an input handler waits for the main loop to drain the update
// channel, but the main loop is busy executing the handler. We set
// in-flight feedback directly here, then dispatch the network round
// trip onto a goroutine where QueueUpdateDraw is safe.
func (a *App) refreshConfig() {
	a.configView.SetText("[gray]refreshing...[white]")
	go a.fetchAndRenderConfig()
}

// fetchAndRenderConfig dials the daemon for a config snapshot and
// updates the config panel. Safe to call from any goroutine — uses
// QueueUpdateDraw for the view mutation. Used both for the initial
// startup populate and for the `r` keystroke refresh.
func (a *App) fetchAndRenderConfig() {
	cfg, err := a.fetchConfig()
	if err != nil {
		a.app.QueueUpdateDraw(func() {
			a.configView.SetText(fmt.Sprintf("[red]get_config failed: %v[white]", err))
		})
		return
	}
	a.UpdateConfig(cfg)
}

// fetchConfig issues get_config and returns the daemon's effective
// config snapshot. Mirrors fetchPending — fresh short-lived UDS dial.
func (a *App) fetchConfig() (daemon.ConfigResponse, error) {
	conn, err := a.dialDaemon()
	if err != nil {
		return daemon.ConfigResponse{}, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemon.Request{Type: daemon.TypeGetConfig}); err != nil {
		return daemon.ConfigResponse{}, err
	}
	var resp daemon.ConfigResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return daemon.ConfigResponse{}, err
	}
	return resp, nil
}

// UpdateConfig is called externally (or on timer) to refresh the config
// display. The whole `daemon.ConfigResponse` is taken so display-time
// preferences (display_local_time, config_path) propagate alongside the
// user-visible fields without growing the function signature each time
// a new field is added to the wire shape.
func (a *App) UpdateConfig(cfg daemon.ConfigResponse) {
	text := fmt.Sprintf("endpoint: [green]%s[white]\n", cfg.Endpoint)
	text += fmt.Sprintf("format:   %s\n", cfg.APIFormat)
	text += fmt.Sprintf("model:    [yellow]%s[white]\n", cfg.Model)
	text += fmt.Sprintf("timeout:  %d ms\n", cfg.TimeoutMs)
	text += fmt.Sprintf("audit:    %v", cfg.AuditEnabled)

	a.app.QueueUpdateDraw(func() {
		a.configView.SetText(text)
		a.displayLocalTime = cfg.DisplayLocalTime
		a.configPath = cfg.ConfigPath
	})
}

// dialDaemon opens a fresh short-lived UDS connection for one
// request/response. Mirrors the hook's pattern. Caller closes the conn.
func (a *App) dialDaemon() (net.Conn, error) {
	if a.socketPath == "" {
		return nil, fmt.Errorf("no socket path configured")
	}
	conn, err := net.DialTimeout("unix", a.socketPath, daemonRequestTimeout)
	if err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Now().Add(daemonRequestTimeout)); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// fetchPending issues list_pending and returns the snapshot plus the
// daemon's audit-enabled flag (so the UI can distinguish empty-queue
// from audit-off).
func (a *App) fetchPending() ([]daemon.PendingEntry, bool, error) {
	conn, err := a.dialDaemon()
	if err != nil {
		return nil, false, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(daemon.Request{Type: daemon.TypeListPending}); err != nil {
		return nil, false, err
	}
	var resp daemon.PendingResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, false, err
	}
	return resp.Pending, resp.AuditEnabled, nil
}

// completePending issues complete_pending; humanDecision is "approved" or "blocked".
func (a *App) completePending(projectHash, key, humanDecision string) error {
	conn, err := a.dialDaemon()
	if err != nil {
		return err
	}
	defer conn.Close()

	req := daemon.Request{
		Type:          daemon.TypeCompletePending,
		Key:           key,
		ProjectHash:   projectHash,
		HumanDecision: humanDecision,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp daemon.CompleteResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("complete_pending: %s", resp.Error)
	}
	return nil
}

// refreshEscalations pulls the latest pending list and rebuilds the list view.
func (a *App) refreshEscalations() {
	defer a.finishEscalationRefresh()

	pending, auditEnabled, err := a.fetchPending()
	if err != nil {
		// Show the error in the empty banner. The list keeps whatever
		// it had — partial visibility is better than blanking it.
		a.app.QueueUpdateDraw(func() {
			a.escalEmpty.SetText(fmt.Sprintf("[red]list_pending failed: %v[white]", err))
		})
		return
	}

	a.app.QueueUpdateDraw(func() {
		a.rebuildEscalationList(pending, auditEnabled)
	})
}

func (a *App) rebuildEscalationList(pending []daemon.PendingEntry, auditEnabled bool) {
	if a.escalations == nil {
		return
	}

	prevProjectHash, prevKey := "", ""
	current := a.escalations.GetCurrentItem()
	if current >= 0 && current < len(a.pending) {
		prev := a.pending[current]
		prevProjectHash = prev.ProjectHash
		prevKey = prev.Key
	}

	a.pending = append([]daemon.PendingEntry(nil), pending...)
	a.escalations.Clear()
	if len(a.pending) == 0 {
		a.escalEmpty.SetText(emptyBannerFor(auditEnabled, 0))
		return
	}
	a.escalEmpty.SetText(emptyBannerFor(auditEnabled, len(a.pending)))
	for _, p := range a.pending {
		a.escalations.AddItem(escalationLabels(p))
	}
	if idx := findPendingIndex(a.pending, prevProjectHash, prevKey); idx >= 0 {
		a.escalations.SetCurrentItem(idx)
	}

	// The header surfaces len(a.pending) — refresh it whenever the
	// queue state changes so the badge doesn't lag the list view.
	a.redrawHeader()
}

// emptyBannerFor returns the banner text for the escalation page.
// Distinguishes "audit off — nothing will ever appear here" from
// "audit on but queue is empty" and from "N pending — keys".
func emptyBannerFor(auditEnabled bool, count int) string {
	if !auditEnabled {
		return "[yellow]audit_enabled = false[gray] — escalations are not retained; flip [white]audit_enabled[gray] in config.toml to use this queue"
	}
	if count == 0 {
		return emptyEscalations
	}
	return fmt.Sprintf("[gray]%d pending — [white]a[gray]:approve  [white]d[gray]:deny", count)
}

// escalationLabels returns the (main, secondary, shortcut, selectFn)
// values for tview.List.AddItem. Pure for testability.
func escalationLabels(p daemon.PendingEntry) (string, string, rune, func()) {
	main := fmt.Sprintf("[yellow]%s[white]: %s", p.Tool, truncate(p.Input, 80))
	secondary := fmt.Sprintf(
		"[gray]%s  [blue]proj:%s[gray]  [yellow]%s[gray]  %s",
		p.Timestamp,
		shortProjectHash(p.ProjectHash),
		strings.ToUpper(p.Verdict),
		truncate(p.Reason, 100),
	)
	return main, secondary, 0, nil
}

func shortProjectHash(projectHash string) string {
	if len(projectHash) <= 12 {
		return projectHash
	}
	return projectHash[:12]
}

func findPendingIndex(pending []daemon.PendingEntry, projectHash, key string) int {
	if projectHash == "" || key == "" {
		return -1
	}
	for i, p := range pending {
		if p.ProjectHash == projectHash && p.Key == key {
			return i
		}
	}
	return -1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// completeSelected resolves the currently-highlighted pending escalation.
// humanDecision is "approved" or "blocked".
func (a *App) completeSelected(humanDecision string) {
	if a.escalations == nil || a.escalations.GetItemCount() == 0 {
		return
	}
	idx := a.escalations.GetCurrentItem()
	if idx < 0 || idx >= len(a.pending) {
		return
	}
	target := a.pending[idx]

	go func() {
		if err := a.completePending(target.ProjectHash, target.Key, humanDecision); err != nil {
			a.app.QueueUpdateDraw(func() {
				a.escalEmpty.SetText(fmt.Sprintf("[red]complete_pending failed: %v[white]", err))
			})
			return
		}
		a.requestEscalationRefresh(true)
	}()
}

// maybeRefreshOnEvent debounces escalation-queue refreshes triggered by
// inbound `escalate` events. Fires asynchronously so we don't block the
// event reader.
func (a *App) maybeRefreshOnEvent(verdict string) {
	if verdict != "escalate" && verdict != "error" {
		return
	}
	a.requestEscalationRefresh(false)
}

func (a *App) requestEscalationRefresh(force bool) {
	if !a.beginEscalationRefresh(force) {
		return
	}
	go a.refreshEscalations()
}

func (a *App) beginEscalationRefresh(force bool) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.refreshing {
		return false
	}
	if !force && time.Since(a.lastRefresh) < refreshDebounce {
		return false
	}

	a.refreshing = true
	if !force {
		a.lastRefresh = time.Now()
	}
	return true
}

func (a *App) finishEscalationRefresh() {
	a.mu.Lock()
	a.refreshing = false
	a.lastRefresh = time.Now()
	a.mu.Unlock()
}

// Close shuts down the TUI and disconnects from the daemon. Concurrent-
// and double-call safe via sync.Once — the prior select-default pattern
// could race two callers into both observing default: and panic on
// close-of-closed-channel.
func (a *App) Close() {
	a.closeOnce.Do(func() {
		if a.clockDone != nil {
			close(a.clockDone)
		}
		if a.conn != nil {
			a.conn.Close()
		}
	})
}
