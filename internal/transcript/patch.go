package transcript

import (
	"strings"
	"time"
)

// Hunk is one hunk of a Claude Code structuredPatch. Each entry in Lines
// starts with ' ' for context, '-' for a removed line, or '+' for an added line.
type Hunk struct {
	OldStart int      `json:"oldStart"`
	OldLines int      `json:"oldLines"`
	NewStart int      `json:"newStart"`
	NewLines int      `json:"newLines"`
	Lines    []string `json:"lines"`
}

// Event is one successful Read, Edit, or Write call from a session
// transcript. Line numbers refer to the file as it was when the call finished.
// Only calls that changed existing content have Hunks, and then Lines is
// ChangedLines(Hunks). Created is true for a Write that created the file.
type Event struct {
	Time    time.Time
	Session string
	Agent   string
	Tool    string
	Path    string
	Lines   []int
	Hunks   []Hunk
	Created bool
}

// ShiftLine maps a line number from before an edit to after it. A removed line
// maps to the line that removedLine returns.
func ShiftLine(line int, hunks []Hunk) int {
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

// ChangedLines returns the post-edit line numbers that the hunks touched: each
// added line, and for each removed line the line that removedLine returns.
func ChangedLines(hunks []Hunk) []int {
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
