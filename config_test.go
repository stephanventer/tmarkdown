package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testView(spaced bool) viewOpts {
	return viewOpts{pal: matrixDark, keys: defaultKeyMap(), spaced: spaced, maxWidth: 100}
}

func writeFile(t *testing.T, path, body string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeEnv(vars map[string]string, home, goos string, files ...string) configEnv {
	return configEnv{
		getenv: func(k string) string { return vars[k] },
		home:   home,
		goos:   goos,
		exists: func(p string) bool {
			for _, f := range files {
				if f == p {
					return true
				}
			}
			return false
		},
	}
}

func TestFindConfigOrder(t *testing.T) {
	home := "/home/u"
	xdg := "/xdg/tmarkdown/config.toml"
	dot := "/home/u/.config/tmarkdown/config.toml"
	app := filepath.Join("/appdata", "tmarkdown", "config.toml")
	all := []string{xdg, dot, app}
	vars := map[string]string{"XDG_CONFIG_HOME": "/xdg", "APPDATA": "/appdata"}

	tests := []struct {
		name     string
		flag     string
		vars     map[string]string
		goos     string
		files    []string
		want     string
		explicit bool
	}{
		{"flag wins", "/f.toml", map[string]string{"TMARKDOWN_CONFIG": "/e.toml", "XDG_CONFIG_HOME": "/xdg"}, "linux", all, "/f.toml", true},
		{"env next", "", map[string]string{"TMARKDOWN_CONFIG": "/e.toml", "XDG_CONFIG_HOME": "/xdg"}, "linux", all, "/e.toml", true},
		{"xdg next", "", vars, "linux", all, xdg, false},
		{"~/.config when xdg file missing", "", vars, "linux", []string{dot, app}, dot, false},
		{"~/.config on macOS", "", nil, "darwin", []string{dot}, dot, false},
		{"relative xdg ignored", "", map[string]string{"XDG_CONFIG_HOME": "rel"}, "linux", nil, dot, false},
		{"appdata on windows only", "", vars, "windows", []string{app}, app, false},
		{"appdata ignored elsewhere", "", vars, "linux", []string{app}, xdg, false},
		{"~/.config beats appdata", "", map[string]string{"APPDATA": "/appdata"}, "windows", []string{dot, app}, dot, false},
		{"nothing found: first candidate", "", nil, "linux", nil, dot, false},
	}
	for _, tt := range tests {
		got, explicit := findConfig(tt.flag, fakeEnv(tt.vars, home, tt.goos, tt.files...))
		if got != tt.want || explicit != tt.explicit {
			t.Errorf("%s: got %q explicit=%v, want %q explicit=%v", tt.name, got, explicit, tt.want, tt.explicit)
		}
	}
}

func TestLoadMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	s, err := loadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	d := defaultSettings()
	if s.path != "" || s.theme != d.theme || s.maxWidth != d.maxWidth || !reflect.DeepEqual(s.keys, d.keys) || len(s.warnings) > 0 {
		t.Errorf("missing file should give defaults, got %+v", s)
	}
	if _, err := loadSettings(p, true); err == nil {
		t.Error("an explicit missing file should be an error")
	}
}

func TestLoadMerges(t *testing.T) {
	p := writeFile(t, filepath.Join(t.TempDir(), "config.toml"), `
appearance = "light"
max_width = 80
[keys]
quit = "x"
help = []
`)
	s, err := loadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.appearance != "light" || s.spacing != "single" || s.maxWidth != 80 || s.theme != "matrix" {
		t.Errorf("merged settings wrong: %+v", s)
	}
	if !reflect.DeepEqual(s.keys["quit"], []string{"x"}) || len(s.keys["help"]) != 0 || !reflect.DeepEqual(s.keys["search"], []string{"/"}) {
		t.Errorf("keys wrong: quit=%v help=%v search=%v", s.keys["quit"], s.keys["help"], s.keys["search"])
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct{ body, want string }{
		{"theme = \"matrix\"\nmax_width = = 3\n", "config.toml:2:"},
		{`appearance = "dim"`, `appearance must be`},
		{`spacing = "triple"`, `spacing must be`},
		{`max_width = 5`, `max_width must be at least 20`},
	}
	for _, tt := range tests {
		p := writeFile(t, filepath.Join(dir, "config.toml"), tt.body)
		_, err := loadSettings(p, false)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: got %v, want error containing %q", tt.body, err, tt.want)
		}
	}
}

func TestLoadWarnings(t *testing.T) {
	p := writeFile(t, filepath.Join(t.TempDir(), "config.toml"), `
colour = "red"
[keys]
jump = ["x"]
reload = ["o"]
next_link = ["tabb"]
`)
	s, err := loadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(s.warnings, "\n")
	for _, want := range []string{`unknown setting "colour"`, `unknown action "jump"`, `"o" is bound to both outline and reload`, `unknown key "tabb"`} {
		if !strings.Contains(all, want) {
			t.Errorf("warnings missing %q:\n%s", want, all)
		}
	}
}

func TestThemeInheritance(t *testing.T) {
	dir := t.TempDir()
	cfg := writeFile(t, filepath.Join(dir, "config.toml"), `
theme = "mine"
[themes.mine]
extends = "dracula"
[themes.mine.dark]
accent = "#123456"
`)
	writeFile(t, filepath.Join(dir, "themes", "dracula.toml"), `
[dark]
text = "#F8F8F2"
accent = "#BD93F9"
`)
	s, err := loadSettings(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	th, err := s.resolveTheme()
	if err != nil {
		t.Fatal(err)
	}
	if th.dark.accent != "#123456" || th.dark.text != "#F8F8F2" || th.dark.muted != matrixDark.muted {
		t.Errorf("dark palette not merged: %+v", th.dark)
	}
	if th.light != matrixLight {
		t.Errorf("light should come from matrix: %+v", th.light)
	}
}

// A theme pack (for example a git clone) can sit in a folder under themes/.
func TestThemeInSubfolder(t *testing.T) {
	dir := t.TempDir()
	cfg := writeFile(t, filepath.Join(dir, "config.toml"), `theme = "pack-dark"`)
	writeFile(t, filepath.Join(dir, "themes", "pack", "pack-dark.toml"), "[dark]\ntext = \"#111111\"\n")
	writeFile(t, filepath.Join(dir, "themes", ".git", "pack-dark.toml"), "[dark]\ntext = \"#999999\"\n")
	s, err := loadSettings(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	th, err := s.resolveTheme()
	if err != nil {
		t.Fatal(err)
	}
	if th.dark.text != "#111111" {
		t.Errorf("subfolder theme not used: %+v", th.dark)
	}

	// A file directly in themes/ wins over one in a subfolder.
	writeFile(t, filepath.Join(dir, "themes", "pack-dark.toml"), "[dark]\ntext = \"#222222\"\n")
	if th, _ = s.resolveTheme(); th.dark.text != "#222222" {
		t.Errorf("top-level theme should win: %+v", th.dark)
	}
}

func TestThemeInTwoSubfolders(t *testing.T) {
	dir := t.TempDir()
	cfg := writeFile(t, filepath.Join(dir, "config.toml"), `theme = "x"`)
	writeFile(t, filepath.Join(dir, "themes", "a", "x.toml"), "[dark]\ntext = \"#111111\"\n")
	writeFile(t, filepath.Join(dir, "themes", "b", "x.toml"), "[dark]\ntext = \"#222222\"\n")
	s, err := loadSettings(cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveTheme(); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Errorf("want ambiguity error, got %v", err)
	}
}

func TestThemeErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct{ body, want string }{
		{"theme = \"nope\"", `unknown theme "nope"`},
		{"theme = \"../x\"", `invalid theme name`},
		{"theme = \"a\"\n[themes.a]\nextends = \"b\"\n[themes.b]\nextends = \"a\"", `extends itself: a → b → a`},
		{"theme = \"a\"\n[themes.a.dark]\ntext = \"green\"", `text = "green" is not a colour`},
	}
	for _, tt := range tests {
		p := writeFile(t, filepath.Join(dir, "config.toml"), tt.body)
		s, err := loadSettings(p, false)
		if err == nil {
			_, err = s.resolveTheme()
		}
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: got %v, want error containing %q", tt.body, err, tt.want)
		}
	}

	p := writeFile(t, filepath.Join(dir, "config.toml"), "theme = \"a\"\n[themes.a.dark]\ntxt = \"#000000\"")
	s, _ := loadSettings(p, false)
	if _, err := s.resolveTheme(); err != nil || len(s.warnings) != 1 || !strings.Contains(s.warnings[0], `unknown colour "txt"`) {
		t.Errorf("unknown role should warn: err=%v warnings=%v", err, s.warnings)
	}
}

func TestPrintConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, filepath.Join(dir, "config.toml"), `
theme = "mine"
spacing = "double"
max_width = 72
[keys]
quit = ["x", "ctrl+q"]
page_down = ["space"]
help = []
[themes.mine]
extends = "matrix"
[themes.mine.light]
text = "#101010"
`)
	a, err := loadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	out := writeFile(t, filepath.Join(dir, "printed.toml"), a.toTOML())
	b, err := loadSettings(out, false)
	if err != nil {
		t.Fatalf("printed config doesn't load: %v\n%s", err, a.toTOML())
	}
	if len(b.warnings) > 0 {
		t.Errorf("printed config gives warnings: %v", b.warnings)
	}
	a.path, b.path = "", ""
	if !reflect.DeepEqual(a, b) {
		t.Errorf("round trip changed settings:\n%+v\n%+v", a, b)
	}
	ta, _ := a.resolveTheme()
	tb, _ := b.resolveTheme()
	if ta != tb {
		t.Error("round trip changed the theme")
	}
	if c := defaultSettings(); !strings.Contains(c.toTOML(), `page_down      = ["pgdown", "space", "f"]`) {
		t.Errorf("default print:\n%s", c.toTOML())
	}
}

func TestParseFlags(t *testing.T) {
	f, err := parseFlags([]string{"--light", "--config", "c.toml", "--single", "x.md"})
	if err != nil || f.appearance != "light" || f.config != "c.toml" || f.spacing != "single" || f.path != "x.md" {
		t.Errorf("got %+v, %v", f, err)
	}
	if f, _ := parseFlags([]string{"--config=a.toml"}); f.config != "a.toml" {
		t.Errorf("--config= not parsed: %+v", f)
	}
	if _, err := parseFlags([]string{"--config"}); err == nil {
		t.Error("--config without a value should fail")
	}
	if f, err := parseFlags([]string{"--bogus", "-h"}); err != nil || !f.help {
		t.Errorf("-h should win over a bad flag: %+v %v", f, err)
	}
}

func TestRemappedKeys(t *testing.T) {
	keys := defaultKeys()
	keys["quit"] = []string{"x"}
	keys["search"] = nil
	o := testView(false)
	o.keys = newKeyMap(keys)
	m := newModel("", "x", []byte("# A\n\ntext\n"), o)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = mm.(model)

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd != nil {
		t.Error("q should no longer quit")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}); cmd == nil {
		t.Error("x should quit")
	}
	if mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}); mm.(model).mode != modeNormal {
		t.Error("unbound search should do nothing")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); cmd == nil {
		t.Error("ctrl+c should always quit")
	}

	m.w, m.h = 100, 40
	help := string(stripANSI(m.helpView()))
	if !strings.Contains(help, "Search as you type") || !strings.Contains(help, "unbound") {
		t.Errorf("help should show unbound search:\n%s", help)
	}
}

func TestHelpPairsSplit(t *testing.T) {
	keys := defaultKeys()
	keys["next_match"] = []string{"n", "ctrl+n"}
	var found []string
	for _, g := range helpGroups(newKeyMap(keys)) {
		for _, r := range g.rows {
			found = append(found, r.action)
		}
	}
	all := strings.Join(found, "|")
	if !strings.Contains(all, "Next match|Previous match") || strings.Contains(all, "Next / previous match") {
		t.Errorf("unequal pair should split into two rows: %s", all)
	}
	if !strings.Contains(all, "Line up / down") {
		t.Errorf("equal pair should stay on one row: %s", all)
	}
}

func TestKeyLabel(t *testing.T) {
	for in, want := range map[string]string{
		"up": "↑", "shift+up": "Shift+↑", "ctrl+u": "Ctrl+u", "pgdown": "PgDn",
		" ": "Space", "shift+tab": "Shift+Tab", "G": "G", "f5": "F5",
	} {
		if got := keyLabel(in); got != want {
			t.Errorf("keyLabel(%q) = %q, want %q", in, got, want)
		}
	}
}
