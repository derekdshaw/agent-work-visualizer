package analyzer

import (
	"bytes"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
	"github.com/derek-shaw/agent-work-visualizer/internal/progress"
)

// Language analyzes every source file of one language in a tree. Calls are
// never resolved across languages, so a language only ever sees its own files.
type Language interface {
	// Name is the language name used in the graph and in log output, e.g. "java".
	Name() string
	// Extensions lists the file extensions this language claims, e.g. ".ts", ".tsx".
	Extensions() []string
	// SkipDirs lists directory names never worth walking, e.g. "target", "node_modules".
	SkipDirs() []string
	// IsTestFile reports whether a root-relative path is a test for this language.
	IsTestFile(relativePath string) bool
	// Analyze parses all files and resolves their calls.
	// The graph it returns holds only this language's nodes and edges.
	Analyze(root string, files []string) (*callgraph.Graph, error)
}

type Options struct {
	IncludeTests bool
}

type Analyzer struct {
	languages   []Language
	byExtension map[string]Language
	skipDirs    map[string]bool
}

// Version-control and environment directories never hold project source.
var commonSkipDirs = []string{".agent-env", ".direnv", ".git", ".hg", ".svn"}

func New() *Analyzer {
	a := &Analyzer{
		byExtension: make(map[string]Language),
		skipDirs:    make(map[string]bool),
	}
	for _, directory := range commonSkipDirs {
		a.skipDirs[directory] = true
	}
	return a
}

func (a *Analyzer) Register(language Language) error {
	for _, registered := range a.languages {
		if registered.Name() == language.Name() {
			return fmt.Errorf("register %s: language is already registered", language.Name())
		}
	}
	for _, extension := range language.Extensions() {
		if existing := a.byExtension[extension]; existing != nil {
			return fmt.Errorf("register %s: extension %s is already registered by %s",
				language.Name(), extension, existing.Name())
		}
	}
	for _, extension := range language.Extensions() {
		a.byExtension[extension] = language
	}
	for _, directory := range language.SkipDirs() {
		a.skipDirs[directory] = true
	}
	a.languages = append(a.languages, language)
	return nil
}

func (a *Analyzer) Analyze(root string, options Options) (*callgraph.Graph, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", absoluteRoot)
	}
	log.Printf("Analyzing %s", absoluteRoot)
	analysisStarted := time.Now()

	discoveryLog := progress.Begin("Discovering source files")
	files, err := a.collectSourceFiles(absoluteRoot, options)
	discoveryLog.Finish(err)
	if err != nil {
		return nil, err
	}
	// The viewer reads nodes, edges, and unresolved as JSON arrays. A nil slice encodes as null.
	graph := &callgraph.Graph{
		Root:       absoluteRoot,
		Nodes:      []callgraph.Function{},
		Edges:      []callgraph.Edge{},
		Unresolved: []callgraph.UnresolvedCall{},
	}
	for _, language := range a.languages {
		paths := files[language.Name()]
		if len(paths) == 0 {
			continue
		}
		operation := progress.Begin(fmt.Sprintf("Analyzing %d %s files", len(paths), language.Name()))
		part, err := language.Analyze(absoluteRoot, paths)
		operation.Finish(err)
		if err != nil {
			return nil, err
		}
		merge(graph, part)
	}
	finishLog := progress.Begin("Finalizing graph")
	finish(graph)
	finishLog.Finish(nil)
	log.Printf("Analysis complete (%s)", time.Since(analysisStarted).Round(time.Millisecond))
	return graph, nil
}

// Supports reports whether a registered language claims the file's extension.
func (a *Analyzer) Supports(path string) bool {
	return a.byExtension[filepath.Ext(path)] != nil
}

// Outline returns the callables of one source file. Qualified names match a
// full analysis of root. A file that no registered language claims has no
// callables and no error.
func (a *Analyzer) Outline(root string, path string) ([]callgraph.Function, error) {
	language := a.byExtension[filepath.Ext(path)]
	if language == nil {
		return nil, nil
	}
	graph, err := language.Analyze(root, []string{path})
	if err != nil {
		return nil, err
	}
	return graph.Nodes, nil
}

// collectSourceFiles returns the absolute paths of analyzable files, grouped by language name.
func (a *Analyzer) collectSourceFiles(root string, options Options) (map[string][]string, error) {
	files := make(map[string][]string)
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && a.skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		language := a.byExtension[filepath.Ext(entry.Name())]
		if language == nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !options.IncludeTests && language.IsTestFile(relative) {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isGenerated(source) {
			return nil
		}
		files[language.Name()] = append(files[language.Name()], path)
		count++
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect source files: %w", err)
	}
	for _, paths := range files {
		sort.Strings(paths)
	}
	log.Printf("Discovered %d source files", count)
	return files, nil
}

func isGenerated(source []byte) bool {
	lines := bytes.Split(source, []byte("\n"))
	if len(lines) > 10 {
		lines = lines[:10]
	}
	header := strings.ToLower(string(bytes.Join(lines, []byte("\n"))))
	return strings.Contains(header, "generated by") ||
		strings.Contains(header, "code generated") ||
		strings.Contains(header, "@generated") ||
		strings.Contains(header, "automatically generated") ||
		strings.Contains(header, "auto-generated")
}

func merge(graph *callgraph.Graph, part *callgraph.Graph) {
	graph.Files = append(graph.Files, part.Files...)
	graph.Nodes = append(graph.Nodes, part.Nodes...)
	graph.Edges = append(graph.Edges, part.Edges...)
	graph.Unresolved = append(graph.Unresolved, part.Unresolved...)
	graph.Stats.Files += part.Stats.Files
	graph.Stats.Definitions += part.Stats.Definitions
	graph.Stats.Calls += part.Stats.Calls
	graph.Stats.Resolved += part.Stats.Resolved
	graph.Stats.Unresolved = len(graph.Unresolved)
}

func finish(graph *callgraph.Graph) {
	sort.Slice(graph.Files, func(i, j int) bool {
		return graph.Files[i].Path < graph.Files[j].Path
	})
	sort.Slice(graph.Nodes, func(i, j int) bool {
		return graph.Nodes[i].ID < graph.Nodes[j].ID
	})
	sort.Slice(graph.Edges, func(i, j int) bool {
		left := graph.Edges[i].Source + "\x00" + graph.Edges[i].Target + "\x00" + graph.Edges[i].Kind
		right := graph.Edges[j].Source + "\x00" + graph.Edges[j].Target + "\x00" + graph.Edges[j].Kind
		return left < right
	})
	sort.Slice(graph.Unresolved, func(i, j int) bool {
		left := fmt.Sprintf("%s:%09d:%s", graph.Unresolved[i].File, graph.Unresolved[i].Line, graph.Unresolved[i].Expression)
		right := fmt.Sprintf("%s:%09d:%s", graph.Unresolved[j].File, graph.Unresolved[j].Line, graph.Unresolved[j].Expression)
		return left < right
	})
}
