package analyzer

import (
	"os"
	"path/filepath"
)

// manifestNames are the files that mark the directory of one project, such as
// a crate, an npm package, or a Go module.
var manifestNames = []string{
	"Cargo.toml",
	"package.json",
	"go.mod",
	"pyproject.toml",
	"setup.py",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
}

// Packages maps each path in files, relative to root, to its package path.
//
// When two or more directories below root hold a manifest, each of them is a
// package, for example the crates of a Rust workspace, or a Go module beside a
// Gradle project. The manifests do not have to be of one kind. The package of
// a file is then the nearest directory above it that holds a manifest.
//
// With fewer than two such directories, and for a file above all of the
// manifests, the package of a file is its directory. Files in different
// directories stay in different packages, which the 2D view shows as
// different cards.
//
// A manifest in root itself does not count, because a single manifest at the
// root, such as the go.mod of this repository, holds the full tree. A single
// manifest below root holds the full tree of its directory in the same way.
func Packages(root string, files []string) map[string]string {
	hasManifest := map[string]bool{}
	for _, file := range files {
		for directory := filepath.Dir(file); directory != "."; directory = filepath.Dir(directory) {
			if _, checked := hasManifest[directory]; checked {
				break
			}
			hasManifest[directory] = holdsManifest(filepath.Join(root, directory))
		}
	}

	// A project can hold no source file, so the files alone do not show all
	// of the projects. Count the projects beside each one that the files show.
	// Each parent is read one time, so no project is counted two times.
	projects := 0
	readParents := map[string]bool{}
	for directory, found := range hasManifest {
		parent := filepath.Dir(directory)
		if !found || readParents[parent] {
			continue
		}
		readParents[parent] = true
		projects += projectsIn(filepath.Join(root, parent))
	}

	packages := make(map[string]string, len(files))
	for _, file := range files {
		packages[file] = packageOf(file, projects >= 2, hasManifest)
	}
	return packages
}

// packageOf gives the package path of one file. See Packages for the rule.
func packageOf(file string, manifestsArePackages bool, hasManifest map[string]bool) string {
	directory := filepath.Dir(file)
	if !manifestsArePackages {
		return directory
	}
	for above := directory; above != "."; above = filepath.Dir(above) {
		if hasManifest[above] {
			return above
		}
	}
	return directory
}

// projectsIn counts the subdirectories of directory that hold a manifest.
func projectsIn(directory string) int {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() && holdsManifest(filepath.Join(directory, entry.Name())) {
			count++
		}
	}
	return count
}

// holdsManifest reports whether directory holds a manifest. It stats the
// known names, so a large directory costs nothing.
func holdsManifest(directory string) bool {
	for _, name := range manifestNames {
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}
