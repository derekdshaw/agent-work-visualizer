// Package callgraph holds the call graph model shared by the language
// analyzers, the orchestrator, and the report renderer.
package callgraph

type Graph struct {
	Root       string           `json:"root"`
	Files      []File           `json:"files"`
	Nodes      []Function       `json:"nodes"`
	Edges      []Edge           `json:"edges"`
	Unresolved []UnresolvedCall `json:"unresolved"`
	Stats      Stats            `json:"stats"`
}

type File struct {
	Path     string `json:"path"`
	Module   string `json:"module"`
	Language string `json:"language"`
}

type Function struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	QualifiedName string   `json:"qualifiedName"`
	Module        string   `json:"module"`
	File          string   `json:"file"`
	Directory     string   `json:"directory"`
	Line          uint     `json:"line"`
	Column        uint     `json:"column"`
	EndLine       uint     `json:"endLine"`
	Signature     string   `json:"signature"`
	Source        string   `json:"source"`
	Decorators    []string `json:"decorators,omitempty"`
	Class         string   `json:"class,omitempty"`
	Parent        string   `json:"parent,omitempty"`
	Async         bool     `json:"async"`
	Entrypoint    bool     `json:"entrypoint"`
	EntryKind     string   `json:"entryKind,omitempty"`
	Language      string   `json:"language"`
	Kind          string   `json:"kind"`
	Modifiers     []string `json:"modifiers,omitempty"`
	Parameters    []string `json:"parameters,omitempty"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Count  int    `json:"count"`
	Lines  []uint `json:"lines"`
}

type UnresolvedCall struct {
	Caller     string `json:"caller"`
	Expression string `json:"expression"`
	File       string `json:"file"`
	Line       uint   `json:"line"`
	Category   string `json:"category"`
	Language   string `json:"language"`
}

type Stats struct {
	Files       int `json:"files"`
	Definitions int `json:"definitions"`
	Calls       int `json:"calls"`
	Resolved    int `json:"resolved"`
	Unresolved  int `json:"unresolved"`
}
