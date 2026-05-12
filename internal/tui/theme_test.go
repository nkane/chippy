package tui

import (
	"testing"
)

// resolveTheme: unknown name → default; explicit valid name → that
// name; NO_COLOR env → mono regardless.
func TestResolveTheme_NameRouting(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	cases := []struct {
		in   string
		want Theme
	}{
		{"", ThemeDefault},
		{"default", ThemeDefault},
		{"mono", ThemeMono},
		{"protan", ThemeProtan},
		{"tritan", ThemeTritan},
		{"MONO", ThemeMono}, // case insensitive
		{"banana", ThemeDefault},
	}
	for _, c := range cases {
		if got := resolveTheme(c.in); got != c.want {
			t.Errorf("resolveTheme(%q) = %s; want %s", c.in, got, c.want)
		}
	}
}

func TestResolveTheme_NoColorEnvForcesMono(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, name := range []string{"", "default", "protan", "tritan", "banana"} {
		if got := resolveTheme(name); got != ThemeMono {
			t.Errorf("NO_COLOR=1: resolveTheme(%q) = %s; want mono", name, got)
		}
	}
}

// applyTheme swaps the global style vars so renderers pick up the new
// palette on the next View(). Mono should strip the foreground color
// entirely; default carries a non-empty color.
func TestApplyTheme_SwapsGlobals(t *testing.T) {
	applyTheme(ThemeDefault)
	if fg := titleStyle.GetForeground(); fg == nil {
		t.Fatalf("default titleStyle should have a foreground")
	}
	applyTheme(ThemeMono)
	if fg, ok := titleStyle.GetForeground().(interface{ Sequence(bool) string }); ok {
		if fg.Sequence(false) != "" {
			t.Errorf("mono titleStyle should have no color sequence; got %q", fg.Sequence(false))
		}
	}
	// Restore so later tests run on the default palette.
	applyTheme(ThemeDefault)
}

// WithTheme on Model overrides the New() default and writes the
// resolved name into m.Theme.
func TestModel_WithThemeOverridesDefault(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	m, _ := stateTestModel(t)
	m2 := m.WithTheme("protan")
	if m2.Theme != string(ThemeProtan) {
		t.Errorf("WithTheme(protan) → Theme=%q want %q", m2.Theme, ThemeProtan)
	}
	// Empty arg is a no-op.
	m3 := m.WithTheme("")
	if m3.Theme != m.Theme {
		t.Errorf("WithTheme(\"\") should not change Theme; got %q (was %q)", m3.Theme, m.Theme)
	}
}

// :theme persists across save → load.
func TestState_ThemeRoundTrips(t *testing.T) {
	m, path := stateTestModel(t)
	m.Theme = string(ThemeProtan)
	m.saveState()

	m2, _ := stateTestModel(t)
	loadState(m2, path)
	if m2.Theme != string(ThemeProtan) {
		t.Errorf("theme not preserved on reload: got %q want %q", m2.Theme, ThemeProtan)
	}
}

// AvailableThemes returns the set used by the completion router.
// Smoke: every name resolveTheme accepts is in the slice.
func TestAvailableThemes_CoversValidNames(t *testing.T) {
	want := map[string]bool{
		string(ThemeDefault): true,
		string(ThemeMono):    true,
		string(ThemeProtan):  true,
		string(ThemeTritan):  true,
	}
	for _, name := range AvailableThemes() {
		delete(want, name)
	}
	if len(want) > 0 {
		t.Fatalf("AvailableThemes missing entries: %v", want)
	}
}
