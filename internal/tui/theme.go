package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Theme names one of chippy's color palettes. Add a new value here +
// a paletteFor case to ship a new theme.
type Theme string

const (
	// ThemeDefault is chippy's shipped default — Catppuccin Mocha.
	ThemeDefault Theme = "default"
	// Catppuccin flavors (https://catppuccin.com). Mocha is the default;
	// "catppuccin" is an alias for it.
	ThemeCatppuccin Theme = "catppuccin"
	ThemeMocha      Theme = "mocha"
	ThemeMacchiato  Theme = "macchiato"
	ThemeFrappe     Theme = "frappe"
	ThemeLatte      Theme = "latte"
	// ThemeNeon is the original high-saturation palette chippy shipped
	// before Catppuccin became the default.
	ThemeNeon Theme = "neon"
	// ThemeMono drops all color. Drives the NO_COLOR fallback and the
	// `--theme mono` command line. Bold / italic / reverse remain.
	ThemeMono Theme = "mono"
	// ThemeProtan replaces red-green pairs with blue-yellow pairs so
	// protanopia / deuteranopia (~5% of users) can still distinguish
	// the four memory-watchpoint sigils + cursor highlights.
	ThemeProtan Theme = "protan"
	// ThemeTritan replaces blue-yellow pairs with magenta-cyan pairs
	// for the ~0.01% tritanopia case + a small subset of acquired
	// color-vision-deficiencies.
	ThemeTritan Theme = "tritan"
)

// AvailableThemes returns the names exposed to the user (via the
// `--theme` flag and the `:theme` command). Order matters: it's the
// completion ordering too.
func AvailableThemes() []string {
	return []string{
		string(ThemeDefault),
		string(ThemeMocha),
		string(ThemeMacchiato),
		string(ThemeFrappe),
		string(ThemeLatte),
		string(ThemeCatppuccin),
		string(ThemeNeon),
		string(ThemeProtan),
		string(ThemeTritan),
		string(ThemeMono),
	}
}

// palette holds one set of color choices. applyTheme writes these into
// the package-level style vars so every renderer picks them up on the
// next View() call.
type palette struct {
	title, reg, flagOn, flagOff,
	help, label, dimAddr,
	curLineBg, curLineFg,
	statusBg, statusFg,
	memBPRead, memBPWrite, memBPRW,
	memEditBg, memEditFg lipgloss.Color
	// useColor controls whether emphasis is via color (+bold) or, for
	// mono, bold + reverse only.
	useColor bool
}

// catppuccin holds the flavor colors chippy's palette maps onto. Only
// the slots chippy renders are listed. Values are the official
// Catppuccin hex codes (https://github.com/catppuccin/catppuccin).
type catppuccin struct {
	mauve, blue, green, red, pink, yellow,
	overlay0, overlay1, surface0, surface1, crust lipgloss.Color
}

var (
	mocha = catppuccin{
		mauve: "#cba6f7", blue: "#89b4fa", green: "#a6e3a1", red: "#f38ba8",
		pink: "#f5c2e7", yellow: "#f9e2af", overlay0: "#6c7086", overlay1: "#7f849c",
		surface0: "#313244", surface1: "#45475a", crust: "#11111b",
	}
	macchiato = catppuccin{
		mauve: "#c6a0f6", blue: "#8aadf4", green: "#a6da95", red: "#ed8796",
		pink: "#f5bde6", yellow: "#eed49f", overlay0: "#6e738d", overlay1: "#8087a2",
		surface0: "#363a4f", surface1: "#494d64", crust: "#181926",
	}
	frappe = catppuccin{
		mauve: "#ca9ee6", blue: "#8caaee", green: "#a6d189", red: "#e78284",
		pink: "#f4b8e4", yellow: "#e5c890", overlay0: "#737994", overlay1: "#838ba7",
		surface0: "#414559", surface1: "#51576d", crust: "#232634",
	}
	latte = catppuccin{
		mauve: "#8839ef", blue: "#1e66f5", green: "#40a02b", red: "#d20f39",
		pink: "#ea76cb", yellow: "#df8e1d", overlay0: "#9ca0b0", overlay1: "#8c8fa1",
		surface0: "#ccd0da", surface1: "#bcc0cc", crust: "#dce0e8",
	}
)

// catppuccinPalette maps a flavor onto chippy's semantic slots: mauve
// headings, blue registers, green "on" flags, a mauve status bar with
// crust text, and the standard watchpoint hues (blue 👁 / red ✏ / pink 🔁).
func catppuccinPalette(c catppuccin) palette {
	return palette{
		useColor:   true,
		title:      c.mauve,
		reg:        c.blue,
		flagOn:     c.green,
		flagOff:    c.overlay0,
		help:       c.overlay1,
		label:      c.pink,
		dimAddr:    c.overlay1,
		curLineBg:  c.surface0,
		curLineFg:  c.yellow,
		statusBg:   c.mauve,
		statusFg:   c.crust,
		memBPRead:  c.blue,
		memBPWrite: c.red,
		memBPRW:    c.pink,
		memEditBg:  c.surface1,
		memEditFg:  c.yellow,
	}
}

func paletteFor(t Theme) palette {
	switch t {
	case ThemeMono:
		return palette{useColor: false}
	case ThemeMacchiato:
		return catppuccinPalette(macchiato)
	case ThemeFrappe:
		return catppuccinPalette(frappe)
	case ThemeLatte:
		return catppuccinPalette(latte)
	case ThemeNeon:
		// The original high-saturation palette (pre-Catppuccin default).
		return palette{
			useColor:   true,
			title:      "213",
			reg:        "39",
			flagOn:     "46",
			flagOff:    "240",
			help:       "245",
			label:      "207",
			dimAddr:    "244",
			curLineBg:  "236",
			curLineFg:  "226",
			statusBg:   "57",
			statusFg:   "231",
			memBPRead:  "33",
			memBPWrite: "196",
			memBPRW:    "213",
			memEditBg:  "88",
			memEditFg:  "226",
		}
	case ThemeProtan:
		// Red→blue, green→yellow. Distinguishable under common
		// red-green deficiencies.
		return palette{
			useColor:   true,
			title:      "33",  // blue
			reg:        "39",  // cyan-blue
			flagOn:     "226", // yellow (was green)
			flagOff:    "240",
			help:       "245",
			label:      "33",
			dimAddr:    "244",
			curLineBg:  "236",
			curLineFg:  "226",
			statusBg:   "24", // dark teal
			statusFg:   "231",
			memBPRead:  "33",  // 👁 blue
			memBPWrite: "226", // ✏ yellow (was red)
			memBPRW:    "111", // 🔁 light blue
			memEditBg:  "24",
			memEditFg:  "226",
		}
	case ThemeTritan:
		return palette{
			useColor:   true,
			title:      "201", // magenta
			reg:        "51",  // cyan
			flagOn:     "201",
			flagOff:    "240",
			help:       "245",
			label:      "201",
			dimAddr:    "244",
			curLineBg:  "236",
			curLineFg:  "231",
			statusBg:   "53", // dark magenta
			statusFg:   "231",
			memBPRead:  "51",
			memBPWrite: "201",
			memBPRW:    "207",
			memEditBg:  "53",
			memEditFg:  "231",
		}
	default: // ThemeDefault / ThemeMocha / ThemeCatppuccin — Catppuccin Mocha
		return catppuccinPalette(mocha)
	}
}

// applyTheme reassigns the package-level styles to match the chosen
// theme. Safe to call from the Bubble Tea Update goroutine; renderers
// pick up the change on the next View().
func applyTheme(t Theme) {
	p := paletteFor(t)
	if !p.useColor {
		// Mono — drop color, keep emphasis hints (bold/italic/reverse)
		// because those work over NO_COLOR terminals too.
		titleStyle = lipgloss.NewStyle().Bold(true)
		panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
		regStyle = lipgloss.NewStyle()
		flagOn = lipgloss.NewStyle().Bold(true)
		flagOff = lipgloss.NewStyle().Faint(true)
		curLine = lipgloss.NewStyle().Reverse(true).Bold(true)
		help = lipgloss.NewStyle().Italic(true)
		statusBar = lipgloss.NewStyle().Reverse(true).Padding(0, 1)
		labelStyle = lipgloss.NewStyle().Bold(true)
		dimAddr = lipgloss.NewStyle().Faint(true)
		memBPRead = lipgloss.NewStyle().Bold(true)
		memBPWrite = lipgloss.NewStyle().Bold(true)
		memBPRW = lipgloss.NewStyle().Bold(true)
		memCursor = lipgloss.NewStyle().Reverse(true).Bold(true)
		memEdit = lipgloss.NewStyle().Reverse(true).Bold(true)
		return
	}
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(p.title)
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	regStyle = lipgloss.NewStyle().Foreground(p.reg)
	flagOn = lipgloss.NewStyle().Foreground(p.flagOn).Bold(true)
	flagOff = lipgloss.NewStyle().Foreground(p.flagOff)
	curLine = lipgloss.NewStyle().Background(p.curLineBg).Foreground(p.curLineFg).Bold(true)
	help = lipgloss.NewStyle().Foreground(p.help).Italic(true)
	statusBar = lipgloss.NewStyle().Background(p.statusBg).Foreground(p.statusFg).Padding(0, 1)
	labelStyle = lipgloss.NewStyle().Foreground(p.label).Bold(true)
	dimAddr = lipgloss.NewStyle().Foreground(p.dimAddr)
	memBPRead = lipgloss.NewStyle().Foreground(p.memBPRead).Bold(true)
	memBPWrite = lipgloss.NewStyle().Foreground(p.memBPWrite).Bold(true)
	memBPRW = lipgloss.NewStyle().Foreground(p.memBPRW).Bold(true)
	memCursor = lipgloss.NewStyle().Reverse(true).Bold(true)
	memEdit = lipgloss.NewStyle().Foreground(p.memEditFg).Background(p.memEditBg).Bold(true)
}

// resolveTheme picks the active theme. Explicit `name` wins; an
// unrecognized name falls back to the default (Catppuccin Mocha).
// NO_COLOR (env var) forces mono regardless of the supplied name — that
// matches the spirit of https://no-color.org, even though chippy's TUI
// keeps emphasis.
func resolveTheme(name string) Theme {
	if v := os.Getenv("NO_COLOR"); v != "" {
		return ThemeMono
	}
	switch Theme(strings.ToLower(name)) {
	case ThemeMono, ThemeProtan, ThemeTritan, ThemeNeon,
		ThemeCatppuccin, ThemeMocha, ThemeMacchiato, ThemeFrappe, ThemeLatte, ThemeDefault:
		return Theme(strings.ToLower(name))
	}
	return ThemeDefault
}
