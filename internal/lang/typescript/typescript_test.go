package typescript

import (
	"testing"

	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/langtest"
)

func TestAnalyzeTypescriptResolutionAssignedFunctionsAndTSX(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "src/helper.ts", `
export function helper(): void {}

export class Worker {
    constructor() {
        helper();
    }

    static build(): void {}
}
`)
	langtest.WriteFixture(t, root, "src/defaulted.ts", `
export default function execute(): void {}
`)
	langtest.WriteFixture(t, root, "src/app.ts", `
import { helper as alias, Worker } from "./helper";
import execute from "./defaulted";

const local = (): void => {
    alias();
};

export async function run(): Promise<void> {
    local();
    alias();
    execute();
    new Worker();
    Worker.build();
}

class App {
    start(): void {
        this.finish();
    }

    finish(): void {}
}
`)
	langtest.WriteFixture(t, root, "src/view.tsx", `
export function View() {
    return <button onClick={() => action()}>Run</button>;
}

function action(): void {}
`)
	langtest.WriteFixture(t, root, "src/app.spec.ts", "function ignored(): void {}\n")

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	if graph.Stats.Files != 4 || graph.Stats.Definitions != 10 {
		t.Fatalf("stats = %+v, want 4 files and 10 definitions", graph.Stats)
	}
	if !langtest.FindFunction(t, graph, "src.app.run").Async {
		t.Fatal("async TypeScript function was not identified")
	}
	langtest.AssertEdge(t, graph, "src.app.run", "src.app.local", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.run", "src.helper.helper", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.run", "src.defaulted.execute", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.run", "src.helper.Worker.constructor", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.run", "src.helper.Worker.build", "call", 1)
	langtest.AssertEdge(t, graph, "src.helper.Worker.constructor", "src.helper.helper", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.local", "src.helper.helper", "call", 1)
	langtest.AssertEdge(t, graph, "src.app.App.start", "src.app.App.finish", "call", 1)
	langtest.AssertEdge(t, graph, "src.view.View", "src.view.action", "call", 1)
}

func TestTypescriptDefaultImportIgnoresSubmoduleDefaults(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "app.ts", `
import format from "./util";

function main(): void {
    format();
}
`)
	langtest.WriteFixture(t, root, "util.ts", "export default function format(): void {}\n")
	langtest.WriteFixture(t, root, "util/strings.ts", "export default function pad(): void {}\n")

	// Map iteration order varies per run, so repeat to expose a wrong match.
	for range 20 {
		graph, err := langtest.Analyze(root, analyzer.Options{}, New())
		if err != nil {
			t.Fatal(err)
		}
		pad := langtest.FindFunction(t, graph, "util.strings.pad").ID
		for _, edge := range graph.Edges {
			if edge.Target == pad {
				t.Fatalf("default import resolved to a submodule's default export: %+v", edge)
			}
		}
		langtest.AssertEdge(t, graph, "app.main", "util.format", "call", 1)
	}
}
