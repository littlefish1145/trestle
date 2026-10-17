package tui

import (
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func assertFg(t *testing.T, actual color.Color, hex string) {
	t.Helper()
	want, ok := lipgloss.Color(hex).(color.RGBA)
	if !ok {
		t.Fatalf("%s is not a plain RGBA color", hex)
	}
	got, ok := actual.(color.RGBA)
	if !ok {
		t.Fatalf("expected a pinned RGBA color, got %#v", actual)
	}
	if got != want {
		t.Fatalf("foreground %#v is not %s", got, hex)
	}
}

func assertBg(t *testing.T, actual color.Color, hex string) {
	t.Helper()
	want, ok := lipgloss.Color(hex).(color.RGBA)
	if !ok {
		t.Fatalf("%s is not a plain RGBA color", hex)
	}
	got, ok := actual.(color.RGBA)
	if !ok {
		t.Fatalf("expected a pinned RGBA background, got %#v", actual)
	}
	if got != want {
		t.Fatalf("background %#v is not %s", got, hex)
	}
}

// The palette keeps prose dependent on the terminal theme: only titles and body
// text stay uncolored, everything else is baked in so the TUI keeps its look.
func TestPaletteLeavesProseToTheTerminalTheme(t *testing.T) {
	for _, light := range []bool{false, true} {
		palette := newPalette(light)
		unset := color.Color(lipgloss.NoColor{})
		if palette.title.GetForeground() != unset || palette.title.GetBackground() != unset || !palette.title.GetBold() {
			t.Fatalf("title must inherit the terminal theme, bolded: %#v", palette.title.GetForeground())
		}
		if palette.text.GetForeground() != unset || palette.text.GetBackground() != unset {
			t.Fatalf("text must inherit the terminal theme: %#v", palette.text.GetForeground())
		}
	}
}

func TestDarkPalettePinsTheSignatureColors(t *testing.T) {
	palette := newPalette(false)
	assertFg(t, palette.brand.GetForeground(), "#67e8f9")
	assertFg(t, palette.muted.GetForeground(), "#94a3b8")
	assertFg(t, palette.faint.GetForeground(), "#64748b")
	assertFg(t, palette.accent.GetForeground(), "#7dd3fc")
	assertFg(t, palette.good.GetForeground(), "#86efac")
	assertFg(t, palette.warning.GetForeground(), "#fcd34d")
	assertFg(t, palette.danger.GetForeground(), "#fda4af")
	assertFg(t, palette.selected.GetForeground(), "#ecfeff")
	assertBg(t, palette.selected.GetBackground(), "#17324d")
	for name, hex := range map[string]string{"pillBg": palette.pillBg, "borderMuted": palette.borderMuted, "borderFocus": palette.borderFocus} {
		if hex == "" {
			t.Fatalf("%s must stay hardcoded, got empty", name)
		}
	}
	if !palette.brand.GetBold() || !palette.accent.GetBold() || !palette.selected.GetBold() {
		t.Fatal("brand, accent and selected styles must stay bold")
	}
}

func TestLightPaletteSwapsToReadableVariants(t *testing.T) {
	dark, light := newPalette(false), newPalette(true)
	assertFg(t, light.brand.GetForeground(), "#0e7490")
	assertFg(t, light.muted.GetForeground(), "#64748b")
	assertFg(t, light.faint.GetForeground(), "#94a3b8")
	assertFg(t, light.accent.GetForeground(), "#0284c7")
	assertFg(t, light.good.GetForeground(), "#15803d")
	assertFg(t, light.warning.GetForeground(), "#b45309")
	assertFg(t, light.danger.GetForeground(), "#be123c")
	assertBg(t, light.selected.GetBackground(), "#0e7490")
	if light.pillBg == dark.pillBg || light.borderMuted == dark.borderMuted || light.borderFocus == dark.borderFocus {
		t.Fatal("light palette borders and chips must differ from dark")
	}
	if light.brand.GetForeground() == nil || dark.brand.GetForeground() == nil {
		t.Fatal("brand identity must be pinned in both modes")
	}
}

// A BackgroundColorMsg from Bubble Tea must flip the palette on the next render.
func TestDashboardAdaptsPaletteToTerminalBackground(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(dashboardModel)
	if model.lightBackground {
		t.Fatal("the console starts on its dark palette")
	}

	updated, _ = model.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}})
	model = updated.(dashboardModel)
	if !model.lightBackground {
		t.Fatal("a light terminal background must switch to the light palette")
	}
	light := model.View().Content
	if !strings.Contains(light, "38;2;14;116;144") {
		t.Fatalf("light render must carry the dark-cyan brand: %s", light)
	}
	if strings.Contains(light, "38;2;103;232;249") {
		t.Fatal("light render must not carry the dark brand cyan")
	}

	updated, _ = model.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 0x10, G: 0x10, B: 0x10, A: 0xff}})
	model = updated.(dashboardModel)
	if model.lightBackground {
		t.Fatal("a dark terminal background must switch back to the dark palette")
	}
	dark := model.View().Content
	if !strings.Contains(dark, "38;2;103;232;249") {
		t.Fatalf("dark render must carry the brand cyan: %s", dark)
	}
}

// Without a background answer the palette stays on the dark signature look.
func TestMissingBackgroundAnswerKeepsDarkPalette(t *testing.T) {
	model := newDashboard("trestle.toml", Services{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	model = updated.(dashboardModel)
	updated, _ = model.Update(tea.BackgroundColorMsg{})
	model = updated.(dashboardModel)
	if model.lightBackground {
		t.Fatal("an unreadable background must default to the dark palette")
	}
}
