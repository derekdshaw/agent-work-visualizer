// Package settings reads and writes the settings file of the viewer. The
// server keeps the settings in one file, so each browser gets the same
// settings. If the file does not exist, the viewer uses the defaults.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Settings holds the settings of the viewer. The web page reads these field
// names from GET /api/settings.
type Settings struct {
	Version int `json:"version"`
	// SkipWaiting jumps over the time that the agent waits for the user
	// during playback.
	SkipWaiting bool `json:"skipWaiting"`
	// ColorScheme is the name of a color scheme in the CSS of the page. The
	// server does not know the names, so it accepts each name that has the
	// valid form.
	ColorScheme string `json:"colorScheme"`
	// AutoRotate, FollowEdit, and Speed set the state of the page when it
	// opens.
	AutoRotate bool    `json:"autoRotate"`
	FollowEdit bool    `json:"followEdit"`
	Speed      float64 `json:"speed"`
	// Themes holds the custom themes of the viewer. The schemes in Schemes
	// stay in Go and are read-only; ColorScheme can name either.
	Themes []Scheme `json:"themes"`
	// ProjectThemes maps the absolute root path of a working tree to the
	// theme of that tree. A tree without an entry uses ColorScheme.
	ProjectThemes map[string]string `json:"projectThemes"`
}

// Speeds lists the valid values of Speed.
var Speeds = []float64{1, 2, 4, 8, 16}

// Scheme is a color scheme of the graph. Colors are #rrggbb strings, which
// the page gives to the packages in sorted name order. The page reads the
// JSON field names in web/src/settings/settings.ts.
type Scheme struct {
	Name   string   `json:"name"`
	Label  string   `json:"label"`
	Colors []string `json:"colors"`
}

// Schemes lists the color schemes. The first is the default. Each scheme
// must pass the checks in the comment at the top of
// web/src/settings/colors.css, against the #04060b graph background and
// the colors in that file.
var Schemes = []Scheme{
	// The dataviz reference hues stepped for dark surfaces (lightness,
	// chroma, contrast, adjacent CVD ΔE >= 8.4). Red and magenta are left
	// out because they read as the coral of an edit. Violet and blue are
	// close (ΔE 9.8), so the ring labels, not color, identify a package.
	{Name: "default", Label: "Default", Colors: []string{"#3987e5", "#d95926", "#199e70", "#c98500", "#9085e9", "#008300"}},
	// Spring: chartreuse, teal, orange, blue, olive, and lavender. Contrast
	// on #04060b >= 6.1, adjacent CVD ΔE >= 59.7 (protanopia and
	// deuteranopia, Viénot), ΔE >= 27.9 to the coral and the edge colors.
	{Name: "spring", Label: "Spring", Colors: []string{"#cdd13c", "#36b8c9", "#e07b39", "#5f8dd9", "#8fae1b", "#b48ae0"}},
}

// Options holds the rules that the page needs: the defaults, the valid
// speeds, and the color schemes. The page has no copy of them.
type Options struct {
	Defaults     Settings  `json:"defaults"`
	Speeds       []float64 `json:"speeds"`
	ColorSchemes []Scheme  `json:"colorSchemes"`
}

// AllOptions returns the options of this server.
func AllOptions() Options {
	return Options{Defaults: Defaults(), Speeds: Speeds, ColorSchemes: Schemes}
}

// schemeName matches a valid color scheme name.
var schemeName = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// hexColor matches one color of a theme.
var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// Defaults returns the settings that apply when the file does not exist.
func Defaults() Settings {
	return Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 1}
}

// DefaultPath returns the path of the settings file. The path is in
// $XDG_CONFIG_HOME, or in ~/.config if XDG_CONFIG_HOME is empty, on all
// operating systems.
func DefaultPath() (string, error) {
	config := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		config = filepath.Join(home, ".config")
	}
	return filepath.Join(config, "agent-work-visualizer", "settings.json"), nil
}

// Loaded is the result of Load. Found is false if the file does not exist.
// Replaced lists each value that was not valid and got its default, so the
// server can log it.
type Loaded struct {
	Settings Settings
	Found    bool
	Replaced []error
}

// Load returns the settings in the file at path. If the file does not exist,
// Load returns Defaults() and no error. If the file is not valid JSON, if a
// value has the wrong type, or if the version is not known, Load returns
// Defaults() and an error. A field that is missing gets its default. A
// field that has a value that is not valid gets its default, and Replaced
// names it. The other fields keep their values. Load ignores fields that it
// does not know.
func Load(path string) (Loaded, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Loaded{Settings: Defaults()}, nil
	}
	if err != nil {
		return Loaded{Settings: Defaults()}, fmt.Errorf("read settings: %w", err)
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		// A value of the wrong type is also an error here, so the server
		// logs it.
		return Loaded{Settings: Defaults()}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validVersion(s.Version); err != nil {
		return Loaded{Settings: Defaults()}, fmt.Errorf("parse %s: %w", path, err)
	}
	loaded := Loaded{Found: true}
	defaults := Defaults()
	kept := make([]Scheme, 0, len(s.Themes))
	seen := map[string]bool{}
	for _, theme := range s.Themes {
		err := validTheme(theme)
		if err == nil && seen[theme.Name] {
			err = fmt.Errorf("themes: the name %q repeats", theme.Name)
		}
		if err != nil {
			loaded.Replaced = append(loaded.Replaced, err)
			continue
		}
		seen[theme.Name] = true
		kept = append(kept, theme)
	}
	// An empty list stays nil, so a Settings without themes compares equal
	// no matter where it came from.
	s.Themes = kept
	if len(kept) == 0 {
		s.Themes = nil
	}
	if err := validColorScheme(s.ColorScheme, s.Themes); err != nil {
		loaded.Replaced = append(loaded.Replaced, err)
		s.ColorScheme = defaults.ColorScheme
	}
	for root, name := range s.ProjectThemes {
		if err := validProjectTheme(root, name, s.Themes); err != nil {
			loaded.Replaced = append(loaded.Replaced, err)
			delete(s.ProjectThemes, root)
		}
	}
	if len(s.ProjectThemes) == 0 {
		s.ProjectThemes = nil
	}
	if err := validSpeed(s.Speed); err != nil {
		loaded.Replaced = append(loaded.Replaced, err)
		s.Speed = defaults.Speed
	}
	loaded.Settings = s
	return loaded, nil
}

// Validate returns an error that names the first field of s that is not
// valid, or nil.
func Validate(s Settings) error {
	if err := validVersion(s.Version); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, theme := range s.Themes {
		if err := validTheme(theme); err != nil {
			return err
		}
		if seen[theme.Name] {
			return fmt.Errorf("themes: the name %q repeats", theme.Name)
		}
		seen[theme.Name] = true
	}
	if err := validColorScheme(s.ColorScheme, s.Themes); err != nil {
		return err
	}
	for _, root := range slices.Sorted(maps.Keys(s.ProjectThemes)) {
		if err := validProjectTheme(root, s.ProjectThemes[root], s.Themes); err != nil {
			return err
		}
	}
	return validSpeed(s.Speed)
}

// validProjectTheme checks one entry of ProjectThemes.
func validProjectTheme(root string, name string, themes []Scheme) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("projectThemes: %q is not an absolute path", root)
	}
	if err := validColorScheme(name, themes); err != nil {
		return fmt.Errorf("projectThemes: %s: %w", root, err)
	}
	return nil
}

// validTheme checks one custom theme, apart from the uniqueness of its name
// in the list.
func validTheme(theme Scheme) error {
	if !schemeName.MatchString(theme.Name) {
		return fmt.Errorf("themes: name %q must be 1 to 32 characters of a-z, 0-9, and -", theme.Name)
	}
	if slices.ContainsFunc(Schemes, func(s Scheme) bool { return s.Name == theme.Name }) {
		return fmt.Errorf("themes: %q is the name of a scheme of the server", theme.Name)
	}
	// Count characters, not bytes, so each label that the page's maxlength
	// allows is valid here too.
	if theme.Label == "" || utf8.RuneCountInString(theme.Label) > 40 {
		return fmt.Errorf("themes: %q: the label must be 1 to 40 characters", theme.Name)
	}
	if len(theme.Colors) == 0 || len(theme.Colors) > 12 {
		return fmt.Errorf("themes: %q: 1 to 12 colors are necessary", theme.Name)
	}
	for _, color := range theme.Colors {
		if !hexColor.MatchString(color) {
			return fmt.Errorf("themes: %q: color %q is not #rrggbb in lower case", theme.Name, color)
		}
	}
	return nil
}

func validVersion(version int) error {
	if version != 1 {
		return fmt.Errorf("version: %d is not supported", version)
	}
	return nil
}

func validColorScheme(name string, themes []Scheme) error {
	known := func(s Scheme) bool { return s.Name == name }
	if !slices.ContainsFunc(Schemes, known) && !slices.ContainsFunc(themes, known) {
		return fmt.Errorf("colorScheme: %q is not a known scheme", name)
	}
	return nil
}

func validSpeed(speed float64) error {
	if !slices.Contains(Speeds, speed) {
		return fmt.Errorf("speed: %v must be one of %v", speed, Speeds)
	}
	return nil
}

// Save writes s to the file at path. It makes the directory if necessary.
// It writes a temporary file in the same directory, then renames it, so a
// failed write does not leave a damaged file. The JSON is indented, so a
// person can edit the file.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("make settings directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, "settings-*.json")
	if err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	err = writeFile(temp, append(data, '\n'))
	if err == nil {
		err = os.Rename(temp.Name(), path)
	}
	if err != nil {
		os.Remove(temp.Name())
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// writeFile writes data to the open file, sets the file mode to 0644, and
// closes the file.
func writeFile(file *os.File, data []byte) error {
	_, err := file.Write(data)
	if err == nil {
		// CreateTemp makes the file with mode 0600.
		err = file.Chmod(0o644)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
