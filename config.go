package main

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// settings are the built-in defaults, overlaid by the config file and then
// by command-line flags.
type settings struct {
	path       string // config file the settings were read from; "" if none
	theme      string
	appearance string // auto | dark | light
	spacing    string // single | double
	maxWidth   int
	keys       map[string][]string  // action -> keys, every action present
	themes     map[string]themeSpec // defined inline in the config file
	themeDir   string               // where named theme files live
	warnings   []string
}

func defaultSettings() settings {
	return settings{
		theme:      defaultTheme,
		appearance: "auto",
		spacing:    "single",
		maxWidth:   100,
		keys:       defaultKeys(),
	}
}

// fileConfig is the shape of config.toml. Unset fields keep their defaults.
type fileConfig struct {
	Theme      string               `toml:"theme"`
	Appearance string               `toml:"appearance"`
	Spacing    string               `toml:"spacing"`
	MaxWidth   *int                 `toml:"max_width"`
	Keys       map[string]keyList   `toml:"keys"`
	Themes     map[string]themeSpec `toml:"themes"`
}

// themeSpec is a theme file, or a [themes.NAME] table in config.toml.
type themeSpec struct {
	Extends string            `toml:"extends"`
	Dark    map[string]string `toml:"dark"`
	Light   map[string]string `toml:"light"`
}

// keyList accepts either one key name or a list of them.
type keyList []string

func (k *keyList) UnmarshalTOML(v any) error {
	switch v := v.(type) {
	case string:
		*k = keyList{v}
		return nil
	case []any:
		out := make(keyList, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("keys must be strings, like \"q\" or \"ctrl+c\"; got %v", e)
			}
			out = append(out, s)
		}
		*k = out
		return nil
	}
	return fmt.Errorf("expected a key name or a list of key names, got %v", v)
}

// configEnv is the part of the environment that decides where the config
// file lives. It is a struct so tests can fake it.
type configEnv struct {
	getenv func(string) string
	home   string
	goos   string
	exists func(string) bool
}

func osConfigEnv(goos string) configEnv {
	home, _ := os.UserHomeDir()
	return configEnv{
		getenv: os.Getenv,
		home:   home,
		goos:   goos,
		exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
	}
}

// findConfig returns the config file to load. An explicit path (from
// --config or $TMARKDOWN_CONFIG) must exist. Otherwise the first existing
// standard location wins; if none exists, the first one is returned so it
// can be shown in --help.
func findConfig(flagPath string, e configEnv) (path string, explicit bool) {
	if flagPath != "" {
		return flagPath, true
	}
	if p := e.getenv("TMARKDOWN_CONFIG"); p != "" {
		return p, true
	}
	var candidates []string
	// The XDG spec says relative paths must be ignored.
	if x := e.getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		candidates = append(candidates, filepath.Join(x, "tmarkdown", "config.toml"))
	}
	// ~/.config on every OS, macOS included: os.UserConfigDir would give
	// ~/Library/Application Support, which CLI users don't expect.
	if e.home != "" {
		candidates = append(candidates, filepath.Join(e.home, ".config", "tmarkdown", "config.toml"))
	}
	if e.goos == "windows" {
		if a := e.getenv("APPDATA"); a != "" {
			candidates = append(candidates, filepath.Join(a, "tmarkdown", "config.toml"))
		}
	}
	for _, c := range candidates {
		if e.exists(c) {
			return c, false
		}
	}
	if len(candidates) > 0 {
		return candidates[0], false
	}
	return "", false
}

// loadSettings reads the config file at path over the defaults. A missing
// file is fine unless the path was given explicitly.
func loadSettings(path string, explicit bool) (settings, error) {
	s := defaultSettings()
	if path == "" {
		return s, nil
	}
	s.themeDir = filepath.Join(filepath.Dir(path), "themes")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && !explicit {
			return s, nil
		}
		return s, fmt.Errorf("cannot read config %s: %w", path, unwrapPathErr(err))
	}
	s.path = path

	var fc fileConfig
	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		return s, tomlError(path, err)
	}
	for _, k := range md.Undecoded() {
		s.warnings = append(s.warnings, fmt.Sprintf("%s: unknown setting %q", path, k.String()))
	}

	if fc.Theme != "" {
		s.theme = fc.Theme
	}
	if fc.Appearance != "" {
		if !slices.Contains([]string{"auto", "dark", "light"}, fc.Appearance) {
			return s, fmt.Errorf("%s: appearance must be \"auto\", \"dark\" or \"light\", not %q", path, fc.Appearance)
		}
		s.appearance = fc.Appearance
	}
	if fc.Spacing != "" {
		if !slices.Contains([]string{"single", "double"}, fc.Spacing) {
			return s, fmt.Errorf("%s: spacing must be \"single\" or \"double\", not %q", path, fc.Spacing)
		}
		s.spacing = fc.Spacing
	}
	if fc.MaxWidth != nil {
		if *fc.MaxWidth < 20 {
			return s, fmt.Errorf("%s: max_width must be at least 20, not %d", path, *fc.MaxWidth)
		}
		s.maxWidth = *fc.MaxWidth
	}
	for _, name := range slices.Sorted(maps.Keys(fc.Keys)) {
		if !isAction(name) {
			s.warnings = append(s.warnings, fmt.Sprintf("%s: [keys] unknown action %q", path, name))
			continue
		}
		s.keys[name] = []string(fc.Keys[name])
	}
	s.warnings = append(s.warnings, prefixAll(path+": ", checkKeys(s.keys))...)
	s.themes = fc.Themes
	return s, nil
}

// tomlError adds the file and line to a TOML error.
func tomlError(path string, err error) error {
	var pe toml.ParseError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s:%d: %s", path, pe.Position.Line, pe.Message)
	}
	return fmt.Errorf("%s: %w", path, err)
}

// ---- themes ----

var hexColour = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// resolveTheme returns the selected theme with everything it extends
// filled in. Warnings (unknown colour roles) are added to s.
func (s *settings) resolveTheme() (theme, error) {
	return s.lookupTheme(s.theme, nil)
}

func (s *settings) lookupTheme(name string, chain []string) (theme, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return theme{}, fmt.Errorf("invalid theme name %q", name)
	}
	if slices.Contains(chain, name) {
		return theme{}, fmt.Errorf("theme %q extends itself: %s", name, strings.Join(append(chain, name), " → "))
	}
	spec, src, ok, err := s.findThemeSpec(name)
	if err != nil {
		return theme{}, err
	}
	if !ok {
		if t, ok := builtinThemes[name]; ok {
			return t, nil
		}
		where := "no [themes." + name + "] table in the config"
		if s.themeDir != "" {
			where += " and no file " + filepath.Join(s.themeDir, name+".toml") + " or " + filepath.Join(s.themeDir, "*", name+".toml")
		}
		return theme{}, fmt.Errorf("unknown theme %q: not built in (%s), %s", name, strings.Join(slices.Sorted(maps.Keys(builtinThemes)), ", "), where)
	}

	// Roles a theme leaves out come from the theme it extends, or Matrix.
	t := builtinThemes[defaultTheme]
	if spec.Extends != "" {
		if t, err = s.lookupTheme(spec.Extends, append(chain, name)); err != nil {
			return theme{}, err
		}
	}
	if err := s.applyRoles(&t.dark, spec.Dark, src, "dark"); err != nil {
		return theme{}, err
	}
	if err := s.applyRoles(&t.light, spec.Light, src, "light"); err != nil {
		return theme{}, err
	}
	return t, nil
}

// findThemeSpec looks for a user theme: first inline in the config file,
// then in the themes directory next to it.
func (s *settings) findThemeSpec(name string) (themeSpec, string, bool, error) {
	if spec, ok := s.themes[name]; ok {
		return spec, fmt.Sprintf("%s [themes.%s]", s.path, name), true, nil
	}
	if s.themeDir == "" {
		return themeSpec{}, "", false, nil
	}
	p, err := s.themeFile(name)
	if p == "" || err != nil {
		return themeSpec{}, "", false, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return themeSpec{}, "", false, fmt.Errorf("cannot read theme %s: %w", p, unwrapPathErr(err))
	}
	var spec themeSpec
	md, err := toml.Decode(string(data), &spec)
	if err != nil {
		return themeSpec{}, "", false, tomlError(p, err)
	}
	for _, k := range md.Undecoded() {
		s.warnings = append(s.warnings, fmt.Sprintf("%s: unknown setting %q", p, k.String()))
	}
	return spec, p, true, nil
}

// themeFile finds NAME.toml in the themes directory, or one folder below
// it so a theme pack can be cloned straight into themes/. Hidden folders
// such as .git are skipped. It returns "" if there is no such file.
func (s *settings) themeFile(name string) (string, error) {
	p := filepath.Join(s.themeDir, name+".toml")
	if _, err := os.Stat(p); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return p, nil // let the read report anything other than "missing"
	}
	entries, err := os.ReadDir(s.themeDir)
	if err != nil {
		return "", nil
	}
	var found []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(s.themeDir, e.Name(), name+".toml")
		if _, err := os.Stat(p); err == nil {
			found = append(found, p)
		}
	}
	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("theme %q is in more than one folder: %s", name, strings.Join(found, ", "))
}

func (s *settings) applyRoles(p *palette, roles map[string]string, src, section string) error {
	for _, name := range slices.Sorted(maps.Keys(roles)) {
		v := roles[name]
		i := slices.IndexFunc(paletteRoles, func(r paletteRole) bool { return r.name == name })
		if i < 0 {
			s.warnings = append(s.warnings, fmt.Sprintf("%s: unknown colour %q in [%s] (known: %s)", src, name, section, strings.Join(roleNames(), ", ")))
			continue
		}
		if !hexColour.MatchString(v) {
			return fmt.Errorf("%s: [%s] %s = %q is not a colour like \"#00FF41\"", src, section, name, v)
		}
		*paletteRoles[i].get(p) = v
	}
	return nil
}

func roleNames() []string {
	out := make([]string, len(paletteRoles))
	for i, r := range paletteRoles {
		out[i] = r.name
	}
	return out
}

// ---- printing ----

// toTOML prints the settings as a config file that loads back to the same
// settings.
func (s settings) toTOML() string {
	var b strings.Builder
	if s.path != "" {
		fmt.Fprintf(&b, "# tmarkdown config (built-in defaults merged with %s)\n\n", s.path)
	} else {
		b.WriteString("# tmarkdown config (built-in defaults)\n\n")
	}
	fmt.Fprintf(&b, "theme      = %s\n", tomlString(s.theme))
	fmt.Fprintf(&b, "appearance = %-9s # auto | dark | light\n", tomlString(s.appearance))
	fmt.Fprintf(&b, "spacing    = %-9s # single | double\n", tomlString(s.spacing))
	fmt.Fprintf(&b, "max_width  = %d\n", s.maxWidth)

	b.WriteString("\n[keys]\n")
	width := 0
	for _, a := range actions {
		width = max(width, len(a.name))
	}
	for _, a := range actions {
		ks := make([]string, len(s.keys[a.name]))
		for i, k := range s.keys[a.name] {
			ks[i] = tomlString(configKey(k))
		}
		fmt.Fprintf(&b, "%-*s = [%s]\n", width, a.name, strings.Join(ks, ", "))
	}

	for _, name := range slices.Sorted(maps.Keys(s.themes)) {
		t := s.themes[name]
		key := "themes." + tomlKey(name)
		fmt.Fprintf(&b, "\n[%s]\n", key)
		if t.Extends != "" {
			fmt.Fprintf(&b, "extends = %s\n", tomlString(t.Extends))
		}
		for _, sec := range []struct {
			name  string
			roles map[string]string
		}{{"dark", t.Dark}, {"light", t.Light}} {
			if len(sec.roles) == 0 {
				continue
			}
			fmt.Fprintf(&b, "\n[%s.%s]\n", key, sec.name)
			for _, r := range paletteRoles {
				if v, ok := sec.roles[r.name]; ok {
					fmt.Fprintf(&b, "%s = %s\n", r.name, tomlString(v))
				}
			}
		}
	}
	return b.String()
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(s string) string {
	if bareKey.MatchString(s) {
		return s
	}
	return tomlString(s)
}

func prefixAll(prefix string, ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = prefix + s
	}
	return out
}
