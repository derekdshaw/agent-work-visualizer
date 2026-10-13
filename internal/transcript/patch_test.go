package transcript

import (
	"slices"
	"testing"
)

func TestShiftLine(t *testing.T) {
	insertion := []Hunk{{OldStart: 3, OldLines: 2, NewStart: 3, NewLines: 4, Lines: []string{" a", "+x", "+y", " b"}}}
	deletion := []Hunk{{OldStart: 2, OldLines: 3, NewStart: 2, NewLines: 1, Lines: []string{" a", "-b", "-c"}}}
	replacement := []Hunk{{OldStart: 5, OldLines: 2, NewStart: 5, NewLines: 3, Lines: []string{"-old1", "-old2", "+new1", "+new2", "+new3"}}}
	multiple := []Hunk{
		{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 2, Lines: []string{" a", "+x"}},
		{OldStart: 10, OldLines: 2, NewStart: 11, NewLines: 1, Lines: []string{" k", "-l"}},
	}
	noNewline := []Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{"-a", `\ No newline at end of file`, "+a", "+b"}}}
	firstLine := []Hunk{{OldStart: 1, OldLines: 2, NewStart: 1, NewLines: 1, Lines: []string{"-a", " b"}}}
	tests := []struct {
		name  string
		line  int
		hunks []Hunk
		want  int
	}{
		{"before insertion", 1, insertion, 1},
		{"context line before inserted lines", 3, insertion, 3},
		{"context line after inserted lines", 4, insertion, 6},
		{"after insertion hunk", 10, insertion, 12},
		{"deleted line maps to the line before", 3, deletion, 2},
		{"second deleted line maps to the line before", 4, deletion, 2},
		{"after deletion hunk", 5, deletion, 3},
		{"first replaced line", 5, replacement, 5},
		{"second replaced line", 6, replacement, 5},
		{"after replacement", 7, replacement, 8},
		{"between hunks", 5, multiple, 6},
		{"context in second hunk", 10, multiple, 11},
		{"deleted at the end of the second hunk", 11, multiple, 11},
		{"deleted first line of the file", 1, firstLine, 1},
		{"after all hunks", 20, multiple, 20},
		{"no-newline marker", 1, noNewline, 1},
		{"no hunks", 7, nil, 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ShiftLine(test.line, test.hunks); got != test.want {
				t.Fatalf("ShiftLine(%d) = %d, want %d", test.line, got, test.want)
			}
		})
	}
}

func TestChangedLines(t *testing.T) {
	tests := []struct {
		name  string
		hunks []Hunk
		want  []int
	}{
		{"insertion", []Hunk{{NewStart: 3, Lines: []string{" a", "+x", "+y", " b"}}}, []int{4, 5}},
		{"deletion", []Hunk{{NewStart: 2, Lines: []string{" a", "-b", "-c", " d"}}}, []int{2, 2}},
		{"deletion at the end of a hunk", []Hunk{{NewStart: 4, Lines: []string{" a", "-b"}}}, []int{4}},
		{"deletion of the first line", []Hunk{{NewStart: 1, Lines: []string{"-a", " b"}}}, []int{1}},
		{"replacement", []Hunk{{NewStart: 5, Lines: []string{"-o", "+n1", "+n2"}}}, []int{5, 5, 6}},
		{"multiple hunks", []Hunk{
			{NewStart: 2, Lines: []string{" a", "+x"}},
			{NewStart: 11, Lines: []string{" k", "-l", `\ No newline at end of file`, "+m"}},
		}, []int{3, 12, 12}},
		{"no hunks", nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ChangedLines(test.hunks); !slices.Equal(got, test.want) {
				t.Fatalf("changedLines = %v, want %v", got, test.want)
			}
		})
	}
}
