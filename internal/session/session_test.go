package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/activity"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
)

var start = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestEventJSONFieldNames(t *testing.T) {
	// web/src/session/events.ts reads these field names.
	ok := false
	tests := map[string]struct {
		value any
		want  string
	}{
		"header": {Header{Type: "session", Root: "/r", Branch: "main", T0: start, GapCap: GapCap},
			`{"t":0,"type":"session","root":"/r","branch":"main","t0":"2026-01-02T03:04:05Z","gapCap":30}`},
		"read": {Event{T: 1.5, At: start, Type: "read", Agent: "main", Path: "a.py", Line: 3, NodeIDs: []string{"a.py\x00a.f"}},
			`{"t":1.5,"at":"2026-01-02T03:04:05Z","type":"read","agent":"main","path":"a.py","line":3,"nodeIds":["a.py\u0000a.f"]}`},
		"edit": {Event{T: 2, At: start, Type: "edit", Agent: "main", Path: "a.py", NodeIDs: []string{"k"}, Created: true,
			Hunks: []transcript.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" a", "+b"}}}},
			`{"t":2,"at":"2026-01-02T03:04:05Z","type":"edit","agent":"main","path":"a.py","nodeIds":["k"],` +
				`"hunks":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" a","+b"]}],"created":true}`},
		"check_end": {Event{T: 3, At: start, Type: "check_end", Agent: "main", OK: &ok},
			`{"t":3,"at":"2026-01-02T03:04:05Z","type":"check_end","agent":"main","ok":false}`},
		"text": {Event{T: 4, At: start, Type: "plan", Agent: "abc", Text: "Fix it"},
			`{"t":4,"at":"2026-01-02T03:04:05Z","type":"plan","agent":"abc","text":"Fix it"}`},
	}
	for name, test := range tests {
		data, err := json.Marshal(test.value)
		if err != nil || string(data) != test.want {
			t.Errorf("%s JSON =\n%s, %v\nwant\n%s", name, data, err, test.want)
		}
	}
}

func TestFromNote(t *testing.T) {
	note := func(kind string, text string, ok bool) transcript.Note {
		return transcript.Note{Time: start, Session: "s1", Agent: "main", Kind: kind, Text: text, OK: ok}
	}
	yes, no := true, false
	tests := []struct {
		note transcript.Note
		want Event
		keep bool
	}{
		{note("title", "Fix", false), Event{At: start, Agent: "main", Type: "title", Text: "Fix"}, true},
		{note("request", "do it", false), Event{At: start, Agent: "main", Type: "prompt", Text: "do it"}, true},
		{note("plan", "I will", false), Event{At: start, Agent: "main", Type: "plan", Text: "I will"}, true},
		{note("step", "Grep x", false), Event{At: start, Agent: "main", Type: "step", Text: "Grep x"}, true},
		{note("subagent", "Find", false), Event{At: start, Agent: "main", Type: "agent", Text: "Find"}, true},
		{note("check_start", "go test", false), Event{At: start, Agent: "main", Type: "check_start", Text: "go test"}, true},
		{note("check_end", "", true), Event{At: start, Agent: "main", Type: "check_end", OK: &yes}, true},
		{note("check_end", "", false), Event{At: start, Agent: "main", Type: "check_end", OK: &no}, true},
		{note("unknown", "x", false), Event{}, false},
	}
	for _, test := range tests {
		got, keep := FromNote(test.note)
		if keep != test.keep || !reflect.DeepEqual(got, test.want) {
			t.Errorf("FromNote(%s) = %+v, %t, want %+v, %t", test.note.Kind, got, keep, test.want, test.keep)
		}
	}
}

func TestFromActivity(t *testing.T) {
	hunks := []transcript.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" a", "+b"}}}
	functions := []activity.Callable{{Key: "a.py\x00a.f", Name: "f"}, {Key: "a.py\x00a.g", Name: "g"}}
	read := FromActivity(activity.Activity{Time: start, Agent: "main", Tool: "Read", Kind: "read", File: "a.py", Line: 4, Functions: functions, Hunks: hunks})
	if want := (Event{At: start, Agent: "main", Type: "read", Path: "a.py", Line: 4, NodeIDs: []string{"a.py\x00a.f", "a.py\x00a.g"}}); !reflect.DeepEqual(read, want) {
		t.Errorf("read = %+v, want %+v", read, want)
	}
	edit := FromActivity(activity.Activity{Time: start, Agent: "main", Tool: "Edit", Kind: "edit", File: "a.py", Line: 2, Functions: functions[:1], Hunks: hunks})
	if want := (Event{At: start, Agent: "main", Type: "edit", Path: "a.py", Line: 2, NodeIDs: []string{"a.py\x00a.f"}, Hunks: hunks}); !reflect.DeepEqual(edit, want) {
		t.Errorf("edit = %+v, want %+v", edit, want)
	}
	created := FromActivity(activity.Activity{Time: start, Tool: "Write", Kind: "edit", File: "n.py", Created: true, Content: "x = 1\n\ny = 2\n", Functions: []activity.Callable{}})
	want := []transcript.Hunk{{NewStart: 1, NewLines: 3, Lines: []string{"+x = 1", "+", "+y = 2"}}}
	if !created.Created || !reflect.DeepEqual(created.Hunks, want) || created.NodeIDs != nil {
		t.Errorf("created = %+v, want one hunk %+v", created, want)
	}
	if empty := FromActivity(activity.Activity{Kind: "edit", Created: true}); !reflect.DeepEqual(empty.Hunks, []transcript.Hunk{{NewStart: 1}}) {
		t.Errorf("empty created file hunks = %+v", empty.Hunks)
	}
}

func TestLogOrdersEventsAndCapsGaps(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	l := NewLog(dir, "20260102-030405-abc", "/repo", "main")
	if _, err := os.Stat(filepath.Join(dir, "20260102-030405-abc.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("log file exists before the first event: %v", err)
	}
	at := func(seconds float64) time.Time { return start.Add(time.Duration(seconds * float64(time.Second))) }
	// The agent note has no time, so it takes the time of the event before it.
	l.Append([]Event{
		{At: at(2), Type: "edit"},
		{Type: "agent", Text: "sub"},
		{At: at(0), Type: "prompt"},
		{At: at(1.25), Type: "plan"},
	})
	// An hour of waiting counts GapCap, and a late event from an earlier time adds no time.
	l.Append([]Event{{At: at(3602), Type: "check_start"}, {At: at(1), Type: "read"}, {At: at(3603.5), Type: "check_end"}})

	var types []string
	var times []float64
	lines := l.Lines(0)
	var header Header
	if err := json.Unmarshal(lines[0], &header); err != nil || header != (Header{Type: "session", Root: "/repo", Branch: "main", T0: at(0), GapCap: GapCap}) {
		t.Fatalf("header = %s, %v", lines[0], err)
	}
	for _, line := range lines[1:] {
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		types = append(types, event.Type)
		times = append(times, event.T)
	}
	if want := []string{"prompt", "plan", "edit", "agent", "read", "check_start", "check_end"}; !reflect.DeepEqual(types, want) {
		t.Fatalf("types = %v, want %v", types, want)
	}
	if want := []float64{0, 1.25, 2, 2, 2, 32, 33.5}; !reflect.DeepEqual(times, want) {
		t.Fatalf("times = %v, want %v", times, want)
	}
	if got := l.Lines(len(lines)); got != nil || l.Len() != 8 || len(l.Lines(6)) != 2 {
		t.Fatalf("Lines past the end = %q, Len = %d", got, l.Len())
	}

	// The file holds the same lines.
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "20260102-030405-abc.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, line := range lines {
		want.Write(line)
		want.WriteByte('\n')
	}
	if string(data) != want.String() {
		t.Fatalf("file =\n%s\nwant\n%s", data, want.String())
	}
}

func TestLogKeepsEventsInMemoryWhenTheFileFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	l := NewLog(filepath.Join(blocker, "sessions"), "20260102-030405-abc", "/repo", "")
	l.Append([]Event{{At: start, Type: "prompt"}})
	l.Append([]Event{{At: start, Type: "plan"}})
	if l.Len() != 3 {
		t.Fatalf("Len = %d, want the header and two events", l.Len())
	}
}

func TestListAndRead(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, root string, title string) {
		l := NewLog(dir, name, root, "main")
		l.Append([]Event{{At: start, Type: "prompt", Text: "go"}, {At: start.Add(5 * time.Second), Type: "title", Text: title}})
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	write("20260101-000000-a", "/repo", "First")
	write("20260102-000000-b", "/other", "Other")
	write("20260103-000000-c", "/repo", "Third")
	if err := os.WriteFile(filepath.Join(dir, "20260104-000000-d.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List(dir, "/repo", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []Summary{
		{Name: "20260103-000000-c", Root: "/repo", Branch: "main", T0: start, Title: "Third", Events: 2, Duration: 5},
		{Name: "20260101-000000-a", Root: "/repo", Branch: "main", T0: start, Title: "First", Events: 2, Duration: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List =\n%+v\nwant\n%+v", got, want)
	}
	if got, _ := List(dir, "/repo", 1); len(got) != 1 || got[0].Name != "20260103-000000-c" {
		t.Fatalf("List with limit 1 = %+v", got)
	}
	if got, err := List(filepath.Join(dir, "missing"), "/repo", 10); err != nil || len(got) != 0 {
		t.Fatalf("List of a missing dir = %+v, %v", got, err)
	}

	data, err := Read(dir, "20260101-000000-a")
	if err != nil || !strings.HasPrefix(string(data), `{"t":0,"type":"session","root":"/repo"`) {
		t.Fatalf("Read = %q, %v", data, err)
	}
	for _, name := range []string{"../20260101-000000-a", "20260101-000000-a.jsonl", "notes", "20260109-000000-z"} {
		if _, err := Read(dir, name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Read(%q) error = %v, want ErrNotFound", name, err)
		}
	}
}

func TestDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	if got, err := DefaultDir(); err != nil || got != filepath.Join(home, ".local", "state", "agent-work-visualizer", "sessions") {
		t.Errorf("DefaultDir = %q, %v", got, err)
	}
	t.Setenv("XDG_STATE_HOME", "/state")
	if got, err := DefaultDir(); err != nil || got != "/state/agent-work-visualizer/sessions" {
		t.Errorf("DefaultDir with XDG_STATE_HOME = %q, %v", got, err)
	}
}

func TestNewNameSortsByTime(t *testing.T) {
	first, second := NewName(start), NewName(start.Add(time.Second))
	if !namePattern.MatchString(first) || first >= second {
		t.Fatalf("NewName = %q, %q", first, second)
	}
}
