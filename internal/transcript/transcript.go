// Package transcript follows Claude Code session transcripts and turns their
// Read, Edit, and Write results into tool events.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const (
	// cwdProbeBytes is the maximum number of bytes that classify reads from
	// the start of a new transcript to find the session's working directory.
	cwdProbeBytes = 4 << 20
	// maxProjectName is the length above which Claude Code shortens a project
	// directory name and adds a hash.
	maxProjectName = 200
)

// Watcher follows Claude Code session transcripts for sessions whose
// working directory contains root or lies inside it, and turns their Read,
// Edit, and Write results into tool events for files under root.
//
// Transcripts are JSONL files under projects/<encoded cwd>/<session>.jsonl, and
// subagents write projects/<encoded cwd>/<session>/subagents/agent-<id>.jsonl.
// The format is internal to Claude Code. The watcher is not safe for
// concurrent use. described holds the subagent transcripts whose meta file
// the watcher read, and titles the latest title of each session.
type Watcher struct {
	projects  string
	root      string
	files     map[string]*transcriptFile
	ignored   map[string]bool
	tools     map[string]string
	described map[string]bool
	titles    map[string]string
	notes     []Note
	scanned   bool
}

// Note records what a session works on. Kind is "title" for the session
// title, "request" for a user prompt, "plan" for text that an agent writes,
// "step" for a tool call other than Read, Edit, Write, and Bash, "subagent"
// for the description of a subagent, "check_start" for a Bash call that an
// agent starts, or "check_end" for its result, with OK false if it failed.
// Agent is "main" or a subagent ID.
type Note struct {
	Time    time.Time `json:"time"`
	Session string    `json:"session"`
	Agent   string    `json:"agent"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"`
	OK      bool      `json:"ok,omitempty"`
}

// maxNoteText is the maximum number of characters in a task note.
const maxNoteText = 500

type transcriptFile struct {
	offset  int64
	partial []byte
}

type transcriptCandidate struct {
	path     string
	size     int64
	modified time.Time
	inside   bool
}

func NewWatcher(projects string, root string) *Watcher {
	return &Watcher{
		projects:  projects,
		root:      root,
		files:     make(map[string]*transcriptFile),
		ignored:   make(map[string]bool),
		tools:     make(map[string]string),
		described: make(map[string]bool),
		titles:    make(map[string]string),
	}
}

// ProjectsDir returns the directory that holds Claude Code session
// transcripts: projects under CLAUDE_CONFIG_DIR, or under ~/.claude if
// CLAUDE_CONFIG_DIR is empty.
func ProjectsDir() (string, error) {
	config := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if config == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		config = filepath.Join(home, ".claude")
	}
	return filepath.Join(config, "projects"), nil
}

// EncodeProjectPath returns the directory name Claude Code uses for a working
// directory. A name longer than maxProjectName keeps its first maxProjectName
// characters, then gets "-" and the base-36 absolute value of the path hash.
func EncodeProjectPath(path string) string {
	name, hash := projectName(path)
	if len(name) <= maxProjectName {
		return name
	}
	return name[:maxProjectName] + "-" + strconv.FormatInt(max(hash, -hash), 36)
}

// projectName replaces each UTF-16 code unit of path that is not an ASCII
// letter or digit with '-', as Claude Code does in JavaScript. It also returns
// the Java-style string hash of the code units.
func projectName(path string) (string, int64) {
	units := utf16.Encode([]rune(path))
	name := make([]byte, len(units))
	var hash int32
	for index, unit := range units {
		hash = hash*31 + int32(unit)
		if unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' {
			name[index] = byte(unit)
		} else {
			name[index] = '-'
		}
	}
	return string(name), int64(hash)
}

// Within reports whether path is base or lies under it.
func Within(path string, base string) bool {
	relative, err := filepath.Rel(base, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// projectDirs returns transcript directories for root, its ancestors, and
// directories whose encoded name extends root's. The encoding is lossy, so the
// prefix match can include unrelated directories. The Scan method filters
// sessions by cwd.
func (w *Watcher) projectDirs() []string {
	var dirs []string
	seen := make(map[string]bool)
	add := func(name string) {
		path := filepath.Join(w.projects, name)
		if seen[path] {
			return
		}
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			seen[path] = true
			dirs = append(dirs, path)
		}
	}
	for directory := w.root; ; directory = filepath.Dir(directory) {
		add(EncodeProjectPath(directory))
		if directory == filepath.Dir(directory) {
			break
		}
	}
	// A directory inside root has root's name, or its first maxProjectName
	// characters, followed by "-".
	name, _ := projectName(w.root)
	prefix := name[:min(len(name), maxProjectName)] + "-"
	entries, err := os.ReadDir(w.projects)
	if err != nil {
		return dirs
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) {
			add(entry.Name())
		}
	}
	return dirs
}

// Scan finds new session and subagent transcripts. On the first scan, it
// replays the newest related session from its start and prefers sessions that
// run inside root. It follows other existing transcripts from their current
// end. It follows transcripts that appear later from their start.
func (w *Watcher) Scan() {
	first := !w.scanned
	w.scanned = true
	var candidates []transcriptCandidate
	for _, dir := range w.projectDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() || filepath.Ext(path) != ".jsonl" || w.files[path] != nil || w.ignored[path] {
				continue
			}
			candidate, known := w.classify(path)
			if !known {
				continue
			}
			if candidate == nil {
				w.ignored[path] = true
				continue
			}
			candidates = append(candidates, *candidate)
		}
	}
	replay := ""
	if first {
		replay = newestCandidate(candidates)
	}
	for _, candidate := range candidates {
		offset := int64(0)
		if first && candidate.path != replay {
			offset = candidate.size
		}
		w.follow(candidate.path, offset)
	}
	var sessions []string
	for path := range w.files {
		if filepath.Base(filepath.Dir(path)) != "subagents" {
			sessions = append(sessions, path)
		}
	}
	for _, path := range sessions {
		subagents := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
		entries, err := os.ReadDir(subagents)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			subagent := filepath.Join(subagents, entry.Name())
			if entry.IsDir() || filepath.Ext(subagent) != ".jsonl" {
				continue
			}
			if w.files[subagent] == nil {
				offset := int64(0)
				if first && path != replay {
					if info, err := entry.Info(); err == nil {
						offset = info.Size()
					}
				}
				w.follow(subagent, offset)
			}
			// Claude Code can write the meta file after the transcript, so
			// later scans try again.
			if !w.described[subagent] {
				w.described[subagent] = w.describeSubagent(strings.TrimSuffix(filepath.Base(path), ".jsonl"), subagent)
			}
		}
	}
}

// describeSubagent adds a subagent note from the agent-<id>.meta.json file
// beside a subagent transcript. It adds nothing if the file is missing, is not
// valid JSON, or has no description. It returns false if it cannot read the
// file or the file is not valid JSON, because Claude Code can still write it.
func (w *Watcher) describeSubagent(session string, transcript string) bool {
	data, err := os.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
	if err != nil {
		return false
	}
	var meta struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return false
	}
	if meta.Description == "" {
		return true
	}
	agent := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(transcript), ".jsonl"), "agent-")
	w.notes = append(w.notes, Note{Session: session, Agent: agent, Kind: "subagent", Text: noteText(meta.Description)})
	return true
}

// SessionIDs returns the IDs of the sessions whose main transcripts the
// watcher follows.
func (w *Watcher) SessionIDs() []string {
	var sessions []string
	for path := range w.files {
		if filepath.Base(filepath.Dir(path)) != "subagents" {
			sessions = append(sessions, strings.TrimSuffix(filepath.Base(path), ".jsonl"))
		}
	}
	return sessions
}

// TakeNotes returns the task notes from the scans and polls since the last
// call.
func (w *Watcher) TakeNotes() []Note {
	notes := w.notes
	w.notes = nil
	return notes
}

func (w *Watcher) follow(path string, offset int64) {
	w.files[path] = &transcriptFile{offset: offset}
	if offset == 0 {
		log.Printf("Following transcript %s", path)
	} else {
		log.Printf("Following transcript %s from byte %d", path, offset)
	}
}

func newestCandidate(candidates []transcriptCandidate) string {
	var newest *transcriptCandidate
	for index := range candidates {
		candidate := &candidates[index]
		if newest == nil || candidate.inside && !newest.inside ||
			candidate.inside == newest.inside && candidate.modified.After(newest.modified) {
			newest = candidate
		}
	}
	if newest == nil {
		return ""
	}
	return newest.path
}

// classify reads the start of a session transcript to find its working
// directory. It returns known=false while the transcript has no cwd yet, and a
// nil candidate when the session is unrelated to root.
func (w *Watcher) classify(path string) (*transcriptCandidate, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false
	}
	cwd := Cwd(file)
	if cwd == "" {
		// A transcript larger than the probe without a cwd is not a session log.
		return nil, info.Size() >= cwdProbeBytes
	}
	if !Within(cwd, w.root) && !Within(w.root, cwd) {
		return nil, true
	}
	return &transcriptCandidate{path: path, size: info.Size(), modified: info.ModTime(), inside: Within(cwd, w.root)}, true
}

// Cwd returns the first working directory that the first
// cwdProbeBytes bytes of a transcript record, or "" if they record none.
func Cwd(transcript io.Reader) string {
	reader := bufio.NewReader(io.LimitReader(transcript, cwdProbeBytes))
	for {
		data, err := reader.ReadBytes('\n')
		if len(data) == 0 && err != nil {
			return ""
		}
		var line struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(data, &line) == nil && line.Cwd != "" {
			return line.Cwd
		}
	}
}

// Poll reads lines appended since the last poll and returns their tool
// events. It keeps an incomplete trailing line until its newline arrives. It
// stops following a transcript that no longer exists.
func (w *Watcher) Poll() []Event {
	paths := make([]string, 0, len(w.files))
	for path := range w.files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var events []Event
	for _, path := range paths {
		chunk, err := w.files[path].read(path)
		if errors.Is(err, fs.ErrNotExist) {
			log.Printf("Transcript %s was removed", path)
			delete(w.files, path)
			continue
		}
		if err != nil {
			log.Printf("Reading transcript %s: %v", path, err)
			continue
		}
		for _, line := range chunk {
			events = append(events, w.parseLine(line)...)
		}
	}
	return events
}

// read returns the complete lines after the last read. If the file is
// shorter than the last read position, it reads the file again from the start.
func (f *transcriptFile) read(path string) ([][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.Size() < f.offset {
		f.offset, f.partial = 0, nil
	}
	if info.Size() == f.offset {
		return nil, nil
	}
	data := make([]byte, info.Size()-f.offset)
	count, err := file.ReadAt(data, f.offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read: %w", err)
	}
	f.offset += int64(count)
	data = append(f.partial, data[:count]...)
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		f.partial = data
		return nil, nil
	}
	f.partial = append([]byte(nil), data[end+1:]...)
	return bytes.Split(data[:end], []byte{'\n'}), nil
}

type transcriptLine struct {
	Type             string    `json:"type"`
	Timestamp        time.Time `json:"timestamp"`
	SessionID        string    `json:"sessionId"`
	AgentID          string    `json:"agentId"`
	IsMeta           bool      `json:"isMeta"`
	IsCompactSummary bool      `json:"isCompactSummary"`
	AITitle          string    `json:"aiTitle"`
	Message          struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

type transcriptBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Text      string          `json:"text"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// toolResult holds the toolUseResult fields of Read, Edit, and Write. Failed
// calls store a plain string there, which does not decode into this type.
type toolResult struct {
	Type            string `json:"type"`
	FilePath        string `json:"filePath"`
	Content         string `json:"content"`
	StructuredPatch []Hunk `json:"structuredPatch"`
	File            struct {
		FilePath  string `json:"filePath"`
		StartLine int    `json:"startLine"`
		NumLines  int    `json:"numLines"`
	} `json:"file"`
}

// parseLine returns the tool events of one transcript line and adds its task
// notes to w.notes.
func (w *Watcher) parseLine(data []byte) []Event {
	var line transcriptLine
	if json.Unmarshal(data, &line) != nil {
		return nil
	}
	agent := line.AgentID
	if agent == "" {
		agent = "main"
	}
	note := func(kind string, text string) {
		w.notes = append(w.notes, Note{Time: line.Timestamp, Session: line.SessionID, Agent: agent, Kind: kind, Text: noteText(text)})
	}
	// Claude Code writes the title line again after later turns.
	if line.Type == "ai-title" && line.AITitle != "" && w.titles[line.SessionID] != line.AITitle {
		w.titles[line.SessionID] = line.AITitle
		note("title", line.AITitle)
		return nil
	}
	if line.Type == "ai-title" {
		return nil
	}
	// The first user line of a subagent is its prompt, not a user request.
	if line.Type == "user" && line.AgentID == "" && !line.IsMeta && !line.IsCompactSummary {
		if text := promptText(line.Message.Content); text != "" {
			note("request", text)
		}
	}
	if len(line.Message.Content) == 0 || line.Message.Content[0] != '[' {
		return nil
	}
	var blocks []transcriptBlock
	if json.Unmarshal(line.Message.Content, &blocks) != nil {
		return nil
	}
	var events []Event
	for _, block := range blocks {
		switch {
		case line.Type == "assistant" && block.Type == "text":
			if text := strings.TrimSpace(block.Text); text != "" {
				note("plan", text)
			}
		case line.Type == "assistant" && block.Type == "tool_use":
			switch block.Name {
			// Their results become events, which name the file and callables.
			case "Read", "Edit", "Write":
				w.tools[block.ID] = block.Name
			case "Bash":
				w.tools[block.ID] = block.Name
				note("check_start", commandText(block))
			default:
				note("step", w.stepText(block))
			}
		case line.Type == "user" && block.Type == "tool_result":
			tool, ok := w.tools[block.ToolUseID]
			delete(w.tools, block.ToolUseID)
			if ok && tool == "Bash" {
				w.notes = append(w.notes, Note{Time: line.Timestamp, Session: line.SessionID, Agent: agent, Kind: "check_end", OK: !block.IsError})
				continue
			}
			if !ok || block.IsError {
				continue
			}
			event, ok := toolResultEvent(tool, line.ToolUseResult)
			if !ok || !Within(event.Path, w.root) {
				continue
			}
			event.Time = line.Timestamp
			event.Session = line.SessionID
			event.Agent = agent
			events = append(events, event)
		}
	}
	return events
}

// pastedContent matches a block of text that the user pasted into a prompt.
var pastedContent = regexp.MustCompile(`(?s)<pasted_content[^>]*>.*?</pasted_content>`)

// promptText returns the text that a user typed, from message content that is
// a string or a list of blocks. It removes pasted blocks, and returns
// "[pasted content]" if nothing else remains. It returns "" for tool results,
// for command and notification messages, which start with "<", and for
// interruption markers.
func promptText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) != nil {
		var blocks []transcriptBlock
		if json.Unmarshal(content, &blocks) != nil {
			return ""
		}
		var parts []string
		for _, block := range blocks {
			switch block.Type {
			case "tool_result":
				return ""
			case "text":
				parts = append(parts, block.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = strings.TrimSpace(text)
	if stripped := strings.TrimSpace(pastedContent.ReplaceAllString(text, "")); stripped != text {
		if stripped == "" {
			return "[pasted content]"
		}
		text = stripped
	}
	if strings.HasPrefix(text, "<") || strings.HasPrefix(text, "[Request interrupted") {
		return ""
	}
	return text
}

// stepText describes a tool call: its description input if it has one, else
// the tool name and the file or pattern that it works on.
func (w *Watcher) stepText(block transcriptBlock) string {
	var input struct {
		Description string `json:"description"`
		FilePath    string `json:"file_path"`
		Pattern     string `json:"pattern"`
	}
	if json.Unmarshal(block.Input, &input) != nil {
		return block.Name
	}
	switch {
	case input.Description != "":
		return input.Description
	case input.FilePath != "":
		path := filepath.Clean(input.FilePath)
		if relative, err := filepath.Rel(w.root, path); err == nil && Within(path, w.root) {
			return block.Name + " " + filepath.ToSlash(relative)
		}
		return block.Name + " " + filepath.Base(path)
	case input.Pattern != "":
		return block.Name + " " + input.Pattern
	}
	return block.Name
}

// commandText describes a Bash call: its description input if it has one,
// else the first line of its command.
func commandText(block transcriptBlock) string {
	var input struct {
		Description string `json:"description"`
		Command     string `json:"command"`
	}
	if json.Unmarshal(block.Input, &input) != nil {
		return block.Name
	}
	if input.Description != "" {
		return input.Description
	}
	command, _, _ := strings.Cut(strings.TrimSpace(input.Command), "\n")
	if command == "" {
		return block.Name
	}
	return command
}

// noteText shortens text to maxNoteText characters.
func noteText(text string) string {
	runes := []rune(text)
	if len(runes) <= maxNoteText {
		return text
	}
	return string(runes[:maxNoteText-1]) + "…"
}

func toolResultEvent(tool string, data json.RawMessage) (Event, bool) {
	var result toolResult
	if json.Unmarshal(data, &result) != nil {
		return Event{}, false
	}
	event := Event{Tool: tool}
	switch {
	case tool == "Read":
		if result.Type != "text" || result.File.FilePath == "" {
			return Event{}, false
		}
		event.Path = result.File.FilePath
		for line := result.File.StartLine; line < result.File.StartLine+result.File.NumLines; line++ {
			event.Lines = append(event.Lines, line)
		}
	case tool == "Write" && result.Type == "create":
		event.Path = result.FilePath
		event.Created = true
		event.Content = result.Content
		for line := 1; line <= lineCount(result.Content); line++ {
			event.Lines = append(event.Lines, line)
		}
	default:
		event.Path = result.FilePath
		event.Hunks = result.StructuredPatch
		event.Lines = ChangedLines(result.StructuredPatch)
	}
	if event.Path == "" {
		return Event{}, false
	}
	event.Path = filepath.Clean(event.Path)
	return event, true
}

func lineCount(content string) int {
	count := strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}
