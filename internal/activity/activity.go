// Package activity attributes transcript tool events to the callables whose
// lines they read or changed.
package activity

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/transcript"
)

// Activity is a tool event attributed to the callables it touched. An empty
// Functions list means the event applies to the file only. Hunks is the
// structuredPatch of an edit. Content is the content of a created file.
type Activity struct {
	Time      time.Time         `json:"time"`
	Session   string            `json:"session"`
	Agent     string            `json:"agent"`
	Tool      string            `json:"tool"`
	Kind      string            `json:"kind"`
	File      string            `json:"file"`
	Line      int               `json:"line,omitempty"`
	Functions []Callable        `json:"functions"`
	Hunks     []transcript.Hunk `json:"hunks,omitempty"`
	Created   bool              `json:"created,omitempty"`
	Content   string            `json:"content,omitempty"`
	Note      string            `json:"note,omitempty"`
}

// Callable identifies a callable by key, which is stable across line shifts,
// unlike callgraph.Function.ID. Hunks holds the indexes of the event hunks
// that changed the callable.
type Callable struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Line  int    `json:"line"`
	Hunks []int  `json:"hunks,omitempty"`
}

// Key identifies a callable by its root-relative file and qualified name.
func Key(file string, qualifiedName string) string {
	return file + "\x00" + qualifiedName
}

type recordedEdit struct {
	time     time.Time
	sequence int
	hunks    []transcript.Hunk
}

func (edit recordedEdit) after(other recordedEdit) bool {
	if edit.time.Equal(other.time) {
		return edit.sequence > other.sequence
	}
	return edit.time.After(other.time)
}

type fileOutline struct {
	modified  time.Time
	size      int64
	functions []callgraph.Function
	err       error
}

// Mapper attributes tool events to callables. It assumes each file on
// disk matches the state after the latest edit that it saw, so it moves an
// event's line numbers forward through every later edit of the same file
// before it looks them up in the file's current outline. It is not safe for
// concurrent use.
type Mapper struct {
	root     string
	analyzer *analyzer.Analyzer
	edits    map[string][]recordedEdit
	outlines map[string]fileOutline
	sequence int
}

// NewMapper returns a Mapper for events under root. It outlines files with
// the languages registered in a.
func NewMapper(root string, a *analyzer.Analyzer) *Mapper {
	return &Mapper{
		root:     root,
		analyzer: a,
		edits:    make(map[string][]recordedEdit),
		outlines: make(map[string]fileOutline),
	}
}

// Map records the edits in events, then attributes each event in time
// order. It keeps the edits from earlier calls, so later batches can map
// events whose timestamps precede edits that it already saw.
func (m *Mapper) Map(events []transcript.Event) []Activity {
	sorted := append([]transcript.Event(nil), events...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Time.Before(sorted[j].Time) })
	positions := make([]recordedEdit, len(sorted))
	for index, event := range sorted {
		m.sequence++
		positions[index] = recordedEdit{time: event.Time, sequence: m.sequence, hunks: event.Hunks}
		if len(event.Hunks) == 0 {
			continue
		}
		edits := append(m.edits[event.Path], positions[index])
		sort.SliceStable(edits, func(i, j int) bool { return edits[j].after(edits[i]) })
		m.edits[event.Path] = edits
	}
	activities := make([]Activity, 0, len(sorted))
	for index, event := range sorted {
		activities = append(activities, m.attribute(event, m.currentLines(event, positions[index])))
	}
	return activities
}

func (m *Mapper) currentLines(event transcript.Event, position recordedEdit) []int {
	lines := append([]int(nil), event.Lines...)
	for _, edit := range m.edits[event.Path] {
		if !edit.after(position) {
			continue
		}
		for index, line := range lines {
			lines[index] = transcript.ShiftLine(line, edit.hunks)
		}
	}
	return lines
}

func (m *Mapper) attribute(event transcript.Event, lines []int) Activity {
	relative, _ := filepath.Rel(m.root, event.Path)
	activity := Activity{
		Time:      event.Time,
		Session:   event.Session,
		Agent:     event.Agent,
		Tool:      event.Tool,
		Kind:      "edit",
		File:      filepath.ToSlash(relative),
		Functions: []Callable{},
		Hunks:     event.Hunks,
		Created:   event.Created,
		Content:   event.Content,
	}
	if event.Tool == "Read" {
		activity.Kind = "read"
	}
	if len(lines) > 0 {
		activity.Line = lines[0]
	}
	functions, err := m.outline(event.Path)
	if err != nil {
		activity.Note = err.Error()
		return activity
	}
	// An edit's lines are the changed lines of each hunk in turn, so hunkOf[i]
	// is the hunk of lines[i]. Reads and created files have no hunks.
	var hunkOf []int
	for index, hunk := range event.Hunks {
		for range transcript.ChangedLines([]transcript.Hunk{hunk}) {
			hunkOf = append(hunkOf, index)
		}
	}
	positions := make(map[string]int)
	for index, line := range lines {
		function := innermostFunction(functions, line)
		if function == nil {
			continue
		}
		key := Key(function.File, function.QualifiedName)
		position, seen := positions[key]
		if !seen {
			position = len(activity.Functions)
			positions[key] = position
			activity.Functions = append(activity.Functions, Callable{Key: key, Name: function.Name, Line: int(function.Line)})
		}
		if index >= len(hunkOf) {
			continue
		}
		// Lines arrive in hunk order, so a hunk repeats only at the end.
		if hunks := activity.Functions[position].Hunks; len(hunks) == 0 || hunks[len(hunks)-1] != hunkOf[index] {
			activity.Functions[position].Hunks = append(hunks, hunkOf[index])
		}
	}
	return activity
}

func (m *Mapper) outline(path string) ([]callgraph.Function, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if cached, ok := m.outlines[path]; ok && cached.modified.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.functions, cached.err
	}
	functions, err := m.analyzer.Outline(m.root, path)
	m.outlines[path] = fileOutline{modified: info.ModTime(), size: info.Size(), functions: functions, err: err}
	return functions, err
}

// innermostFunction returns the callable with the smallest span that contains
// line, so a nested function wins over its parent.
func innermostFunction(functions []callgraph.Function, line int) *callgraph.Function {
	var best *callgraph.Function
	for index := range functions {
		function := &functions[index]
		if line < int(function.Line) || line > int(function.EndLine) {
			continue
		}
		if best == nil || function.EndLine-function.Line < best.EndLine-best.Line {
			best = function
		}
	}
	return best
}
