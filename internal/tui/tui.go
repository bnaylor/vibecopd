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
	pageHelp         = "help"
	defaultPage      = pageActivity
	refreshDebounce  = 250 * time.Millisecond
	emptyEscalations = "[gray](no pending escalations)"
)

// App is the tview-based TUI.
type App struct {
	app        *tview.Application
	conn       net.Conn
	scanner    *bufio.Scanner
	socketPath string

	headerView  *tview.TextView
	activity    *tview.List
	latencyView *tview.TextView
	configView  *tview.TextView
	logView     *tview.TextView
	escalations *tview.List
	escalEmpty  *tview.TextView
	helpView    *tview.TextView
	statusBar   *tview.TextView
	pages       *tview.Pages

	// Snapshot of the currently rendered escalation list. Accessed only on
	// the application goroutine so it stays aligned with the visible rows.
	pending []daemon.PendingEntry

	currentPage string
	prevPage    string
	lastRefresh time.Time
	refreshing  bool

	latency *latencyStats
	events  int
	mu      sync.Mutex
}

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
		conn:        conn,
		scanner:     bufio.NewScanner(conn),
		socketPath:  socketPath,
		latency:     &latencyStats{},
		currentPage: defaultPage,
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

	// Pages — activity, escalations, help.
	a.pages = tview.NewPages()
	a.pages.AddPage(pageActivity, a.buildActivityPage(), true, true)
	a.pages.AddPage(pageEscalations, a.buildEscalationsPage(), true, false)
	a.pages.AddPage(pageHelp, a.buildHelpPage(), true, false)
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

	a.app.SetRoot(root, true)
	return a.app.Run()
}

func (a *App) buildActivityPage() tview.Primitive {
	flex := tview.NewFlex().SetDirection(tview.FlexRow)

	rightPanel := tview.NewFlex().SetDirection(tview.FlexRow)

	a.latencyView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.latencyView.SetTitle("latency").SetBorder(true)
	a.latencyView.SetText("waiting for data...")
	rightPanel.AddItem(a.latencyView, 0, 1, false)

	a.configView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	a.configView.SetTitle("config").SetBorder(true)
	a.configView.SetText("waiting for data...")
	rightPanel.AddItem(a.configView, 0, 1, false)

	a.activity = tview.NewList().ShowSecondaryText(true)
	a.activity.SetTitle("activity").SetBorder(true)

	middle := tview.NewFlex().
		AddItem(a.activity, 0, 3, false).
		AddItem(rightPanel, 0, 2, false)
	flex.AddItem(middle, 0, 1, true)

	a.logView = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft).
		SetScrollable(true)
	a.logView.SetTitle("log").SetBorder(true)
	flex.AddItem(a.logView, 7, 0, false)

	return flex
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
		"    [white]↑/↓[gray]          scroll activity",
		"    [white]r[gray]            refresh config",
		"",
		"  [yellow]Escalations page[white]",
		"    [white]↑/↓[gray]          scroll pending list",
		"    [white]a[gray]            approve selected (audit only — agent already saw harness prompt)",
		"    [white]d[gray]            deny selected (audit only)",
		"    [white]R[gray]            refresh queue",
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
		}
	}
	return event
}

// escalationsInput handles keys when the escalations List is focused.
// The List itself absorbs ↑/↓; we intercept `a` and `d` for verdicts
// and let everything else fall through to globalInput.
func (a *App) escalationsInput(event *tcell.EventKey) *tcell.EventKey {
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
		hint = "[white]q[gray]:quit  [white]e[gray]:escalations  [white]↑/↓[gray]:scroll  [white]r[gray]:refresh config"
	case pageEscalations:
		hint = "[white]q[gray]:quit  [white]a[gray]:approve  [white]d[gray]:deny  [white]R[gray]:refresh  [white]Esc[gray]:back"
	case pageHelp:
		hint = "[gray]press any key to close help"
	default:
		hint = "[white]q[gray]:quit"
	}
	if a.statusBar == nil {
		return
	}
	a.statusBar.SetText(fmt.Sprintf("[yellow]%s[gray]   %s   [white]?[gray]:help", a.currentPage, hint))
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

func (a *App) addActivity(evt daemon.Event) {
	label := verdictLabel(evt.Verdict)
	color := verdictColor(evt.Verdict)
	colorName := color.String()

	mainText := fmt.Sprintf("[%s::] %s", colorName, evt.Tool)
	if len(evt.Input) > 60 {
		mainText += ": " + evt.Input[:57] + "..."
	} else {
		mainText += ": " + evt.Input
	}

	secondary := fmt.Sprintf("[%s::]%s[-:-:-]  %s", colorName, label, evt.Timestamp)

	a.app.QueueUpdateDraw(func() {
		a.activity.InsertItem(0, mainText, secondary, 0, nil)
		// Trim.
		for a.activity.GetItemCount() > maxActivityItems {
			a.activity.RemoveItem(a.activity.GetItemCount() - 1)
		}
	})
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

	ts := evt.Timestamp
	if len(ts) > 19 {
		ts = ts[:19] // strip timezone for display
	}
	line := fmt.Sprintf("[%s]%s[white] [gray]%s[white] %s", levelColor, strings.ToUpper(evt.Level), ts, evt.Message)

	a.app.QueueUpdateDraw(func() {
		fmt.Fprintln(a.logView, line)
		text := a.logView.GetText(true)
		lineSlice := strings.Split(text, "\n")
		if len(lineSlice) > maxLogLines+1 {
			a.logView.SetText(strings.Join(lineSlice[len(lineSlice)-maxLogLines-1:], "\n"))
		}
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

func (a *App) updateHeader(_ daemon.Event) {
	a.app.QueueUpdateDraw(func() {
		a.headerView.SetText(fmt.Sprintf(
			"[green]vibecop[white] ● running  |  events: %d",
			a.events,
		))
	})
}

// refreshConfig runs on the tview main goroutine (input handler). Same
// deadlock rule as switchTo — set the primitive directly.
func (a *App) refreshConfig() {
	a.configView.SetText("(press r to refresh from daemon)")
}

// UpdateConfig is called externally (or on timer) to refresh the config display.
func (a *App) UpdateConfig(endpoint, apiFormat, model string, timeoutMs int, auditEnabled bool) {
	text := fmt.Sprintf("endpoint: [green]%s[white]\n", endpoint)
	text += fmt.Sprintf("format:   %s\n", apiFormat)
	text += fmt.Sprintf("model:    [yellow]%s[white]\n", model)
	text += fmt.Sprintf("timeout:  %d ms\n", timeoutMs)
	text += fmt.Sprintf("audit:    %v", auditEnabled)

	a.app.QueueUpdateDraw(func() {
		a.configView.SetText(text)
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

// Close shuts down the TUI and disconnects from the daemon.
func (a *App) Close() {
	if a.conn != nil {
		a.conn.Close()
	}
}
