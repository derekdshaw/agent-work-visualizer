package main

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestShiftLine(t *testing.T) {
	insertion := []patchHunk{{OldStart: 3, OldLines: 2, NewStart: 3, NewLines: 4, Lines: []string{" a", "+x", "+y", " b"}}}
	deletion := []patchHunk{{OldStart: 2, OldLines: 3, NewStart: 2, NewLines: 1, Lines: []string{" a", "-b", "-c"}}}
	replacement := []patchHunk{{OldStart: 5, OldLines: 2, NewStart: 5, NewLines: 3, Lines: []string{"-old1", "-old2", "+new1", "+new2", "+new3"}}}
	multiple := []patchHunk{
		{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 2, Lines: []string{" a", "+x"}},
		{OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 1, Lines: []string{" k", "-l"}},
	}
	noNewline := []patchHunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{"-a", `\ No newline at end of file`, "+a", "+b"}}}
	firstLine := []patchHunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 1, Lines: []string{"-a", " b"}}}
	tests := []struct {
		name  string
		line  int
		hunks []patchHunk
		want  int
	}{
		{"before insertion", 1, insertion, 1},
		{"context line before inserted lines", 3, insertion, 3},
		{"context line after inserted lines", 4, insertion, 6},
		{"after insertion hunk", 10, insertion, 12},
		{"deleted line maps to the line before", 3, deletion, 2},
		{"second deleted line maps to the line before", 4, deletion, 2},
		{"after deletion hunk", 5, deletion, 3},
		{"first replaced line", 5, replacement, 5},
		{"second replaced line", 6, replacement, 5},
		{"after replacement", 7, replacement, 8},
		{"between hunks", 5, multiple, 6},
		{"context in second hunk", 10, multiple, 11},
		{"deleted at the end of the second hunk", 11, multiple, 11},
		{"deleted first line of the file", 1, firstLine, 1},
		{"after all hunks", 20, multiple, 20},
		{"no-newline marker", 1, noNewline, 1},
		{"no hunks", 7, nil, 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shiftLine(test.line, test.hunks); got != test.want {
				t.Fatalf("shiftLine(%d) = %d, want %d", test.line, got, test.want)
			}
		})
	}
}

func TestChangedLines(t *testing.T) {
	tests := []struct {
		name  string
		hunks []patchHunk
		want  []int
	}{
		{"insertion", []patchHunk{{NewStart: 3, Lines: []string{" a", "+x", "+y", " b"}}}, []int{4, 5}},
		{"deletion", []patchHunk{{NewStart: 2, Lines: []string{" a", "-b", "-c", " d"}}}, []int{2, 2}},
		{"deletion at the end of a hunk", []patchHunk{{NewStart: 4, Lines: []string{" a", "-b"}}}, []int{4}},
		{"deletion of the first line", []patchHunk{{NewStart: 1, Lines: []string{"-a", " b"}}}, []int{1}},
		{"replacement", []patchHunk{{NewStart: 5, Lines: []string{"-o", "+n1", "+n2"}}}, []int{5, 5, 6}},
		{"multiple hunks", []patchHunk{
			{NewStart: 2, Lines: []string{" a", "+x"}},
			{NewStart: 11, Lines: []string{" k", "-l", `\ No newline at end of file`, "+m"}},
		}, []int{3, 12, 12}},
		{"no hunks", nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := changedLines(test.hunks); !slices.Equal(got, test.want) {
				t.Fatalf("changedLines = %v, want %v", got, test.want)
			}
		})
	}
}

func TestOutlineFileMatchesAnalyzeKeys(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "py/app.py", `
class Worker:
    def run(self):
        def nested():
            return 1
        return nested()

def helper():
    return Worker().run()
`)
	writeFixture(t, root, "go.mod", "module example.com/demo\n\ngo 1.24\n")
	writeFixture(t, root, "gopkg/server.go", `
package gopkg

type Server struct{}

func (s *Server) Start() { helper() }

func helper() {}
`)
	writeFixture(t, root, "java/com/example/Service.java", `
package com.example;

public class Service {
    public Service() {}

    public void handle(String value) {
        clean(value);
    }

    private static String clean(String value) {
        return value;
    }
}
`)
	writeFixture(t, root, "rust/src/lib.rs", `
mod inner {
    pub fn inside() {}
}

pub struct Worker;

impl Worker {
    pub fn build() {
        inner::inside();
    }
}
`)
	writeFixture(t, root, "ts/app.ts", `
export class Worker {
    constructor() {}
    run(): void {}
}

export const local = (): void => {
    new Worker().run();
};
`)
	graph, err := Analyze(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := make(map[string][]string)
	for _, function := range graph.Nodes {
		want[function.File] = append(want[function.File], activityKey(function.File, function.QualifiedName))
	}
	if len(want) != 5 {
		t.Fatalf("analysis found callables in %d files, want 5", len(want))
	}
	for file, keys := range want {
		functions, err := outlineFile(root, filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Fatalf("outline %s: %v", file, err)
		}
		var got []string
		for _, function := range functions {
			got = append(got, activityKey(function.File, function.QualifiedName))
		}
		slices.Sort(got)
		slices.Sort(keys)
		if !slices.Equal(got, keys) {
			t.Fatalf("outline keys for %s = %q, want %q", file, got, keys)
		}
	}
}

func TestOutlineFileSkipsUnsupportedLanguages(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "README.md", "# Title\n")
	functions, err := outlineFile(root, filepath.Join(root, "README.md"))
	if err != nil || functions != nil {
		t.Fatalf("outlineFile = %v, %v, want no callables and no error", functions, err)
	}
}

func TestActivityMapperForwardMapsEarlierEvents(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after the edit below. Before the edit,
	// second was on lines 4 and 5.
	writeFixture(t, root, "app.py", `
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
	read := toolEvent{Time: start, Agent: "main", Tool: "Read", Path: path, Lines: []int{4, 5}}
	hunks := []patchHunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def first():", "+    a = 1", "+    b = 2", "+    c = 3", "     return 1"}}}
	edit := toolEvent{Time: start.Add(time.Second), Agent: "agent-1", Tool: "Edit", Path: path, Lines: changedLines(hunks), Hunks: hunks}

	mapper := newActivityMapper(root)
	// Events from different transcripts arrive out of order.
	activities := mapper.mapEvents([]toolEvent{edit, read})
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
	activities = mapper.mapEvents([]toolEvent{late})
	assertActivity(t, activities[0], "read", "Read", 7, "second")

	// A read after the edit already uses current line numbers.
	current := toolEvent{Time: start.Add(2 * time.Second), Tool: "Read", Path: path, Lines: []int{4}}
	activities = mapper.mapEvents([]toolEvent{current})
	assertActivity(t, activities[0], "read", "Read", 4, "first")
}

func TestActivityMapperOrdersEditsAcrossBatches(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after both edits below.
	writeFixture(t, root, "app.py", `
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
	read := toolEvent{Time: start, Tool: "Read", Path: path, Lines: []int{3}}
	firstHunks := []patchHunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def first():", "+    a = 1", "+    b = 2", "+    c = 3", "     return 1"}}}
	firstEdit := toolEvent{Time: start.Add(time.Second), Tool: "Edit", Path: path, Lines: changedLines(firstHunks), Hunks: firstHunks}
	secondHunks := []patchHunk{{OldStart: 5, OldLines: 2, NewStart: 5, NewLines: 4, Lines: []string{"     return 1", "+def middle():", "+    return 0", " def second():"}}}
	secondEdit := toolEvent{Time: start.Add(2 * time.Second), Tool: "Edit", Path: path, Lines: changedLines(secondHunks), Hunks: secondHunks}

	mapper := newActivityMapper(root)
	assertActivity(t, mapper.mapEvents([]toolEvent{secondEdit})[0], "edit", "Edit", 6, "middle")
	// The earlier edit arrives in a later batch. The read must move through
	// the first edit and then the second edit.
	activities := mapper.mapEvents([]toolEvent{firstEdit, read})
	assertActivity(t, activities[0], "read", "Read", 8, "second")
	assertActivity(t, activities[1], "edit", "Edit", 2, "first")

	// Edits with equal timestamps keep their arrival order.
	firstEdit.Time = secondEdit.Time
	activities = newActivityMapper(root).mapEvents([]toolEvent{read, firstEdit, secondEdit})
	assertActivity(t, activities[0], "read", "Read", 8, "second")
}

func TestActivityMapperKeepsEditsPerFile(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.py", "def a():\n    x = 1\n    y = 2\n    z = 3\n    return 1\n")
	writeFixture(t, root, "b.py", "def b():\n    return 2\n")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	hunks := []patchHunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 5, Lines: []string{" def a():", "+    x = 1", "+    y = 2", "+    z = 3", "     return 1"}}}
	read := toolEvent{Time: start, Tool: "Read", Path: filepath.Join(root, "b.py"), Lines: []int{2}}
	edit := toolEvent{Time: start.Add(time.Second), Tool: "Edit", Path: filepath.Join(root, "a.py"), Lines: changedLines(hunks), Hunks: hunks}
	activities := newActivityMapper(root).mapEvents([]toolEvent{read, edit})
	assertActivity(t, activities[0], "read", "Read", 2, "b")
}

func TestActivityMapperAttributesDeletionsToTheEnclosingFunction(t *testing.T) {
	root := t.TempDir()
	// Each file on disk is the state after its edit, which deletes "b = 2".
	writeFixture(t, root, "before_next.py", "def f():\n    a = 1\ndef g():\n    pass\n")
	writeFixture(t, root, "at_end.py", "def f():\n    a = 1\n")
	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		file  string
		hunks []patchHunk
	}{
		{"before_next.py", []patchHunk{{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 3, Lines: []string{" def f():", "     a = 1", "-    b = 2", " def g():"}}}},
		{"at_end.py", []patchHunk{{OldStart: 1, OldLines: 3, NewStart: 1, NewLines: 2, Lines: []string{" def f():", "     a = 1", "-    b = 2"}}}},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			path := filepath.Join(root, test.file)
			read := toolEvent{Time: start, Tool: "Read", Path: path, Lines: []int{3}}
			edit := toolEvent{Time: start.Add(time.Second), Tool: "Edit", Path: path, Lines: changedLines(test.hunks), Hunks: test.hunks}
			activities := newActivityMapper(root).mapEvents([]toolEvent{read, edit})
			assertActivity(t, activities[0], "read", "Read", 2, "f")
			assertActivity(t, activities[1], "edit", "Edit", 2, "f")
		})
	}
}

func TestActivityMapperRecordsTheHunksOfEachFunction(t *testing.T) {
	root := t.TempDir()
	// The file on disk is the state after the edit below.
	writeFixture(t, root, "app.py", "def first():\n    a = 1\n    x = 0\n    return 1\n\ndef second():\n    b = 2\n    return 2\n")
	path := filepath.Join(root, "app.py")
	hunks := []patchHunk{
		{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" def first():", "+    a = 1"}},
		{OldStart: 2, OldLines: 1, NewStart: 3, NewLines: 2, Lines: []string{"+    x = 0", "     return 1"}},
		{OldStart: 4, OldLines: 2, NewStart: 6, NewLines: 3, Lines: []string{" def second():", "+    b = 2", "     return 2"}},
	}
	edit := toolEvent{Tool: "Write", Path: path, Lines: changedLines(hunks), Hunks: hunks}
	activity := newActivityMapper(root).mapEvents([]toolEvent{edit})[0]
	want := []ActivityFunction{
		{Key: activityKey("app.py", "app.first"), Name: "first", Line: 1, Hunks: []int{0, 1}},
		{Key: activityKey("app.py", "app.second"), Name: "second", Line: 6, Hunks: []int{2}},
	}
	if !reflect.DeepEqual(activity.Functions, want) || !reflect.DeepEqual(activity.Hunks, hunks) || activity.Created {
		t.Fatalf("activity = %+v, want functions %+v and the event hunks", activity, want)
	}

	// A hunk with changed lines in two callables goes to each callable once.
	writeFixture(t, root, "one.py", "def first():\n    a = 1\n    b = 2\n\ndef second():\n    c = 3\n    return 3\n")
	hunks = []patchHunk{{OldStart: 1, OldLines: 4, NewStart: 1, NewLines: 7, Lines: []string{" def first():", "+    a = 1", "+    b = 2", " ", " def second():", "+    c = 3", "     return 3"}}}
	edit = toolEvent{Tool: "Edit", Path: filepath.Join(root, "one.py"), Lines: changedLines(hunks), Hunks: hunks}
	activity = newActivityMapper(root).mapEvents([]toolEvent{edit})[0]
	want = []ActivityFunction{
		{Key: activityKey("one.py", "one.first"), Name: "first", Line: 1, Hunks: []int{0}},
		{Key: activityKey("one.py", "one.second"), Name: "second", Line: 5, Hunks: []int{0}},
	}
	if !reflect.DeepEqual(activity.Functions, want) {
		t.Fatalf("one hunk functions = %+v, want %+v", activity.Functions, want)
	}

	// A created file and a read have no hunks.
	for _, event := range []toolEvent{
		{Tool: "Write", Path: path, Lines: []int{1, 2, 3, 4, 5, 6, 7, 8}, Created: true},
		{Tool: "Read", Path: path, Lines: []int{2, 7}},
	} {
		activity := newActivityMapper(root).mapEvents([]toolEvent{event})[0]
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
	writeFixture(t, root, "broken.py", "def broken(:\n    pass\n")
	writeFixture(t, root, "notes.md", "# Notes\n")
	writeFixture(t, root, "module.py", "import os\n\ndef inside():\n    pass\n")
	mapper := newActivityMapper(root)
	activities := mapper.mapEvents([]toolEvent{
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
	writeFixture(t, root, "app.py", "def old():\n    pass\n")
	path := filepath.Join(root, "app.py")
	mapper := newActivityMapper(root)
	assertActivity(t, mapper.mapEvents([]toolEvent{{Tool: "Read", Path: path, Lines: []int{1}}})[0], "read", "Read", 1, "old")
	// The new content has a different size, so the cached outline is stale.
	writeFixture(t, root, "app.py", "def renamed():\n    pass\n")
	assertActivity(t, mapper.mapEvents([]toolEvent{{Tool: "Read", Path: path, Lines: []int{1}}})[0], "read", "Read", 1, "renamed")
}

func TestInnermostFunctionPrefersNestedCallable(t *testing.T) {
	functions := []Function{
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
