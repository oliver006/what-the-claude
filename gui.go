package main

import (
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"
)

type logEntry struct {
	time       time.Time
	duration   time.Duration
	model      string
	status     int
	events     int
	txBytes    int64
	rxBytes    int64
	totalTx    int64
	totalRx    int64
	use5h      float64
	use7d      float64
	reset5h    int64
	reset7d    int64
	reqBody    string
	respBody   string
	respEvents []sseEvent
	anthropic  map[string]string
	sessionID  string
	sessionTag string
	filePath   string
}

type session struct {
	id           string
	tag          string
	started      time.Time
	lastActivity time.Time
	entries      []logEntry
}

type guiModel struct {
	mu              sync.Mutex
	sessions        []*session
	filtered        []*session
	sessionIndex    map[string]int
	selectedSession int
	sessionCount    int
	updateChan      chan logEntry
	listenAddr      string
	remoteURL       string
	use5h           float64
	use7d           float64
	reset5h         int64
	reset7d         int64
	totalTx         int64
	totalRx         int64
	totalReqs       int
	hasRateLimits   bool
	store           *storage
	windowVisible   bool

	app              fyne.App
	window           fyne.Window
	sessionList      *widget.List
	reqList          *widget.List
	sessionInfo      *widget.Label
	status           *widget.Label
	statsLabel       *widget.Label
	statsText        *canvas.Text
	statsRangeSelect *widget.Select
	statsRange       time.Duration
	envVarButton     *widget.Button
	resumeButton     *widget.Button
}

func newGUI(a fyne.App, listenAddr, remoteURL string, store *storage) *guiModel {
	log.Printf("[gui] newGUI: listenAddr=%s remoteURL=%s", listenAddr, remoteURL)

	g := &guiModel{
		app:             a,
		updateChan:      make(chan logEntry, 64),
		listenAddr:      listenAddr,
		remoteURL:       remoteURL,
		selectedSession: -1,
		sessionIndex:    make(map[string]int),
		store:           store,
	}

	g.window = a.NewWindow("what-the-claude 0.2")
	g.window.Resize(fyne.NewSize(1200, 700))

	g.sessionList = widget.NewList(
		func() int {
			g.mu.Lock()
			n := len(g.filtered)
			g.mu.Unlock()
			return n
		},
		func() fyne.CanvasObject {
			t := canvas.NewText("placeholder", theme.ForegroundColor())
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = 12
			return t
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			g.mu.Lock()
			defer g.mu.Unlock()
			t := obj.(*canvas.Text)
			if id < len(g.filtered) {
				s := g.filtered[id]
				t.Text = fmt.Sprintf("%s  %s  (%d reqs)", fmtTimestamp(s.lastActivity), s.id, len(s.entries))
			}
			t.Refresh()
		},
	)

	g.sessionInfo = widget.NewLabel("")
	g.sessionInfo.TextStyle = fyne.TextStyle{Monospace: true}

	g.resumeButton = widget.NewButton("Copy \"resume\" cli command", func() {})
	g.resumeButton.Importance = widget.LowImportance
	g.resumeButton.Hide()

	g.sessionList.OnSelected = func(id widget.ListItemID) {
		log.Printf("[sessionList.OnSelected] id=%d", id)
		g.mu.Lock()
		g.selectedSession = id
		var headerText string
		var sid string
		if id < len(g.filtered) {
			s := g.filtered[id]
			log.Printf("[sessionList.OnSelected] session=%s entries=%d", s.id, len(s.entries))
			headerText = g.sessionHeaderText(s)
			sid = s.id
		} else {
			log.Printf("[sessionList.OnSelected] id=%d out of range (filtered=%d)", id, len(g.filtered))
		}
		g.mu.Unlock()
		log.Printf("[sessionList.OnSelected] setting sessionInfo, calling reqList.Refresh()")
		g.sessionInfo.SetText(headerText)
		if sid != "" {
			resumeCmd := fmt.Sprintf("ANTHROPIC_BASE_URL=http://%s claude --resume %s", listenAddr, sid)
			g.resumeButton.OnTapped = func() {
				g.app.Clipboard().SetContent(resumeCmd)
				log.Printf("[resumeButton] copied to clipboard: %s", resumeCmd)
			}
			g.resumeButton.Show()
		} else {
			g.resumeButton.Hide()
		}
		g.reqList.Refresh()
		g.reqList.ScrollToBottom()
		log.Printf("[sessionList.OnSelected] done")
	}

	g.reqList = widget.NewList(
		func() int {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.selectedSession >= 0 && g.selectedSession < len(g.filtered) {
				n := len(g.filtered[g.selectedSession].entries)
				// log.Printf("[reqList.Length] selectedSession=%d entries=%d", g.selectedSession, n)
				return n
			}
			// log.Printf("[reqList.Length] selectedSession=%d (no session), returning 0", g.selectedSession)
			return 0
		},
		func() fyne.CanvasObject {
			t := canvas.NewText("placeholder", theme.ForegroundColor())
			t.TextStyle = fyne.TextStyle{Monospace: true}
			t.TextSize = 12
			return t
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			g.mu.Lock()
			defer g.mu.Unlock()
			t := obj.(*canvas.Text)
			if g.selectedSession >= 0 && g.selectedSession < len(g.filtered) {
				entries := g.filtered[g.selectedSession].entries
				if id < len(entries) {
					line := entries[id].displayLine()
					// log.Printf("[reqList.Update] id=%d line=%q", id, line)
					t.Text = line
				} else {
					log.Printf("[reqList.Update] id=%d out of range (entries=%d)", id, len(entries))
				}
			} else {
				log.Printf("[reqList.Update] id=%d selectedSession=%d out of range", id, g.selectedSession)
			}
			t.Refresh()
		},
	)

	g.reqList.OnSelected = func(id widget.ListItemID) {
		log.Printf("[reqList.OnSelected] id=%d", id)
		g.mu.Lock()
		var entry *logEntry
		if g.selectedSession >= 0 && g.selectedSession < len(g.filtered) {
			entries := g.filtered[g.selectedSession].entries
			if id < len(entries) {
				e := entries[id]
				entry = &e
				log.Printf("[reqList.OnSelected] entry: time=%s model=%s filePath=%s", e.time.Format("15:04:05"), e.model, e.filePath)
			}
		}
		g.mu.Unlock()
		if entry != nil {
			g.showDetailWindow(*entry)
		}
		g.reqList.UnselectAll()
	}

	statsText := canvas.NewText("requests: 0  |  sessions: 0", theme.Color(theme.ColorNameForeground))
	statsText.TextSize = 18
	statsText.Alignment = fyne.TextAlignCenter
	g.statsText = statsText

	g.statsRange = 24 * time.Hour
	g.statsRangeSelect = widget.NewSelect([]string{"1h", "6h", "12h", "24h", "7d", "all-time"}, func(s string) {
		switch s {
		case "1h":
			g.statsRange = time.Hour
		case "6h":
			g.statsRange = 6 * time.Hour
		case "12h":
			g.statsRange = 12 * time.Hour
		case "24h":
			g.statsRange = 24 * time.Hour
		case "7d":
			g.statsRange = 7 * 24 * time.Hour
		case "all-time":
			g.statsRange = 0
		}
		g.mu.Lock()
		g.selectedSession = -1
		g.applyFilter()
		g.mu.Unlock()
		g.sessionInfo.SetText("")
		g.sessionList.Refresh()
		g.sessionList.UnselectAll()
		g.reqList.Refresh()
		g.refreshStats()
	})
	g.statsRangeSelect.SetSelected("24h")

	statsPanel := container.NewVBox(
		container.NewBorder(nil, nil, g.statsRangeSelect, nil,
			container.NewCenter(g.statsText),
		),
		widget.NewSeparator(),
	)

	g.status = widget.NewLabel("5h: N/A  7d: N/A")

	envVarText := fmt.Sprintf("ANTHROPIC_BASE_URL=http://%s", listenAddr)
	g.envVarButton = widget.NewButton(envVarText, func() {
		g.app.Clipboard().SetContent(envVarText)
		log.Printf("[envVarButton] copied to clipboard: %s", envVarText)
	})
	g.envVarButton.Importance = widget.LowImportance

	rightPanel := container.NewBorder(
		container.NewVBox(g.sessionInfo, container.NewHBox(g.resumeButton), widget.NewSeparator()),
		nil, nil, nil,
		g.reqList,
	)

	leftPanel := g.sessionList

	split := container.NewHSplit(leftPanel, rightPanel)
	split.Offset = 0.3

	statusBar := container.NewHBox(g.status, g.envVarButton, widget.NewLabel("(click to copy)"))
	content := container.NewBorder(statsPanel, statusBar, nil, nil, split)
	g.window.SetContent(content)

	if desk, ok := a.(desktop.App); ok {
		menu := fyne.NewMenu("what-the-claude",
			fyne.NewMenuItem("Show", func() {
				log.Printf("[systray] Show clicked")
				g.window.Show()
				g.window.RequestFocus()
				g.mu.Lock()
				g.windowVisible = true
				g.mu.Unlock()
			}),
			fyne.NewMenuItem("Quit", func() {
				log.Printf("[systray] Quit clicked")
				a.Quit()
			}),
		)
		desk.SetSystemTrayMenu(menu)

		ico := theme.InfoIcon()

		desk.SetSystemTrayIcon(ico)
		log.Printf("[gui] systray menu and icon set")
	}
	systray.SetTitle("N/A")
	systray.SetTooltip("what-the-claude")

	systray.SetOnTapped(func() {
		log.Printf("[systray] tapped")
		fyne.Do(func() {
			g.toggleWindow()
		})
	})

	g.window.SetCloseIntercept(func() {
		log.Printf("[window] close intercepted, hiding")
		g.mu.Lock()
		g.windowVisible = false
		g.mu.Unlock()
		g.window.Hide()
	})

	g.windowVisible = true

	a.Lifecycle().SetOnStarted(func() {
		log.Printf("[lifecycle] OnStarted, refreshing systray")
		g.refreshSystray()
	})

	log.Printf("[gui] newGUI done")

	return g
}

func (g *guiModel) toggleWindow() {
	g.mu.Lock()
	visible := g.windowVisible
	g.windowVisible = !visible
	g.mu.Unlock()
	if visible {
		log.Printf("[toggleWindow] hiding window")
		g.window.Hide()
	} else {
		log.Printf("[toggleWindow] showing window")
		g.window.Show()
		g.window.RequestFocus()
	}
}

func (g *guiModel) loadFromDisk() {
	log.Printf("[loadFromDisk] start")
	sessions, sessionIndex := g.store.loadAllSessions()
	g.mu.Lock()
	g.sessions = sessions
	g.sessionIndex = sessionIndex
	g.sessionCount = len(sessions)
	g.applyFilter()
	nFiltered := len(g.filtered)

	var mostRecent *logEntry
	var totalReqs int
	for _, s := range sessions {
		for i := range s.entries {
			e := &s.entries[i]
			totalReqs++
			if mostRecent == nil || e.time.After(mostRecent.time) {
				mostRecent = e
			}
		}
	}

	if mostRecent != nil && mostRecent.reset5h > 0 {
		g.use5h = mostRecent.use5h
		g.use7d = mostRecent.use7d
		g.reset5h = mostRecent.reset5h
		g.reset7d = mostRecent.reset7d
		g.totalTx = mostRecent.totalTx
		g.totalRx = mostRecent.totalRx
		g.hasRateLimits = true
		log.Printf("[loadFromDisk] restored rate limits from %s: 5h=%.0f%% 7d=%.0f%% reset5h=%d reset7d=%d",
			mostRecent.time.Format(time.RFC3339), g.use5h*100, g.use7d*100, g.reset5h, g.reset7d)
	}
	g.totalReqs = totalReqs

	g.mu.Unlock()
	log.Printf("[loadFromDisk] loaded %d sessions, %d pass filter, %d total requests", len(sessions), nFiltered, totalReqs)
	for i, s := range sessions {
		log.Printf("[loadFromDisk] session[%d] id=%s entries=%d lastActivity=%s", i, s.id, len(s.entries), s.lastActivity.Format(time.RFC3339))
	}

	fyne.Do(func() {
		g.refreshStatus()
		g.refreshStats()
		g.refreshSystray()
	})
}

func (g *guiModel) sortSessions() {
	sort.Slice(g.sessions, func(i, j int) bool {
		return g.sessions[i].lastActivity.After(g.sessions[j].lastActivity)
	})
	for i, s := range g.sessions {
		g.sessionIndex[s.id] = i
	}
}

func (g *guiModel) applyFilter() {
	if g.statsRange == 0 {
		g.filtered = g.sessions
		log.Printf("[applyFilter] range=all-time, filtered=%d", len(g.filtered))
		return
	}
	cutoff := time.Now().Add(-g.statsRange)
	g.filtered = nil
	for _, s := range g.sessions {
		if s.lastActivity.After(cutoff) {
			g.filtered = append(g.filtered, s)
		}
	}
	log.Printf("[applyFilter] range=%s cutoff=%s, filtered=%d/%d", g.statsRange, cutoff.Format(time.RFC3339), len(g.filtered), len(g.sessions))
}

func (g *guiModel) listenForUpdates() {
	log.Printf("[listenForUpdates] started")
	for entry := range g.updateChan {
		log.Printf("[listenForUpdates] got entry: sessionID=%s model=%s status=%d", entry.sessionID, entry.model, entry.status)
		g.mu.Lock()

		sid := entry.sessionID
		if sid == "" {
			sid = "__no_session__"
		}

		idx, exists := g.sessionIndex[sid]
		if !exists {
			g.sessionCount++
			tag := fmt.Sprintf("s%02d", g.sessionCount)
			entry.sessionTag = tag
			s := &session{
				id:           sid,
				tag:          tag,
				started:      entry.time,
				lastActivity: entry.time,
				entries:      []logEntry{entry},
			}
			idx = len(g.sessions)
			g.sessions = append(g.sessions, s)
			g.sessionIndex[sid] = idx
			log.Printf("[listenForUpdates] new session: sid=%s tag=%s idx=%d", sid, tag, idx)
		} else {
			entry.sessionTag = g.sessions[idx].tag
			g.sessions[idx].entries = append(g.sessions[idx].entries, entry)
			g.sessions[idx].lastActivity = entry.time
		}

		var prevSelectedID string
		if g.selectedSession >= 0 && g.selectedSession < len(g.filtered) {
			prevSelectedID = g.filtered[g.selectedSession].id
		}

		g.sortSessions()
		g.applyFilter()

		if prevSelectedID != "" {
			g.selectedSession = -1
			for i, s := range g.filtered {
				if s.id == prevSelectedID {
					g.selectedSession = i
					break
				}
			}
		}

		g.use5h = entry.use5h
		g.use7d = entry.use7d
		g.reset5h = entry.reset5h
		g.reset7d = entry.reset7d
		g.totalTx = entry.totalTx
		g.totalRx = entry.totalRx
		g.totalReqs++
		g.hasRateLimits = true

		filteredIdx := -1
		for i, s := range g.filtered {
			if s.id == sid {
				filteredIdx = i
				break
			}
		}
		isSelectedSession := g.selectedSession >= 0 && g.selectedSession == filteredIdx
		isNewSession := !exists
		selectedSession := g.selectedSession
		log.Printf("[listenForUpdates] filteredIdx=%d isSelectedSession=%v isNewSession=%v selectedSession=%d", filteredIdx, isSelectedSession, isNewSession, selectedSession)
		g.mu.Unlock()

		fyne.Do(func() {
			g.sessionList.Refresh()
			if isNewSession && selectedSession == -1 {
				g.sessionList.Select(0)
			}
			if isSelectedSession {
				g.reqList.Refresh()
				g.reqList.ScrollToBottom()
			}
			g.refreshStatus()
			g.refreshStats()
		})

		g.refreshSystray()
	}
}

func (g *guiModel) rateLimitText() (title, tooltip string) {
	g.mu.Lock()
	use5h := g.use5h
	use7d := g.use7d
	reset5h := g.reset5h
	reset7d := g.reset7d
	has := g.hasRateLimits
	g.mu.Unlock()

	return formatRateLimits(use5h, use7d, reset5h, reset7d, has, time.Now())
}

func formatRateLimits(use5h, use7d float64, reset5h, reset7d int64, has bool, now time.Time) (title, tooltip string) {
	if !has {
		return "N/A", "what-the-claude"
	}

	delta5h := time.Unix(reset5h, 0).Sub(now)
	var resetStr, reset5hStr string
	if delta5h <= 0 {
		title = "0%"
		reset5hStr = "reset passed"
	} else {
		if delta5h.Minutes() < 90 {
			resetStr = fmt.Sprintf("%dm", int(delta5h.Minutes()))
		} else {
			halfHours := int(delta5h.Minutes()+15) / 30
			if halfHours%2 == 0 {
				resetStr = fmt.Sprintf("%dh", halfHours/2)
			} else {
				resetStr = fmt.Sprintf("%.1fh", float64(halfHours)/2)
			}
		}
		title = fmt.Sprintf("%.0f%% %s", use5h*100, resetStr)
		reset5hStr = fmt.Sprintf("reset in %02d:%02d", int(delta5h.Hours()), int(delta5h.Minutes())%60)
	}

	delta7d := time.Unix(reset7d, 0).Sub(now)
	var reset7dStr string
	if delta7d <= 0 {
		reset7dStr = "reset passed"
	} else if delta7d.Hours() >= 48 {
		reset7dStr = fmt.Sprintf("reset in %dd", int(delta7d.Hours()/24))
	} else {
		reset7dStr = fmt.Sprintf("reset in %dh", int(delta7d.Hours()))
	}
	tooltip = fmt.Sprintf("5h: %.0f%% (%s)\n7d: %.0f%% (%s)", use5h*100, reset5hStr, use7d*100, reset7dStr)

	return title, tooltip
}

func (g *guiModel) refreshSystray() {
	title, tooltip := g.rateLimitText()
	log.Printf("[refreshSystray] title=%q tooltip=%q", title, tooltip)
	systray.SetTitle(title)
	systray.SetTooltip(tooltip)
}

func (g *guiModel) periodicRefresh() {
	ticker := time.NewTicker(time.Second * 30)
	defer ticker.Stop()
	for range ticker.C {
		g.refreshSystray()
		fyne.Do(func() {
			g.refreshStatus()
		})
	}
}

func (g *guiModel) showDetailWindow(e logEntry) {
	log.Printf("[showDetailWindow] time=%s model=%s status=%d filePath=%s", e.time.Format("15:04:05"), e.model, e.status, e.filePath)
	w := g.app.NewWindow(fmt.Sprintf("%s %s - %d", e.time.Format("15:04:05"), e.model, e.status))
	w.Resize(fyne.NewSize(900, 600))

	openBtn := widget.NewButton("Open json file", func() {
		log.Printf("[showDetailWindow] Open json file clicked: %s", e.filePath)
		if e.filePath != "" {
			exec.Command("open", e.filePath).Start()
		}
	})
	if e.filePath == "" {
		openBtn.Disable()
	}
	toolbar := container.NewHBox(openBtn)

	detail := widget.NewLabel(g.entryDetail(e))
	detail.Wrapping = fyne.TextWrapWord
	detail.TextStyle = fyne.TextStyle{Monospace: true}

	content := container.NewBorder(toolbar, nil, nil, nil, container.NewVScroll(detail))
	w.SetContent(content)
	w.Show()
}

func (g *guiModel) entryDetail(e logEntry) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Time: %s\n", e.time.Format("15:04:05")))
	if e.sessionID != "" {
		b.WriteString(fmt.Sprintf("Session: %s (%s)\n", e.sessionID, e.sessionTag))
	}
	b.WriteString(fmt.Sprintf("Model: %s\n", e.model))
	b.WriteString(fmt.Sprintf("Status: %d\n", e.status))
	b.WriteString(fmt.Sprintf("Events: %d\n", e.events))
	b.WriteString(fmt.Sprintf("TX: %s  RX: %s\n\n", fmtBytes(e.txBytes), fmtBytes(e.rxBytes)))

	b.WriteString(fmt.Sprintf("Request body: %s (use Open button to view)\n", fmtBytes(int64(len(e.reqBody)))))

	return b.String()
}

func (g *guiModel) refreshStatus() {
	_, rateText := g.rateLimitText()
	rateText = strings.ReplaceAll(rateText, "\n", "  ")
	g.status.SetText(rateText)
}

func (g *guiModel) refreshStats() {
	g.mu.Lock()
	statsRange := g.statsRange
	var cutoff time.Time
	if statsRange > 0 {
		cutoff = time.Now().Add(-statsRange)
	}

	var totalReqs, totalSessions int
	sessionCounted := make(map[string]bool)

	for _, s := range g.sessions {
		for _, e := range s.entries {
			if statsRange > 0 && e.time.Before(cutoff) {
				continue
			}
			totalReqs++
			if !sessionCounted[s.id] {
				sessionCounted[s.id] = true
				totalSessions++
			}
		}
	}
	g.mu.Unlock()

	text := fmt.Sprintf("requests: %d  |  sessions: %d", totalReqs, totalSessions)
	g.statsText.Text = text
	g.statsText.Refresh()
}

func (e logEntry) displayLine() string {
	dur := "-"
	if e.duration > 0 {
		if e.duration < time.Second {
			dur = fmt.Sprintf("%dms", e.duration.Milliseconds())
		} else {
			dur = fmt.Sprintf("%.1fs", e.duration.Seconds())
		}
	}
	return fmt.Sprintf("%s  %3d  %6s  %s",
		e.fmtTime(),
		e.status,
		dur,
		e.model,
	)
}

func (e logEntry) fmtTime() string {
	return fmtTimestamp(e.time)
}

func fmtTimestamp(t time.Time) string {
	now := time.Now()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04:05")
	}
	return t.Format("01/02 15:04:05")
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1fmb", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1fkb", float64(b)/1024)
	default:
		return fmt.Sprintf("%db", b)
	}
}

func (g *guiModel) sessionHeaderText(s *session) string {
	return fmt.Sprintf("%s  |  %d requests", s.id, len(s.entries))
}

type tappableLabel struct {
	widget.BaseWidget
	text           *canvas.Text
	onSecondaryTap func(pos fyne.Position)
}

func newTappableLabel() *tappableLabel {
	t := &tappableLabel{
		text: canvas.NewText("", theme.Color(theme.ColorNameForeground)),
	}
	t.text.TextStyle = fyne.TextStyle{Monospace: true}
	t.text.TextSize = 12
	t.ExtendBaseWidget(t)
	return t
}

func (t *tappableLabel) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(t.text)
}

func (t *tappableLabel) SetText(s string) {
	t.text.Text = s
	t.text.Refresh()
}

func (t *tappableLabel) TappedSecondary(ev *fyne.PointEvent) {
	log.Printf("[tappableLabel.TappedSecondary] pos=%v", ev.AbsolutePosition)
	if t.onSecondaryTap != nil {
		t.onSecondaryTap(ev.AbsolutePosition)
	}
}

func (g *guiModel) showSessionContextMenu(sessionID string, pos fyne.Position) {
	log.Printf("[showSessionContextMenu] sessionID=%s pos=%v", sessionID, pos)
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("Resume", func() {
			log.Printf("[contextMenu] Resume clicked for session=%s", sessionID)
			g.resumeSession(sessionID)
		}),
	)
	popup := widget.NewPopUpMenu(menu, g.window.Canvas())
	popup.ShowAtPosition(pos)
}

func (g *guiModel) resumeSession(sessionID string) {
	log.Printf("[resumeSession] sessionID=%s listenAddr=%s", sessionID, g.listenAddr)
	cmd := exec.Command("osascript", "-e", fmt.Sprintf(`
		tell application "Terminal"
			activate
			do script "ANTHROPIC_BASE_URL=http://%s claude --resume %s"
		end tell
	`, g.listenAddr, sessionID))
	if err := cmd.Start(); err != nil {
		log.Printf("[resumeSession] error: %v", err)
	}
}
