package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var transcriptTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// The expected names come from the encoder in Claude Code 2.1.296.
func TestEncodeProjectPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/Users/a.b/c_d-e", "-Users-a-b-c-d-e"},
		{"/zZ/aA/09/{[`@:/", "-zZ-aA-09-------"},
		{"/Users/a/caf\u00e9/\U0001F600x", "-Users-a-caf----x"},
		{"/" + strings.Repeat("a", 199), "-" + strings.Repeat("a", 199)},
		{"/" + strings.Repeat("a", 250), "-" + strings.Repeat("a", 199) + "-feo44x"},
		{"/" + strings.Repeat("\U0001F600", 60) + "/" + strings.Repeat("b", 100), strings.Repeat("-", 122) + strings.Repeat("b", 78) + "-h34izw"},
	}
	for _, test := range tests {
		if got := encodeProjectPath(test.path); got != test.want {
			t.Errorf("encodeProjectPath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
}

func TestClaudeProjectsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, test := range []struct{ config, want string }{
		{"", filepath.Join(home, ".claude", "projects")},
		{"  ", filepath.Join(home, ".claude", "projects")},
		{"/custom/claude", filepath.Join("/custom/claude", "projects")},
	} {
		t.Setenv("CLAUDE_CONFIG_DIR", test.config)
		if got, err := claudeProjectsDir(); err != nil || got != test.want {
			t.Errorf("CLAUDE_CONFIG_DIR %q: claudeProjectsDir = %q, %v, want %q", test.config, got, err, test.want)
		}
	}
}

func TestTranscriptWatcherFindsLongProjectNames(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), strings.Repeat("a", 120), strings.Repeat("b", 120))
	inside := filepath.Join(root, "sub")
	rootSession := filepath.Join(projects, encodeProjectPath(root), "root.jsonl")
	insideSession := filepath.Join(projects, encodeProjectPath(inside), "inside.jsonl")
	writeTranscript(t, rootSession, sessionStart(root))
	writeTranscript(t, insideSession, sessionStart(inside))

	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	if watcher.files[rootSession] == nil || watcher.files[insideSession] == nil {
		t.Fatalf("followed %v, want %s and %s", slices.Collect(maps.Keys(watcher.files)), rootSession, insideSession)
	}
}

func TestTranscriptWatcherParsesToolResults(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, encodeProjectPath(root), "s1.jsonl")
	source := filepath.Join(root, "a.py")
	created := filepath.Join(root, "new.py")
	patch := []patchHunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" a", "+b"}}}
	writeTranscript(t, session,
		sessionStart(root),
		toolUseLine("r1", "Read"), toolResultLine("r1", readResult(source, 3, 2)),
		toolUseLine("e1", "Edit"), toolResultLine("e1", map[string]any{"filePath": source, "structuredPatch": patch}),
		toolUseLine("w1", "Write"), toolResultLine("w1", map[string]any{"type": "create", "filePath": created, "content": "a\nb", "structuredPatch": []any{}}),
		toolUseLine("w2", "Write"), toolResultLine("w2", map[string]any{"type": "update", "filePath": created, "content": "a\nb\n", "structuredPatch": patch}),
		toolUseLine("e2", "Edit"), withField(toolResultLine("e2", map[string]any{"filePath": source, "structuredPatch": patch}), "is_error", true),
		toolUseLine("e3", "Edit"), toolResultLine("e3", "Error: String to replace not found in file."),
		toolUseLine("r2", "Read"), toolResultLine("r2", readResult(filepath.Join(filepath.Dir(root), "outside.py"), 1, 1)),
		toolUseLine("b1", "Bash"), toolResultLine("b1", map[string]any{"stdout": "ok"}),
		toolUseLine("i1", "Read"), toolResultLine("i1", map[string]any{"type": "image", "file": map[string]any{"filePath": source}}),
		toolResultLine("unpaired", readResult(source, 1, 1)),
	)
	subagent := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "agent-abc.jsonl")
	writeTranscript(t, subagent,
		withAgent(toolUseLine("r3", "Read"), "abc"), withAgent(toolResultLine("r3", readResult(source, 1, 1)), "abc"),
	)

	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	got := watcher.poll()
	want := []toolEvent{
		{Time: transcriptTime, Session: "s1", Agent: "main", Tool: "Read", Path: source, Lines: []int{3, 4}},
		{Time: transcriptTime, Session: "s1", Agent: "main", Tool: "Edit", Path: source, Lines: []int{2}, Hunks: patch},
		{Time: transcriptTime, Session: "s1", Agent: "main", Tool: "Write", Path: created, Lines: []int{1, 2}, Created: true},
		{Time: transcriptTime, Session: "s1", Agent: "main", Tool: "Write", Path: created, Lines: []int{2}, Hunks: patch},
		{Time: transcriptTime, Session: "s1", Agent: "abc", Tool: "Read", Path: source, Lines: []int{1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events =\n%+v\nwant\n%+v", got, want)
	}

	// A line without its newline stays buffered until the rest arrives.
	result, err := json.Marshal(toolResultLine("r4", readResult(source, 5, 1)))
	if err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, session, mustJSON(t, toolUseLine("r4", "Read"))+"\n"+string(result[:20]))
	if events := watcher.poll(); len(events) != 0 {
		t.Fatalf("partial line produced events %+v", events)
	}
	appendTranscript(t, session, string(result[20:40]))
	if events := watcher.poll(); len(events) != 0 {
		t.Fatalf("second part of the line produced events %+v", events)
	}
	appendTranscript(t, session, string(result[40:])+"\n")
	want = []toolEvent{{Time: transcriptTime, Session: "s1", Agent: "main", Tool: "Read", Path: source, Lines: []int{5}}}
	if events := watcher.poll(); !reflect.DeepEqual(events, want) {
		t.Fatalf("completed line events = %+v, want %+v", events, want)
	}
}

func TestTranscriptWatcherRecordsTaskNotes(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, encodeProjectPath(root), "s1.jsonl")
	userLine := func(content any, fields map[string]any) map[string]any {
		line := map[string]any{"type": "user", "sessionId": "s1", "timestamp": transcriptTime, "message": map[string]any{"content": content}}
		maps.Copy(line, fields)
		return line
	}
	toolUse := func(id string, name string, input map[string]any) map[string]any {
		line := toolUseLine(id, name)
		withField(line, "input", input)
		return line
	}
	writeTranscript(t, session,
		sessionStart(root),
		map[string]any{"type": "ai-title", "sessionId": "s1", "aiTitle": "Fix the parser"},
		map[string]any{"type": "ai-title", "sessionId": "s1", "aiTitle": ""},
		userLine([]any{map[string]any{"type": "text", "text": "  first part"}, map[string]any{"type": "image"}, map[string]any{"type": "text", "text": "second part"}}, nil),
		userLine("system reminder", map[string]any{"isMeta": true}),
		userLine("summary of earlier work", map[string]any{"isCompactSummary": true}),
		userLine("<command-name>/skills</command-name>", nil),
		userLine([]any{map[string]any{"type": "text", "text": "[Request interrupted by user]"}}, nil),
		userLine(strings.Repeat("é", maxNoteText+5), nil),
		userLine(strings.Repeat("a", maxNoteText), nil),
		userLine("<pasted_content id=\"p1\">\nlong log\n</pasted_content>\n fix this", nil),
		userLine(`<pasted_content id="p2">only pasted text</pasted_content>`, nil),
		userLine([]any{map[string]any{"type": "tool_result", "tool_use_id": "b0"}, map[string]any{"type": "text", "text": "after the result"}}, nil),
		toolUse("b1", "Bash", map[string]any{"command": "go test ./...", "description": "Run the tests"}),
		// Assistant text is not a request.
		map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": transcriptTime, "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "I will build it."},
			map[string]any{"type": "tool_use", "id": "b2", "name": "Bash", "input": map[string]any{"description": "Build"}},
		}}},
		toolUse("r1", "Read", map[string]any{"file_path": filepath.Join(root, "pkg", "a.go")}),
		toolUse("r2", "Read", map[string]any{"file_path": "/elsewhere/notes.md"}),
		toolUse("g1", "Grep", map[string]any{"pattern": "func main"}),
		toolUse("t1", "TaskList", map[string]any{}),
		toolUseLine("x1", "NoInput"),
	)
	subagents := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	writeTranscript(t, filepath.Join(subagents, "agent-abc.jsonl"),
		withAgent(userLine("the subagent prompt", nil), "abc"),
		withAgent(toolUse("s1", "Bash", map[string]any{"description": "List files"}), "abc"),
	)
	if err := os.WriteFile(filepath.Join(subagents, "agent-abc.meta.json"), []byte(`{"agentType":"Explore","description":"Find the parser"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, filepath.Join(subagents, "agent-nometa.jsonl"))
	// Meta files without a description add no note.
	for name, meta := range map[string]string{"agent-empty": `{"agentType":"Explore","description":""}`, "agent-partial": ``} {
		writeTranscript(t, filepath.Join(subagents, name+".jsonl"))
		if err := os.WriteFile(filepath.Join(subagents, name+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	watcher.poll()
	note := func(agent string, kind string, text string) taskNote {
		return taskNote{Time: transcriptTime, Session: "s1", Agent: agent, Kind: kind, Text: text}
	}
	want := []taskNote{
		{Session: "s1", Agent: "abc", Kind: "subagent", Text: "Find the parser"},
		note("main", "request", "start"),
		{Session: "s1", Agent: "main", Kind: "title", Text: "Fix the parser"},
		note("main", "request", "first part\nsecond part"),
		note("main", "request", strings.Repeat("é", maxNoteText-1)+"…"),
		note("main", "request", strings.Repeat("a", maxNoteText)),
		note("main", "request", "fix this"),
		note("main", "request", "[pasted content]"),
		note("main", "step", "Run the tests"),
		note("main", "step", "Build"),
		note("main", "step", "Read pkg/a.go"),
		note("main", "step", "Read notes.md"),
		note("main", "step", "Grep func main"),
		note("main", "step", "TaskList"),
		note("main", "step", "NoInput"),
		note("abc", "step", "List files"),
	}
	if got := watcher.takeNotes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes =\n%+v\nwant\n%+v", got, want)
	}
	if got := watcher.takeNotes(); len(got) != 0 {
		t.Fatalf("second takeNotes = %+v, want none", got)
	}

	// A meta file that appears or becomes complete after a scan adds its note at
	// a later scan, once.
	for name, meta := range map[string]string{"agent-nometa": `{"description":"Late meta"}`, "agent-partial": `{"description":"Complete meta"}`} {
		if err := os.WriteFile(filepath.Join(subagents, name+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	watcher.scan()
	want = []taskNote{
		{Session: "s1", Agent: "nometa", Kind: "subagent", Text: "Late meta"},
		{Session: "s1", Agent: "partial", Kind: "subagent", Text: "Complete meta"},
	}
	if got := watcher.takeNotes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes after the late meta file = %+v, want %+v", got, want)
	}
	watcher.scan()
	if got := watcher.takeNotes(); len(got) != 0 {
		t.Fatalf("notes after a second scan = %+v, want none", got)
	}
	if got := watcher.sessionIDs(); !reflect.DeepEqual(got, []string{"s1"}) {
		t.Fatalf("sessionIDs = %v, want [s1]", got)
	}
}

func TestTranscriptWatcherFollowsRelatedSessions(t *testing.T) {
	projects := t.TempDir()
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	source := filepath.Join(root, "a.py")
	project := filepath.Join(projects, encodeProjectPath(root))
	now := time.Now()

	older := filepath.Join(project, "older.jsonl")
	writeTranscript(t, older, sessionStart(root), toolUseLine("old", "Read"), toolResultLine("old", readResult(source, 1, 1)))
	olderSubagent := filepath.Join(project, "older", "subagents", "agent-x.jsonl")
	writeTranscript(t, olderSubagent, withAgent(toolUseLine("oldsub", "Read"), "x"), withAgent(toolResultLine("oldsub", readResult(source, 1, 1)), "x"))
	setModified(t, older, now.Add(-time.Hour))

	// The first line of the replayed session is longer than a default scanner buffer.
	inside := filepath.Join(project, "inside.jsonl")
	writeTranscript(t, inside,
		map[string]any{"type": "summary", "summary": strings.Repeat("x", 100_000)},
		sessionStart(filepath.Join(root, "sub")),
		toolUseLine("in", "Read"), toolResultLine("in", readResult(source, 2, 1)),
	)
	setModified(t, inside, now.Add(-30*time.Minute))

	// A newer session in an ancestor directory loses to a session inside root.
	parent := filepath.Join(projects, encodeProjectPath(base), "parent.jsonl")
	writeTranscript(t, parent, sessionStart(base), toolUseLine("par", "Read"), toolResultLine("par", readResult(source, 3, 1)))

	// The encoded name extends root's, but the session runs elsewhere.
	unrelated := filepath.Join(projects, encodeProjectPath(root)+"-other", "unrelated.jsonl")
	writeTranscript(t, unrelated, sessionStart(root+"-other"), toolUseLine("un", "Read"), toolResultLine("un", readResult(source, 4, 1)))

	empty := filepath.Join(project, "empty.jsonl")
	writeTranscript(t, empty)

	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	assertReadLines(t, watcher.poll(), 2)

	appendTranscript(t, older, mustJSON(t, toolUseLine("old2", "Read"))+"\n"+mustJSON(t, toolResultLine("old2", readResult(source, 10, 1)))+"\n")
	appendTranscript(t, olderSubagent, mustJSON(t, withAgent(toolUseLine("oldsub2", "Read"), "x"))+"\n"+mustJSON(t, withAgent(toolResultLine("oldsub2", readResult(source, 11, 1)), "x"))+"\n")
	appendTranscript(t, parent, mustJSON(t, toolUseLine("par2", "Read"))+"\n"+mustJSON(t, toolResultLine("par2", readResult(source, 12, 1)))+"\n")
	appendTranscript(t, unrelated, mustJSON(t, toolUseLine("un2", "Read"))+"\n"+mustJSON(t, toolResultLine("un2", readResult(source, 13, 1)))+"\n")
	assertReadLines(t, watcher.poll(), 10, 11, 12)

	// Sessions that appear or get a cwd after the first scan replay from the start.
	appendTranscript(t, empty, mustJSON(t, sessionStart(root))+"\n"+mustJSON(t, toolUseLine("em", "Read"))+"\n"+mustJSON(t, toolResultLine("em", readResult(source, 20, 1)))+"\n")
	added := filepath.Join(project, "added.jsonl")
	writeTranscript(t, added, sessionStart(root), toolUseLine("ad", "Read"), toolResultLine("ad", readResult(source, 21, 1)))
	addedSubagent := filepath.Join(project, "added", "subagents", "agent-y.jsonl")
	writeTranscript(t, addedSubagent, withAgent(toolUseLine("adsub", "Read"), "y"), withAgent(toolResultLine("adsub", readResult(source, 22, 1)), "y"))
	watcher.scan()
	assertReadLines(t, watcher.poll(), 21, 22, 20)
}

func TestTranscriptWatcherRereadsShortenedTranscripts(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	source := filepath.Join(root, "a.py")
	session := filepath.Join(projects, encodeProjectPath(root), "s1.jsonl")
	writeTranscript(t, session,
		sessionStart(root),
		toolUseLine("r1", "Read"), toolResultLine("r1", readResult(source, 1, 1)),
		toolUseLine("r2", "Read"), toolResultLine("r2", readResult(source, 2, 1)),
	)
	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	assertReadLines(t, watcher.poll(), 1, 2)
	// The incomplete line from before the rewrite must not join the new first line.
	appendTranscript(t, session, `{"type":"user","message":`)
	assertReadLines(t, watcher.poll())

	writeTranscript(t, session, toolUseLine("r3", "Read"), toolResultLine("r3", readResult(source, 3, 1)))
	appendTranscript(t, session, mustJSON(t, toolUseLine("r4", "Read"))+"\n"+mustJSON(t, toolResultLine("r4", readResult(source, 4, 1)))+"\n")
	assertReadLines(t, watcher.poll(), 3, 4)
}

func TestTranscriptWatcherStopsFollowingRemovedTranscripts(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, encodeProjectPath(root), "s1.jsonl")
	writeTranscript(t, session, sessionStart(root))
	watcher := newTranscriptWatcher(projects, root)
	watcher.scan()
	if err := os.Remove(session); err != nil {
		t.Fatal(err)
	}
	if events := watcher.poll(); len(events) != 0 {
		t.Fatalf("removed transcript produced events %+v", events)
	}
	watcher.scan()
	if len(watcher.files) != 0 {
		t.Fatalf("still following %v after removal", slices.Collect(maps.Keys(watcher.files)))
	}
}

func assertReadLines(t *testing.T, events []toolEvent, want ...int) {
	t.Helper()
	var got []int
	for _, event := range events {
		got = append(got, event.Lines...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read lines = %v, want %v", got, want)
	}
}

func sessionStart(cwd string) map[string]any {
	return map[string]any{"type": "user", "cwd": cwd, "sessionId": "s1", "timestamp": transcriptTime, "message": map[string]any{"content": "start"}}
}

func toolUseLine(id string, name string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"sessionId": "s1",
		"timestamp": transcriptTime,
		"message":   map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name}}},
	}
}

func toolResultLine(id string, result any) map[string]any {
	return map[string]any{
		"type":          "user",
		"sessionId":     "s1",
		"timestamp":     transcriptTime,
		"message":       map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id}}},
		"toolUseResult": result,
	}
}

func readResult(path string, start int, count int) map[string]any {
	return map[string]any{"type": "text", "file": map[string]any{"filePath": path, "startLine": start, "numLines": count}}
}

// withField sets a field on the line's first content block.
func withField(line map[string]any, key string, value any) map[string]any {
	line["message"].(map[string]any)["content"].([]any)[0].(map[string]any)[key] = value
	return line
}

func withAgent(line map[string]any, agent string) map[string]any {
	line["agentId"] = agent
	line["isSidechain"] = true
	return line
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTranscript(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(mustJSON(t, line))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTranscript(t *testing.T, path string, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func setModified(t *testing.T, path string, modified time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}
