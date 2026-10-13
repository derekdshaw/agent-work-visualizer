package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type bazelSources struct {
	workspace string
	java      []string
	proto     []string
}

func discoverBazelSources(root string, options Options) (*bazelSources, error) {
	workspace := findBazelWorkspace(root)
	if workspace == "" {
		return nil, nil
	}
	relative, err := filepath.Rel(workspace, root)
	if err != nil {
		return nil, fmt.Errorf("resolve Bazel package path: %w", err)
	}
	targets := "//..."
	if relative != "." {
		targets = "//" + filepath.ToSlash(relative) + "/..."
	}
	targetKinds := `java_(library|binary|import)`
	if options.IncludeTests {
		targetKinds = `java_.*`
	}
	expression := fmt.Sprintf(`kind("source file", deps(kind("%s", %s)))`, targetKinds, targets)
	context, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(context, "bazel", "query", expression, "--output=location")
	command.Dir = workspace
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if context.Err() != nil {
			return nil, fmt.Errorf("Bazel source query timed out: %w", context.Err())
		}
		return nil, fmt.Errorf("Bazel source query failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	java, proto := parseBazelSourceLocations(stdout.String(), options)
	return &bazelSources{workspace: workspace, java: java, proto: proto}, nil
}

func findBazelWorkspace(root string) string {
	for directory := filepath.Clean(root); ; directory = filepath.Dir(directory) {
		for _, marker := range []string{"MODULE.bazel", "WORKSPACE.bazel", "WORKSPACE"} {
			if info, err := os.Stat(filepath.Join(directory, marker)); err == nil && !info.IsDir() {
				return directory
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return ""
		}
	}
}

func parseBazelSourceLocations(output string, options Options) ([]string, []string) {
	var java []string
	var proto []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		path, _, found := strings.Cut(line, ":1:1: source file ")
		if !found {
			continue
		}
		path = filepath.Clean(path)
		if seen[path] {
			continue
		}
		seen[path] = true
		switch filepath.Ext(path) {
		case ".java":
			if options.IncludeTests || !isTestPath(path, "java") {
				java = append(java, path)
			}
		case ".proto":
			proto = append(proto, path)
		}
	}
	sort.Strings(java)
	sort.Strings(proto)
	return java, proto
}
