package server

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
)

//go:embed viewer.html
var viewerTemplate string

//go:embed assets/cytoscape.min.js
var cytoscapeJS string

//go:embed assets/3d-force-graph.min.js
var forceGraphJS string

// renderViewer returns the viewer page with the graph data and the live state
// embedded. live is the JSON of the page's LIVE constant.
func renderViewer(graph *callgraph.Graph, graphJSON []byte, live string) ([]byte, error) {
	if strings.Count(viewerTemplate, "/*__GRAPH__*/") != 1 ||
		strings.Count(viewerTemplate, "/*__LIVE__*/") != 1 ||
		strings.Count(viewerTemplate, "<!--__METAFLOW_OPTION__-->") != 1 {
		return nil, errors.New("viewer template placeholder is missing or duplicated")
	}
	rendered := strings.Replace(viewerTemplate, "/*__CYTOSCAPE__*/", cytoscapeJS, 1)
	rendered = strings.Replace(rendered, "/*__FORCE_GRAPH__*/", forceGraphJS, 1)
	rendered = strings.NewReplacer("/*__LIVE__*/null", live, "/*__GRAPH__*/", string(graphJSON)).Replace(rendered)
	metaflowOption := ""
	if graphHasMetaflow(graph) {
		metaflowOption = `<option value="metaflow-step">Metaflow steps</option>`
	}
	rendered = strings.Replace(rendered, "<!--__METAFLOW_OPTION__-->", metaflowOption, 1)
	return []byte(rendered), nil
}

func graphHasMetaflow(graph *callgraph.Graph) bool {
	for _, node := range graph.Nodes {
		if node.EntryKind == "metaflow-step" {
			return true
		}
	}
	for _, edge := range graph.Edges {
		if edge.Kind == "transition" {
			return true
		}
	}
	return false
}

// marshalGraph encodes graph for embedding in the page's script element.
func marshalGraph(graph *callgraph.Graph) ([]byte, error) {
	data, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	if bytes.Contains(data, []byte("</script")) {
		return nil, errors.New("encoded graph contains an unsafe script terminator")
	}
	return data, nil
}
