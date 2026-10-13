package main

import (
	"strings"
	"testing"
)

func TestParseArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		root      string
		port      int
		err       string
	}{
		{name: "defaults", arguments: []string{"repo"}, root: "repo", port: defaultPort},
		{name: "options after root", arguments: []string{"repo", "--port", "9000"}, root: "repo", port: 9000},
		{name: "options before root", arguments: []string{"--port", "0", "repo"}, root: "repo", port: 0},
		{name: "no root", arguments: nil, root: "", port: defaultPort},
		{name: "help", arguments: []string{"--help"}, err: "usage: agent-work-visualizer [ROOT]"},
		{name: "two roots", arguments: []string{"a", "b"}, err: "at most one ROOT"},
		{name: "missing port", arguments: []string{"repo", "--port"}, err: "--port requires a number"},
		{name: "invalid port", arguments: []string{"repo", "--port", "70000"}, err: `invalid port "70000"`},
		{name: "unknown option", arguments: []string{"repo", "--output", "x"}, err: "unknown option --output"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root, port, err := parseArguments(test.arguments)
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("error = %v, want %q", err, test.err)
				}
				return
			}
			if err != nil || root != test.root || port != test.port {
				t.Fatalf("parseArguments = %q, %d, %v", root, port, err)
			}
		})
	}
}
