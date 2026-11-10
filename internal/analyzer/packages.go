package analyzer

import (
	"os"
	"path/filepath"
	"sort"
)

// manifestKinds maps the name of a manifest file to its kind. Two names can
// have one kind, as the two Gradle build files do.
var manifestKinds = map[string]string{
	"Cargo.toml":       "cargo",
	"package.json":     "npm",
	"go.mod":           "go",
	"pyproject.toml":   "pyproject",
	"setup.py":         "setup",
	"pom.xml":          "maven",
	"build.gradle":     "gradle",
	"build.gradle.kts": "gradle",
}

// Packages maps each path in files, relative to root, to its package path.
//
// A kind of manifest is a workspace kind when two or more directories below
// root hold a manifest of that kind, for example the crates of a Rust
// workspace. The package of a file is then the nearest directory above it
// that holds a manifest of any workspace kind. A tree can have more than one
// workspace kind, as a tree with crates below crates/ and npm packages below
// packages/ has, and each file gets the package that is nearest to it.
//
// The package of a file above all of the manifests is the directory of the
// file, not root. Files in different directories stay in different packages,
// which the 2D view shows as different cards.
//
// A manifest in root itself does not count, because a single manifest at the
// root, such as the go.mod of this repository, holds the full tree.
func Packages(root string, files []string) map[string]string {
	kindsByDirectory := map[string][]string{}
	for _, file := range files {
		for directory := filepath.Dir(file); directory != "."; directory = filepath.Dir(directory) {
			if _, checked := kindsByDirectory[directory]; checked {
				break
			}
			kindsByDirectory[directory] = manifestKindsIn(filepath.Join(root, directory))
		}
	}

	// A member of a workspace can hold no source file, so count the manifests
	// of each kind beside those that the files show, not only those.
	countOfKind := map[string]int{}
	counted := map[string]bool{}
	for _, directory := range sortedKeys(kindsByDirectory) {
		for _, kind := range kindsByDirectory[directory] {
			parent := filepath.Dir(directory)
			if counted[parent+"\x00"+kind] {
				continue
			}
			counted[parent+"\x00"+kind] = true
			if count := membersIn(filepath.Join(root, parent), kind); count > countOfKind[kind] {
				countOfKind[kind] = count
			}
		}
	}

	workspaceKinds := map[string]bool{}
	for kind, count := range countOfKind {
		if count >= 2 {
			workspaceKinds[kind] = true
		}
	}

	packages := make(map[string]string, len(files))
	for _, file := range files {
		packages[file] = packageOf(file, workspaceKinds, kindsByDirectory)
	}
	return packages
}

// packageOf gives the package path of one file. See Packages for the rule.
func packageOf(file string, workspaceKinds map[string]bool, kindsByDirectory map[string][]string) string {
	directory := filepath.Dir(file)
	if len(workspaceKinds) == 0 {
		return directory
	}
	for above := directory; above != "."; above = filepath.Dir(above) {
		for _, kind := range kindsByDirectory[above] {
			if workspaceKinds[kind] {
				return above
			}
		}
	}
	return directory
}

// membersIn counts the subdirectories of directory that hold a manifest of
// one kind. Two or more of them make the directory a workspace of that kind.
func membersIn(directory string, kind string) int {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, found := range manifestKindsIn(filepath.Join(directory, entry.Name())) {
			if found == kind {
				count++
				break
			}
		}
	}
	return count
}

// manifestKindsIn lists each kind of manifest in one directory, one time per
// kind. It stats the known names, so a large directory costs nothing.
func manifestKindsIn(directory string) []string {
	found := map[string]bool{}
	for name, kind := range manifestKinds {
		if found[kind] {
			continue
		}
		if info, err := os.Stat(filepath.Join(directory, name)); err == nil && !info.IsDir() {
			found[kind] = true
		}
	}
	return sortedKeys(found)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
