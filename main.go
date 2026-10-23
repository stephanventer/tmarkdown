// tmarkdown shows a rendered markdown file in the terminal.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fsnotify/fsnotify"
	"golang.org/x/term"
)

const usage = `tmarkdown - read markdown in the terminal

Usage:
  tmarkdown [flags] FILE
  tmarkdown [flags] -          read from stdin
  cat FILE | tmarkdown         read from stdin

Flags:
  --dark           force the dark theme
  --light          force the light theme
  --plain          print the rendered text and exit (also used when output is piped)
  --double         start with double line spacing (press s in the viewer to toggle)
  --single         start with single line spacing
  --config PATH    read settings from PATH instead of the default config file
  --print-config   print the effective settings as TOML and exit
  -v               show the version
  -h               show this help

Press ? inside the viewer for keys.
`

// version is set at build time with -ldflags "-X main.version=1.2.3". Without
// it, the module version recorded by go install is used.
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

const largeFile = 5 << 20

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tmarkdown:", err)
		os.Exit(1)
	}
}

// flags holds the command line. Empty strings mean "not given".
type flags struct {
	appearance, spacing, config, path string
	plain, printConfig, help, version bool
}

func parseFlags(args []string) (flags, error) {
	var f flags
	var bad error
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--dark", a == "--light":
			f.appearance = a[2:]
		case a == "--double", a == "--single":
			f.spacing = a[2:]
		case a == "--plain":
			f.plain = true
		case a == "--print-config":
			f.printConfig = true
		case a == "--config":
			if i+1 >= len(args) {
				return f, errors.New("--config needs a file path")
			}
			i++
			f.config = args[i]
		case strings.HasPrefix(a, "--config="):
			f.config = strings.TrimPrefix(a, "--config=")
		case a == "-v", a == "--version":
			f.version = true
		case a == "-h", a == "--help":
			f.help = true
		case strings.HasPrefix(a, "--"):
			if bad == nil {
				bad = fmt.Errorf("unknown flag %s (try -h)", a)
			}
		default:
			if f.path != "" && bad == nil {
				bad = errors.New("only one file at a time")
			}
			f.path = a
		}
	}
	if f.help || f.version {
		return f, nil
	}
	return f, bad
}

func run(args []string) error {
	f, err := parseFlags(args)
	if err != nil {
		return err
	}
	cfgPath, explicit := findConfig(f.config, osConfigEnv(runtime.GOOS))
	if f.version {
		fmt.Println("tmarkdown", versionString())
		return nil
	}
	if f.help {
		fmt.Print(usage)
		fmt.Print(configHelp(cfgPath, explicit))
		return nil
	}

	cfg, err := loadSettings(cfgPath, explicit)
	if err != nil {
		return err
	}
	if f.appearance != "" {
		cfg.appearance = f.appearance
	}
	if f.spacing != "" {
		cfg.spacing = f.spacing
	}
	th, err := cfg.resolveTheme()
	if err != nil {
		return err
	}
	if f.printConfig {
		fmt.Print(cfg.toTOML())
		return nil
	}
	for _, w := range cfg.warnings {
		fmt.Fprintln(os.Stderr, "tmarkdown: warning:", w)
	}

	path := f.path
	stdinPiped := !term.IsTerminal(int(os.Stdin.Fd()))
	if path == "" && !stdinPiped {
		fmt.Print(usage)
		return nil
	}

	var src []byte
	name := path
	if path == "" || path == "-" {
		path, name = "", "stdin"
		src, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("cannot read stdin: %w", err)
		}
	} else {
		src, err = os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot open %s: %w", path, unwrapPathErr(err))
		}
		name = filepath.Base(path)
	}
	// PowerShell pipes and some editors add a UTF-8 byte order mark.
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	if looksBinary(src) {
		return fmt.Errorf("%s looks binary", name)
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	style := cfg.appearance
	if style == "auto" {
		style = "dark"
		if isTTY && !detectDark() {
			style = "light"
		}
	}
	pal := th.palette(style)

	if f.plain || !isTTY {
		d, err := buildDoc(src, 80, 0, pal, true)
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(d.lines, "\n"))
		return nil
	}

	var flash string
	if n := len(cfg.warnings); n > 0 {
		flash = "config: " + cfg.warnings[0]
		if n > 1 {
			flash += fmt.Sprintf(" (+%d more, printed on exit)", n-1)
		}
	}
	m := newModel(path, name, src, viewOpts{
		pal:      pal,
		keys:     newKeyMap(cfg.keys),
		spaced:   cfg.spacing == "double",
		maxWidth: cfg.maxWidth,
		flash:    flash,
	})
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	if path == "" {
		opts = append(opts, tea.WithInputTTY()) // stdin holds the document
	}
	p := tea.NewProgram(m, opts...)
	if path != "" {
		stop, err := watch(path, func() { p.Send(fileChangedMsg{}) })
		if err == nil {
			defer stop()
		}
	}
	_, err = p.Run()
	return err
}

// configHelp is the part of --help that says where the config file is.
func configHelp(path string, explicit bool) string {
	switch {
	case path == "":
		return "\nConfig: no config location (home directory unknown)\n"
	case explicit:
		return "\nConfig: " + path + "\n"
	}
	if _, err := os.Stat(path); err == nil {
		return "\nConfig: " + path + "\n"
	}
	return "\nConfig: " + path + " (not found; defaults in use)\n"
}

func unwrapPathErr(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

func looksBinary(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// detectDark asks the terminal for its background colour. Some terminals
// never answer, so give up after 200 ms and assume dark.
func detectDark() bool {
	ch := make(chan bool, 1)
	go func() { ch <- lipgloss.HasDarkBackground() }()
	select {
	case dark := <-ch:
		return dark
	case <-time.After(200 * time.Millisecond):
		return true
	}
}

// watch calls onChange when path is written, replaced or removed. It
// watches the directory because many editors save by renaming a temp file.
func watch(path string, onChange func()) (func(), error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(filepath.Dir(abs)); err != nil {
		w.Close()
		return nil, err
	}
	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if strings.EqualFold(filepath.Clean(ev.Name), abs) && !ev.Has(fsnotify.Chmod) {
					onChange()
				}
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return func() { w.Close() }, nil
}
