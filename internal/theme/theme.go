// Package theme holds encore's palette, text styles and glyph sets. The look is
// deliberately small: terminal-default foreground, one dim tone, one faint tone
// for rules, one accent, and two subtle row backgrounds. See docs/design.md,
// "Visual language".
package theme

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette is the set of colour tokens. A nil colour means "terminal default".
type Palette struct {
	Name   string
	Dark   bool
	FG     color.Color // primary text; nil = terminal default
	Dim    color.Color // secondary text
	Faint  color.Color // rules, inactive glyphs, scrollbar track
	Accent color.Color // playing, focus marker, progress, active tab, current lyric
	Sel    color.Color // selected row background
	Hover  color.Color // hovered row background
	Warn   color.Color // errors and warnings
	Shadow color.Color // overlay surface background
}

var palettes = map[string]Palette{
	"encore": {
		Name: "encore", Dark: true,
		Dim:    lipgloss.Color("#8B8B93"),
		Faint:  lipgloss.Color("#4A4A52"),
		Accent: lipgloss.Color("#FA586A"),
		Sel:    lipgloss.Color("#2A2A30"),
		Hover:  lipgloss.Color("#202024"),
		Warn:   lipgloss.Color("#E5B567"),
		Shadow: lipgloss.Color("#18181B"),
	},
	"encore-light": {
		Name: "encore-light", Dark: false,
		Dim:    lipgloss.Color("#6E6E76"),
		Faint:  lipgloss.Color("#C9C9CF"),
		Accent: lipgloss.Color("#E0263D"),
		Sel:    lipgloss.Color("#E9E9EE"),
		Hover:  lipgloss.Color("#F2F2F5"),
		Warn:   lipgloss.Color("#A86A00"),
		Shadow: lipgloss.Color("#FAFAFC"),
	},
	"mono": {
		Name: "mono", Dark: true,
		Dim:    lipgloss.Color("#8B8B93"),
		Faint:  lipgloss.Color("#4A4A52"),
		Accent: lipgloss.Color("#F2F2F2"),
		Sel:    lipgloss.Color("#2A2A30"),
		Hover:  lipgloss.Color("#202024"),
		Warn:   lipgloss.Color("#E5B567"),
		Shadow: lipgloss.Color("#18181B"),
	},
}

// Names lists the built-in palettes.
func Names() []string { return []string{"encore", "encore-light", "mono"} }

// Lookup returns a built-in palette by name. "auto" and unknown names resolve
// to "encore" or "encore-light" depending on dark.
func Lookup(name string, dark bool) Palette {
	if p, ok := palettes[name]; ok {
		return p
	}
	if dark {
		return palettes["encore"]
	}
	return palettes["encore-light"]
}

// WithOverrides applies hex overrides keyed by token name (fg, dim, faint,
// accent, sel, hover, warn, shadow). Unknown keys are returned.
func (p Palette) WithOverrides(o map[string]string) (Palette, []string) {
	var unknown []string
	for k, v := range o {
		c := lipgloss.Color(v)
		switch strings.ToLower(k) {
		case "fg":
			p.FG = c
		case "dim":
			p.Dim = c
		case "faint":
			p.Faint = c
		case "accent":
			p.Accent = c
		case "sel":
			p.Sel = c
		case "hover":
			p.Hover = c
		case "warn":
			p.Warn = c
		case "shadow":
			p.Shadow = c
		default:
			unknown = append(unknown, k)
		}
	}
	return p, unknown
}

// Theme is a palette turned into ready-to-use styles plus a glyph set.
type Theme struct {
	P Palette
	G Glyphs

	Text     lipgloss.Style // primary text
	Bold     lipgloss.Style // primary, bold (titles)
	Dim      lipgloss.Style
	Faint    lipgloss.Style
	Accent   lipgloss.Style
	AccentB  lipgloss.Style // accent, bold
	Warn     lipgloss.Style
	Selected lipgloss.Style // background for the selected row in the focused pane
	Hovered  lipgloss.Style // background for the hovered row
	Surface  lipgloss.Style // overlay body
	Border   lipgloss.Style // overlay border (rounded, faint)
	Match    lipgloss.Style // matched characters in search results
}

// New builds a Theme. noColor strips all colour (NO_COLOR).
func New(p Palette, g Glyphs, noColor bool) *Theme {
	t := &Theme{P: p, G: g}
	fg := func(c color.Color) lipgloss.Style {
		s := lipgloss.NewStyle()
		if c != nil && !noColor {
			s = s.Foreground(c)
		}
		return s
	}
	bg := func(c color.Color) lipgloss.Style {
		s := lipgloss.NewStyle()
		if c != nil && !noColor {
			s = s.Background(c)
		}
		return s
	}
	t.Text = fg(p.FG)
	t.Bold = fg(p.FG).Bold(true)
	t.Dim = fg(p.Dim)
	t.Faint = fg(p.Faint)
	t.Accent = fg(p.Accent)
	t.AccentB = fg(p.Accent).Bold(true)
	t.Warn = fg(p.Warn)
	t.Selected = bg(p.Sel)
	t.Hovered = bg(p.Hover)
	t.Surface = bg(p.Shadow)
	t.Border = fg(p.Faint)
	t.Match = fg(p.Accent).Bold(true)
	if noColor {
		t.Selected = lipgloss.NewStyle().Reverse(true)
		t.Hovered = lipgloss.NewStyle()
		t.Accent = lipgloss.NewStyle().Bold(true)
		t.Match = lipgloss.NewStyle().Underline(true)
	}
	return t
}
