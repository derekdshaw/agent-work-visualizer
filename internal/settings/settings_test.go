package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func writeSettings(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadReturnsDefaultsWithoutAFile(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "none", "settings.json"))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got.Found {
		t.Fatal("Load found a file that does not exist")
	}
	if got.Settings != Defaults() {
		t.Fatalf("Load = %+v, want %+v", got.Settings, Defaults())
	}
}

func TestLoadReadsAFullFile(t *testing.T) {
	path := writeSettings(t, `{"version":1,"skipWaiting":false,"colorScheme":"default","autoRotate":false,"followEdit":false,"speed":4}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	want := Settings{Version: 1, SkipWaiting: false, ColorScheme: "default", AutoRotate: false, FollowEdit: false, Speed: 4}
	if !got.Found || len(got.Replaced) != 0 {
		t.Fatalf("Load found = %v, replaced = %v; want found and nothing replaced", got.Found, got.Replaced)
	}
	if got.Settings != want {
		t.Fatalf("Load = %+v, want %+v", got.Settings, want)
	}
}

func TestLoadReplacesAFieldThatIsNotValid(t *testing.T) {
	tests := map[string]struct {
		text     string
		want     Settings
		replaced []string
	}{
		"speed 3": {
			`{"version":1,"skipWaiting":false,"colorScheme":"default","autoRotate":false,"followEdit":false,"speed":3}`,
			Settings{Version: 1, SkipWaiting: false, ColorScheme: "default", AutoRotate: false, FollowEdit: false, Speed: 1},
			[]string{"speed"},
		},
		"unknown color scheme": {
			`{"version":1,"colorScheme":"nosuch","speed":8}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 8},
			[]string{"colorScheme"},
		},
		"both": {
			`{"version":1,"colorScheme":"nosuch","speed":3}`,
			Defaults(),
			[]string{"colorScheme", "speed"},
		},
		"missing fields": {
			`{"version":1,"autoRotate":false}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: false, FollowEdit: true, Speed: 1},
			nil,
		},
		"unknown field": {
			`{"version":1,"speed":2,"fontSize":12}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 2},
			nil,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeSettings(t, test.text))
			if err != nil {
				t.Fatalf("Load error = %v", err)
			}
			if got.Settings != test.want {
				t.Fatalf("Load = %+v, want %+v", got.Settings, test.want)
			}
			if len(got.Replaced) != len(test.replaced) {
				t.Fatalf("Load replaced %v, want %v", got.Replaced, test.replaced)
			}
			for i, field := range test.replaced {
				if !strings.HasPrefix(got.Replaced[i].Error(), field+":") {
					t.Fatalf("Load replaced[%d] = %q, want the field %q", i, got.Replaced[i], field)
				}
			}
		})
	}
}

func TestLoadReturnsDefaultsAndAnErrorForABrokenFile(t *testing.T) {
	tests := map[string]string{
		"not JSON":   `{"version": 1,`,
		"wrong type": `{"version":1,"colorScheme":"default","speed":"fast"}`,
		"version 2":  `{"version":2,"speed":4}`,
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeSettings(t, text))
			if err == nil {
				t.Fatal("Load error = nil, want an error")
			}
			if got.Settings != Defaults() {
				t.Fatalf("Load = %+v, want %+v", got.Settings, Defaults())
			}
		})
	}
}

func TestSaveThenLoadGivesTheSameSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-work-visualizer", "settings.json")
	want := Settings{Version: 1, SkipWaiting: false, ColorScheme: "default", AutoRotate: false, FollowEdit: true, Speed: 16}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got.Settings != want {
		t.Fatalf("Load = %+v, want %+v", got.Settings, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("file mode = %o, want 644", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\n  \"speed\": 16\n") {
		t.Fatalf("file is not indented:\n%s", data)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("directory holds %d entries, want only settings.json", len(entries))
	}
}

func TestValidateNamesTheFieldThatIsNotValid(t *testing.T) {
	if err := Validate(Defaults()); err != nil {
		t.Fatalf("Validate(Defaults()) = %v", err)
	}
	tests := map[string]struct {
		change func(*Settings)
		field  string
	}{
		"speed 3":              {func(s *Settings) { s.Speed = 3 }, "speed"},
		"empty color scheme":   {func(s *Settings) { s.ColorScheme = "" }, "colorScheme"},
		"unknown color scheme": {func(s *Settings) { s.ColorScheme = "nosuch" }, "colorScheme"},
		"version 2":            {func(s *Settings) { s.Version = 2 }, "version"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := Defaults()
			test.change(&s)
			err := Validate(s)
			if err == nil {
				t.Fatal("Validate = nil, want an error")
			}
			if !strings.HasPrefix(err.Error(), test.field+":") {
				t.Fatalf("Validate = %q, want the field %q", err, test.field)
			}
		})
	}
}

func TestSchemesAreWellFormed(t *testing.T) {
	if len(Schemes) == 0 || Schemes[0].Name != "default" {
		t.Fatal("the first scheme must be default")
	}
	hex := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	seen := map[string]bool{}
	for _, scheme := range Schemes {
		if !schemeName.MatchString(scheme.Name) {
			t.Errorf("scheme name %q is not valid", scheme.Name)
		}
		if seen[scheme.Name] {
			t.Errorf("scheme name %q repeats", scheme.Name)
		}
		seen[scheme.Name] = true
		if scheme.Label == "" {
			t.Errorf("scheme %q has no label", scheme.Name)
		}
		if len(scheme.Colors) == 0 {
			t.Errorf("scheme %q has no colors", scheme.Name)
		}
		for _, color := range scheme.Colors {
			if !hex.MatchString(color) {
				t.Errorf("scheme %q: color %q is not #rrggbb", scheme.Name, color)
			}
		}
	}
	if Validate(Defaults()) != nil {
		t.Fatal("the default color scheme is not in Schemes")
	}
}

func TestOptionsJSONFieldNames(t *testing.T) {
	// web/src/settings/settings.ts reads these field names.
	options := Options{
		Defaults:     Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 1},
		Speeds:       []float64{1, 2},
		ColorSchemes: []Scheme{{Name: "default", Label: "Default", Colors: []string{"#3987e5"}}},
	}
	want := `{"defaults":{"version":1,"skipWaiting":true,"colorScheme":"default","autoRotate":true,"followEdit":true,"speed":1},` +
		`"speeds":[1,2],"colorSchemes":[{"name":"default","label":"Default","colors":["#3987e5"]}]}`
	if data, err := json.Marshal(options); err != nil || string(data) != want {
		t.Errorf("options JSON = %s, %v, want %s", data, err, want)
	}
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "agent-work-visualizer", "settings.json"); got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "/etc/xdg")
	got, err = DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/etc/xdg", "agent-work-visualizer", "settings.json"); got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}
