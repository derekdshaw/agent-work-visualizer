// Package langtest holds fixtures and assertions shared by the analyzer tests.
package langtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
)

// Analyze runs the orchestrator with only the given languages registered.
func Analyze(root string, options analyzer.Options, languages ...analyzer.Language) (*callgraph.Graph, error) {
	a := analyzer.New()
	for _, language := range languages {
		if err := a.Register(language); err != nil {
			return nil, err
		}
	}
	return a.Analyze(root, options)
}

func WriteFixture(t *testing.T, root string, relative string, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(contents, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func FindFunction(t *testing.T, graph *callgraph.Graph, qualifiedName string) *callgraph.Function {
	t.Helper()
	for index := range graph.Nodes {
		if graph.Nodes[index].QualifiedName == qualifiedName {
			return &graph.Nodes[index]
		}
	}
	t.Fatalf("function %s not found", qualifiedName)
	return nil
}

func AssertEdge(t *testing.T, graph *callgraph.Graph, source string, target string, kind string, count int) {
	t.Helper()
	sourceID := FindFunction(t, graph, source).ID
	targetID := FindFunction(t, graph, target).ID
	for _, edge := range graph.Edges {
		if edge.Source == sourceID && edge.Target == targetID && edge.Kind == kind {
			if edge.Count != count {
				t.Fatalf("%s -> %s count = %d, want %d", source, target, edge.Count, count)
			}
			return
		}
	}
	t.Fatalf("edge %s -> %s (%s) not found", source, target, kind)
}
