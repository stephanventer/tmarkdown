package main

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func sampleDoc(t *testing.T) *Doc {
	t.Helper()
	src, err := os.ReadFile("sample.md")
	if err != nil {
		t.Fatal(err)
	}
	d, err := buildDoc(src, 80, 3, matrixDark, false)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestHeadingsMapped(t *testing.T) {
	d := sampleDoc(t)
	want := []string{"tmarkdown sample", "Lists", "Quote", "Code", "Tables", "Deeper heading", "Final section"}
	if len(d.headings) != len(want) {
		t.Fatalf("got %d headings, want %d", len(d.headings), len(want))
	}
	for i, h := range d.headings {
		if h.text != want[i] || !strings.Contains(strings.ToLower(string(d.plain[h.line])), strings.ToLower(h.text)) {
			t.Errorf("heading %q mapped to line %d: %q", h.text, h.line, string(d.plain[h.line]))
		}
	}
}

func TestHeadingStyles(t *testing.T) {
	d := sampleDoc(t)
	for _, p := range d.plain {
		if strings.Contains(string(p), "##") {
			t.Fatalf("hash marks left: %q", string(p))
		}
	}
	rule := func(h heading) string { return strings.TrimSpace(string(d.plain[h.line+1])) }
	if r := rule(d.headings[1]); r != strings.Repeat("━", len("Lists")) {
		t.Errorf("H2 rule = %q", r)
	}
	if r := rule(d.headings[5]); r != strings.Repeat("─", len("Deeper heading")) {
		t.Errorf("H3 rule = %q", r)
	}
	if !strings.Contains(string(d.plain[d.headings[0].line]), "TMARKDOWN SAMPLE") {
		t.Error("H1 should be uppercase")
	}
}

func TestLinksHiddenAndLocated(t *testing.T) {
	d := sampleDoc(t)
	all := strings.Join(d.lines, "\n")
	if strings.ContainsAny(all, string([]rune{markLink, markAutoStart, markAutoEnd})) {
		t.Fatal("marker runes left in output")
	}
	if strings.Contains(all, "example.com/docs") || strings.Contains(all, "Textualize") {
		t.Error("regular link URL is visible")
	}
	if !strings.Contains(all, "https://github.com/charmbracelet/glamour") {
		t.Error("bare URL should stay visible")
	}
	if len(d.links) != 8 {
		t.Fatalf("got %d links, want 8", len(d.links))
	}
	for _, l := range d.links {
		var got []string
		for _, g := range append([]seg{{l.line, l.start, l.end}}, l.more...) {
			got = append(got, string(d.plain[g.line][g.start:g.end]))
		}
		all := strings.Join(got, " ")
		if all == "" || strings.Join(strings.Fields(all), "") != strings.Join(strings.Fields(l.text), "") || strings.HasPrefix(all, " ") {
			t.Errorf("link %q at line %d [%d,%d) covers %q", l.text, l.line, l.start, l.end, all)
		}
	}
	if d.links[4].url != "./README.md" {
		t.Errorf("local link url = %q", d.links[4].url)
	}
}

func TestSearchSmartCase(t *testing.T) {
	plain := [][]rune{[]rune("Go go GO"), []rune("nothing")}
	if n := len(findMatches(plain, "go")); n != 3 {
		t.Errorf("lowercase query: %d matches, want 3", n)
	}
	if m := findMatches(plain, "GO"); len(m) != 1 || m[0].start != 6 {
		t.Errorf("capital query: %+v", m)
	}
}

func TestDecorateKeepsColours(t *testing.T) {
	line := "\x1b[31mred\x1b[0m \x1b[32mgreen text\x1b[0m"
	got := decorate(line, []span{{start: 4, end: 9, on: "\x1b[7m"}})
	want := "\x1b[31mred\x1b[0m \x1b[32m\x1b[7mgreen\x1b[0m\x1b[32m text\x1b[0m"
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if string(stripANSI(got)) != "red green text" {
		t.Error("decorate changed the text")
	}
	// A reset inside the span re-applies the highlight.
	got = decorate("\x1b[31mab\x1b[0mcd", []span{{start: 1, end: 3, on: "\x1b[7m"}})
	if !strings.Contains(got, "\x1b[0m\x1b[7mc") {
		t.Errorf("highlight not re-applied after reset: %q", got)
	}
}

func TestLooksBinary(t *testing.T) {
	if looksBinary([]byte("# hi\n")) || !looksBinary([]byte("MZ\x00\x01")) {
		t.Error("binary detection wrong")
	}
}

func TestRenderSpeed(t *testing.T) {
	sample, _ := os.ReadFile("sample.md")
	var b strings.Builder
	for b.Len() < 1<<20 {
		b.Write(sample)
		b.WriteString("\n")
	}
	start := time.Now()
	if _, err := buildDoc([]byte(b.String()), 96, 2, matrixDark, false); err != nil {
		t.Fatal(err)
	}
	t.Logf("1 MB render: %v", time.Since(start))
}

func TestWheelSteps(t *testing.T) {
	m := newModel("", "x", nil, testView(false))
	m.vp.Height = 10
	m.vp.SetContent(strings.Repeat("x\n", 500))
	m.wheel(1, false)
	m.wheel(1, false)
	if m.vp.YOffset != 2*wheelStep {
		t.Errorf("plain wheel: YOffset = %d, want %d", m.vp.YOffset, 2*wheelStep)
	}
	m.wheel(1, true)
	if m.vp.YOffset != 2*wheelStep+fastWheel {
		t.Errorf("alt wheel: YOffset = %d, want %d", m.vp.YOffset, 2*wheelStep+fastWheel)
	}
}

func TestDoubleSpacing(t *testing.T) {
	d := sampleDoc(t)
	out, rows := spaceLines(d.lines, d.plain, d.headings, true)
	if len(out) <= len(d.lines) {
		t.Fatalf("spaced has %d rows, doc has %d lines", len(out), len(d.lines))
	}
	for i, r := range rows {
		if out[r] != d.lines[i] {
			t.Fatalf("line %d not at row %d", i, r)
		}
	}
	// No two blank rows are added in a row, and a heading stays on its rule.
	h := d.headings[1]
	if rows[h.line+1] != rows[h.line]+1 {
		t.Error("gap added between H2 and its rule")
	}
	m := newModel("", "x", nil, testView(true))
	m.rows = rows
	for i := range rows {
		if m.toLine(m.toRow(i)) != i {
			t.Fatalf("toLine(toRow(%d)) = %d", i, m.toLine(m.toRow(i)))
		}
	}
	if _, rows := spaceLines(d.lines, d.plain, d.headings, false); rows[5] != 5 {
		t.Error("compact rows should be identity")
	}
}

func TestHighlightsDrawn(t *testing.T) {
	src, _ := os.ReadFile("sample.md")
	for _, spaced := range []bool{false, true} {
		mm, _ := newModel("", "x", src, testView(spaced)).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
		m := mm.(model)
		m.query = "Second"
		m.matches = findMatches(m.doc.plain, m.query)
		m.refresh()
		m.show(m.matches[0].line)
		if v := m.docView(); !strings.Contains(v, m.th.current+"Second") {
			t.Errorf("spaced=%v: current match not highlighted", spaced)
		}
		mm, _ = m.stepLink(true)
		m = mm.(model)
		if v := m.docView(); !strings.Contains(v, "▶") || !strings.Contains(v, m.th.link) {
			t.Errorf("spaced=%v: selected link not drawn", spaced)
		}
		if n := strings.Count(m.docView(), "\n"); n != m.vp.Height-1 {
			t.Errorf("spaced=%v: view has %d rows, want %d", spaced, n+1, m.vp.Height)
		}
	}
}

// A heading's text can appear inside an earlier paragraph ("use" in
// USERPROFILE); the heading must still map to its own line.
func TestHeadingNotMatchedInsideParagraph(t *testing.T) {
	src := []byte("# Title\n\nSet %USERPROFILE% first.\n\n## Use\n\nBody.\n")
	d, err := buildDoc(src, 80, 3, matrixDark, false)
	if err != nil {
		t.Fatal(err)
	}
	var para bool
	for _, p := range d.plain {
		if strings.Contains(string(p), "USERPROFILE") {
			para = true
		}
	}
	if !para {
		t.Fatal("paragraph containing USERPROFILE was replaced by a heading")
	}
	for _, p := range d.plain {
		if strings.Contains(string(p), "## Use") {
			t.Errorf("raw heading marker left in output: %q", string(p))
		}
	}
}

// A bare URL longer than the width wraps; the selection must cover every part.
func TestWrappedAutolinkCoversAllLines(t *testing.T) {
	u := "https://example.com/api/project_badges/measure?project=Some.Long.Name&metric=coverage&token=abcdef0123456789"
	d, err := buildDoc([]byte("See "+u+" ok\n"), 40, 0, matrixDark, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.links) != 1 {
		t.Fatalf("got %d links", len(d.links))
	}
	l := d.links[0]
	var got []rune
	for _, g := range append([]seg{{l.line, l.start, l.end}}, l.more...) {
		got = append(got, d.plain[g.line][g.start:g.end]...)
	}
	if string(got) != u {
		t.Errorf("link covers %q, want %q", string(got), u)
	}
}

// A link around an image wraps over several lines; all of them are covered.
func TestWrappedImageLinkCoversAllLines(t *testing.T) {
	u := "https://example.com/api/project_badges/measure?project=Some.Long.Name&token=abcdef0123456789"
	d, err := buildDoc([]byte("[![Coverage]("+u+")](https://x.io/a)\n"), 40, 2, matrixDark, false)
	if err != nil {
		t.Fatal(err)
	}
	l := d.links[0]
	if len(l.more) == 0 || l.line != 1 {
		t.Fatalf("link not extended over wrapped lines: %+v", l)
	}
	last := l.more[len(l.more)-1]
	if got := string(d.plain[last.line][last.start:last.end]); !strings.HasSuffix(u, got) {
		t.Errorf("last part covers %q", got)
	}
}

// A heading above a wrapped link adds rule lines; every part of the link
// must move down with it.
func TestWrappedLinkBelowHeadingCoversAllLines(t *testing.T) {
	u := "https://example.com/api/project_badges/measure?project=Some.Long.Name&token=abcdef0123456789"
	d, err := buildDoc([]byte("## Quality\n\n[![Coverage]("+u+")](https://x.io/a)\n"), 40, 2, matrixDark, false)
	if err != nil {
		t.Fatal(err)
	}
	l := d.links[0]
	var got []rune
	for _, g := range append([]seg{{l.line, l.start, l.end}}, l.more...) {
		got = append(got, d.plain[g.line][g.start:g.end]...)
	}
	if !strings.HasSuffix(string(got), u) {
		t.Errorf("link covers %q, want it to end with %q", string(got), u)
	}
}
