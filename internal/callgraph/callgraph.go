// Package callgraph holds the call graph model shared by the language
// analyzers and the orchestrator.
package callgraph

type Graph struct {
	Root  string     `json:"root"`
	Nodes []Function `json:"nodes"`
	Edges []Edge     `json:"edges"`
}

type Function struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	QualifiedName string   `json:"qualifiedName"`
	Module        string   `json:"module"`
	File          string   `json:"file"`
	Directory     string   `json:"directory"`
	Line          uint     `json:"line"`
	EndLine       uint     `json:"endLine"`
	Decorators    []string `json:"decorators,omitempty"`
	Class         string   `json:"class,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	Language      string   `json:"language"`
	Kind          string   `json:"kind"`
	Modifiers     []string `json:"modifiers,omitempty"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
}
