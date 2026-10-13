// Package transcripttest writes Claude Code transcript fixtures for tests.
package transcripttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Time is the timestamp of every fixture line.
var Time = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// SessionStart returns the first line of session s1, which records cwd.
func SessionStart(cwd string) map[string]any {
	return map[string]any{"type": "user", "cwd": cwd, "sessionId": "s1", "timestamp": Time, "message": map[string]any{"content": "start"}}
}

// ToolUseLine returns an assistant line that starts tool call id of tool name.
func ToolUseLine(id string, name string) map[string]any {
	return map[string]any{
		"type":      "assistant",
		"sessionId": "s1",
		"timestamp": Time,
		"message":   map[string]any{"content": []any{map[string]any{"type": "tool_use", "id": id, "name": name}}},
	}
}

// ToolResultLine returns the user line with the result of tool call id.
func ToolResultLine(id string, result any) map[string]any {
	return map[string]any{
		"type":          "user",
		"sessionId":     "s1",
		"timestamp":     Time,
		"message":       map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": id}}},
		"toolUseResult": result,
	}
}

// ReadResult returns the toolUseResult of a Read of count lines from start.
func ReadResult(path string, start int, count int) map[string]any {
	return map[string]any{"type": "text", "file": map[string]any{"filePath": path, "startLine": start, "numLines": count}}
}

// WithField sets a field on the line's first content block.
func WithField(line map[string]any, key string, value any) map[string]any {
	line["message"].(map[string]any)["content"].([]any)[0].(map[string]any)[key] = value
	return line
}

// WithAgent marks line as a line of subagent agent.
func WithAgent(line map[string]any, agent string) map[string]any {
	line["agentId"] = agent
	line["isSidechain"] = true
	return line
}

// MustJSON encodes value or fails the test.
func MustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// WriteTranscript writes lines as a JSONL transcript at path.
func WriteTranscript(t *testing.T, path string, lines ...map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var builder strings.Builder
	for _, line := range lines {
		builder.WriteString(MustJSON(t, line))
		builder.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// AppendTranscript appends text to the transcript at path.
func AppendTranscript(t *testing.T, path string, text string) {
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

// SetModified sets the modification time of path.
func SetModified(t *testing.T, path string, modified time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}
