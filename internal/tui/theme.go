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
	// ThemeDefault is the colorful palette chippy shipped with.
	ThemeDefault Theme = "default"
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
		string(ThemeMono),
		string(ThemeProtan),
		string(ThemeTritan),
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
	// itemsBold and useColor jointly control whether emphasis is via
	// color, bold, or both. mono leans on bold + reverse only.
	useColor bool
}

func paletteFor(t Theme) palette {
	switch t {
	case ThemeMono:
		return palette{useColor: false}
	case ThemeProtan:
		// Red→blue, green→yellow. Distinguishable under common
		// red-green deficiencies. ANSI 256-color codes that match
		// safely on most terminal themes.
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
	default: // ThemeDefault
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
// unrecognized name falls back to default. NO_COLOR (env var) forces
// mono regardless of the supplied name — that matches the spirit of
// https://no-color.org, even though chippy's TUI keeps emphasis.
func resolveTheme(name string) Theme {
	if v := os.Getenv("NO_COLOR"); v != "" {
		return ThemeMono
	}
	switch Theme(strings.ToLower(name)) {
	case ThemeMono, ThemeProtan, ThemeTritan, ThemeDefault:
		return Theme(strings.ToLower(name))
	}
	return ThemeDefault
}
