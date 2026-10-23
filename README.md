# tmarkdown

A minimal markdown reader for the terminal. It shows the rendered,
human-readable version of a file in a full-screen viewer, with a green and
grey "Matrix" theme.

## Install

tmarkdown is installed with Go (version 1.27 or newer). Install Go first,
then tmarkdown.

### 1. Install Go

**macOS**

```
brew install go
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.zshrc
source ~/.zshrc
```

**Linux**

```
sudo apt install golang-go      # Debian, Ubuntu
sudo dnf install golang         # Fedora
sudo pacman -S go               # Arch
```

**Windows**

```
winget install GoLang.Go
```

Open a new terminal afterwards and check it worked:

```
go version
```

### 2. Install tmarkdown

```
go install github.com/stephanventer/tmarkdown@latest
```

Check it with `tmarkdown --version` or `tmarkdown --help`.

### Check the version

```
tmarkdown --version
```

This prints something like `tmarkdown v0.1.2`. To see the newest release,
look at the [releases page](https://github.com/stephanventer/tmarkdown/releases).

### Update

Run the install command again to get the latest release:

```
go install github.com/stephanventer/tmarkdown@latest
```

Just after a release the version may not change; install it directly with `@v0.1.2` instead of `@latest`.

## Use

```
tmarkdown README.md          open a file
cat notes.md | tmarkdown     read from stdin
tmarkdown --plain file.md    print the rendered text and exit
```

When output is piped, tmarkdown prints instead of opening the viewer.
Flags: `--dark`, `--light` (the theme is auto-detected otherwise), `--plain`,
`--double` / `--single` (starting line spacing), `--config PATH` and
`--print-config` (see [Configuration](#configuration)).

## Features

- Headings set apart by weight and underlines, not `#` marks
- Tables, task lists, blockquotes and syntax-highlighted code blocks
- Word wrap at up to 100 columns, centred
- Search as you type, with every match highlighted
- Heading outline you can filter and jump from
- Links stay clean in the text; select one to see its URL and open it
- Reloads when the file changes on disk, keeping your place
- Dark and light themes, auto-detected
- Reads from a file or stdin; prints plain text when piped
- Optional double line spacing
- Config file for themes, key bindings and defaults

## Keys

**Scrolling**

| Action | Keys |
|---|---|
| Line up / down | <kbd>↑</kbd> <kbd>↓</kbd> or <kbd>k</kbd> <kbd>j</kbd> |
| 10 lines up / down | <kbd>Shift</kbd>+<kbd>↑</kbd> <kbd>↓</kbd> or <kbd>K</kbd> <kbd>J</kbd> |
| Half page up / down | <kbd>Ctrl</kbd>+<kbd>u</kbd> <kbd>Ctrl</kbd>+<kbd>d</kbd> or <kbd>u</kbd> <kbd>d</kbd> |
| Page up / down | <kbd>PgUp</kbd> <kbd>PgDn</kbd> or <kbd>b</kbd> <kbd>Space</kbd> (also <kbd>f</kbd>) |
| Top / bottom | <kbd>g</kbd> <kbd>G</kbd> or <kbd>Home</kbd> <kbd>End</kbd> |
| Scroll | mouse wheel |
| Scroll fast | <kbd>Alt</kbd>+wheel |

**Finding things**

| Action | Keys |
|---|---|
| Search (as you type) | <kbd>/</kbd> |
| Next / previous match | <kbd>n</kbd> <kbd>N</kbd> |
| Heading outline | <kbd>o</kbd> |
| Select next / previous link | <kbd>Tab</kbd> <kbd>Shift</kbd>+<kbd>Tab</kbd> |
| Open selected link | <kbd>Enter</kbd> |

**General**

| Action | Keys |
|---|---|
| Back out one level | <kbd>Esc</kbd> |
| Reload file | <kbd>r</kbd> |
| Double / single spacing | <kbd>s</kbd> |
| Help | <kbd>?</kbd> or <kbd>h</kbd> |
| Quit | <kbd>q</kbd> or <kbd>Ctrl</kbd>+<kbd>c</kbd> |

Only `http`, `https` and `mailto` links are opened.

These are the default keys. They can be changed in the config file; the
help screen (<kbd>?</kbd>) always shows the keys you actually have.

## Line spacing

Press <kbd>s</kbd> in the viewer for double spacing: a blank row goes
between neighbouring lines, while headings stay joined to their rules.
Press it again for single spacing, or start double-spaced with `--double`.

## Configuration

tmarkdown reads an optional TOML config file. The first of these that is
found is used:

1. `--config PATH`
2. `$TMARKDOWN_CONFIG`
3. `$XDG_CONFIG_HOME/tmarkdown/config.toml`
4. `~/.config/tmarkdown/config.toml` (on every OS, macOS included)
5. `%APPDATA%\tmarkdown\config.toml` (Windows only)

Without a config file nothing changes. A path given with `--config` or
`$TMARKDOWN_CONFIG` must exist. `tmarkdown --help` shows which file is used.

Settings apply in this order, later ones winning: built-in defaults, the
config file, then flags. For example `--light` beats `appearance = "dark"`.

To start a config file from the current settings:

```
mkdir -p ~/.config/tmarkdown
tmarkdown --print-config > ~/.config/tmarkdown/config.toml
```

### Example

```toml
# ~/.config/tmarkdown/config.toml

theme      = "matrix"   # built-in name, a [themes.NAME] table, or themes/NAME.toml
appearance = "auto"     # auto | dark | light   (same as --dark / --light)
spacing    = "single"   # single | double       (same as --single / --double)
max_width  = 100        # widest the text gets in the viewer

[keys]
scroll_up      = ["up", "k"]
scroll_down    = ["down", "j"]
scroll_up_10   = ["shift+up", "K"]
scroll_down_10 = ["shift+down", "J"]
half_page_up   = ["ctrl+u", "u"]
half_page_down = ["ctrl+d", "d"]
page_up        = ["pgup", "b"]
page_down      = ["pgdown", "space", "f"]
top            = ["g", "home"]
bottom         = ["G", "end"]
search         = ["/"]
next_match     = ["n"]
prev_match     = ["N"]
outline        = ["o"]
next_link      = ["tab"]
prev_link      = ["shift+tab"]
open_link      = ["enter"]
back           = ["esc"]
reload         = ["r"]
toggle_spacing = ["s"]
help           = ["?", "h"]
quit           = ["q", "ctrl+c"]
```

### Keys

- Leave an action out to keep its default keys; set it to `[]` to unbind it.
  A single key can be written without brackets: `quit = "x"`.
- Key names are as Bubble Tea reports them: letters as typed (`G` is
  Shift+g), `ctrl+x`, `alt+x`, `shift+tab`, `up`, `pgdown`, `home`, `enter`,
  `esc`, `space`, `f1` and so on.
- Unknown actions, unknown key names and a key bound to two actions are
  reported as warnings (on stderr, and in the status bar at startup) rather
  than stopping the program.
- <kbd>Ctrl</kbd>+<kbd>c</kbd> always quits. Typing in search and the outline
  filter is not remappable.

### Themes

A theme sets colours for these roles, separately for dark and light
backgrounds:

| Role | Used for |
|---|---|
| `text` | body text |
| `muted` | bullets, quotes, code comments, help hints |
| `faint` | thin rules, table borders, search match background |
| `accent_strong` | H1 banner, H2, code keywords, inline code, status bar text |
| `accent` | H3, links, H2 rule, selected link |
| `accent_soft` | H4 and below, strings in code |
| `emphasis` | bold text, function names, numbers, keycap text |
| `code_bg` | code background |
| `status_bg` | status bar background |
| `keycap_bg` | keycaps on the help screen |
| `on_accent` | text drawn on an accent background |

Colours are written `"#RRGGBB"`. Put a theme in
`~/.config/tmarkdown/themes/NAME.toml` (the `themes` folder next to your
config file) and select it with `theme = "NAME"`:

```toml
# ~/.config/tmarkdown/themes/dracula.toml
extends = "matrix"   # optional; roles you leave out come from this theme

[dark]
text          = "#F8F8F2"
accent_strong = "#FF79C6"
accent        = "#BD93F9"
code_bg       = "#21222C"

[light]
# left out: light mode uses the extended theme's light colours
```

A theme can also live in `config.toml` itself, as `[themes.NAME]`,
`[themes.NAME.dark]` and `[themes.NAME.light]` tables. Without `extends`,
missing roles come from Matrix, the built-in default.

Theme packs can be dropped into a folder under `themes/`, for example a git
clone at `~/.config/tmarkdown/themes/catppuccin/`. tmarkdown also looks one
folder down for `NAME.toml`, skipping hidden folders such as `.git`. A file
directly in `themes/` wins; if two folders have the same name, that's an
error.

Built with [glamour](https://github.com/charmbracelet/glamour) and
[Bubble Tea](https://github.com/charmbracelet/bubbletea).
