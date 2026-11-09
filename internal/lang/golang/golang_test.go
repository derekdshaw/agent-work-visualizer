package golang

import (
	"strings"
	"testing"

	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langtest"
)

func TestAnalyzeGoResolutionEntrypointsImportsMethodsAndTests(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "go.mod", "module example.com/project\n\ngo 1.24\n")
	langtest.WriteFixture(t, root, "cmd/app/main.go", `
package main

import (
	"fmt"

	. "example.com/project/internal/dot"
	"example.com/project/internal/cleaner"
	util "example.com/project/internal/shared"
)

type Runner struct{}

func local[T any](value T) T {
	return value
}

func run() {
	local(1)
	helper.Clean()
	util.Share()
	Dot()
	fmt.Println("done")
	_ = len([]int{})
}

func (runner *Runner) Start() {
	runner.finish()
}

func (runner Runner) finish() {}

func main() {
	run()
}
`)
	langtest.WriteFixture(t, root, "internal/cleaner/helper.go", `
package helper

func Clean() {}
`)
	langtest.WriteFixture(t, root, "internal/shared/shared.go", `
package shared

func Share() {}
`)
	langtest.WriteFixture(t, root, "internal/dot/dot.go", `
package dot

func Dot() {}
`)
	langtest.WriteFixture(t, root, "cmd/app/main_test.go", "package main\n\nfunc ignored() {}\n")
	langtest.WriteFixture(t, root, "cmd/app/testdata/fixture.go", "package fixture\n\nfunc ignored() {}\n")
	langtest.WriteFixture(t, root, "vendor/example.com/dependency/dependency.go", "package dependency\n\nfunc ignored() {}\n")

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 8 {
		t.Fatalf("nodes = %d, want 8", len(graph.Nodes))
	}
	main := langtest.FindFunction(t, graph, "cmd.app.main")
	if main.Language != "go" {
		t.Fatalf("Go main metadata = %+v", *main)
	}
	start := langtest.FindFunction(t, graph, "cmd.app.Runner.Start")
	if start.Kind != "method" || start.Class != "Runner" {
		t.Fatalf("Go method metadata = %+v", *start)
	}
	langtest.AssertEdge(t, graph, "cmd.app.main", "cmd.app.run", "call")
	langtest.AssertEdge(t, graph, "cmd.app.run", "cmd.app.local", "call")
	langtest.AssertEdge(t, graph, "cmd.app.run", "internal.cleaner.Clean", "call")
	langtest.AssertEdge(t, graph, "cmd.app.run", "internal.shared.Share", "call")
	langtest.AssertEdge(t, graph, "cmd.app.run", "internal.dot.Dot", "call")
	langtest.AssertEdge(t, graph, "cmd.app.Runner.Start", "cmd.app.Runner.finish", "call")
	included, err := langtest.Analyze(root, analyzer.Options{IncludeTests: true}, New())
	if err != nil {
		t.Fatal(err)
	}
	langtest.FindFunction(t, included, "cmd.app.ignored")
	langtest.FindFunction(t, included, "cmd.app.testdata.ignored")
}

func TestAnalyzeGoInitializerEntrypoint(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "go.mod", "module example.com/project\n\ngo 1.24\n")
	langtest.WriteFixture(t, root, "cmd/app/root.go", `
package app

func init() {
	register()
}

func register() {}
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	langtest.FindFunction(t, graph, "cmd.app.init")
	langtest.AssertEdge(t, graph, "cmd.app.init", "cmd.app.register", "call")
}

func TestAnalyzeGoNewExpression(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "go.mod", "module example.com/project\n\ngo 1.26\n")
	langtest.WriteFixture(t, root, "cmd/app/root.go", `
package app

type Box struct {
	Inner struct{ Value int; Ptr *int }
}

type Pair[K, V any] struct{}

func size() int { return 1 }

func f(n int) int { return n }

func build(name string, box Box, grid [][]int, row *[]int, i, j int) []any {
	return []any{
		new(name + "ü"), new(int(size())), new(f(new(int(size())))), f(1),
		new(box.Inner.Value), new(grid[0]), new(grid[i][j]), new((*row)[i]),
		new([]int), new(Box), new(*[]int), new(([]int)), new(*Pair[int, string]),
		new(map[string]int), new(chan int), new(<-chan int), new(func(int) string), new(interface{ M() }), new(struct{ A int }),
		new((i + 1)), new(*box.Inner.Ptr),
	}
}

func after() {
	size()
}
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	build := langtest.FindFunction(t, graph, "cmd.app.build")
	if build.Line != 13 || build.EndLine != 21 {
		t.Fatalf("build metadata = %+v", *build)
	}
	langtest.AssertEdge(t, graph, "cmd.app.build", "cmd.app.size", "call")
	langtest.AssertEdge(t, graph, "cmd.app.build", "cmd.app.f", "call")
	langtest.AssertEdge(t, graph, "cmd.app.after", "cmd.app.size", "call")
}

func TestGoSyntaxErrorAfterNewExpression(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "broken.go", "package broken\n\nvar value = new(int(1))\n\nfunc broken( {}\n")
	_, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err == nil || !strings.Contains(err.Error(), "broken.go:5:") {
		t.Fatalf("Analyze error = %v, want a syntax error at broken.go:5", err)
	}
}
