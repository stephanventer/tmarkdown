package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	xansi "github.com/charmbracelet/x/ansi"
)

type mode int

const (
	modeNormal mode = iota
	modeSearch      // typing a search query
	modeOutline
	modeHelp
)

const (
	fastLines = 10 // Shift+arrow / J K
	wheelStep = 3  // lines per wheel notch
	fastWheel = 15 // Alt+wheel
)

type (
	fileChangedMsg struct{}
	reloadMsg      struct{ gen int }
	resizeMsg      struct{ gen int }
	clearFlashMsg  struct{ gen int }
)

type model struct {
	path, name string // path is "" for stdin
	src        []byte
	pal        palette
	th         uiTheme
	keys       keyMap
	maxWidth   int

	w, h  int
	pad   int
	doc   *Doc
	vp    viewport.Model
	ready bool
	mode  mode

	query   string
	prevQ   string // query before this search started, restored on Esc
	matches []match
	cur     int

	sel int // selected link, -1 for none

	spaced  bool           // blank row between text lines
	base    []string       // rendered rows in the viewport, no highlights
	rows    []int          // doc line -> viewport row
	rowLine []int          // viewport row -> doc line, -1 for spacing rows
	spans   map[int][]span // highlights by doc line

	filter string // outline filter
	oItems []int  // indexes into doc.headings
	oSel   int
	oTop   int

	flash     string
	flashGen  int
	reloadGen int
	resizeGen int
	deleted   bool
	errText   string
}

// viewOpts are the viewer settings that come from flags and the config.
type viewOpts struct {
	pal      palette
	keys     keyMap
	spaced   bool
	maxWidth int
	flash    string // shown in the status bar at startup, e.g. config warnings
}

func newModel(path, name string, src []byte, o viewOpts) model {
	return model{
		path: path, name: name, src: src, sel: -1,
		pal: o.pal, th: newUITheme(o.pal), keys: o.keys, maxWidth: o.maxWidth,
		spaced: o.spaced, flash: o.flash,
	}
}

func (m model) Init() tea.Cmd {
	if m.flash == "" {
		return nil
	}
	gen := m.flashGen
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{gen} })
}

// ---- rendering and layout ----

func (m *model) rerender() {
	wrap := min(m.w-4, m.maxWidth)
	if wrap < 20 {
		wrap = max(m.w, 10)
	}
	m.pad = max((m.w-wrap)/2, 0)
	d, err := buildDoc(m.src, wrap, m.pad, m.pal, false)
	if err != nil {
		m.errText = err.Error()
		d = &Doc{}
	}
	m.doc = d
	m.matches = findMatches(d.plain, m.query)
	if m.cur >= len(m.matches) {
		m.cur = 0
	}
	if m.sel >= len(d.links) {
		m.sel = -1
	}
	m.layout()
}

// layout fills the viewport with the plain rendered rows (with or without
// double spacing). It runs only when the text or spacing changes; search
// and link highlights are added to the visible rows in docView.
func (m *model) layout() {
	if len(bytes.TrimSpace(m.src)) == 0 {
		msg := "(empty file)"
		top := strings.Repeat("\n", max(m.vp.Height/2-1, 0))
		m.base, m.rows, m.rowLine = strings.Split(top+strings.Repeat(" ", max((m.w-len(msg))/2, 0))+msg, "\n"), nil, nil
	} else {
		m.base, m.rows = spaceLines(m.doc.lines, m.doc.plain, m.doc.headings, m.spaced)
		m.rowLine = make([]int, len(m.base))
		for i := range m.rowLine {
			m.rowLine[i] = -1
		}
		for ln, r := range m.rows {
			m.rowLine[r] = ln
		}
	}
	m.vp.SetContent(strings.Join(m.base, "\n"))
	m.refresh()
}

// refresh recomputes the search and link highlights, keyed by doc line.
// It is cheap: only the lines on screen are decorated, at draw time.
func (m *model) refresh() {
	m.spans = map[int][]span{}
	for i, mt := range m.matches {
		code := m.th.match
		if i == m.cur {
			code = m.th.current
		}
		m.spans[mt.line] = append(m.spans[mt.line], span{start: mt.start, end: mt.end, on: code})
	}
	if m.sel >= 0 {
		l := m.doc.links[m.sel]
		m.spans[l.line] = append(m.spans[l.line], span{start: l.start, end: l.end, on: m.th.link})
		for _, g := range l.more {
			m.spans[g.line] = append(m.spans[g.line], span{start: g.start, end: g.end, on: m.th.link})
		}
	}
}

// docView draws the visible rows with their highlights, padded to the
// viewport size the same way the viewport would.
func (m model) docView() string {
	top := min(max(m.vp.YOffset, 0), len(m.base))
	end := min(top+m.vp.Height, len(m.base))
	selLine := -1
	if m.sel >= 0 {
		selLine = m.doc.links[m.sel].line
	}
	out := make([]string, 0, end-top)
	for r := top; r < end; r++ {
		l := m.base[r]
		if r < len(m.rowLine) {
			if ln := m.rowLine[r]; ln >= 0 {
				if sp := m.spans[ln]; len(sp) > 0 {
					l = decorate(l, sp)
				}
				if ln == selLine && m.pad >= 2 && strings.HasPrefix(l, strings.Repeat(" ", m.pad)) {
					l = strings.Repeat(" ", m.pad-2) + "▶" + l[m.pad-1:]
				}
			}
		}
		out = append(out, l)
	}
	w, h := m.vp.Width, m.vp.Height
	return lipgloss.NewStyle().Width(w).Height(h).MaxWidth(w).MaxHeight(h).Render(strings.Join(out, "\n"))
}

// anchor remembers the position as "n lines below heading i" so it
// survives re-wrapping and edits above it.
type anchor struct {
	head, off int
	text      string
	pct       float64
}

func (m *model) anchor() anchor {
	a := anchor{head: -1, pct: m.vp.ScrollPercent()}
	if i := m.currentHeading(); i >= 0 {
		h := m.doc.headings[i]
		a.head, a.off, a.text = i, m.toLine(m.vp.YOffset)-h.line, h.text
	}
	return a
}

func (m *model) restore(a anchor) {
	if a.head >= 0 {
		hs := m.doc.headings
		idx := -1
		if a.head < len(hs) && hs[a.head].text == a.text {
			idx = a.head
		} else {
			for i, h := range hs {
				if h.text == a.text {
					idx = i
					break
				}
			}
		}
		if idx >= 0 {
			m.vp.SetYOffset(m.toRow(hs[idx].line + a.off))
			return
		}
	}
	total := max(m.vp.TotalLineCount()-m.vp.Height, 0)
	m.vp.SetYOffset(int(a.pct * float64(total)))
}

func (m *model) currentHeading() int {
	if m.doc == nil {
		return -1
	}
	idx, top := -1, m.toLine(m.vp.YOffset)
	for i, h := range m.doc.headings {
		if h.line > top {
			break
		}
		idx = i
	}
	return idx
}

// show scrolls so doc line is visible, about a third from the top.
func (m *model) show(line int) {
	line = m.toRow(line)
	if line < m.vp.YOffset || line >= m.vp.YOffset+m.vp.Height {
		m.vp.SetYOffset(line - m.vp.Height/3)
	}
}

func (m *model) setFlash(s string) tea.Cmd {
	m.flash = s
	m.flashGen++
	gen := m.flashGen
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearFlashMsg{gen} })
}

// ---- update ----

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		if !m.ready {
			m.vp = viewport.New(m.w, m.h-1)
			m.vp.KeyMap = viewport.KeyMap{} // keys are handled in updateNormal
			m.ready = true
			m.rerender()
			var cmd tea.Cmd
			if len(m.src) > largeFile {
				cmd = m.setFlash("large file")
			}
			return m, cmd
		}
		// Re-rendering a big file is slow, so wait until resizing stops.
		m.vp.Width, m.vp.Height = m.w, m.h-1
		m.resizeGen++
		gen := m.resizeGen
		return m, tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return resizeMsg{gen} })

	case resizeMsg:
		if msg.gen == m.resizeGen {
			a := m.anchor()
			m.rerender()
			m.restore(a)
		}
		return m, nil

	case fileChangedMsg:
		m.reloadGen++
		gen := m.reloadGen
		return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return reloadMsg{gen} })

	case reloadMsg:
		if msg.gen != m.reloadGen {
			return m, nil
		}
		return m.reload(false)

	case clearFlashMsg:
		if msg.gen == m.flashGen {
			m.flash = ""
		}
		return m, nil

	case tea.MouseMsg:
		debugf("mouse button=%v action=%v shift=%v alt=%v ctrl=%v", msg.Button, msg.Action, msg.Shift, msg.Alt, msg.Ctrl)
		if m.mode == modeNormal {
			if msg.Action == tea.MouseActionPress {
				if dir := wheelDir(msg.Button); dir != 0 {
					m.wheel(dir, msg.Alt)
					return m, nil
				}
			}
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		debugf("key %q", msg.String())
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		switch m.mode {
		case modeSearch:
			return m.updateSearch(msg)
		case modeOutline:
			return m.updateOutline(msg)
		case modeHelp:
			m.mode = modeNormal
			return m, nil
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m model) reload(manual bool) (tea.Model, tea.Cmd) {
	if m.path == "" {
		return m, m.setFlash("stdin can't be reloaded")
	}
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.deleted = true
			return m, m.setFlash("file deleted")
		}
		return m, m.setFlash("reload failed: " + unwrapPathErr(err).Error())
	}
	m.deleted = false
	if bytes.Equal(data, m.src) && !manual {
		return m, nil
	}
	a := m.anchor()
	m.src = data
	m.sel = -1
	m.rerender()
	m.restore(a)
	return m, m.setFlash("reloaded")
}

// wheelDir maps wheel buttons to -1 (up), +1 (down) or 0.
func wheelDir(b tea.MouseButton) int {
	switch b {
	case tea.MouseButtonWheelUp:
		return -1
	case tea.MouseButtonWheelDown:
		return 1
	}
	return 0
}

// wheel scrolls one wheel notch: 3 lines, or 15 with Alt held. (Windows
// Terminal keeps Shift+wheel for itself, so Alt is the fast modifier.)
func (m *model) wheel(dir int, fast bool) {
	n := wheelStep
	if fast {
		n = fastWheel
	}
	debugf("wheel dir=%d step=%d", dir, n)
	if dir < 0 {
		m.vp.ScrollUp(n)
	} else {
		m.vp.ScrollDown(n)
	}
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := m.keys
	switch {
	case key.Matches(msg, k.Quit):
		return m, tea.Quit
	case key.Matches(msg, k.Back):
		if m.sel >= 0 {
			m.sel = -1
		} else {
			m.query, m.matches = "", nil
		}
		m.refresh()
	case key.Matches(msg, k.Search):
		m.mode, m.prevQ, m.query = modeSearch, m.query, ""
		m.matches = nil
		m.refresh()
	case key.Matches(msg, k.NextMatch, k.PrevMatch):
		if len(m.matches) == 0 {
			if m.query != "" {
				return m, m.setFlash("no matches")
			}
			return m, nil
		}
		if key.Matches(msg, k.NextMatch) {
			m.cur = (m.cur + 1) % len(m.matches)
		} else {
			m.cur = (m.cur - 1 + len(m.matches)) % len(m.matches)
		}
		m.refresh()
		m.show(m.matches[m.cur].line)
	case key.Matches(msg, k.Outline):
		if len(m.doc.headings) == 0 {
			return m, m.setFlash("no headings")
		}
		m.mode, m.filter = modeOutline, ""
		m.buildOutline()
		m.oSel = 0
		cur := m.currentHeading()
		for i, h := range m.oItems {
			if h == cur {
				m.oSel = i
			}
		}
	case key.Matches(msg, k.NextLink, k.PrevLink):
		return m.stepLink(key.Matches(msg, k.NextLink))
	case key.Matches(msg, k.OpenLink):
		if m.sel >= 0 {
			return m, m.setFlash(openLink(m.doc.links[m.sel].url))
		}
	case key.Matches(msg, k.Reload):
		return m.reload(true)
	case key.Matches(msg, k.ToggleSpacing):
		a := m.anchor()
		m.spaced = !m.spaced
		m.layout()
		m.restore(a)
		if m.spaced {
			return m, m.setFlash("double spacing")
		}
		return m, m.setFlash("single spacing")
	case key.Matches(msg, k.Help):
		m.mode = modeHelp
	case key.Matches(msg, k.Top):
		m.vp.GotoTop()
	case key.Matches(msg, k.Bottom):
		m.vp.GotoBottom()
	case key.Matches(msg, k.ScrollUp):
		m.vp.ScrollUp(1)
	case key.Matches(msg, k.ScrollDown):
		m.vp.ScrollDown(1)
	case key.Matches(msg, k.ScrollUp10):
		m.vp.ScrollUp(fastLines)
	case key.Matches(msg, k.ScrollDown10):
		m.vp.ScrollDown(fastLines)
	case key.Matches(msg, k.HalfPageUp):
		m.vp.HalfPageUp()
	case key.Matches(msg, k.HalfPageDown):
		m.vp.HalfPageDown()
	case key.Matches(msg, k.PageUp):
		m.vp.PageUp()
	case key.Matches(msg, k.PageDown):
		m.vp.PageDown()
	}
	return m, nil
}

func (m model) stepLink(forward bool) (tea.Model, tea.Cmd) {
	links := m.doc.links
	if len(links) == 0 {
		return m, m.setFlash("no links")
	}
	switch {
	case m.sel < 0 && forward:
		m.sel = 0
		for i, l := range links {
			if l.line >= m.toLine(m.vp.YOffset) {
				m.sel = i
				break
			}
		}
	case m.sel < 0:
		m.sel = len(links) - 1
		for i := len(links) - 1; i >= 0; i-- {
			if links[i].line <= m.toLine(m.vp.YOffset+m.vp.Height-1) {
				m.sel = i
				break
			}
		}
	case forward:
		m.sel = (m.sel + 1) % len(links)
	default:
		m.sel = (m.sel - 1 + len(links)) % len(links)
	}
	m.refresh()
	m.show(links[m.sel].line)
	return m, nil
}

func (m model) updateSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode, m.query = modeNormal, m.prevQ
		m.matches = findMatches(m.doc.plain, m.query)
		m.cur = 0
		m.refresh()
		return m, nil
	case tea.KeyEnter:
		m.mode = modeNormal
		if m.query != "" && len(m.matches) == 0 {
			return m, m.setFlash("no matches")
		}
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.query); len(r) > 0 {
			m.query = string(r[:len(r)-1])
		}
	case tea.KeyRunes, tea.KeySpace:
		m.query += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.query += " "
		}
	default:
		return m, nil
	}
	// Search as you type: jump to the first match at or below the view top.
	m.matches = findMatches(m.doc.plain, m.query)
	m.cur = 0
	for i, mt := range m.matches {
		if mt.line >= m.toLine(m.vp.YOffset) {
			m.cur = i
			break
		}
	}
	m.refresh()
	if len(m.matches) > 0 {
		m.show(m.matches[m.cur].line)
	}
	return m, nil
}

func (m *model) buildOutline() {
	m.oItems = m.oItems[:0]
	f := strings.ToLower(m.filter)
	for i, h := range m.doc.headings {
		if f == "" || strings.Contains(strings.ToLower(h.text), f) {
			m.oItems = append(m.oItems, i)
		}
	}
	m.oSel, m.oTop = 0, 0
}

func (m model) updateOutline(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.outlineRows()
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = modeNormal
		return m, nil
	case tea.KeyEnter:
		if len(m.oItems) > 0 {
			m.vp.SetYOffset(m.toRow(m.doc.headings[m.oItems[m.oSel]].line))
		}
		m.mode = modeNormal
		return m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		m.oSel = max(m.oSel-1, 0)
	case tea.KeyDown, tea.KeyCtrlN:
		m.oSel = min(m.oSel+1, max(len(m.oItems)-1, 0))
	case tea.KeyPgUp:
		m.oSel = max(m.oSel-rows, 0)
	case tea.KeyPgDown:
		m.oSel = min(m.oSel+rows, max(len(m.oItems)-1, 0))
	case tea.KeyBackspace:
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.buildOutline()
		}
	case tea.KeyRunes, tea.KeySpace:
		m.filter += string(msg.Runes)
		if msg.Type == tea.KeySpace {
			m.filter += " "
		}
		m.buildOutline()
	}
	if m.oSel < m.oTop {
		m.oTop = m.oSel
	}
	if m.oSel >= m.oTop+rows {
		m.oTop = m.oSel - rows + 1
	}
	return m, nil
}

func (m model) outlineRows() int { return max(m.h-4, 1) }

// openLink opens web and mail links in the default app. Other schemes and
// local paths are not opened: a markdown file shouldn't be able to launch
// programs.
func openLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "bad link: " + raw
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
	default:
		return "not opened (only web/mail links): " + raw
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u.String())
	case "darwin":
		cmd = exec.Command("open", u.String())
	default:
		cmd = exec.Command("xdg-open", u.String())
	}
	if err := cmd.Start(); err != nil {
		return "open failed: " + err.Error()
	}
	go cmd.Wait() //nolint:errcheck
	return "opened in browser"
}

// ---- view ----

// uiTheme holds the viewer's own colours, all taken from the palette.
type uiTheme struct {
	match, current, link string // SGR codes for highlights in the text
	bar, dim, sel, head  lipgloss.Style
}

func newUITheme(p palette) uiTheme {
	esc := func(params ...string) string { return "[" + strings.Join(params, ";") + "m" }
	return uiTheme{
		match:   esc(fgSGR(p.emphasis), bgSGR(p.faint)),
		current: esc("1", fgSGR(p.onAccent), bgSGR(p.accentStrong)),
		link:    esc(fgSGR(p.onAccent), bgSGR(p.accent)),
		bar:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.accentStrong)).Background(lipgloss.Color(p.statusBg)),
		dim:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.muted)),
		sel:     lipgloss.NewStyle().Foreground(lipgloss.Color(p.onAccent)).Background(lipgloss.Color(p.accent)),
		head:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.accentStrong)),
	}
}

func (m model) View() string {
	if !m.ready {
		return ""
	}
	var body string
	switch m.mode {
	case modeOutline:
		body = m.outlineView()
	case modeHelp:
		body = m.helpView()
	default:
		body = m.docView()
	}
	return body + "\n" + m.statusBar()
}

func (m model) statusBar() string {
	var left, right string
	switch {
	case m.mode == modeSearch:
		left = "/" + m.query + "█"
		if m.query != "" {
			right = fmt.Sprintf("%d/%d", min(m.cur+1, len(m.matches)), len(m.matches))
		}
	case m.mode == modeOutline:
		left = "Outline: type to filter · ↑↓ move · Enter jump · Esc close"
	case m.flash != "":
		left = m.name + " · " + m.flash
	case m.sel >= 0:
		left = "→ " + m.doc.links[m.sel].url
		right = "Enter open · Tab next · Esc"
	default:
		left = m.name
		if m.deleted {
			left += " · file deleted"
		}
		if i := m.currentHeading(); i >= 0 {
			left += " · " + m.doc.headings[i].text
		}
		if m.query != "" && len(m.matches) > 0 {
			right = fmt.Sprintf("/%s %d/%d · ", m.query, m.cur+1, len(m.matches))
		}
		right += fmt.Sprintf("%3.f%% · ? help", m.vp.ScrollPercent()*100)
	}
	if m.errText != "" {
		left = "render error: " + m.errText
	}
	left, right = " "+left, right+" "
	room := m.w - lipgloss.Width(right) - 1
	if room < 1 {
		right, room = "", m.w
	}
	left = xansi.Truncate(left, room, "…")
	gap := max(m.w-lipgloss.Width(left)-lipgloss.Width(right), 0)
	return m.th.bar.Render(left + strings.Repeat(" ", gap) + right)
}

func (m model) outlineView() string {
	rows := m.outlineRows()
	lines := []string{"", m.th.head.Render("  Outline") + m.th.dim.Render("  "+m.filter), ""}
	cur := m.currentHeading()
	if len(m.oItems) == 0 {
		lines = append(lines, m.th.dim.Render("  no matching headings"))
	}
	for i := m.oTop; i < len(m.oItems) && i < m.oTop+rows; i++ {
		h := m.doc.headings[m.oItems[i]]
		text := strings.Repeat("  ", h.level-1) + h.text
		mark := "  "
		if m.oItems[i] == cur {
			mark = "• "
		}
		text = xansi.Truncate(mark+text, max(m.w-6, 1), "…")
		if i == m.oSel {
			text = m.th.sel.Render(" " + text + " ")
		} else {
			text = " " + text
		}
		lines = append(lines, "  "+text)
	}
	return fill(lines, m.h-1)
}

func (m model) helpView() string {
	p := m.pal
	c := func(hex string) lipgloss.Color { return lipgloss.Color(hex) }
	title := lipgloss.NewStyle().Bold(true).Foreground(c(p.accentStrong))
	group := lipgloss.NewStyle().Bold(true).Foreground(c(p.accent))
	action := lipgloss.NewStyle().Foreground(c(p.text))
	dim := lipgloss.NewStyle().Foreground(c(p.muted))
	keycap := lipgloss.NewStyle().Foreground(c(p.emphasis)).Background(c(p.keycapBg)).Padding(0, 1)
	groups := helpGroups(m.keys)

	keys := func(r helpRow) string {
		if len(r.choices) == 0 {
			return dim.Render("unbound")
		}
		var parts []string
		for _, choice := range r.choices {
			var caps []string
			for _, k := range choice {
				caps = append(caps, keycap.Render(k))
			}
			parts = append(parts, strings.Join(caps, " "))
		}
		return strings.Join(parts, dim.Render(" or "))
	}

	actionW := 0
	for _, g := range groups {
		for _, r := range g.rows {
			actionW = max(actionW, lipgloss.Width(r.action))
		}
	}
	var lines []string
	lines = append(lines, title.Render("Keys"), "")
	for gi, g := range groups {
		if gi > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, group.Render(g.title))
		for _, r := range g.rows {
			a := action.Render(r.action + strings.Repeat(" ", actionW-lipgloss.Width(r.action)))
			lines = append(lines, "  "+a+"   "+keys(r))
		}
	}
	lines = append(lines, "", dim.Render("The file reloads by itself when it changes on disk."),
		dim.Render("Press any key to close."))

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(c(p.faint)).
		Padding(1, 3).
		Render(strings.Join(lines, "\n"))
	return fill(strings.Split(lipgloss.Place(m.w, m.h-1, lipgloss.Center, lipgloss.Center, panel), "\n"), m.h-1)
}

// fill pads or cuts lines to exactly n rows.
func fill(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[:max(n, 0)]
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}
