package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPackages(t *testing.T) {
	cases := []struct {
		name      string
		manifests []string
		files     []string
		want      []string
	}{
		{
			name:      "go repo with a web folder",
			manifests: []string{"go.mod", "web/package.json"},
			files:     []string{"main.go", "internal/a/a.go", "web/src/x.ts"},
			want:      []string{".", "internal/a", "web/src"},
		},
		{
			name:      "one crate",
			manifests: []string{"Cargo.toml"},
			files:     []string{"src/main.rs", "src/parser/pack.rs", "tests/parse.rs"},
			want:      []string{"src", "src/parser", "tests"},
		},
		{
			name:      "rust workspace",
			manifests: []string{"Cargo.toml", "crates/a/Cargo.toml", "crates/b/Cargo.toml"},
			files:     []string{"crates/a/src/lib.rs", "crates/a/tests/t.rs", "crates/b/src/x/y.rs"},
			want:      []string{"crates/a", "crates/a", "crates/b"},
		},
		{
			name:      "npm workspace",
			manifests: []string{"package.json", "packages/ui/package.json", "packages/api/package.json"},
			files:     []string{"packages/ui/src/a.ts", "scripts/b.ts"},
			want:      []string{"packages/ui", "scripts"},
		},
		{
			name:      "two workspace kinds",
			manifests: []string{"Cargo.toml", "crates/a/Cargo.toml", "crates/b/Cargo.toml", "packages/ui/package.json", "packages/api/package.json"},
			files:     []string{"crates/a/src/lib.rs", "crates/b/src/x/y.rs", "packages/ui/src/a.ts", "packages/api/index.ts"},
			want:      []string{"crates/a", "crates/b", "packages/ui", "packages/api"},
		},
		{
			name:      "crates below a subdirectory",
			manifests: []string{"backend/Cargo.toml", "backend/crates/kaju-api/Cargo.toml", "backend/crates/kaju-web/Cargo.toml"},
			files: []string{
				"backend/crates/kaju-api/src/config.rs",
				"backend/crates/kaju-api/src/routes/auth.rs",
				"backend/crates/kaju-web/src/routes/routines.rs",
				"backend/migrations/init.rs",
				"frontend/web/static/js/app.js",
			},
			want: []string{
				"backend/crates/kaju-api",
				"backend/crates/kaju-api",
				"backend/crates/kaju-web",
				"backend",
				"frontend/web/static/js",
			},
		},
		{
			name:  "no manifests",
			files: []string{"a/b.py", "c.py"},
			want:  []string{"a", "."},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, path := range append(append([]string{}, test.manifests...), test.files...) {
				write(t, filepath.Join(root, filepath.FromSlash(path)))
			}
			files := make([]string, len(test.files))
			for index, file := range test.files {
				files[index] = filepath.FromSlash(file)
			}

			packages := Packages(root, files)

			for index, file := range files {
				want := filepath.FromSlash(test.want[index])
				if got := packages[file]; got != want {
					t.Errorf("package of %s = %q, want %q", file, got, want)
				}
			}
		})
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}
