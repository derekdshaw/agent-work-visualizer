// Package session records the agent work on one folder as a JSONL event log.
// The first line is a header, and each later line is one event. The viewer
// folds the events up to a time into what it shows, so the live view and a
// replay read the same lines.
package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/activity"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
)

// GapCap is the longest idle time, in seconds, between two events on the
// timeline. An agent can wait hours for the user, and the server replays
// sessions from earlier days, so real gaps would leave the timeline empty.
const GapCap = 30.0

// Header is the first line of a log. T0 is the wall time of the first event.
type Header struct {
	T      float64   `json:"t"`
	Type   string    `json:"type"`
	Root   string    `json:"root"`
	Branch string    `json:"branch,omitempty"`
	T0     time.Time `json:"t0"`
	GapCap float64   `json:"gapCap"`
}

// Event is one line after the header. T is the time in seconds on the
// timeline and At the wall time. Type is one of:
//
//   - "title": Text is the session title.
//   - "prompt": Text is a user request.
//   - "plan": Text is what the agent wrote.
//   - "think": Text is what the agent thought, which it wrote down at T. Since
//     is the time on the timeline when the thinking began.
//   - "step": Text describes a tool call that is not a read, edit, or check.
//   - "agent": Text describes a subagent that started.
//   - "read": the agent read Path from Line. NodeIDs are the callables read.
//   - "edit": the agent changed Path. NodeIDs are the callables changed, the
//     first being the edit target, and Hunks the change. Created is true for a
//     new file, whose content is then one hunk of added lines.
//   - "check_start": the agent ran the command that Text describes.
//   - "check_end": the command finished. OK reports whether it succeeded.
//
// Paths are relative to the header's root, and node IDs are activity keys.
type Event struct {
	T       float64           `json:"t"`
	At      time.Time         `json:"at"`
	Type    string            `json:"type"`
	Agent   string            `json:"agent,omitempty"`
	Text    string            `json:"text,omitempty"`
	Path    string            `json:"path,omitempty"`
	Line    int               `json:"line,omitempty"`
	NodeIDs []string          `json:"nodeIds,omitempty"`
	Hunks   []transcript.Hunk `json:"hunks,omitempty"`
	Created bool              `json:"created,omitempty"`
	OK      *bool             `json:"ok,omitempty"`
	Since   *float64          `json:"since,omitempty"`
	// Start is the wall time that Since comes from.
	Start time.Time `json:"-"`
}

// FromNote returns the event for a transcript note, and false for a note kind
// that has no event.
func FromNote(note transcript.Note) (Event, bool) {
	event := Event{At: note.Time, Agent: note.Agent, Text: note.Text}
	switch note.Kind {
	case "title":
		event.Type = "title"
	case "request":
		event.Type = "prompt"
	case "plan", "step", "check_start":
		event.Type = note.Kind
	case "think":
		event.Type = "think"
		event.Start = note.Start
	case "subagent":
		event.Type = "agent"
	case "check_end":
		event.Type = "check_end"
		event.Text = ""
		event.OK = &note.OK
	default:
		return Event{}, false
	}
	return event, true
}

// FromActivity returns the read or edit event for an activity.
func FromActivity(item activity.Activity) Event {
	event := Event{At: item.Time, Agent: item.Agent, Type: item.Kind, Path: item.File, Line: item.Line}
	for _, callable := range item.Functions {
		event.NodeIDs = append(event.NodeIDs, callable.Key)
	}
	if item.Kind != "edit" {
		return event
	}
	event.Hunks = item.Hunks
	event.Created = item.Created
	if item.Created {
		event.Hunks = []transcript.Hunk{createdHunk(item.Content)}
	}
	return event
}

// createdHunk returns the hunk that adds every line of content.
func createdHunk(content string) transcript.Hunk {
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	if content == "" {
		lines = nil
	}
	hunk := transcript.Hunk{NewStart: 1, NewLines: len(lines)}
	for _, line := range lines {
		hunk.Lines = append(hunk.Lines, "+"+line)
	}
	return hunk
}

// Log is the event log of one opened folder. It keeps every line in memory
// for the event stream and appends each line to its file. It creates the file
// with the first event, because the header holds that event's time.
type Log struct {
	name   string
	dir    string
	root   string
	branch string

	mu      sync.Mutex
	file    *os.File
	failed  bool
	lines   [][]byte
	lastT   float64
	lastAt  time.Time
	started bool
}

// NewLog returns an empty log that writes dir/name.jsonl.
func NewLog(dir string, name string, root string, branch string) *Log {
	return &Log{name: name, dir: dir, root: root, branch: branch}
}

// Name identifies the log among the logs in its directory.
func (l *Log) Name() string {
	return l.name
}

// Append orders events by wall time and adds them to the log. An event
// without a wall time takes the time of the event before it. Each event's T is
// the previous T plus the wall time between them, at most GapCap. It reorders
// the events slice.
func (l *Log) Append(events []Event) {
	if len(events) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	previous := l.lastAt
	if previous.IsZero() {
		previous = firstTime(events)
	}
	for index := range events {
		if events[index].At.IsZero() {
			events[index].At = previous
		}
		previous = events[index].At
	}
	slices.SortStableFunc(events, func(a, b Event) int { return a.At.Compare(b.At) })
	if !l.started {
		l.started = true
		l.lastAt = events[0].At
		l.write(Header{Type: "session", Root: l.root, Branch: l.branch, T0: events[0].At, GapCap: GapCap})
	}
	for _, event := range events {
		gap := min(max(event.At.Sub(l.lastAt).Seconds(), 0), GapCap)
		l.lastT = math.Round((l.lastT+gap)*1000) / 1000
		if event.At.After(l.lastAt) {
			l.lastAt = event.At
		}
		event.T = l.lastT
		if !event.Start.IsZero() {
			// The timeline caps each idle gap, so the thinking spans at most GapCap.
			since := math.Round(max(event.T-min(event.At.Sub(event.Start).Seconds(), GapCap), 0)*1000) / 1000
			event.Since = &since
		}
		l.write(event)
	}
}

// firstTime returns the first wall time in events, or now if none has one.
func firstTime(events []Event) time.Time {
	for _, event := range events {
		if !event.At.IsZero() {
			return event.At
		}
	}
	return time.Now()
}

// write adds one line. A log whose file fails keeps working in memory, so the
// live view goes on without a replay file.
func (l *Log) write(value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("Encoding session event: %v", err)
		return
	}
	l.lines = append(l.lines, data)
	if l.failed {
		return
	}
	if err := l.writeFile(data); err != nil {
		l.failed = true
		log.Printf("Writing session log, keeping events in memory only: %v", err)
	}
}

func (l *Log) writeFile(data []byte) error {
	if l.file == nil {
		// A log holds the user's prompts and the source the agent changed.
		if err := os.MkdirAll(l.dir, 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(l.dir, l.name+".jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		l.file = file
	}
	_, err := l.file.Write(append(data, '\n'))
	return err
}

// Lines returns the lines from index from onward. Line n of the file is
// index n-1. The result shares storage with the log, which only appends.
func (l *Log) Lines(from int) [][]byte {
	l.mu.Lock()
	defer l.mu.Unlock()
	if from >= len(l.lines) {
		return nil
	}
	return l.lines[from:len(l.lines):len(l.lines)]
}

// Len returns the number of lines.
func (l *Log) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.lines)
}

// Close closes the file of the log.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	l.failed = true
	return err
}

// DefaultDir returns the directory for session logs:
// $XDG_STATE_HOME/agent-work-visualizer/sessions, or ~/.local/state/... if
// XDG_STATE_HOME is empty.
func DefaultDir() (string, error) {
	state := strings.TrimSpace(os.Getenv("XDG_STATE_HOME"))
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "agent-work-visualizer", "sessions"), nil
}

// NewName returns a log name that sorts by creation time.
func NewName(now time.Time) string {
	return now.Format("20060102-150405") + "-" + strconv.FormatInt(now.UnixNano()%1e9, 36)
}

var namePattern = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-z]+$`)

// Summary describes a finished or running log for the session picker.
type Summary struct {
	Name     string    `json:"name"`
	Root     string    `json:"root"`
	Branch   string    `json:"branch,omitempty"`
	T0       time.Time `json:"t0"`
	Title    string    `json:"title,omitempty"`
	Events   int       `json:"events"`
	Duration float64   `json:"duration"`
}

// List returns the summaries of the newest limit logs in dir whose root is
// root, newest first. A missing dir has no logs.
func List(dir string, root string, limit int) ([]Summary, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list session logs: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if name, ok := strings.CutSuffix(entry.Name(), ".jsonl"); ok && namePattern.MatchString(name) && !entry.IsDir() {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	slices.Reverse(names)
	summaries := []Summary{}
	for _, name := range names {
		if len(summaries) == limit {
			break
		}
		data, err := os.ReadFile(filepath.Join(dir, name+".jsonl"))
		if err != nil {
			continue
		}
		if summary, ok := summarize(name, data); ok && summary.Root == root {
			summaries = append(summaries, summary)
		}
	}
	return summaries, nil
}

// summarize reads the header, the latest title, the event count, and the last
// event time of a log.
func summarize(name string, data []byte) (Summary, bool) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(nil, 64<<20)
	if !scanner.Scan() {
		return Summary{}, false
	}
	var header Header
	if json.Unmarshal(scanner.Bytes(), &header) != nil || header.Type != "session" {
		return Summary{}, false
	}
	summary := Summary{Name: name, Root: header.Root, Branch: header.Branch, T0: header.T0}
	for scanner.Scan() {
		var event struct {
			T    float64 `json:"t"`
			Type string  `json:"type"`
			Text string  `json:"text"`
		}
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			continue
		}
		summary.Events++
		summary.Duration = event.T
		if event.Type == "title" {
			summary.Title = event.Text
		}
	}
	return summary, true
}

// ErrNotFound means that no log has the requested name.
var ErrNotFound = errors.New("no session log has this name")

// Read returns the content of the log name in dir.
func Read(dir string, name string) ([]byte, error) {
	if !namePattern.MatchString(name) {
		return nil, ErrNotFound
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return data, err
}
