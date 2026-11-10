// Package settings reads and writes the settings file of the viewer. The
// server keeps the settings in one file, so each browser gets the same
// settings. If the file does not exist, the viewer uses the defaults.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
}

// Speeds lists the valid values of Speed.
var Speeds = []float64{1, 2, 4, 8, 16}

// schemeName matches a valid color scheme name.
var schemeName = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

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

// Load returns the settings in the file at path. If the file does not exist,
// Load returns Defaults() and no error. If the file is not valid JSON, if a
// value has the wrong type, or if the version is not known, Load returns
// Defaults() and an error. A field that is missing or has a value that is
// not valid gets its default. The other fields keep their values. Load
// ignores fields that it does not know.
func Load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return Defaults(), fmt.Errorf("read settings: %w", err)
	}
	s := Defaults()
	if err := json.Unmarshal(data, &s); err != nil {
		// A value of the wrong type is also an error here, so the server
		// logs it.
		return Defaults(), fmt.Errorf("parse %s: %w", path, err)
	}
	if err := validVersion(s.Version); err != nil {
		return Defaults(), fmt.Errorf("parse %s: %w", path, err)
	}
	defaults := Defaults()
	if validColorScheme(s.ColorScheme) != nil {
		s.ColorScheme = defaults.ColorScheme
	}
	if validSpeed(s.Speed) != nil {
		s.Speed = defaults.Speed
	}
	return s, nil
}

// Validate returns an error that names the first field of s that is not
// valid, or nil.
func Validate(s Settings) error {
	if err := validVersion(s.Version); err != nil {
		return err
	}
	if err := validColorScheme(s.ColorScheme); err != nil {
		return err
	}
	return validSpeed(s.Speed)
}

func validVersion(version int) error {
	if version != 1 {
		return fmt.Errorf("version: %d is not supported", version)
	}
	return nil
}

func validColorScheme(name string) error {
	if !schemeName.MatchString(name) {
		return fmt.Errorf("colorScheme: %q must be 1 to 32 characters of a-z, 0-9, and -", name)
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
