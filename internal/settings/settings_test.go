package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
	if !reflect.DeepEqual(got.Settings, Defaults()) {
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
	if !reflect.DeepEqual(got.Settings, want) {
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
			if !reflect.DeepEqual(got.Settings, test.want) {
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
			if !reflect.DeepEqual(got.Settings, Defaults()) {
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
	if !reflect.DeepEqual(got.Settings, want) {
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
	if !strings.Contains(string(data), "\n  \"speed\": 16,\n") {
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

func TestValidateChecksTheThemes(t *testing.T) {
	theme := Scheme{Name: "warm", Label: "Warm", Colors: []string{"#ff0000", "#00ff00"}}
	s := Defaults()
	s.Themes = []Scheme{theme}
	s.ColorScheme = "warm"
	if err := Validate(s); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	tests := map[string]struct {
		change func(*Settings)
		text   string
	}{
		"name of a server scheme": {func(s *Settings) { s.Themes[0].Name = "default" }, "scheme of the server"},
		"name of the wrong form":  {func(s *Settings) { s.Themes[0].Name = "Bad Name" }, "1 to 32 characters"},
		"name that repeats":       {func(s *Settings) { s.Themes = append(s.Themes, theme) }, "repeats"},
		"empty label":             {func(s *Settings) { s.Themes[0].Label = "" }, "label"},
		"no colors":               {func(s *Settings) { s.Themes[0].Colors = nil }, "1 to 12 colors"},
		"13 colors":               {func(s *Settings) { s.Themes[0].Colors = make([]string, 13) }, "1 to 12 colors"},
		"color that is not hex":   {func(s *Settings) { s.Themes[0].Colors = []string{"red"} }, "#rrggbb"},
		"color in upper case":     {func(s *Settings) { s.Themes[0].Colors = []string{"#FF0000"} }, "#rrggbb"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			s := Defaults()
			s.Themes = []Scheme{{Name: "warm", Label: "Warm", Colors: []string{"#ff0000"}}}
			test.change(&s)
			err := Validate(s)
			if err == nil || !strings.HasPrefix(err.Error(), "themes:") {
				t.Fatalf("Validate = %v, want a themes error", err)
			}
			if !strings.Contains(err.Error(), test.text) {
				t.Fatalf("Validate = %q, want %q", err, test.text)
			}
		})
	}
}

func TestLoadRemovesAThemeThatIsNotValid(t *testing.T) {
	text := `{"version":1,"colorScheme":"bad","themes":[` +
		`{"name":"good","label":"Good","colors":["#ff0000"]},` +
		`{"name":"bad","label":"Bad","colors":["red"]}]}`
	got, err := Load(writeSettings(t, text))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(got.Settings.Themes) != 1 || got.Settings.Themes[0].Name != "good" {
		t.Fatalf("Load kept %+v, want only the good theme", got.Settings.Themes)
	}
	// colorScheme named the removed theme, so it gets its default.
	if got.Settings.ColorScheme != "default" {
		t.Fatalf("Load colorScheme = %q, want default", got.Settings.ColorScheme)
	}
	if len(got.Replaced) != 2 {
		t.Fatalf("Load replaced %v, want the theme and the color scheme", got.Replaced)
	}
}

func TestLoadRemovesAProjectThemeThatIsNotValid(t *testing.T) {
	text := `{"version":1,"themes":[{"name":"warm","label":"Warm","colors":["#ff0000"]}],` +
		`"projectThemes":{"/a":"warm","/b":"nosuch","c":"warm"}}`
	got, err := Load(writeSettings(t, text))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if want := map[string]string{"/a": "warm"}; !reflect.DeepEqual(got.Settings.ProjectThemes, want) {
		t.Fatalf("Load kept %v, want %v", got.Settings.ProjectThemes, want)
	}
	if len(got.Replaced) != 2 {
		t.Fatalf("Load replaced %v, want the two bad entries", got.Replaced)
	}
}

func TestValidateChecksTheProjectThemes(t *testing.T) {
	s := Defaults()
	s.ProjectThemes = map[string]string{"/a": "spring"}
	if err := Validate(s); err != nil {
		t.Fatalf("Validate = %v, want nil", err)
	}
	s.ProjectThemes = map[string]string{"/a": "nosuch"}
	if err := Validate(s); err == nil || !strings.HasPrefix(err.Error(), "projectThemes:") {
		t.Fatalf("Validate = %v, want a projectThemes error", err)
	}
	s.ProjectThemes = map[string]string{"a": "spring"}
	if err := Validate(s); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Validate = %v, want an absolute path error", err)
	}
}

func TestLoadKeepsAColorSchemeThatNamesATheme(t *testing.T) {
	text := `{"version":1,"colorScheme":"warm","themes":[{"name":"warm","label":"Warm","colors":["#ff0000"]}]}`
	got, err := Load(writeSettings(t, text))
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got.Settings.ColorScheme != "warm" || len(got.Replaced) != 0 {
		t.Fatalf("Load = %+v with replaced %v, want the theme kept", got.Settings, got.Replaced)
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
		Defaults:     Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 1, Themes: []Scheme{{Name: "warm", Label: "Warm", Colors: []string{"#ff0000"}}}},
		Speeds:       []float64{1, 2},
		ColorSchemes: []Scheme{{Name: "default", Label: "Default", Colors: []string{"#3987e5"}}},
	}
	want := `{"defaults":{"version":1,"skipWaiting":true,"colorScheme":"default","autoRotate":true,"followEdit":true,"speed":1,` +
		`"themes":[{"name":"warm","label":"Warm","colors":["#ff0000"]}],"projectThemes":null},` +
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
