package main

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// glamour prints "text url" for every link. To hide the URL we render it
// through a template func that swaps it for a private-use marker rune, then
// strip the markers in postProcess and remember where each link landed.
const (
	markLink      = '\uE000' // regular link: URL hidden
	markAutoStart = '\uE001' // bare URL: kept visible between start/end
	markAutoEnd   = '\uE002'
)

// Set before each render; read by the template func. Rendering happens on
// one goroutine at a time, so plain package state is fine.
//
// glamour renders hrefs in document order, so the k-th call matches the
// k-th entry from scanAST. If the count ever disagrees (a glamour change),
// buildDoc renders again keyed by URL only.
var (
	renderEntries []linkEntry
	renderAutoSet map[string]bool
	renderByOrder bool
	renderCalls   int
)

func init() {
	gansi.TemplateFuncMap["tmdHref"] = func(values ...interface{}) string {
		u, _ := values[0].(string)
		auto := renderAutoSet[u]
		if renderByOrder && renderCalls < len(renderEntries) {
			auto = renderEntries[renderCalls].auto
		}
		renderCalls++
		if auto {
			return string(markAutoStart) + u + string(markAutoEnd)
		}
		return string(markLink)
	}
}

type linkEntry struct {
	text, url string
	auto      bool // bare URL, shown as is
}

type heading struct {
	level int
	text  string
	line  int
}

type link struct {
	text       string
	url        string
	line       int
	start, end int   // rune columns in the plain line, end exclusive
	more       []seg // a wrapped link continues on the lines after line
}

// seg is one wrapped piece of a link: rune columns [start,end) of a line.
type seg struct{ line, start, end int }

// Doc is one rendering of a markdown source at a given width.
type Doc struct {
	lines    []string // ANSI-styled lines
	plain    [][]rune // same lines with escape codes removed
	headings []heading
	links    []link
}

// buildDoc renders src wrapped at width, indents every line by pad spaces,
// and indexes headings and links. With osc8, link text gets OSC 8 hyperlinks.
func buildDoc(src []byte, width, pad int, p palette, osc8 bool) (*Doc, error) {
	heads, entries := scanAST(src)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(styleConfig(p)),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
		glamour.WithInlineTableLinks(true),
	)
	if err != nil {
		return nil, err
	}
	autos := map[string]bool{}
	for _, e := range entries {
		if e.auto {
			autos[e.url] = true
		}
	}
	renderEntries, renderAutoSet = entries, autos
	defer func() { renderEntries, renderAutoSet = nil, nil }()
	out, err := "", error(nil)
	for _, byOrder := range []bool{true, false} {
		renderByOrder, renderCalls = byOrder, 0
		out, err = r.Render(string(src))
		if err != nil {
			return nil, err
		}
		if renderCalls == len(entries) {
			break
		}
	}
	if !renderByOrder || renderCalls != len(entries) {
		entries = nil // order unknown: links get no text or URL
	}

	d := postProcess(out, entries)
	if pad > 0 {
		sp := strings.Repeat(" ", pad)
		for i := range d.lines {
			d.lines[i] = sp + d.lines[i]
			d.plain[i] = append([]rune(sp), d.plain[i]...)
		}
		for i := range d.links {
			d.links[i].start += pad
			d.links[i].end += pad
			for j := range d.links[i].more {
				d.links[i].more[j].start += pad
				d.links[i].more[j].end += pad
			}
		}
	}
	d.headings = mapHeadings(heads, d.plain)
	styleHeadings(d, width, pad, p)
	if osc8 {
		addHyperlinks(d)
	}
	return d, nil
}

// scanAST lists headings, and in document order every link whose URL
// glamour renders.
func scanAST(src []byte) ([]heading, []linkEntry) {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.DefinitionList))
	root := md.Parser().Parse(text.NewReader(src))
	var heads []heading
	var links []linkEntry
	_ = ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := n.(type) {
		case *ast.Heading:
			heads = append(heads, heading{level: v.Level, text: nodeText(v, src)})
		case *ast.Link:
			// glamour skips anchor-only and unparsable URLs.
			dest := string(v.Destination)
			if u, err := url.Parse(dest); err == nil && "#"+u.Fragment != dest {
				links = append(links, linkEntry{text: nodeText(v, src), url: dest})
			}
		case *ast.AutoLink:
			u := string(v.URL(src))
			if v.AutoLinkType == ast.AutoLinkEmail {
				if !strings.HasPrefix(strings.ToLower(u), "mailto:") {
					u = "mailto:" + u
				}
				links = append(links, linkEntry{text: string(v.Label(src)), url: u})
			} else {
				links = append(links, linkEntry{text: u, url: u, auto: true})
			}
		}
		return ast.WalkContinue, nil
	})
	return heads, links
}

func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if img, ok := c.(*ast.Image); ok {
			// glamour draws an image as "Image: alt → url".
			if entering {
				b.WriteString("Image: ")
			} else {
				b.WriteString(" → " + string(img.Destination))
			}
			return ast.WalkContinue, nil
		}
		if !entering {
			return ast.WalkContinue, nil
		}
		switch v := c.(type) {
		case *ast.Text:
			b.Write(v.Segment.Value(src))
			if v.SoftLineBreak() || v.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(v.Value)
		case *ast.AutoLink:
			b.Write(v.Label(src))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// escLen returns the byte length of the escape sequence starting at s[i]
// (which must be ESC). Handles CSI and OSC; other escapes count as 2 bytes.
func escLen(s string, i int) int {
	if i+1 >= len(s) {
		return 1
	}
	switch s[i+1] {
	case '[':
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j - i + 1
			}
		}
		return len(s) - i
	case ']':
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j - i + 2
			}
		}
		return len(s) - i
	}
	return 2
}

func stripANSI(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escLen(s, i)
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		out = append(out, r)
		i += n
	}
	return out
}

// postProcess removes link markers from glamour output and records where
// each link ended up. entries[k] belongs to the k-th marker.
func postProcess(out string, entries []linkEntry) *Doc {
	d := &Doc{}
	var cur []byte
	col := 0
	lastSpace := -1 // byte index in cur of a trailing plain space, if any
	k := 0
	auto := -1    // index in d.links of an open autolink
	autoFrom := 0 // column where the open autolink starts on this line

	next := func() linkEntry {
		k++
		if k-1 < len(entries) {
			return entries[k-1]
		}
		return linkEntry{}
	}
	flush := func() {
		line := string(cur)
		d.lines = append(d.lines, line)
		d.plain = append(d.plain, stripANSI(line))
		cur = cur[:0]
		col = 0
		lastSpace = -1
	}

	for i := 0; i < len(out); {
		c := out[i]
		if c == 0x1b {
			n := escLen(out, i)
			cur = append(cur, out[i:i+n]...)
			i += n
			continue
		}
		if c == '\n' {
			if auto >= 0 { // bare URL wrapped: record this line's part
				l := &d.links[auto]
				from, e := textBounds(stripANSI(string(cur)), autoFrom)
				if len(l.more) == 0 && l.end == 0 {
					l.end = e
				} else {
					l.more = append(l.more, seg{len(d.lines), from, e})
				}
				autoFrom = 0
			}
			flush()
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(out[i:])
		i += n
		switch r {
		case markLink:
			e := next()
			l := link{text: e.text, url: e.url}
			before := stripANSI(string(cur))
			if strings.TrimSpace(string(before)) == "" && len(d.plain) > 0 {
				// Wrapped right before the hidden URL: the text is on the
				// previous line, and the wrap already ate the space.
				l.line = len(d.plain) - 1
				prev := []rune(strings.TrimRight(string(d.plain[l.line]), " "))
				l.end = len(prev)
				l.start = l.end - suffixMatch(prev, e.text)
			} else {
				if lastSpace >= 0 {
					cur = append(cur[:lastSpace], cur[lastSpace+1:]...)
					col--
					before = stripANSI(string(cur))
				}
				l.line, l.end = len(d.lines), col
				l.start = l.end - suffixMatch(before, e.text)
			}
			extendBack(&l, d.plain, stripANSI(string(cur)), e.text)
			lastSpace = -1
			d.links = append(d.links, l)
		case markAutoStart:
			e := next()
			d.links = append(d.links, link{text: e.text, url: e.url, line: len(d.lines), start: col})
			auto = len(d.links) - 1
		case markAutoEnd:
			if auto >= 0 {
				l := &d.links[auto]
				if l.end == 0 && len(l.more) == 0 {
					l.end = col
				} else {
					from, e := textBounds(stripANSI(string(cur)), autoFrom)
					l.more = append(l.more, seg{len(d.lines), from, min(e, col)})
				}
				auto = -1
			}
		default:
			if r == ' ' {
				lastSpace = len(cur)
			} else {
				lastSpace = -1
			}
			cur = append(cur, out[i-n:i]...)
			col++
		}
	}
	if len(cur) > 0 {
		flush()
	}
	// Drop trailing blank lines glamour adds.
	for len(d.lines) > 0 && strings.TrimSpace(string(d.plain[len(d.plain)-1])) == "" {
		d.lines = d.lines[:len(d.lines)-1]
		d.plain = d.plain[:len(d.plain)-1]
	}
	return d
}

// extendBack grows a link whose text wrapped onto earlier lines. The text
// it has found so far sits at the end of l; the rest is matched backwards
// character by character (wrapping moves spaces around). If the earlier
// lines don't hold the text, l is left as it was.
func extendBack(l *link, plain [][]rune, cur []rune, text string) {
	need := []rune(strings.Join(strings.Fields(text), ""))
	row := cur // the line being built isn't in plain yet
	if l.line < len(plain) {
		row = plain[l.line]
	}
	have := 0
	for _, r := range row[l.start:min(l.end, len(row))] {
		if r != ' ' {
			have++
		}
	}
	if len(need) == 0 || have >= len(need) {
		return
	}
	rem := need[:len(need)-have]
	var segs []seg // later lines first
	for ln := min(l.line, len(plain)) - 1; ln >= 0 && len(rem) > 0; ln-- {
		first, last := -1, -1
		for j := len(plain[ln]) - 1; j >= 0 && len(rem) > 0; j-- {
			if plain[ln][j] == ' ' {
				continue
			}
			if plain[ln][j] != rem[len(rem)-1] {
				return
			}
			rem = rem[:len(rem)-1]
			if last < 0 {
				last = j + 1
			}
			first = j
		}
		if first >= 0 {
			segs = append(segs, seg{ln, first, last})
		}
	}
	if len(rem) > 0 || len(segs) == 0 {
		return
	}
	more := []seg{}
	for i := len(segs) - 2; i >= 0; i-- {
		more = append(more, segs[i])
	}
	if l.end > l.start {
		more = append(more, seg{l.line, l.start, l.end})
	}
	top := segs[len(segs)-1]
	l.line, l.start, l.end, l.more = top.line, top.start, top.end, more
}

// textBounds returns the columns of line's text at or after from, leaving
// out the indent before it and the padding glamour adds after it.
func textBounds(line []rune, from int) (int, int) {
	end := len(line)
	for end > from && line[end-1] == ' ' {
		end--
	}
	for from < end && line[from] == ' ' {
		from++
	}
	return from, end
}

// suffixMatch returns how many runes at the end of line match a suffix of
// text. Wrapped link text only partly sits on the marker's line.
func suffixMatch(line []rune, text string) int {
	t := []rune(text)
	for n := min(len(t), len(line)); n > 0; n-- {
		if string(line[len(line)-n:]) == string(t[len(t)-n:]) {
			for n > 0 && line[len(line)-n] == ' ' { // don't start on a space
				n--
			}
			return n
		}
	}
	// Unknown text (e.g. emoji shortcodes): fall back to the last word.
	n := 0
	for k := len(line) - 1; k >= 0 && line[k] != ' '; k-- {
		n++
	}
	return n
}

// mapHeadings finds each heading's rendered line, scanning in order.
func mapHeadings(heads []heading, plain [][]rune) []heading {
	out := make([]heading, 0, len(heads))
	ln := 0
	for _, h := range heads {
		want := strings.ToLower(h.text)
		for k := ln; k < len(plain); k++ {
			t := strings.ToLower(strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(string(plain[k])), "#")))
			if t != "" && strings.HasPrefix(want, t) {
				h.line = k
				out = append(out, h)
				ln = k + 1
				break
			}
		}
	}
	return out
}

func addHyperlinks(d *Doc) {
	byLine := map[int][]span{}
	for _, l := range d.links {
		if !isWebLink(l.url) || l.end <= l.start {
			continue
		}
		on, off := "\x1b]8;;"+l.url+"\x1b\\", "\x1b]8;;\x1b\\"
		byLine[l.line] = append(byLine[l.line], span{l.start, l.end, on, off})
		for _, g := range l.more {
			byLine[g.line] = append(byLine[g.line], span{g.start, g.end, on, off})
		}
	}
	for ln, sp := range byLine {
		d.lines[ln] = decorate(d.lines[ln], sp)
	}
}

func isWebLink(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto":
		return true
	}
	return false
}
