// tmarkdown shows a rendered markdown file in the terminal.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
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
  --dark     force the dark theme
  --light    force the light theme
  --plain    print the rendered text and exit (also used when output is piped)
  --double   start with double line spacing (press s in the viewer to toggle)
  --preview  also open a live rendered preview in the browser (press p in the viewer)
  --preview-only
             serve the browser preview without the viewer; Ctrl+C to stop
  -v         show the version
  -h         show this help

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

func run(args []string) error {
	style, plain, path, spaced := "", false, "", false
	preview, previewOnly := false, false
	for _, a := range args {
		switch a {
		case "--dark":
			style = "dark"
		case "--light":
			style = "light"
		case "--plain":
			plain = true
		case "--double":
			spaced = true
		case "--preview":
			preview = true
		case "--preview-only":
			previewOnly = true
		case "-v", "--version":
			fmt.Println("tmarkdown", versionString())
			return nil
		case "-h", "--help":
			fmt.Print(usage)
			return nil
		default:
			if strings.HasPrefix(a, "--") {
				return fmt.Errorf("unknown flag %s (try -h)", a)
			}
			if path != "" {
				return errors.New("only one file at a time")
			}
			path = a
		}
	}

	stdinPiped := !term.IsTerminal(int(os.Stdin.Fd()))
	if path == "" && !stdinPiped {
		fmt.Print(usage)
		return nil
	}

	var src []byte
	var err error
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

	if (preview || previewOnly) && path == "" {
		return errors.New("--preview needs a file, not stdin")
	}

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if style == "" {
		style = "dark"
		if isTTY && !detectDark() {
			style = "light"
		}
	}

	if previewOnly {
		return runPreviewOnly(path, src, style)
	}

	if plain || !isTTY {
		d, err := buildDoc(src, 80, 0, style, true)
		if err != nil {
			return err
		}
		fmt.Println(strings.Join(d.lines, "\n"))
		return nil
	}

	m := newModel(path, name, src, style, spaced)
	if preview {
		pv, err := startPreview(src, path, style)
		if err != nil {
			return err
		}
		m.preview = pv
		openURL(pv.url) //nolint:errcheck
	}
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
	final, err := p.Run()
	if fm, ok := final.(model); ok && fm.preview != nil {
		fm.preview.Close()
	} else {
		m.preview.Close()
	}
	return err
}

// runPreviewOnly serves the browser preview and reloads it when the file
// changes, until interrupted.
func runPreviewOnly(path string, src []byte, style string) error {
	pv, err := startPreview(src, path, style)
	if err != nil {
		return err
	}
	defer pv.Close()
	stop, err := watch(path, func() {
		if b, err := os.ReadFile(path); err == nil {
			pv.SetSource(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
		}
	})
	if err == nil {
		defer stop()
	}
	fmt.Println("preview at", pv.url, "(Ctrl+C to stop)")
	if err := openURL(pv.url); err != nil {
		fmt.Fprintln(os.Stderr, "tmarkdown: open failed:", err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return nil
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
