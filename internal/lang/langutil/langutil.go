// Package langutil holds helpers shared by the language analyzers.
package langutil

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// EachFile calls analyze for every file, logging progress and any file that
// takes unusually long so a stalled run is attributable.
func EachFile(root string, files []string, analyze func(path string) error) error {
	progressInterval := max(1, len(files)/20)
	for index, path := range files {
		displayPath := displayPath(root, path)
		// Progress for one file is noise, and Analyzer.Outline parses one file per call.
		if len(files) > 1 && (index == 0 || index%progressInterval == 0 || index == len(files)-1) {
			log.Printf("Parsing source files: %d/%d (%s)", index+1, len(files), displayPath)
		}
		fileStarted := time.Now()
		slowFileLog := time.AfterFunc(5*time.Second, func() {
			log.Printf("Still parsing source file %d/%d: %s (%s elapsed)",
				index+1, len(files), displayPath, time.Since(fileStarted).Round(time.Second))
		})
		err := analyze(path)
		slowFileLog.Stop()
		if err != nil {
			return err
		}
	}
	return nil
}

func displayPath(root string, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return relative
}

// Parse parses source and fails closed: any ERROR or missing node is reported
// with its path, line, and column. The caller closes the returned tree.
func Parse(path string, source []byte, language *tree_sitter.Language) (*tree_sitter.Tree, error) {
	return ParseWithFallback(path, source, language, nil)
}

// ParseWithFallback is Parse for a grammar that rejects some valid source.
// When the first parse has an error and fallback returns a non-nil rewrite of
// source, it parses the rewrite instead. The rewrite must keep every byte
// offset, because callers read node text from source.
func ParseWithFallback(path string, source []byte, language *tree_sitter.Language, fallback func([]byte) []byte) (*tree_sitter.Tree, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(language); err != nil {
		return nil, fmt.Errorf("configure parser for %s: %w", path, err)
	}
	tree := parser.Parse(source, nil)
	if tree != nil && tree.RootNode().HasError() && fallback != nil {
		if rewritten := fallback(source); rewritten != nil {
			tree.Close()
			tree = parser.Parse(rewritten, nil)
		}
	}
	if tree == nil {
		return nil, fmt.Errorf("parse %s: parser returned no tree", path)
	}
	if bad := firstInvalidNode(tree.RootNode()); bad != nil {
		// bad points into the tree, so read it before the tree is freed.
		defer tree.Close()
		point := bad.StartPosition()
		kind := "syntax error"
		if bad.IsMissing() {
			kind = "missing syntax"
		}
		return nil, fmt.Errorf("%s:%d:%d: %s (%s)", path, point.Row+1, point.Column+1, kind, bad.Kind())
	}
	return tree, nil
}

func firstInvalidNode(node *tree_sitter.Node) *tree_sitter.Node {
	if node.IsError() || node.IsMissing() {
		return node
	}
	if !node.HasError() {
		return nil
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if bad := firstInvalidNode(node.Child(i)); bad != nil {
			return bad
		}
	}
	return node
}

// InDirectory reports whether any component of a root-relative path is one of names.
func InDirectory(path string, names ...string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		for _, name := range names {
			if part == name {
				return true
			}
		}
	}
	return false
}

// Qualified joins the non-empty parts with dots.
func Qualified(parts ...string) string {
	var nonempty []string
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, ".")
}
