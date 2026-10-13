package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// patchHunk is one hunk of a Claude Code structuredPatch. Each entry in Lines
// starts with ' ' for context, '-' for a removed line, or '+' for an added line.
type patchHunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// toolEvent is one successful Read, Edit, or Write call from a session
// transcript. Line numbers refer to the file as it was when the call finished.
// Only calls that changed existing content have Hunks, and then Lines is
// changedLines(Hunks). Created is true for a Write that created the file.
type toolEvent struct {
	Time    time.Time
	Session string
	Agent   string
	Tool    string
	Path    string
	Lines   []int
	Hunks   []patchHunk
	Created bool
}

// Activity is a tool event attributed to the callables it touched. An empty
// Functions list means the event applies to the file only. Hunks is the
// structuredPatch of an edit.
type Activity struct {
	Time      time.Time          `json:"time"`
	Session   string             `json:"session"`
	Agent     string             `json:"agent"`
	Tool      string             `json:"tool"`
	Kind      string             `json:"kind"`
	File      string             `json:"file"`
	Line      int                `json:"line,omitempty"`
	Functions []ActivityFunction `json:"functions"`
	Hunks     []patchHunk        `json:"hunks,omitempty"`
	Created   bool               `json:"created,omitempty"`
	Note      string             `json:"note,omitempty"`
}

// ActivityFunction identifies a callable by key, which is stable across line
// shifts, unlike Function.ID. Hunks holds the indexes of the event hunks that
// changed the callable.
type ActivityFunction struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Line  int    `json:"line"`
	Hunks []int  `json:"hunks,omitempty"`
}

func activityKey(file string, qualifiedName string) string {
	return file + "\x00" + qualifiedName
}

// shiftLine maps a line number from before an edit to after it. A removed line
// maps to the line that removedLine returns.
func shiftLine(line int, hunks []patchHunk) int {
	offset := 0
	for _, hunk := range hunks {
		if line < hunk.OldStart {
			break
		}
		if line >= hunk.OldStart+hunk.OldLines {
			offset = hunk.NewStart + hunk.NewLines - hunk.OldStart - hunk.OldLines
			continue
		}
		oldLine, newLine := hunk.OldStart, hunk.NewStart
		for index, text := range hunk.Lines {
			switch {
			case strings.HasPrefix(text, "+"):
				newLine++
			case strings.HasPrefix(text, "-"):
				if oldLine == line {
					return removedLine(hunk.Lines, index, newLine)
				}
				oldLine++
			case strings.HasPrefix(text, `\`):
				// "\ No newline at end of file" belongs to neither side.
			default:
				if oldLine == line {
					return newLine
				}
				oldLine++
				newLine++
			}
		}
		return newLine
	}
	return line + offset
}

// removedLine returns the post-edit line for the removed line at index in a
// hunk. newLine is the post-edit number of the next added or context line. A
// replaced line maps to the first replacement line. A deleted line maps to the
// line before the deletion, because in Python the line after it can start the
// next function. A deletion at the start of the file maps to line 1.
func removedLine(lines []string, index int, newLine int) int {
	next := index
	for next < len(lines) && (strings.HasPrefix(lines[next], "-") || strings.HasPrefix(lines[next], `\`)) {
		next++
	}
	if newLine > 1 && (next == len(lines) || !strings.HasPrefix(lines[next], "+")) {
		return newLine - 1
	}
	return newLine
}

// changedLines returns the post-edit line numbers that the hunks touched: each
// added line, and for each removed line the line that removedLine returns.
func changedLines(hunks []patchHunk) []int {
	var lines []int
	for _, hunk := range hunks {
		newLine := hunk.NewStart
		for index, text := range hunk.Lines {
			switch {
			case strings.HasPrefix(text, "+"):
				lines = append(lines, newLine)
				newLine++
			case strings.HasPrefix(text, "-"):
				lines = append(lines, removedLine(hunk.Lines, index, newLine))
			case strings.HasPrefix(text, `\`):
			default:
				newLine++
			}
		}
	}
	return lines
}

// outlineFile analyzes one source file without resolving calls. Qualified names
// match a full analysis of root. Files in languages the analyzer does not
// support return no callables and no error.
func outlineFile(root string, path string) ([]Function, error) {
	language := sourceLanguage(path)
	if language == "" {
		return nil, nil
	}
	a := newAnalyzer(root)
	if err := a.analyzeFile(sourceFile{path: path, language: language}); err != nil {
		return nil, err
	}
	return a.graph.Nodes, nil
}

type recordedEdit struct {
	time     time.Time
	sequence int
	hunks    []patchHunk
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
	functions []Function
	err       error
}

// activityMapper attributes tool events to callables. It assumes each file on
// disk matches the state after the latest edit that it saw, so it moves an
// event's line numbers forward through every later edit of the same file
// before it looks them up in the file's current outline. It is not safe for
// concurrent use.
type activityMapper struct {
	root     string
	edits    map[string][]recordedEdit
	outlines map[string]fileOutline
	sequence int
}

func newActivityMapper(root string) *activityMapper {
	return &activityMapper{
		root:     root,
		edits:    make(map[string][]recordedEdit),
		outlines: make(map[string]fileOutline),
	}
}

// mapEvents records the edits in events, then attributes each event in time
// order. It keeps the edits from earlier calls, so later batches can map
// events whose timestamps precede edits that it already saw.
func (m *activityMapper) mapEvents(events []toolEvent) []Activity {
	sorted := append([]toolEvent(nil), events...)
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

func (m *activityMapper) currentLines(event toolEvent, position recordedEdit) []int {
	lines := append([]int(nil), event.Lines...)
	for _, edit := range m.edits[event.Path] {
		if !edit.after(position) {
			continue
		}
		for index, line := range lines {
			lines[index] = shiftLine(line, edit.hunks)
		}
	}
	return lines
}

func (m *activityMapper) attribute(event toolEvent, lines []int) Activity {
	relative, _ := filepath.Rel(m.root, event.Path)
	activity := Activity{
		Time:      event.Time,
		Session:   event.Session,
		Agent:     event.Agent,
		Tool:      event.Tool,
		Kind:      "edit",
		File:      filepath.ToSlash(relative),
		Functions: []ActivityFunction{},
		Hunks:     event.Hunks,
		Created:   event.Created,
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
		for range changedLines([]patchHunk{hunk}) {
			hunkOf = append(hunkOf, index)
		}
	}
	positions := make(map[string]int)
	for index, line := range lines {
		function := innermostFunction(functions, line)
		if function == nil {
			continue
		}
		key := activityKey(function.File, function.QualifiedName)
		position, seen := positions[key]
		if !seen {
			position = len(activity.Functions)
			positions[key] = position
			activity.Functions = append(activity.Functions, ActivityFunction{Key: key, Name: function.Name, Line: int(function.Line)})
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

func (m *activityMapper) outline(path string) ([]Function, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if cached, ok := m.outlines[path]; ok && cached.modified.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.functions, cached.err
	}
	functions, err := outlineFile(m.root, path)
	m.outlines[path] = fileOutline{modified: info.ModTime(), size: info.Size(), functions: functions, err: err}
	return functions, err
}

// innermostFunction returns the callable with the smallest span that contains
// line, so a nested function wins over its parent.
func innermostFunction(functions []Function, line int) *Function {
	var best *Function
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
