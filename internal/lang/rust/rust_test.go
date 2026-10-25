package rust

import (
	"testing"

	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langtest"
)

func TestAnalyzeRustResolutionEntrypointsAndTests(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "Cargo.toml", "[package]\nname = 'app'\n")
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

func TestAnalyzeRustResolvesCallsAcrossWorkspaceCrates(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\n")
	langtest.WriteFixture(t, root, "crates/engraph-retrieve/Cargo.toml", "[package]\nname = \"engraph-retrieve\"\nversion = \"0.1.0\"\n\n[dependencies]\nname = \"not-the-package\"\n")
	langtest.WriteFixture(t, root, "crates/engraph-retrieve/src/lib.rs", "pub mod scope;\n")
	langtest.WriteFixture(t, root, "crates/engraph-retrieve/src/scope.rs", `
pub fn add_member() {}

pub fn reset() {
    crate::scope::add_member();
}
`)
	langtest.WriteFixture(t, root, "crates/engraph-cli/Cargo.toml", "[package]\nname = \"engraph-cli\"\n")
	langtest.WriteFixture(t, root, "crates/engraph-cli/src/main.rs", `
mod cli;

use engraph_retrieve::scope;

fn main() {
    scope::add_member();
    engraph_retrieve::scope::add_member();
    cli::run();
}
`)
	langtest.WriteFixture(t, root, "crates/engraph-cli/src/cli.rs", "pub fn run() {}\n")

	graph, err := langtest.Analyze(root, analyzer.Options{}, New())
	if err != nil {
		t.Fatal(err)
	}
	const retrieve = "crates.engraph-retrieve.src."
	const cli = "crates.engraph-cli.src."
	langtest.AssertEdge(t, graph, cli+"main", retrieve+"scope.add_member", "call", 2)
	langtest.AssertEdge(t, graph, cli+"main", cli+"cli.run", "call", 1)
	langtest.AssertEdge(t, graph, retrieve+"scope.reset", retrieve+"scope.add_member", "call", 1)
}
