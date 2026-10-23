package main

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

// keyMap holds the remappable keys of the viewer. Typing in the search and
// outline-filter inputs is not remappable, and Ctrl+c always quits.
type keyMap struct {
	ScrollUp, ScrollDown         key.Binding
	ScrollUp10, ScrollDown10     key.Binding
	HalfPageUp, HalfPageDown     key.Binding
	PageUp, PageDown             key.Binding
	Top, Bottom                  key.Binding
	Search, NextMatch, PrevMatch key.Binding
	Outline                      key.Binding
	NextLink, PrevLink, OpenLink key.Binding
	Back, Reload, ToggleSpacing  key.Binding
	Help, Quit                   key.Binding
}

type actionDef struct {
	name string
	def  []string // default keys
	get  func(*keyMap) *key.Binding
}

// actions lists every remappable action in the order it is printed, with
// its default keys. Key names are those of tea.KeyMsg.String(), except that
// "space" stands for the space bar.
var actions = []actionDef{
	{"scroll_up", []string{"up", "k"}, func(k *keyMap) *key.Binding { return &k.ScrollUp }},
	{"scroll_down", []string{"down", "j"}, func(k *keyMap) *key.Binding { return &k.ScrollDown }},
	{"scroll_up_10", []string{"shift+up", "K"}, func(k *keyMap) *key.Binding { return &k.ScrollUp10 }},
	{"scroll_down_10", []string{"shift+down", "J"}, func(k *keyMap) *key.Binding { return &k.ScrollDown10 }},
	{"half_page_up", []string{"ctrl+u", "u"}, func(k *keyMap) *key.Binding { return &k.HalfPageUp }},
	{"half_page_down", []string{"ctrl+d", "d"}, func(k *keyMap) *key.Binding { return &k.HalfPageDown }},
	{"page_up", []string{"pgup", "b"}, func(k *keyMap) *key.Binding { return &k.PageUp }},
	{"page_down", []string{"pgdown", "space", "f"}, func(k *keyMap) *key.Binding { return &k.PageDown }},
	{"top", []string{"g", "home"}, func(k *keyMap) *key.Binding { return &k.Top }},
	{"bottom", []string{"G", "end"}, func(k *keyMap) *key.Binding { return &k.Bottom }},
	{"search", []string{"/"}, func(k *keyMap) *key.Binding { return &k.Search }},
	{"next_match", []string{"n"}, func(k *keyMap) *key.Binding { return &k.NextMatch }},
	{"prev_match", []string{"N"}, func(k *keyMap) *key.Binding { return &k.PrevMatch }},
	{"outline", []string{"o"}, func(k *keyMap) *key.Binding { return &k.Outline }},
	{"next_link", []string{"tab"}, func(k *keyMap) *key.Binding { return &k.NextLink }},
	{"prev_link", []string{"shift+tab"}, func(k *keyMap) *key.Binding { return &k.PrevLink }},
	{"open_link", []string{"enter"}, func(k *keyMap) *key.Binding { return &k.OpenLink }},
	{"back", []string{"esc"}, func(k *keyMap) *key.Binding { return &k.Back }},
	{"reload", []string{"r"}, func(k *keyMap) *key.Binding { return &k.Reload }},
	{"toggle_spacing", []string{"s"}, func(k *keyMap) *key.Binding { return &k.ToggleSpacing }},
	{"help", []string{"?", "h"}, func(k *keyMap) *key.Binding { return &k.Help }},
	{"quit", []string{"q", "ctrl+c"}, func(k *keyMap) *key.Binding { return &k.Quit }},
}

func isAction(name string) bool {
	return slices.ContainsFunc(actions, func(a actionDef) bool { return a.name == name })
}

// defaultKeys returns a fresh action -> keys map with the built-in keys.
func defaultKeys() map[string][]string {
	m := make(map[string][]string, len(actions))
	for _, a := range actions {
		m[a.name] = slices.Clone(a.def)
	}
	return m
}

// newKeyMap builds the bindings from an action -> keys map. Actions missing
// from the map, or with no keys, are left unbound.
func newKeyMap(keys map[string][]string) keyMap {
	var km keyMap
	for _, a := range actions {
		b := a.get(&km)
		ks := make([]string, 0, len(keys[a.name]))
		for _, k := range keys[a.name] {
			ks = append(ks, msgKey(k))
		}
		if len(ks) == 0 {
			*b = key.NewBinding(key.WithDisabled())
			continue
		}
		*b = key.NewBinding(key.WithKeys(ks...))
	}
	return km
}

func defaultKeyMap() keyMap { return newKeyMap(defaultKeys()) }

// msgKey turns a config key name into what tea.KeyMsg.String() reports.
func msgKey(k string) string {
	if k == "space" {
		return " "
	}
	return k
}

// configKey is the reverse of msgKey, for printing.
func configKey(k string) string {
	if k == " " {
		return "space"
	}
	return k
}

// knownKeyNames are the named keys Bubble Tea reports, such as "pgdown" or
// "ctrl+shift+up".
var knownKeyNames = func() map[string]bool {
	m := map[string]bool{"space": true}
	for t := tea.KeyType(-1000); t < 1000; t++ {
		if s := t.String(); s != "" && t != tea.KeyRunes {
			m[s] = true
		}
	}
	return m
}()

func validKey(k string) bool {
	k = strings.TrimPrefix(k, "alt+")
	return utf8.RuneCountInString(k) == 1 || knownKeyNames[k]
}

// checkKeys returns warnings for unknown key names and for keys bound to
// more than one action.
func checkKeys(keys map[string][]string) []string {
	var warns []string
	owner := map[string]string{}
	for _, a := range actions {
		for _, k := range keys[a.name] {
			if !validKey(k) {
				warns = append(warns, fmt.Sprintf("[keys] %s: unknown key %q", a.name, k))
			}
			mk := msgKey(k)
			if prev, ok := owner[mk]; ok && prev != a.name {
				warns = append(warns, fmt.Sprintf("[keys] %q is bound to both %s and %s; only %s will work", k, prev, a.name, prev))
				continue
			}
			owner[mk] = a.name
		}
	}
	return warns
}

// keyLabel turns a key name into the text drawn on a keycap.
func keyLabel(k string) string {
	if k == " " || k == "space" {
		return "Space"
	}
	if utf8.RuneCountInString(k) == 1 {
		return k
	}
	names := map[string]string{
		"up": "↑", "down": "↓", "left": "←", "right": "→",
		"pgup": "PgUp", "pgdown": "PgDn", "home": "Home", "end": "End",
		"tab": "Tab", "enter": "Enter", "esc": "Esc", "backspace": "Backspace",
		"delete": "Del", "insert": "Ins", "space": "Space",
		"ctrl": "Ctrl", "alt": "Alt", "shift": "Shift",
	}
	parts := strings.Split(k, "+")
	for i, p := range parts {
		if n, ok := names[p]; ok {
			parts[i] = n
		} else if len(p) > 1 && p[0] == 'f' && strings.Trim(p[1:], "0123456789") == "" {
			parts[i] = "F" + p[1:]
		}
	}
	return strings.Join(parts, "+")
}

// helpRow is an action and its keys. Each choice is one or more keys drawn
// as keycaps side by side; choices are separated by "or".
type helpRow struct {
	action  string
	choices [][]string
}

type helpGroup struct {
	title string
	rows  []helpRow
}

// helpGroups builds the help screen from the keymap, so it shows what the
// user actually has bound.
func helpGroups(km keyMap) []helpGroup {
	labels := func(b key.Binding) []string {
		var out []string
		for _, k := range b.Keys() {
			out = append(out, keyLabel(k))
		}
		return out
	}
	single := func(action string, b key.Binding) []helpRow {
		var ch [][]string
		for _, l := range labels(b) {
			ch = append(ch, []string{l})
		}
		return []helpRow{{action, ch}}
	}
	// pair shows two opposite actions on one row when their keys line up,
	// e.g. "↑ ↓ or k j", and on two rows otherwise.
	pair := func(both, first, second string, a, b key.Binding) []helpRow {
		la, lb := labels(a), labels(b)
		if len(la) != len(lb) || len(la) == 0 {
			return append(single(first, a), single(second, b)...)
		}
		var ch [][]string
		for i := range la {
			ch = append(ch, []string{la[i], lb[i]})
		}
		return []helpRow{{both, ch}}
	}
	fixed := func(action, k string) []helpRow { return []helpRow{{action, [][]string{{k}}}} }
	rows := func(rs ...[]helpRow) []helpRow { return slices.Concat(rs...) }

	return []helpGroup{
		{"Scrolling", rows(
			pair("Line up / down", "Line up", "Line down", km.ScrollUp, km.ScrollDown),
			pair("10 lines up / down", "10 lines up", "10 lines down", km.ScrollUp10, km.ScrollDown10),
			pair("Half page up / down", "Half page up", "Half page down", km.HalfPageUp, km.HalfPageDown),
			pair("Page up / down", "Page up", "Page down", km.PageUp, km.PageDown),
			pair("Top / bottom", "Top", "Bottom", km.Top, km.Bottom),
			fixed("Scroll", "wheel"),
			fixed("Scroll fast", "Alt+wheel"),
		)},
		{"Finding things", rows(
			single("Search as you type", km.Search),
			pair("Next / previous match", "Next match", "Previous match", km.NextMatch, km.PrevMatch),
			single("Heading outline", km.Outline),
			pair("Next / previous link", "Next link", "Previous link", km.NextLink, km.PrevLink),
			single("Open selected link", km.OpenLink),
		)},
		{"General", rows(
			single("Back out one level", km.Back),
			single("Reload file", km.Reload),
			single("Double / single spacing", km.ToggleSpacing),
			single("This help", km.Help),
			single("Quit", km.Quit),
		)},
	}
}
