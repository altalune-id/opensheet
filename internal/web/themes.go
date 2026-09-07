package web

// Theme is a named color theme registered on the page.
type Theme struct {
	Key         string
	DisplayName string
	SwatchHex   string
}

// DefaultThemeKey is the fallback theme when no localStorage preference is set.
const DefaultThemeKey = "slate"

// Themes lists every theme defined in internal/web/static/themes.css. Adding a new theme means dropping a CSS block into themes.css and appending an entry here.
func Themes() []Theme {
	return []Theme{
		{Key: "slate", DisplayName: "Slate", SwatchHex: "#0f172a"},
		{Key: "glacier", DisplayName: "Glacier", SwatchHex: "#0891b2"},
		{Key: "savanna", DisplayName: "Savanna", SwatchHex: "#dc6b1d"},
		{Key: "forest", DisplayName: "Forest", SwatchHex: "#1f8f5a"},
	}
}

// ColorMode names a light/dark preference. Auto follows the OS-level prefers-color-scheme.
type ColorMode string

const (
	ColorModeAuto  ColorMode = "auto"
	ColorModeLight ColorMode = "light"
	ColorModeDark  ColorMode = "dark"
)

// ColorModes returns the full list for rendering the mode picker.
func ColorModes() []ColorMode {
	return []ColorMode{ColorModeAuto, ColorModeLight, ColorModeDark}
}
