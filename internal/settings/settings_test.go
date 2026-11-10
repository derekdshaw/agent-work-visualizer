package settings

import (
	"os"
	"path/filepath"
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
	if got != Defaults() {
		t.Fatalf("Load = %+v, want %+v", got, Defaults())
	}
}

func TestLoadReadsAFullFile(t *testing.T) {
	path := writeSettings(t, `{"version":1,"skipWaiting":false,"colorScheme":"warm","autoRotate":false,"followEdit":false,"speed":4}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	want := Settings{Version: 1, SkipWaiting: false, ColorScheme: "warm", AutoRotate: false, FollowEdit: false, Speed: 4}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadReplacesAFieldThatIsNotValid(t *testing.T) {
	tests := map[string]struct {
		text string
		want Settings
	}{
		"speed 3": {
			`{"version":1,"skipWaiting":false,"colorScheme":"warm","autoRotate":false,"followEdit":false,"speed":3}`,
			Settings{Version: 1, SkipWaiting: false, ColorScheme: "warm", AutoRotate: false, FollowEdit: false, Speed: 1},
		},
		"bad color scheme name": {
			`{"version":1,"colorScheme":"Bad Name","speed":8}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 8},
		},
		"missing fields": {
			`{"version":1,"autoRotate":false}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: false, FollowEdit: true, Speed: 1},
		},
		"unknown field": {
			`{"version":1,"speed":2,"fontSize":12}`,
			Settings{Version: 1, SkipWaiting: true, ColorScheme: "default", AutoRotate: true, FollowEdit: true, Speed: 2},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeSettings(t, test.text))
			if err != nil {
				t.Fatalf("Load error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Load = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestLoadReturnsDefaultsAndAnErrorForABrokenFile(t *testing.T) {
	tests := map[string]string{
		"not JSON":   `{"version": 1,`,
		"wrong type": `{"version":1,"colorScheme":"warm","speed":"fast"}`,
		"version 2":  `{"version":2,"speed":4}`,
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := Load(writeSettings(t, text))
			if err == nil {
				t.Fatal("Load error = nil, want an error")
			}
			if got != Defaults() {
				t.Fatalf("Load = %+v, want %+v", got, Defaults())
			}
		})
	}
}

func TestSaveThenLoadGivesTheSameSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-work-visualizer", "settings.json")
	want := Settings{Version: 1, SkipWaiting: false, ColorScheme: "warm", AutoRotate: false, FollowEdit: true, Speed: 16}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if got != want {
		t.Fatalf("Load = %+v, want %+v", got, want)
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
		"speed 3":               {func(s *Settings) { s.Speed = 3 }, "speed"},
		"empty color scheme":    {func(s *Settings) { s.ColorScheme = "" }, "colorScheme"},
		"color scheme Bad Name": {func(s *Settings) { s.ColorScheme = "Bad Name" }, "colorScheme"},
		"version 2":             {func(s *Settings) { s.Version = 2 }, "version"},
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
