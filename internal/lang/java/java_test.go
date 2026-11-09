package java

import (
	"regexp"
	"slices"
	"testing"

	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langtest"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/python"
)

func TestAnalyzeJavaResolutionAndMixedLanguages(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "src/main/java/com/example/Base.java", `
package com.example;

public class Base {
    public Base(String value) {}

    public String inherited(String value) {
        return value;
    }
}
`)
	langtest.WriteFixture(t, root, "src/main/java/com/example/Helper.java", `
package com.example;

public class Helper extends Base {
    public Helper(String value) {
        super(value);
    }

    public static String clean(String value) {
        return value.trim();
    }

    public static String clean(int value) {
        return String.valueOf(value);
    }

    public int parse(String value) {
        return Integer.parseInt(value);
    }

    public int parse(int value) {
        return value;
    }
}
`)
	langtest.WriteFixture(t, root, "src/main/java/com/example/App.java", `
package com.example;

import static com.example.Helper.clean;

public class App {
    private final Helper helper;

    public App(Helper helper) {
        this.helper = helper;
    }

    public static void main(String[] args) {
        Helper helper = new Helper(clean(args[0]));
        helper.parse(args[0]);
        helper.inherited("value");
    }

    public int run(int value) {
        return helper.parse(value);
    }

    public Runnable callback() {
        return () -> helper.parse("1");
    }
}

record Item(String value) {
    Item {
        clean(value);
    }
}

@interface Flags {
    String value();
}
`)
	langtest.WriteFixture(t, root, "src/main/java/com/use/Use.java", `
package com.use;

import com.example.*;
import static com.example.Helper.*;

class Use {
    void run() {
        Helper helper = new Helper(clean("value"));
        helper.parse("1");
    }
}
`)
	langtest.WriteFixture(t, root, "same.py", `
def main():
    return helper()

def helper():
    return 1
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New(), python.New())
	if err != nil {
		t.Fatal(err)
	}
	main := langtest.FindFunction(t, graph, "com.example.App.main(String[])")
	if main.Language != "java" || main.Kind != "method" {
		t.Fatalf("Java main metadata = %+v", *main)
	}
	constructor := langtest.FindFunction(t, graph, "com.example.Helper.<init>(String)")
	if constructor.Kind != "constructor" || constructor.Language != "java" {
		t.Fatalf("constructor metadata = %+v", *constructor)
	}
	langtest.FindFunction(t, graph, "com.example.Item.<init>(String)")
	langtest.FindFunction(t, graph, "com.example.Flags.value()")
	langtest.AssertEdge(t, graph, "com.example.Helper.<init>(String)", "com.example.Base.<init>(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.main(String[])", "com.example.Helper.<init>(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.main(String[])", "com.example.Helper.clean(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.main(String[])", "com.example.Helper.parse(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.main(String[])", "com.example.Base.inherited(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.run(int)", "com.example.Helper.parse(int)", "call")
	langtest.AssertEdge(t, graph, "com.example.App.callback()", "com.example.Helper.parse(String)", "call")
	langtest.AssertEdge(t, graph, "com.example.Item.<init>(String)", "com.example.Helper.clean(String)", "call")
	langtest.AssertEdge(t, graph, "com.use.Use.run()", "com.example.Helper.<init>(String)", "call")
	langtest.AssertEdge(t, graph, "com.use.Use.run()", "com.example.Helper.clean(String)", "call")
	langtest.AssertEdge(t, graph, "com.use.Use.run()", "com.example.Helper.parse(String)", "call")
	langtest.AssertEdge(t, graph, "same.main", "same.helper", "call")
}

func TestJavaAmbiguousOverloadRemainsUnresolved(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "Overloads.java", `
class Overloads {
    void use(String value) {}
    void use(Integer value) {}

    void choose(Object value) {
        use(value);
    }
}
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New(), python.New())
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(graph.Edges, func(edge callgraph.Edge) bool {
		return edge.Source == langtest.FindFunction(t, graph, "Overloads.choose(Object)").ID
	}) {
		t.Fatal("ambiguous overload was resolved")
	}
}

func TestJavaTestAndGeneratedExclusions(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "src/main/java/Visible.java", "class Visible { void visible() {} }\n")
	langtest.WriteFixture(t, root, "src/test/java/VisibleTest.java", "class VisibleTest { void test() {} }\n")
	langtest.WriteFixture(t, root, "src/main/java/Generated.java", `
package generated;

@javax.annotation.processing.Generated("fixture")
class Generated {
    void omitted() {}
}
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New(), python.New())
	if err != nil {
		t.Fatal(err)
	}
	defined := func(graph *callgraph.Graph, qualifiedName string) bool {
		return slices.ContainsFunc(graph.Nodes, func(function callgraph.Function) bool {
			return function.QualifiedName == qualifiedName
		})
	}
	langtest.FindFunction(t, graph, "Visible.visible()")
	if defined(graph, "VisibleTest.test()") || defined(graph, "generated.Generated.omitted()") {
		t.Fatal("a test or generated Java source was analyzed")
	}

	included, err := langtest.Analyze(root, analyzer.Options{IncludeTests: true}, New(), python.New())
	if err != nil {
		t.Fatal(err)
	}
	if !defined(included, "VisibleTest.test()") {
		t.Fatal("the test source was not analyzed with IncludeTests")
	}
	if defined(included, "generated.Generated.omitted()") {
		t.Fatal("a generated Java source was analyzed with IncludeTests")
	}
}

func TestRejectsJavaSyntaxErrors(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "Broken.java", "class Broken { void broken( { }\n")
	_, err := langtest.Analyze(root, analyzer.Options{}, New(), python.New())
	if err == nil {
		t.Fatal("Analyze accepted malformed Java")
	}
	matched, matchErr := regexp.MatchString(`Broken\.java:\d+:\d+:`, err.Error())
	if matchErr != nil {
		t.Fatal(matchErr)
	}
	if !matched {
		t.Fatalf("error lacks path, line, and column: %v", err)
	}
}

func TestJavaDispatchEdges(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "Base.java", `
class Base<T> {
    void run(T value) {}
}

class Child extends Base<String> {
    @Override
    void run(String value) {}

    void invoke() {
        run("value");
    }
}

@interface Immutable {}

@Immutable
abstract class AbstractGenerated {
    void execute() {}
}

class UsesGenerated {
    Generated generated;

    void invoke() {
        generated.execute();
    }
}

class Callback {
    void target(String value) {}

    void register() {
        java.util.function.Consumer<String> callback = this::target;
    }
}
`)

	graph, err := langtest.Analyze(root, analyzer.Options{}, New(), python.New())
	if err != nil {
		t.Fatal(err)
	}
	base := langtest.FindFunction(t, graph, "Base.run(T)")
	child := langtest.FindFunction(t, graph, "Child.run(String)")
	if !slices.ContainsFunc(graph.Edges, func(edge callgraph.Edge) bool {
		return edge.Source == base.ID && edge.Target == child.ID && edge.Kind == "dispatch"
	}) {
		t.Fatal("override dispatch edge was not created")
	}
	langtest.AssertEdge(t, graph, "UsesGenerated.invoke()", "AbstractGenerated.execute()", "call")
	langtest.AssertEdge(t, graph, "Callback.register()", "Callback.target(String)", "dispatch")
}
