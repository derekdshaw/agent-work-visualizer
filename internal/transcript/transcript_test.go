package transcript

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

	"github.com/derek-shaw/agent-work-visualizer/internal/transcript/transcripttest"
)

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
		if got := EncodeProjectPath(test.path); got != test.want {
			t.Errorf("EncodeProjectPath(%q) = %q, want %q", test.path, got, test.want)
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
		if got, err := ProjectsDir(); err != nil || got != test.want {
			t.Errorf("CLAUDE_CONFIG_DIR %q: ProjectsDir = %q, %v, want %q", test.config, got, err, test.want)
		}
	}
}

func TestTranscriptWatcherFindsLongProjectNames(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), strings.Repeat("a", 120), strings.Repeat("b", 120))
	inside := filepath.Join(root, "sub")
	rootSession := filepath.Join(projects, EncodeProjectPath(root), "root.jsonl")
	insideSession := filepath.Join(projects, EncodeProjectPath(inside), "inside.jsonl")
	transcripttest.WriteTranscript(t, rootSession, transcripttest.SessionStart(root))
	transcripttest.WriteTranscript(t, insideSession, transcripttest.SessionStart(inside))

	watcher := NewWatcher(projects, root)
	watcher.Scan()
	if watcher.files[rootSession] == nil || watcher.files[insideSession] == nil {
		t.Fatalf("followed %v, want %s and %s", slices.Collect(maps.Keys(watcher.files)), rootSession, insideSession)
	}
}

func TestTranscriptWatcherParsesToolResults(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	source := filepath.Join(root, "a.py")
	created := filepath.Join(root, "new.py")
	patch := []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" a", "+b"}}}
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		transcripttest.ToolUseLine("r1", "Read"), transcripttest.ToolResultLine("r1", transcripttest.ReadResult(source, 3, 2)),
		transcripttest.ToolUseLine("e1", "Edit"), transcripttest.ToolResultLine("e1", map[string]any{"filePath": source, "structuredPatch": patch}),
		transcripttest.ToolUseLine("w1", "Write"), transcripttest.ToolResultLine("w1", map[string]any{"type": "create", "filePath": created, "content": "a\nb", "structuredPatch": []any{}}),
		transcripttest.ToolUseLine("w2", "Write"), transcripttest.ToolResultLine("w2", map[string]any{"type": "update", "filePath": created, "content": "a\nb\n", "structuredPatch": patch}),
		transcripttest.ToolUseLine("e2", "Edit"), transcripttest.WithField(transcripttest.ToolResultLine("e2", map[string]any{"filePath": source, "structuredPatch": patch}), "is_error", true),
		transcripttest.ToolUseLine("e3", "Edit"), transcripttest.ToolResultLine("e3", "Error: String to replace not found in file."),
		transcripttest.ToolUseLine("r2", "Read"), transcripttest.ToolResultLine("r2", transcripttest.ReadResult(filepath.Join(filepath.Dir(root), "outside.py"), 1, 1)),
		transcripttest.ToolUseLine("b1", "Bash"), transcripttest.ToolResultLine("b1", map[string]any{"stdout": "ok"}),
		transcripttest.ToolUseLine("i1", "Read"), transcripttest.ToolResultLine("i1", map[string]any{"type": "image", "file": map[string]any{"filePath": source}}),
		transcripttest.ToolResultLine("unpaired", transcripttest.ReadResult(source, 1, 1)),
	)
	subagent := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents", "agent-abc.jsonl")
	transcripttest.WriteTranscript(t, subagent,
		transcripttest.WithAgent(transcripttest.ToolUseLine("r3", "Read"), "abc"), transcripttest.WithAgent(transcripttest.ToolResultLine("r3", transcripttest.ReadResult(source, 1, 1)), "abc"),
	)

	watcher := NewWatcher(projects, root)
	watcher.Scan()
	got := watcher.Poll()
	want := []Event{
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Read", Path: source, Lines: []int{3, 4}},
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Edit", Path: source, Lines: []int{2}, Hunks: patch},
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Write", Path: created, Lines: []int{1, 2}, Created: true, Content: "a\nb"},
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Write", Path: created, Lines: []int{2}, Hunks: patch},
		{Time: transcripttest.Time, Session: "s1", Agent: "abc", Tool: "Read", Path: source, Lines: []int{1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events =\n%+v\nwant\n%+v", got, want)
	}

	// A line without its newline stays buffered until the rest arrives.
	result, err := json.Marshal(transcripttest.ToolResultLine("r4", transcripttest.ReadResult(source, 5, 1)))
	if err != nil {
		t.Fatal(err)
	}
	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("r4", "Read"))+"\n"+string(result[:20]))
	if events := watcher.Poll(); len(events) != 0 {
		t.Fatalf("partial line produced events %+v", events)
	}
	transcripttest.AppendTranscript(t, session, string(result[20:40]))
	if events := watcher.Poll(); len(events) != 0 {
		t.Fatalf("second part of the line produced events %+v", events)
	}
	transcripttest.AppendTranscript(t, session, string(result[40:])+"\n")
	want = []Event{{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Read", Path: source, Lines: []int{5}}}
	if events := watcher.Poll(); !reflect.DeepEqual(events, want) {
		t.Fatalf("completed line events = %+v, want %+v", events, want)
	}
}

func TestTranscriptWatcherRecordsTaskNotes(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	userLine := func(content any, fields map[string]any) map[string]any {
		line := map[string]any{"type": "user", "sessionId": "s1", "timestamp": transcripttest.Time, "message": map[string]any{"content": content}}
		maps.Copy(line, fields)
		return line
	}
	toolUse := func(id string, name string, input map[string]any) map[string]any {
		line := transcripttest.ToolUseLine(id, name)
		transcripttest.WithField(line, "input", input)
		return line
	}
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		map[string]any{"type": "ai-title", "sessionId": "s1", "aiTitle": "Fix the parser"},
		map[string]any{"type": "ai-title", "sessionId": "s1", "aiTitle": ""},
		map[string]any{"type": "ai-title", "sessionId": "s1", "aiTitle": "Fix the parser"},
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
		// Assistant text is a plan, not a request.
		map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": transcripttest.Time, "message": map[string]any{"content": []any{
			map[string]any{"type": "text", "text": "I will build it."},
			map[string]any{"type": "text", "text": "  "},
			map[string]any{"type": "tool_use", "id": "b2", "name": "Bash", "input": map[string]any{"description": "Build"}},
		}}},
		transcripttest.ToolResultLine("b1", map[string]any{"stdout": "ok"}),
		transcripttest.WithField(transcripttest.ToolResultLine("b2", "Exit code 1"), "is_error", true),
		toolUse("b3", "Bash", map[string]any{"command": "  go vet ./...\necho done"}),
		toolUse("r1", "Read", map[string]any{"file_path": filepath.Join(root, "pkg", "a.go")}),
		toolUse("r2", "Read", map[string]any{"file_path": "/elsewhere/notes.md"}),
		toolUse("g1", "Grep", map[string]any{"pattern": "func main"}),
		toolUse("t1", "TaskList", map[string]any{}),
		transcripttest.ToolUseLine("x1", "NoInput"),
	)
	subagents := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	transcripttest.WriteTranscript(t, filepath.Join(subagents, "agent-abc.jsonl"),
		transcripttest.WithAgent(userLine("the subagent prompt", nil), "abc"),
		transcripttest.WithAgent(toolUse("s1", "Bash", map[string]any{"description": "List files"}), "abc"),
	)
	if err := os.WriteFile(filepath.Join(subagents, "agent-abc.meta.json"), []byte(`{"agentType":"Explore","description":"Find the parser"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	transcripttest.WriteTranscript(t, filepath.Join(subagents, "agent-nometa.jsonl"))
	// Meta files without a description add no note.
	for name, meta := range map[string]string{"agent-empty": `{"agentType":"Explore","description":""}`, "agent-partial": ``} {
		transcripttest.WriteTranscript(t, filepath.Join(subagents, name+".jsonl"))
		if err := os.WriteFile(filepath.Join(subagents, name+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	watcher := NewWatcher(projects, root)
	watcher.Scan()
	watcher.Poll()
	note := func(agent string, kind string, text string) Note {
		return Note{Time: transcripttest.Time, Session: "s1", Agent: agent, Kind: kind, Text: text}
	}
	want := []Note{
		{Session: "s1", Agent: "abc", Kind: "subagent", Text: "Find the parser"},
		note("main", "request", "start"),
		{Session: "s1", Agent: "main", Kind: "title", Text: "Fix the parser"},
		note("main", "request", "first part\nsecond part"),
		note("main", "request", strings.Repeat("é", maxNoteText-1)+"…"),
		note("main", "request", strings.Repeat("a", maxNoteText)),
		note("main", "request", "fix this"),
		note("main", "request", "[pasted content]"),
		note("main", "check_start", "Run the tests"),
		note("main", "plan", "I will build it."),
		note("main", "check_start", "Build"),
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Kind: "check_end", OK: true},
		{Time: transcripttest.Time, Session: "s1", Agent: "main", Kind: "check_end"},
		note("main", "check_start", "go vet ./..."),
		note("main", "step", "Grep func main"),
		note("main", "step", "TaskList"),
		note("main", "step", "NoInput"),
		note("abc", "check_start", "List files"),
	}
	if got := watcher.TakeNotes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes =\n%+v\nwant\n%+v", got, want)
	}
	if got := watcher.TakeNotes(); len(got) != 0 {
		t.Fatalf("second takeNotes = %+v, want none", got)
	}

	// A meta file that appears or becomes complete after a scan adds its note at
	// a later scan, once.
	for name, meta := range map[string]string{"agent-nometa": `{"description":"Late meta"}`, "agent-partial": `{"description":"Complete meta"}`} {
		if err := os.WriteFile(filepath.Join(subagents, name+".meta.json"), []byte(meta), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	watcher.Scan()
	want = []Note{
		{Session: "s1", Agent: "nometa", Kind: "subagent", Text: "Late meta"},
		{Session: "s1", Agent: "partial", Kind: "subagent", Text: "Complete meta"},
	}
	if got := watcher.TakeNotes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("notes after the late meta file = %+v, want %+v", got, want)
	}
	watcher.Scan()
	if got := watcher.TakeNotes(); len(got) != 0 {
		t.Fatalf("notes after a second scan = %+v, want none", got)
	}
	if got := watcher.SessionIDs(); !reflect.DeepEqual(got, []string{"s1"}) {
		t.Fatalf("sessionIDs = %v, want [s1]", got)
	}
}

func TestTranscriptWatcherFollowsRelatedSessions(t *testing.T) {
	projects := t.TempDir()
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	source := filepath.Join(root, "a.py")
	project := filepath.Join(projects, EncodeProjectPath(root))
	now := time.Now()

	older := filepath.Join(project, "older.jsonl")
	transcripttest.WriteTranscript(t, older, transcripttest.SessionStart(root), transcripttest.ToolUseLine("old", "Read"), transcripttest.ToolResultLine("old", transcripttest.ReadResult(source, 1, 1)))
	olderSubagent := filepath.Join(project, "older", "subagents", "agent-x.jsonl")
	transcripttest.WriteTranscript(t, olderSubagent, transcripttest.WithAgent(transcripttest.ToolUseLine("oldsub", "Read"), "x"), transcripttest.WithAgent(transcripttest.ToolResultLine("oldsub", transcripttest.ReadResult(source, 1, 1)), "x"))
	transcripttest.SetModified(t, older, now.Add(-time.Hour))

	// The first line of the replayed session is longer than a default scanner buffer.
	inside := filepath.Join(project, "inside.jsonl")
	transcripttest.WriteTranscript(t, inside,
		map[string]any{"type": "summary", "summary": strings.Repeat("x", 100_000)},
		transcripttest.SessionStart(filepath.Join(root, "sub")),
		transcripttest.ToolUseLine("in", "Read"), transcripttest.ToolResultLine("in", transcripttest.ReadResult(source, 2, 1)),
	)
	transcripttest.SetModified(t, inside, now.Add(-30*time.Minute))

	// A newer session in an ancestor directory loses to a session inside root.
	parent := filepath.Join(projects, EncodeProjectPath(base), "parent.jsonl")
	transcripttest.WriteTranscript(t, parent, transcripttest.SessionStart(base), transcripttest.ToolUseLine("par", "Read"), transcripttest.ToolResultLine("par", transcripttest.ReadResult(source, 3, 1)))

	// The encoded name extends root's, but the session runs elsewhere.
	unrelated := filepath.Join(projects, EncodeProjectPath(root)+"-other", "unrelated.jsonl")
	transcripttest.WriteTranscript(t, unrelated, transcripttest.SessionStart(root+"-other"), transcripttest.ToolUseLine("un", "Read"), transcripttest.ToolResultLine("un", transcripttest.ReadResult(source, 4, 1)))

	empty := filepath.Join(project, "empty.jsonl")
	transcripttest.WriteTranscript(t, empty)

	watcher := NewWatcher(projects, root)
	watcher.Scan()
	assertReadLines(t, watcher.Poll(), 2)

	transcripttest.AppendTranscript(t, older, transcripttest.MustJSON(t, transcripttest.ToolUseLine("old2", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("old2", transcripttest.ReadResult(source, 10, 1)))+"\n")
	transcripttest.AppendTranscript(t, olderSubagent, transcripttest.MustJSON(t, transcripttest.WithAgent(transcripttest.ToolUseLine("oldsub2", "Read"), "x"))+"\n"+transcripttest.MustJSON(t, transcripttest.WithAgent(transcripttest.ToolResultLine("oldsub2", transcripttest.ReadResult(source, 11, 1)), "x"))+"\n")
	transcripttest.AppendTranscript(t, parent, transcripttest.MustJSON(t, transcripttest.ToolUseLine("par2", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("par2", transcripttest.ReadResult(source, 12, 1)))+"\n")
	transcripttest.AppendTranscript(t, unrelated, transcripttest.MustJSON(t, transcripttest.ToolUseLine("un2", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("un2", transcripttest.ReadResult(source, 13, 1)))+"\n")
	assertReadLines(t, watcher.Poll(), 10, 11, 12)

	// Sessions that appear or get a cwd after the first scan replay from the start.
	transcripttest.AppendTranscript(t, empty, transcripttest.MustJSON(t, transcripttest.SessionStart(root))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolUseLine("em", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("em", transcripttest.ReadResult(source, 20, 1)))+"\n")
	added := filepath.Join(project, "added.jsonl")
	transcripttest.WriteTranscript(t, added, transcripttest.SessionStart(root), transcripttest.ToolUseLine("ad", "Read"), transcripttest.ToolResultLine("ad", transcripttest.ReadResult(source, 21, 1)))
	addedSubagent := filepath.Join(project, "added", "subagents", "agent-y.jsonl")
	transcripttest.WriteTranscript(t, addedSubagent, transcripttest.WithAgent(transcripttest.ToolUseLine("adsub", "Read"), "y"), transcripttest.WithAgent(transcripttest.ToolResultLine("adsub", transcripttest.ReadResult(source, 22, 1)), "y"))
	watcher.Scan()
	assertReadLines(t, watcher.Poll(), 21, 22, 20)
}

func TestTranscriptWatcherRereadsShortenedTranscripts(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	source := filepath.Join(root, "a.py")
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		transcripttest.ToolUseLine("r1", "Read"), transcripttest.ToolResultLine("r1", transcripttest.ReadResult(source, 1, 1)),
		transcripttest.ToolUseLine("r2", "Read"), transcripttest.ToolResultLine("r2", transcripttest.ReadResult(source, 2, 1)),
	)
	watcher := NewWatcher(projects, root)
	watcher.Scan()
	assertReadLines(t, watcher.Poll(), 1, 2)
	// The incomplete line from before the rewrite must not join the new first line.
	transcripttest.AppendTranscript(t, session, `{"type":"user","message":`)
	assertReadLines(t, watcher.Poll())

	transcripttest.WriteTranscript(t, session, transcripttest.ToolUseLine("r3", "Read"), transcripttest.ToolResultLine("r3", transcripttest.ReadResult(source, 3, 1)))
	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("r4", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("r4", transcripttest.ReadResult(source, 4, 1)))+"\n")
	assertReadLines(t, watcher.Poll(), 3, 4)
}

func TestTranscriptWatcherStopsFollowingRemovedTranscripts(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	transcripttest.WriteTranscript(t, session, transcripttest.SessionStart(root))
	watcher := NewWatcher(projects, root)
	watcher.Scan()
	if err := os.Remove(session); err != nil {
		t.Fatal(err)
	}
	if events := watcher.Poll(); len(events) != 0 {
		t.Fatalf("removed transcript produced events %+v", events)
	}
	watcher.Scan()
	if len(watcher.files) != 0 {
		t.Fatalf("still following %v after removal", slices.Collect(maps.Keys(watcher.files)))
	}
}

func assertReadLines(t *testing.T, events []Event, want ...int) {
	t.Helper()
	var got []int
	for _, event := range events {
		got = append(got, event.Lines...)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read lines = %v, want %v", got, want)
	}
}

func TestTranscriptWatcherNotesThinkingFromTheLineBefore(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	at := func(seconds int) time.Time { return transcripttest.Time.Add(time.Duration(seconds) * time.Second) }
	thinking := func(seconds int, text string) map[string]any {
		return map[string]any{"type": "assistant", "sessionId": "s1", "timestamp": at(seconds), "message": map[string]any{"content": []any{map[string]any{"type": "thinking", "thinking": text, "signature": "sig"}}}}
	}
	result := transcripttest.ToolResultLine("b1", map[string]any{"stdout": ""})
	result["timestamp"] = at(3)
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		result,
		// Claude Code writes attachment lines, the last one at the thought's own time.
		map[string]any{"type": "attachment", "sessionId": "s1", "timestamp": at(4)},
		map[string]any{"type": "attachment", "sessionId": "s1", "timestamp": at(11)},
		thinking(11, "  Check the parser first.  "),
		thinking(12, ""),
	)
	watcher := NewWatcher(projects, root)
	watcher.Scan()
	watcher.Poll()
	var thinks []Note
	for _, note := range watcher.TakeNotes() {
		if note.Kind == "think" {
			thinks = append(thinks, note)
		}
	}
	want := []Note{{Time: at(11), Start: at(3), Session: "s1", Agent: "main", Kind: "think", Text: "Check the parser first."}}
	if !reflect.DeepEqual(thinks, want) {
		t.Fatalf("thinking notes = %+v, want %+v", thinks, want)
	}
}
