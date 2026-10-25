package server

import (
	"github.com/derekdshaw/agent-work-visualizer/internal/activity"
	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
)

// graphPayload is the answer of GET /api/graph. View names the session log
// that the page streams. Node IDs are activity keys, the IDs that session
// events use, so they survive the line shifts of an edit.
type graphPayload struct {
	View    string      `json:"view"`
	Root    string      `json:"root"`
	Branch  string      `json:"branch,omitempty"`
	Version int         `json:"version"`
	Nodes   []graphNode `json:"nodes"`
	Edges   []graphEdge `json:"edges"`
}

// graphNode is a callable. Module is its directory, "." for the root.
type graphNode struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	QualifiedName string `json:"qualifiedName"`
	Module        string `json:"module"`
	Kind          string `json:"kind"`
	Language      string `json:"language"`
	File          string `json:"file"`
	Line          uint   `json:"line"`
	EndLine       uint   `json:"endLine"`
}

// graphEdge is a call from A to B.
type graphEdge struct {
	A    string `json:"a"`
	B    string `json:"b"`
	Kind string `json:"kind"`
}

// slimGraph returns the nodes and edges of graph keyed by activity key.
// Callables that share a key, such as Java overloads, become one node, and
// edges between them disappear.
func slimGraph(graph *callgraph.Graph) ([]graphNode, []graphEdge) {
	nodes := []graphNode{}
	keys := make(map[string]string, len(graph.Nodes))
	seen := make(map[string]bool, len(graph.Nodes))
	for _, function := range graph.Nodes {
		key := activity.Key(function.File, function.QualifiedName)
		keys[function.ID] = key
		if seen[key] {
			continue
		}
		seen[key] = true
		module := function.Directory
		if module == "" {
			module = "."
		}
		nodes = append(nodes, graphNode{
			ID: key, Label: function.Name, QualifiedName: function.QualifiedName, Module: module, Kind: function.Kind,
			Language: function.Language, File: function.File, Line: function.Line, EndLine: function.EndLine,
		})
	}
	edges := []graphEdge{}
	linked := make(map[graphEdge]bool, len(graph.Edges))
	for _, edge := range graph.Edges {
		slim := graphEdge{A: keys[edge.Source], B: keys[edge.Target], Kind: edge.Kind}
		if slim.A == "" || slim.B == "" || slim.A == slim.B || linked[slim] {
			continue
		}
		linked[slim] = true
		edges = append(edges, slim)
	}
	return nodes, edges
}
