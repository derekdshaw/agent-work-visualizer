package main

import (
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

//go:embed viewer.html
var viewerTemplate string

//go:embed assets/cytoscape.min.js
var cytoscapeJS string

//go:embed assets/3d-force-graph.min.js
var forceGraphJS string

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) > 0 && arguments[0] == "serve" {
		return runServe(arguments[1:])
	}
	root, output, includeTests, err := parseArguments(arguments)
	if err != nil {
		return err
	}
	graph, err := Analyze(root, Options{IncludeTests: includeTests})
	if err != nil {
		return err
	}
	if output == "" {
		cleanRoot := filepath.Clean(root)
		output = filepath.Join("dist", filepath.Base(cleanRoot)+"-call-graph.html")
	}
	renderLog := beginOperation("Rendering HTML report")
	html, err := renderHTML(graph)
	renderLog.finish(err)
	if err != nil {
		return err
	}
	log.Printf("Writing HTML report to %s", output)
	return writeAtomic(output, html, graph)
}

func parseArguments(arguments []string) (root string, output string, includeTests bool, err error) {
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--output":
			index++
			if index >= len(arguments) {
				return "", "", false, errors.New("--output requires a path")
			}
			output = arguments[index]
		case "--include-tests":
			includeTests = true
		case "-h", "--help":
			return "", "", false, errors.New("usage: code-graph-visualizer ROOT [--output PATH] [--include-tests]")
		default:
			if strings.HasPrefix(arguments[index], "-") {
				return "", "", false, fmt.Errorf("unknown option %s", arguments[index])
			}
			if root != "" {
				return "", "", false, errors.New("exactly one ROOT is required")
			}
			root = arguments[index]
		}
	}
	if root == "" {
		return "", "", false, errors.New("usage: code-graph-visualizer ROOT [--output PATH] [--include-tests]")
	}
	return root, output, includeTests, nil
}

func renderHTML(graph *Graph) ([]byte, error) {
	graphJSON, err := marshalGraph(graph)
	if err != nil {
		return nil, err
	}
	return renderViewer(graph, graphJSON, "null")
}

// renderViewer fills the viewer template with graph and graphJSON, the output
// of marshalGraph for graph. live is a JavaScript literal that becomes the
// viewer's LIVE constant: "null" for a static report, or the live graph state
// in serve mode.
func renderViewer(graph *Graph, graphJSON []byte, live string) ([]byte, error) {
	if strings.Count(viewerTemplate, "/*__GRAPH__*/") != 1 ||
		strings.Count(viewerTemplate, "/*__LIVE__*/") != 1 ||
		strings.Count(viewerTemplate, "<!--__METAFLOW_OPTION__-->") != 1 {
		return nil, errors.New("viewer template placeholder is missing or duplicated")
	}
	rendered := strings.Replace(viewerTemplate, "/*__CYTOSCAPE__*/", cytoscapeJS, 1)
	rendered = strings.Replace(rendered, "/*__FORCE_GRAPH__*/", forceGraphJS, 1)
	// One pass replaces LIVE and GRAPH, so a marker inside the graph data or
	// inside the root path of the live state is never replaced.
	rendered = strings.NewReplacer("/*__LIVE__*/null", live, "/*__GRAPH__*/", string(graphJSON)).Replace(rendered)
	metaflowOption := ""
	if graphHasMetaflow(graph) {
		metaflowOption = `<option value="metaflow-step">Metaflow steps</option>`
	}
	rendered = strings.Replace(rendered, "<!--__METAFLOW_OPTION__-->", metaflowOption, 1)
	return []byte(rendered), nil
}

func graphHasMetaflow(graph *Graph) bool {
	for _, node := range graph.Nodes {
		if node.EntryKind == "metaflow-step" {
			return true
		}
	}
	for _, edge := range graph.Edges {
		if edge.Kind == "transition" {
			return true
		}
	}
	return false
}

func writeAtomic(path string, contents []byte, graph *Graph) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	file, err := os.CreateTemp(directory, ".code-graph-*.html")
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	temporary := file.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return fmt.Errorf("write output: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output: %w", err)
	}
	if err := os.Chmod(temporary, 0o644); err != nil {
		return fmt.Errorf("set output permissions: %w", err)
	}
	if err := os.Rename(temporary, absolute); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	keep = true
	if graph.Stats.DependencyFiles > 0 {
		fmt.Printf("wrote %s (%d project files, %d dependency files, %d project definitions, %d dependency definitions, %d calls)\n",
			absolute, graph.Stats.ProjectFiles, graph.Stats.DependencyFiles,
			graph.Stats.ProjectDefinitions, graph.Stats.DependencyDefinitions, graph.Stats.Calls)
	} else {
		fmt.Printf("wrote %s (%d files, %d definitions, %d calls)\n",
			absolute, graph.Stats.Files, graph.Stats.Definitions, graph.Stats.Calls)
	}
	return nil
}
