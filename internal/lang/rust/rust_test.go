package rust

import (
	"testing"

	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/langtest"
)

func TestAnalyzeRustResolutionEntrypointsAndTests(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "src/main.rs", `
mod util;

use crate::util::{helper as alias, Worker};

mod inline {
    pub fn inside() {}

    pub fn caller() {
        inside();
    }
}

fn local() {
    alias();
}

fn main() {
    inline::caller();
    local();
    alias();
    Worker::build();
}
`)
	langtest.WriteFixture(t, root, "src/util.rs", `
pub fn helper() {}

pub struct Worker;

impl Worker {
    pub fn build() -> Self {
        Self
    }

    pub fn run(&self) {
        helper();
        self.finish();
    }

    fn finish(&self) {}
}
`)
	langtest.WriteFixture(t, root, "tests/integration_test.rs", "fn ignored() {}\n")

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	if graph.Stats.Files != 2 || graph.Stats.Definitions != 8 {
		t.Fatalf("stats = %+v, want 2 files and 8 definitions", graph.Stats)
	}
	if main := langtest.FindFunction(t, graph, "main"); !main.Entrypoint || main.EntryKind != "main" {
		t.Fatal("Rust main was not marked as an entrypoint")
	}
	langtest.AssertEdge(t, graph, "main", "local", "call", 1)
	langtest.AssertEdge(t, graph, "main", "inline.caller", "call", 1)
	langtest.AssertEdge(t, graph, "inline.caller", "inline.inside", "call", 1)
	langtest.AssertEdge(t, graph, "main", "util.helper", "call", 1)
	langtest.AssertEdge(t, graph, "main", "util.Worker.build", "call", 1)
	langtest.AssertEdge(t, graph, "util.Worker.run", "util.helper", "call", 1)
	langtest.AssertEdge(t, graph, "util.Worker.run", "util.Worker.finish", "call", 1)
}
