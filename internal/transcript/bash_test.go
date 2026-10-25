package transcript

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/derekdshaw/agent-work-visualizer/internal/transcript/transcripttest"
)

// writeLines writes a file of count numbered lines under dir.
func writeLines(t *testing.T, dir string, name string, count int) string {
	t.Helper()
	var builder strings.Builder
	for line := 1; line <= count; line++ {
		builder.WriteString("line\n")
	}
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func span(from int, to int) []int {
	var lines []int
	for line := from; line <= to; line++ {
		lines = append(lines, line)
	}
	return lines
}

func TestBashReadsNameFilesAndLines(t *testing.T) {
	root := t.TempDir()
	a := writeLines(t, root, "pkg/a.go", 30)
	b := writeLines(t, root, "pkg/b.go", 5)
	type read struct {
		path  string
		lines []int
	}
	cases := []struct {
		command string
		stdout  string
		want    []read
	}{
		{"cat pkg/a.go", "", []read{{a, span(1, 30)}}},
		{"head -n 3 pkg/a.go pkg/b.go", "", []read{{a, span(1, 3)}, {b, span(1, 3)}}},
		{"head -4 pkg/a.go", "", []read{{a, span(1, 4)}}},
		{"head pkg/a.go", "", []read{{a, span(1, 10)}}},
		{"tail -n 2 pkg/a.go", "", []read{{a, span(29, 30)}}},
		{"tail -n +28 pkg/a.go", "", []read{{a, span(28, 30)}}},
		{"sed -n '5,7p;20p' pkg/a.go", "", []read{{a, append(span(5, 7), 20)}}},
		{"sed -n 3,8p " + a + " | head -2", "", []read{{a, span(3, 8)}}},
		{"cd pkg && sed -n '1,2p' b.go", "", []read{{b, span(1, 2)}}},
		{"rg -n Run pkg/a.go", "4:func Run()\n9-context\n12:Run()\n", []read{{a, []int{4, 12}}}},
		{"grep -n x pkg/a.go pkg/b.go", "pkg/a.go:2:x\npkg/b.go:5:x\npkg/a.go-3-ctx\n", []read{{a, []int{2}}, {b, []int{5}}}},
		{"cat pkg/a.go 2>/dev/null; echo done", "", []read{{a, span(1, 30)}}},
		{"python3 - <<'EOF'\ncat pkg/b.go\nEOF\ncat pkg/a.go", "", []read{{a, span(1, 30)}}},
	}
	for _, c := range cases {
		var got []read
		for _, r := range bashReads(c.command, root) {
			got = append(got, read{r.path, r.lines(c.stdout)})
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q = %v, want %v", c.command, got, c.want)
		}
	}

	// None of these names a regular file that the command reads by line.
	for _, command := range []string{
		"cat pkg/*.go",
		"cat $FILE",
		"cat pkg",
		"cat missing.go",
		"sed -i 's/a/b/' pkg/a.go",
		"sed -n '/func/p' pkg/a.go",
		"sed 's/a/b/' pkg/a.go",
		"rg -n Run pkg",
		"echo pkg/a.go",
		"git show HEAD:pkg/a.go",
		"cd \"$DIR\" && cat a.go",
		"echo x > pkg/a.go",
	} {
		if reads := bashReads(command, root); len(reads) != 0 {
			t.Errorf("%q reads %+v, want nothing", command, reads)
		}
	}
}

func TestTranscriptWatcherTurnsBashReadsIntoReadEvents(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(t.TempDir(), "repo")
	source := writeLines(t, root, "a.py", 6)
	outside := writeLines(t, filepath.Dir(root), "outside.py", 3)
	session := filepath.Join(projects, EncodeProjectPath(root), "s1.jsonl")
	bash := func(id string, command string) map[string]any {
		line := transcripttest.WithField(transcripttest.ToolUseLine(id, "Bash"), "input", map[string]any{"command": command})
		line["cwd"] = root
		return line
	}
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		bash("b1", "sed -n '2,3p' a.py"), transcripttest.ToolResultLine("b1", map[string]any{"stdout": "line\nline\n"}),
		bash("b2", "cat "+outside), transcripttest.ToolResultLine("b2", map[string]any{"stdout": "line\n"}),
		bash("b3", "cat a.py"), transcripttest.WithField(transcripttest.ToolResultLine("b3", "Error: Exit code 1"), "is_error", true),
		bash("b4", "rg -n nomatch a.py"), transcripttest.ToolResultLine("b4", map[string]any{"stdout": ""}),
	)
	watcher := NewWatcher(projects, root)
	watcher.Scan()
	want := []Event{{Time: transcripttest.Time, Session: "s1", Agent: "main", Tool: "Read", Path: source, Lines: []int{2, 3}}}
	if got := watcher.Poll(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events =\n%+v\nwant\n%+v", got, want)
	}
}
