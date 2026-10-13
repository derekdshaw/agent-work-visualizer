package activity

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/langtest"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/python"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
)

func TestActivityMapperForwardMapsEarlierEvents(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after the edit below. Before the edit,
	// second was on lines 4 and 5.
	langtest.WriteFixture(t, root, "app.py", `
def first():
    a = 1
    b = 2
    c = 3
    return 1

def second():
    return 2
`)
	path := filepath.Join(root, "app.py")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	read := transcript.Event{Time: start, Agent: "main", Tool: "Read", Path: path, Lines: []int{4, 5}}
	hunks := []transcript.Hunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def first():", "+    a = 1", "+    b = 2", "+    c = 3", "     return 1"}}}
	edit := transcript.Event{Time: start.Add(time.Second), Agent: "agent-1", Tool: "Edit", Path: path, Lines: transcript.ChangedLines(hunks), Hunks: hunks}

	mapper := NewMapper(root, pythonAnalyzer(t))
	// Events from different transcripts arrive out of order.
	activities := mapper.Map([]transcript.Event{edit, read})
	if len(activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(activities))
	}
	assertActivity(t, activities[0], "read", "Read", 7, "second")
	assertActivity(t, activities[1], "edit", "Edit", 2, "first")
	if activities[1].Agent != "agent-1" || activities[1].File != "app.py" {
		t.Fatalf("edit activity = %+v, want agent agent-1 and file app.py", activities[1])
	}

	// A read from an earlier batch time still moves through the recorded edit.
	late := read
	late.Time = start.Add(-time.Second)
	activities = mapper.Map([]transcript.Event{late})
	assertActivity(t, activities[0], "read", "Read", 7, "second")

	// A read after the edit already uses current line numbers.
	current := transcript.Event{Time: start.Add(2 * time.Second), Tool: "Read", Path: path, Lines: []int{4}}
	activities = mapper.Map([]transcript.Event{current})
	assertActivity(t, activities[0], "read", "Read", 4, "first")
}

func TestActivityMapperOrdersEditsAcrossBatches(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after both edits below.
	langtest.WriteFixture(t, root, "app.py", `
def first():
    a = 1
    b = 2
    c = 3
    return 1
def middle():
    return 0
def second():
    return 2
`)
	path := filepath.Join(root, "app.py")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	read := transcript.Event{Time: start, Tool: "Read", Path: path, Lines: []int{3}}
	firstHunks := []transcript.Hunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def first():", "+    a = 1", "+    b = 2", "+    c = 3", "     return 1"}}}
	firstEdit := transcript.Event{Time: start.Add(time.Second), Tool: "Edit", Path: path, Lines: transcript.ChangedLines(firstHunks), Hunks: firstHunks}
	secondHunks := []transcript.Hunk{{OldStart: 5, OldLines: 2, NewStart: 5, NewLines: 4, Lines: []string{"     return 1", "+def middle():", "+    return 0", " def second():"}}}
	secondEdit := transcript.Event{Time: start.Add(2 * time.Second), Tool: "Edit", Path: path, Lines: transcript.ChangedLines(secondHunks), Hunks: secondHunks}

	mapper := NewMapper(root, pythonAnalyzer(t))
	assertActivity(t, mapper.Map([]transcript.Event{secondEdit})[0], "edit", "Edit", 6, "middle")
	// The earlier edit arrives in a later batch. The read must move through
	// the first edit and then the second edit.
	activities := mapper.Map([]transcript.Event{firstEdit, read})
	assertActivity(t, activities[0], "read", "Read", 8, "second")
	assertActivity(t, activities[1], "edit", "Edit", 2, "first")

	// Edits with equal timestamps keep their arrival order.
	firstEdit.Time = secondEdit.Time
	activities = NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{read, firstEdit, secondEdit})
	assertActivity(t, activities[0], "read", "Read", 8, "second")
}

func TestActivityMapperKeepsEditsPerFile(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "a.py", "def a():\n    x = 1\n    y = 2\n    z = 3\n    return 1\n")
	langtest.WriteFixture(t, root, "b.py", "def b():\n    return 2\n")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	hunks := []transcript.Hunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def a():", "+    x = 1", "+    y = 2", "+    z = 3", "     return 1"}}}
	read := transcript.Event{Time: start, Tool: "Read", Path: filepath.Join(root, "b.py"), Lines: []int{2}}
	edit := transcript.Event{Time: start.Add(time.Second), Tool: "Edit", Path: filepath.Join(root, "a.py"), Lines: transcript.ChangedLines(hunks), Hunks: hunks}
	activities := NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{read, edit})
	assertActivity(t, activities[0], "read", "Read", 2, "b")
}

func TestActivityMapperAttributesDeletionsToTheEnclosingFunction(t *testing.T) {
	root := t.TempDir()
	// Each file on disk is the state after its edit, which deletes "b = 2".
	langtest.WriteFixture(t, root, "before_next.py", "def f():\n    a = 1\ndef g():\n    pass\n")
	langtest.WriteFixture(t, root, "at_end.py", "def f():\n    a = 1\n")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		file  string
		hunks []transcript.Hunk
	}{
		{"before_next.py", []transcript.Hunk{{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 3, Lines: []string{" def f():", "     a = 1", "-    b = 2", " def g():"}}}},
		{"at_end.py", []transcript.Hunk{{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 2, Lines: []string{" def f():", "     a = 1", "-    b = 2"}}}},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			path := filepath.Join(root, test.file)
			read := transcript.Event{Time: start, Tool: "Read", Path: path, Lines: []int{3}}
			edit := transcript.Event{Time: start.Add(time.Second), Tool: "Edit", Path: path, Lines: transcript.ChangedLines(test.hunks), Hunks: test.hunks}
			activities := NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{read, edit})
			assertActivity(t, activities[0], "read", "Read", 2, "f")
			assertActivity(t, activities[1], "edit", "Edit", 2, "f")
		})
	}
}

func TestActivityMapperRecordsTheHunksOfEachFunction(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after the edit below.
	langtest.WriteFixture(t, root, "app.py", "def first():\n    a = 1\n    x = 0\n    return 1\n\ndef second():\n    b = 2\n    return 2\n")
	path := filepath.Join(root, "app.py")
	hunks := []transcript.Hunk{
		{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" def first():", "+    a = 1"}},
		{OldStart: 2, OldLines: 1, NewStart: 3, NewLines: 2, Lines: []string{"+    x = 0", "     return 1"}},
		{OldStart: 4, OldLines: 2, NewStart: 6, NewLines: 3, Lines: []string{" def second():", "+    b = 2", "     return 2"}},
	}
	edit := transcript.Event{Tool: "Write", Path: path, Lines: transcript.ChangedLines(hunks), Hunks: hunks}
	activity := NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{edit})[0]
	want := []Callable{
		{Key: Key("app.py", "app.first"), Name: "first", Line: 1, Hunks: []int{0, 1}},
		{Key: Key("app.py", "app.second"), Name: "second", Line: 6, Hunks: []int{2}},
	}
	if !reflect.DeepEqual(activity.Functions, want) || !reflect.DeepEqual(activity.Hunks, hunks) || activity.Created {
		t.Fatalf("activity = %+v, want functions %+v and the event hunks", activity, want)
	}

	// A hunk with changed lines in two callables goes to each callable once.
	langtest.WriteFixture(t, root, "one.py", "def first():\n    a = 1\n    b = 2\n\ndef second():\n    c = 3\n    return 3\n")
	hunks = []transcript.Hunk{{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 7, Lines: []string{" def first():", "+    a = 1", "+    b = 2", " ", " def second():", "+    c = 3", "     return 3"}}}
	edit = transcript.Event{Tool: "Edit", Path: filepath.Join(root, "one.py"), Lines: transcript.ChangedLines(hunks), Hunks: hunks}
	activity = NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{edit})[0]
	want = []Callable{
		{Key: Key("one.py", "one.first"), Name: "first", Line: 1, Hunks: []int{0}},
		{Key: Key("one.py", "one.second"), Name: "second", Line: 5, Hunks: []int{0}},
	}
	if !reflect.DeepEqual(activity.Functions, want) {
		t.Fatalf("one hunk functions = %+v, want %+v", activity.Functions, want)
	}

	// A created file and a read have no hunks.
	for _, event := range []transcript.Event{
		{Tool: "Write", Path: path, Lines: []int{1, 2, 3, 4, 5, 6, 7, 8}, Created: true},
		{Tool: "Read", Path: path, Lines: []int{2, 7}},
	} {
		activity := NewMapper(root, pythonAnalyzer(t)).Map([]transcript.Event{event})[0]
		if activity.Created != event.Created || activity.Hunks != nil || len(activity.Functions) != 2 {
			t.Fatalf("%s activity = %+v, want created %t, no hunks, and two callables", event.Tool, activity, event.Created)
		}
		for _, function := range activity.Functions {
			if function.Hunks != nil {
				t.Fatalf("%s activity callable %+v has hunks", event.Tool, function)
			}
		}
	}
}

func TestActivityMapperFileLevelEvents(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "broken.py", "def broken(:\n    pass\n")
	langtest.WriteFixture(t, root, "notes.md", "# Notes\n")
	langtest.WriteFixture(t, root, "module.py", "import os\n\ndef inside():\n    pass\n")
	mapper := NewMapper(root, pythonAnalyzer(t))
	activities := mapper.Map([]transcript.Event{
		{Tool: "Read", Path: filepath.Join(root, "broken.py"), Lines: []int{1}},
		{Tool: "Write", Path: filepath.Join(root, "notes.md"), Lines: []int{1}},
		{Tool: "Read", Path: filepath.Join(root, "module.py"), Lines: []int{1}},
		{Tool: "Read", Path: filepath.Join(root, "missing.py"), Lines: []int{1}},
	})
	if activities[0].Note == "" || len(activities[0].Functions) != 0 {
		t.Fatalf("broken file activity = %+v, want a note and no callables", activities[0])
	}
	if activities[1].Note != "" || len(activities[1].Functions) != 0 || activities[1].Kind != "edit" {
		t.Fatalf("markdown activity = %+v, want a file-level edit without a note", activities[1])
	}
	if activities[2].Note != "" || len(activities[2].Functions) != 0 {
		t.Fatalf("module-level read = %+v, want a file-level read", activities[2])
	}
	if activities[3].Note == "" {
		t.Fatalf("missing file activity = %+v, want a note", activities[3])
	}
}

func TestActivityMapperRefreshesOutlineAfterFileChanges(t *testing.T) {
	root := t.TempDir()
	langtest.WriteFixture(t, root, "app.py", "def old():\n    pass\n")
	path := filepath.Join(root, "app.py")
	mapper := NewMapper(root, pythonAnalyzer(t))
	assertActivity(t, mapper.Map([]transcript.Event{{Tool: "Read", Path: path, Lines: []int{1}}})[0], "read", "Read", 1, "old")
	// The new content has a different size, so the cached outline is stale.
	langtest.WriteFixture(t, root, "app.py", "def renamed():\n    pass\n")
	assertActivity(t, mapper.Map([]transcript.Event{{Tool: "Read", Path: path, Lines: []int{1}}})[0], "read", "Read", 1, "renamed")
}

func TestInnermostFunctionPrefersNestedCallable(t *testing.T) {
	functions := []callgraph.Function{
		{Name: "outer", Line: 1, EndLine: 10},
		{Name: "nested", Line: 3, EndLine: 5},
	}
	if got := innermostFunction(functions, 4); got == nil || got.Name != "nested" {
		t.Fatalf("innermostFunction(4) = %v, want nested", got)
	}
	if got := innermostFunction(functions, 8); got == nil || got.Name != "outer" {
		t.Fatalf("innermostFunction(8) = %v, want outer", got)
	}
	if got := innermostFunction(functions, 11); got != nil {
		t.Fatalf("innermostFunction(11) = %v, want nil", got)
	}
}

func assertActivity(t *testing.T, activity Activity, kind string, tool string, line int, names ...string) {
	t.Helper()
	var got []string
	for _, function := range activity.Functions {
		got = append(got, function.Name)
	}
	if activity.Kind != kind || activity.Tool != tool || activity.Line != line || !slices.Equal(got, names) {
		t.Fatalf("activity = %s %s line %d %v (note %q), want %s %s line %d %v", activity.Kind, activity.Tool, activity.Line, got, activity.Note, kind, tool, line, names)
	}
}

// pythonAnalyzer returns an analyzer that outlines the Python fixtures.
func pythonAnalyzer(t *testing.T) *analyzer.Analyzer {
	t.Helper()
	a := analyzer.New()
	if err := a.Register(python.New()); err != nil {
		t.Fatal(err)
	}
	return a
}
