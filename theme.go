package main

import (
	"fmt"
	"strconv"

	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// palette is the single source of colour. Roles are named for what they
// are used for, so a theme can recolour them freely.
type palette struct {
	text         string // body text
	muted        string // bullets, quotes, comments, rules
	faint        string // thin rules, table borders, search matches
	accentStrong string // H1 banner, H2, keywords, inline code, status bar
	accent       string // H3, links, H2 rule, selected link
	accentSoft   string // H4+, strings in code
	emphasis     string // bold text, functions, numbers
	codeBg       string // background for inline code and code blocks
	statusBg     string // status bar background
	keycapBg     string // keycaps on the help screen
	onAccent     string // text on an accent background
}

// paletteRoles lists the palette roles in the order they are printed, with
// the names used in theme files.
type paletteRole struct {
	name string
	get  func(*palette) *string
}

var paletteRoles = []paletteRole{
	{"text", func(p *palette) *string { return &p.text }},
	{"muted", func(p *palette) *string { return &p.muted }},
	{"faint", func(p *palette) *string { return &p.faint }},
	{"accent_strong", func(p *palette) *string { return &p.accentStrong }},
	{"accent", func(p *palette) *string { return &p.accent }},
	{"accent_soft", func(p *palette) *string { return &p.accentSoft }},
	{"emphasis", func(p *palette) *string { return &p.emphasis }},
	{"code_bg", func(p *palette) *string { return &p.codeBg }},
	{"status_bg", func(p *palette) *string { return &p.statusBg }},
	{"keycap_bg", func(p *palette) *string { return &p.keycapBg }},
	{"on_accent", func(p *palette) *string { return &p.onAccent }},
}

// Matrix theme: greens for structure and emphasis, greys for body text and
// secondary detail.
var matrixDark = palette{
	text:         "#B4C4B4",
	muted:        "#6B7B6B",
	faint:        "#344034",
	accentStrong: "#00FF41",
	accent:       "#00C832",
	accentSoft:   "#5FAF6F",
	emphasis:     "#D0FFD0",
	codeBg:       "#0C170C",
	statusBg:     "#0F2410",
	keycapBg:     "#1C2E1C",
	onAccent:     "#000000",
}

// Matrix on a light background: same roles, darker inks.
var matrixLight = palette{
	text:         "#253525",
	muted:        "#667566",
	faint:        "#B8C4B8",
	accentStrong: "#00801F",
	accent:       "#00661A",
	accentSoft:   "#3F7F4F",
	emphasis:     "#0B3D14",
	codeBg:       "#E6F2E6",
	statusBg:     "#D4EBD4",
	keycapBg:     "#D0E2D0",
	onAccent:     "#FFFFFF",
}

// theme is a dark and a light palette.
type theme struct{ dark, light palette }

const defaultTheme = "matrix"

var builtinThemes = map[string]theme{
	"matrix": {matrixDark, matrixLight},
}

func (t theme) palette(style string) palette {
	if style == "light" {
		return t.light
	}
	return t.dark
}

// fgSGR and bgSGR turn "#RRGGBB" into 24-bit colour escape parameters.
func rgb(hex string) (int, int, int) {
	v, _ := strconv.ParseUint(hex[1:], 16, 32)
	return int(v >> 16 & 0xff), int(v >> 8 & 0xff), int(v & 0xff)
}

func fgSGR(hex string) string {
	r, g, b := rgb(hex)
	return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
}

func bgSGR(hex string) string {
	r, g, b := rgb(hex)
	return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
}

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }

// styleConfig starts from glamour's dark layout (margins, bullets, indents)
// and replaces every colour with the palette.
func styleConfig(p palette) gansi.StyleConfig {
	c := styles.DarkStyleConfig

	c.Document.Color = sp(p.text)
	c.BlockQuote.Color = sp(p.muted)
	c.BlockQuote.Italic = bp(true)
	c.Heading.Color = sp(p.accentStrong) // replaced by headings.go, kept for safety
	c.H1.Color, c.H1.BackgroundColor = sp(p.onAccent), sp(p.accentStrong)
	c.H6.Color = sp(p.accentSoft)
	c.Strong.Color = sp(p.emphasis)
	c.Emph.Color = sp(p.text)
	c.Strikethrough.Color = sp(p.muted)
	c.HorizontalRule.Color = sp(p.faint)
	c.HorizontalRule.Format = "\n" + "────────────────────────────────" + "\n"
	c.Item.Color = sp(p.text)
	c.Enumeration.Color = sp(p.muted)
	c.Task.Color = sp(p.accent)

	c.Link.Color = sp(p.accent) // bare URLs; regular link URLs are hidden
	c.Link.Underline = bp(true)
	c.Link.Format = "{{tmdHref .text}}"
	c.LinkText.Color = sp(p.accent)
	c.LinkText.Bold = bp(false)
	c.LinkText.Underline = bp(true)
	c.Image.Color = sp(p.accentSoft)
	c.ImageText.Color = sp(p.muted)

	c.Code.Color, c.Code.BackgroundColor = sp(p.accentStrong), sp(p.codeBg)
	c.CodeBlock.Color = sp(p.text)
	c.CodeBlock.Chroma = &gansi.Chroma{
		Text:                gansi.StylePrimitive{Color: sp(p.text)},
		Error:               gansi.StylePrimitive{Color: sp(p.emphasis), BackgroundColor: sp(p.faint)},
		Comment:             gansi.StylePrimitive{Color: sp(p.muted), Italic: bp(true)},
		CommentPreproc:      gansi.StylePrimitive{Color: sp(p.muted)},
		Keyword:             gansi.StylePrimitive{Color: sp(p.accentStrong), Bold: bp(true)},
		KeywordReserved:     gansi.StylePrimitive{Color: sp(p.accentStrong), Bold: bp(true)},
		KeywordNamespace:    gansi.StylePrimitive{Color: sp(p.accent)},
		KeywordType:         gansi.StylePrimitive{Color: sp(p.accent)},
		Operator:            gansi.StylePrimitive{Color: sp(p.text)},
		Punctuation:         gansi.StylePrimitive{Color: sp(p.muted)},
		Name:                gansi.StylePrimitive{Color: sp(p.text)},
		NameBuiltin:         gansi.StylePrimitive{Color: sp(p.accent)},
		NameTag:             gansi.StylePrimitive{Color: sp(p.accent)},
		NameAttribute:       gansi.StylePrimitive{Color: sp(p.accentSoft)},
		NameClass:           gansi.StylePrimitive{Color: sp(p.emphasis), Bold: bp(true)},
		NameDecorator:       gansi.StylePrimitive{Color: sp(p.accentSoft)},
		NameFunction:        gansi.StylePrimitive{Color: sp(p.emphasis)},
		LiteralNumber:       gansi.StylePrimitive{Color: sp(p.emphasis)},
		LiteralString:       gansi.StylePrimitive{Color: sp(p.accentSoft)},
		LiteralStringEscape: gansi.StylePrimitive{Color: sp(p.emphasis)},
		GenericDeleted:      gansi.StylePrimitive{Color: sp(p.muted), CrossedOut: bp(true)},
		GenericEmph:         gansi.StylePrimitive{Italic: bp(true)},
		GenericInserted:     gansi.StylePrimitive{Color: sp(p.accentStrong)},
		GenericStrong:       gansi.StylePrimitive{Bold: bp(true)},
		GenericSubheading:   gansi.StylePrimitive{Color: sp(p.muted)},
		Background:          gansi.StylePrimitive{BackgroundColor: sp(p.codeBg)},
	}
	c.Table.Color = sp(p.text)
	c.DefinitionDescription.Color = sp(p.text)
	return c
}
