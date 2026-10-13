package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
)

// The viewer changes the Lambert fragment shader of the bundled three.js by
// replacing a shader include. The replacement does nothing when the shader has
// no such include, so a bundle upgrade can remove the node shading without an
// error.
func TestViewerShaderAnchorsExistInBundle(t *testing.T) {
	lambert := regexp.MustCompile(`meshlambert_frag:"([^"]*)"`).FindStringSubmatch(forceGraphJS)
	if lambert == nil {
		t.Fatal("3d-force-graph bundle has no Lambert fragment shader")
	}
	anchors := regexp.MustCompile(`\.replace\("(#include <\w+>)"`).FindAllStringSubmatch(viewerTemplate, -1)
	if len(anchors) == 0 {
		t.Fatal("viewer has no shader replacements")
	}
	for _, anchor := range anchors {
		if !strings.Contains(lambert[1], anchor[1]) {
			t.Errorf("Lambert fragment shader in the 3d-force-graph bundle has no %q", anchor[1])
		}
	}
}

func TestViewerLiveCameraFrame(t *testing.T) {
	// trailLinks marks a trail link only with its kind, so the camera fit must leave out trail links by kind.
	if !strings.Contains(viewerTemplate, `{source,target,kind:"trail",count:0}`) ||
		!strings.Contains(viewerTemplate, `for(const link of data.links)if(link.kind!=="trail")`) {
		t.Error("camera fit does not leave out trail links by the kind that trailLinks sets")
	}
	if !strings.Contains(viewerTemplate, `if(selected?.id===id)LIVE?frameNodes(node=>node.id===id,.8):fit3D(20,600)`) {
		t.Error("isolation does not keep fit3D for the static report and frameNodes for the live view, or moves the camera after the selection changes")
	}
	if !strings.Contains(viewerTemplate, `setTimeout(()=>{if(selected?.id===id)frameNodes(node=>node.id===id,.8)},220)`) {
		t.Error("an activity click on the selected callable moves the camera after the selection changes")
	}
	// frameLatest returns false while initialFitPending is true, so initialFit must clear the flag first.
	if !strings.Contains(viewerTemplate, `function initialFit(){initialFitPending=false;if(!frameLatest())fit3D(80,500)}`) ||
		strings.Count(viewerTemplate, `if(initialFitPending)initialFit()`) != 2 {
		t.Error("the first view does not frame the latest step, or does not fall back to the overview fit")
	}
	// The static report has no latest step, so this check gives it the overview fit.
	if !strings.Contains(viewerTemplate, `if(!frameGoal(matches,.5))return false;`) ||
		!strings.Contains(viewerTemplate, `frameNodes(matches,.5);return true;`) {
		t.Error("frameLatest does not report whether it moves the camera")
	}
	if !strings.Contains(viewerTemplate, `function placed(node){return Number.isFinite(node.x)&&Number.isFinite(node.y)&&Number.isFinite(node.z)}`) ||
		!strings.Contains(viewerTemplate, `if(source!==target&&(centers.has(source)||centers.has(target))&&Number.isFinite(length))links.push({source,target,length});`) {
		t.Error("camera fit counts recursive calls or links to unplaced nodes in the link median")
	}
	if !strings.Contains(viewerTemplate, `const longest=2.5*lengths[(lengths.length-1)>>1];`) {
		t.Error("camera fit does not drop links longer than 2.5 times the lower median of the links of the matching nodes")
	}
}

func TestViewerLiveFolderPicker(t *testing.T) {
	if !strings.Contains(viewerTemplate, "function startLive(){\n  startRootPicker();") {
		t.Error("the live view does not start the folder picker")
	}
	// The Modules, Unresolved, and Activity views hide .context-right, so the
	// picker must be its sibling to stay visible.
	if !strings.Contains(viewerTemplate, "      </div>\n    </div>\n    <div id=\"rootPicker\" hidden>") {
		t.Error("the folder picker is not a sibling of .context-right")
	}
	// The server sends these field names. TestLiveEventJSONFieldNames checks them.
	for _, field := range []string{`payload.choices.map(choice=>choice.path)`, `const current=choice.path===payload.current;`, `choice.session?`, `choice.worktree?"Worktree"`} {
		if !strings.Contains(viewerTemplate, field) {
			t.Errorf("the folder picker does not read %s", field)
		}
	}
	if !strings.Contains(viewerTemplate, `if(!LIVE.root)setRootPanel(true);else loadRoots();`) {
		t.Error("a page without a folder does not open the folder picker")
	}
	if !strings.Contains(viewerTemplate, `fetch("root",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({path})});`) ||
		!strings.Contains(viewerTemplate, `if(response.ok){location.reload();return}`) {
		t.Error("the folder picker does not post the path as JSON or does not reload after the switch")
	}
}

func TestRenderViewerEscapesDataAndHasNoRemoteScripts(t *testing.T) {
	graph := &callgraph.Graph{
		Nodes: []callgraph.Function{{
			ID:     "</script><script>alert(1)</script>",
			Name:   "<unsafe>",
			Source: `const graphPlaceholder = "/*__GRAPH__*/"`,
		}},
	}
	rendered, err := renderPage(graph)
	if err != nil {
		t.Fatal(err)
	}
	html := string(rendered)
	if strings.Contains(html, `"</script><script>alert(1)</script>"`) {
		t.Fatal("graph data was not escaped")
	}
	if strings.Contains(html, `<option value="metaflow-step">`) {
		t.Fatal("Metaflow filter was rendered without Metaflow nodes or transitions")
	}
	remoteScript := regexp.MustCompile(`(?i)<script[^>]+src\s*=\s*["']https?://`)
	if remoteScript.MatchString(html) {
		t.Fatal("generated HTML contains a remote script")
	}
	if !strings.Contains(html, "cytoscape") || !strings.Contains(html, "ForceGraph3D") {
		t.Fatal("renderer assets were not embedded")
	}
	if !strings.Contains(html, `id="zoomBox"`) ||
		!strings.Contains(html, `addEventListener("pointerdown"`) ||
		!strings.Contains(html, "zoomToFit") {
		t.Fatal("3D rectangle zoom controls were not embedded")
	}
	if !strings.Contains(html, `class="primary-toolbar"`) ||
		!strings.Contains(html, `class="context-toolbar"`) ||
		!strings.Contains(html, `--tools-width:1024px`) ||
		!strings.Contains(html, `width:min(var(--tools-width),calc(100% - 112px))`) ||
		!strings.Contains(html, `minmax(180px,460px)`) ||
		!strings.Contains(html, `id="searchResults"`) ||
		!strings.Contains(html, `role="listbox"`) ||
		!strings.Contains(html, `aria-activedescendant`) ||
		!strings.Contains(html, `aria-selected`) ||
		!strings.Contains(html, `function renderSearchResults(matches)`) ||
		!strings.Contains(html, `matches.slice(0,100)`) ||
		!strings.Contains(html, `id="statusBar" role="status" aria-live="polite"`) ||
		!strings.Contains(html, `function updateStatus(data)`) {
		t.Fatal("graph-first toolbar, search results, or live status was not embedded")
	}
	if !strings.Contains(html, `<details id="key">`) ||
		!strings.Contains(html, `max-height:calc(50vh - 42px)`) ||
		!strings.Contains(html, `overflow:auto`) ||
		!strings.Contains(html, `id="directoryKey"`) ||
		!strings.Contains(html, `id="keyNodeSection"`) ||
		!strings.Contains(html, `id="keySelected"`) ||
		!strings.Contains(html, `id="keySearchMatch"`) ||
		!strings.Contains(html, `id="keyLineSection"`) ||
		!strings.Contains(html, `id="keyDispatch"`) ||
		!strings.Contains(html, "Metaflow transition") {
		t.Fatal("graph color key was not embedded")
	}
	paletteMatch := regexp.MustCompile(`(?s)const palette=\[(.*?)\];`).FindStringSubmatch(html)
	if len(paletteMatch) != 2 {
		t.Fatal("node color palette was not embedded")
	}
	paletteColors := regexp.MustCompile(`#[0-9a-fA-F]{6}`).FindAllString(paletteMatch[1], -1)
	uniquePaletteColors := make(map[string]bool)
	for _, color := range paletteColors {
		uniquePaletteColors[color] = true
	}
	if len(paletteColors) != 25 || len(uniquePaletteColors) != 25 ||
		strings.Contains(html, `%palette.length`) ||
		!strings.Contains(html, `function assignDirectoryColors(nodes)`) ||
		!strings.Contains(html, `const previousColors=new Map(directoryColors)`) ||
		!strings.Contains(html, `directoryColors.clear()`) ||
		!strings.Contains(html, `palette.find(candidate=>!usedColors.has(candidate))`) ||
		!strings.Contains(html, `assignDirectoryColors(data.nodes);graph.graphData(data)`) ||
		!strings.Contains(html, "const data=currentData();const nodes=data.nodes;const edges=data.links;\n  assignDirectoryColors(nodes);") ||
		!strings.Contains(html, `Palette overflow (`) {
		t.Fatal("25-color non-repeating directory palette was not embedded")
	}
	if !strings.Contains(html, `incoming=new Map(`) ||
		!strings.Contains(html, `searchMatches=new Set`) ||
		!strings.Contains(html, `if(!visible.size||![...visible].some(id=>adjacent.get(id)?.size))DATA.nodes.slice(0,50)`) ||
		!strings.Contains(html, `for(const edge of incoming.get(id)||[])visible.add(edge.source)`) ||
		!strings.Contains(html, `!searchMatches||searchMatches.has(target)`) {
		t.Fatal("direct-caller search was not embedded")
	}
	if !strings.Contains(html, `id="backButton"`) ||
		!strings.Contains(html, `aria-label="Back"`) ||
		!strings.Contains(html, `const navigationHistory=[]`) ||
		!strings.Contains(html, `navigationHistory.push(captureViewState())`) ||
		!strings.Contains(html, `function restoreViewState(state)`) ||
		!strings.Contains(html, `const state=navigationHistory.pop()`) {
		t.Fatal("drill-in navigation history was not embedded")
	}
	if !strings.Contains(html, `id="labelToggle"`) ||
		!strings.Contains(html, `id="labelLayer"`) ||
		!strings.Contains(html, `function draw3DLabels()`) ||
		!strings.Contains(html, `graph.graph2ScreenCoords(node.x,node.y,node.z)`) ||
		!strings.Contains(html, `labelLayer.style.width=cssWidth`) ||
		!strings.Contains(html, `labelLayer.style.height=cssHeight`) ||
		!strings.Contains(html, `labelsVisible=!labelsVisible`) {
		t.Fatal("3D function label toggle was not embedded")
	}
	if !strings.Contains(html, `data-view="unresolved"`) ||
		!strings.Contains(html, `id="unresolvedView"`) ||
		!strings.Contains(html, `function renderUnresolved()`) ||
		!strings.Contains(html, `Language runtime / built-ins`) ||
		!strings.Contains(html, `External / imported APIs`) ||
		!strings.Contains(html, `Dynamic / unknown`) ||
		!strings.Contains(html, `["Call / function","Count","File path:line"]`) ||
		!strings.Contains(html, "`${value.file}:${value.line}`") ||
		!strings.Contains(html, `table-layout:fixed`) ||
		!strings.Contains(html, `width:clamp(180px,28vw,320px)`) ||
		!strings.Contains(html, `if(index!==1)cell.title=text`) {
		t.Fatal("unresolved-call tab was not embedded")
	}
	if !strings.Contains(html, `data-view="modules"`) ||
		!strings.Contains(html, `id="modulesView"`) ||
		!strings.Contains(html, `function connectedComponents(ids)`) ||
		!strings.Contains(html, `function renderInventory()`) ||
		!strings.Contains(html, `function openGraphScope(type,label,ids)`) ||
		!strings.Contains(html, `openGraphScope("module"`) ||
		!strings.Contains(html, `openGraphScope("component"`) ||
		!strings.Contains(html, `graphScope?graphScope.ids:visible`) ||
		!strings.Contains(html, `scope:graphScope?{type:graphScope.type,label:graphScope.label,ids:[...graphScope.ids]}:null`) {
		t.Fatal("module and component inventory navigation was not embedded")
	}
	if !strings.Contains(html, `function focusedScope(start,depth,filter)`) ||
		!strings.Contains(html, `edge.kind==="transition"`) ||
		!strings.Contains(html, `if(edge.kind==="call"){ids.add(edge.target);edges.add(edge)}`) ||
		!strings.Contains(html, `scope?scope.ids.has(node.id)`) ||
		!strings.Contains(html, `function metaflowMaximumDepth(start)`) ||
		!strings.Contains(html, `function componentMaximumDepth(start,filter)`) ||
		!strings.Contains(html, `return componentMaximumDepth(selected.id,filter)`) ||
		!strings.Contains(html, `Depth ${isolationDepth} / ${maximum}`) ||
		strings.Contains(html, `?metaflowMaximumDepth(selected.id):4}`) ||
		!strings.Contains(html, `Math.min(maximumDepth(),Math.max(1,depth))`) ||
		!strings.Contains(html, `directed:document.querySelector("#entryFilter").value==="metaflow-step"`) {
		t.Fatal("focused entrypoint filters were not embedded")
	}
	if !strings.Contains(html, `id="modeTabs"`) ||
		!strings.Contains(html, `id="focusView"`) ||
		!strings.Contains(html, `id="depthDown"`) ||
		!strings.Contains(html, `id="depthUp"`) ||
		!strings.Contains(html, `function setDepth(depth)`) ||
		!strings.Contains(html, `function nodeSize(node)`) ||
		!strings.Contains(html, `return node.entrypoint?18:8`) ||
		!strings.Contains(html, `.linkWidth(edge=>Math.min(2.5+1.5*Math.log2(edge.count),8))`) ||
		!strings.Contains(html, `.linkDirectionalArrowLength(edge=>edge.kind==="transition"?7:edge.kind==="dispatch"?6:4)`) ||
		!strings.Contains(html, `function isolateNode(id,view)`) ||
		!strings.Contains(html, `focusGraph.on("tap","node"`) ||
		!strings.Contains(html, `function compactText(value,maximum=56)`) ||
		strings.Contains(html, `.nodeRelSize(`) ||
		!strings.Contains(html, `function fit3D(padding=20,duration=400)`) ||
		strings.Contains(html, `cap3DZoom`) ||
		!strings.Contains(html, `fit3D(20,600)`) ||
		!strings.Contains(html, `roots:[selected.id]`) ||
		!strings.Contains(html, `"min-zoomed-font-size":12`) ||
		!strings.Contains(html, `"text-max-width":180`) ||
		!strings.Contains(html, `"text-outline-color":"#0b1020"`) ||
		!strings.Contains(html, `"width":44,"height":44`) ||
		!strings.Contains(html, `"width":4,"arrow-scale":1.35`) ||
		!strings.Contains(html, `function renderKey(nodes,edges)`) {
		t.Fatal("full-window 3D/2D isolation controls were not embedded")
	}
	if !strings.Contains(html, `id="panelToggle"`) ||
		!strings.Contains(html, `class="panel-side"`) ||
		!strings.Contains(html, `right:10px`) ||
		!strings.Contains(html, `<aside id="callPanel"`) ||
		!strings.Contains(html, `id="panelResize"`) ||
		!strings.Contains(html, `body.panel-open #graph`) ||
		!strings.Contains(html, `function setPanelOpen(open)`) ||
		!strings.Contains(html, `function setPanelWidth(width)`) ||
		!strings.Contains(html, `id="stackRows"`) ||
		!strings.Contains(html, `.stack-table{width:100%;table-layout:fixed`) ||
		!strings.Contains(html, `function renderCallStack()`) ||
		!strings.Contains(html, `<th>Callable</th><th>Calls</th><th>Location</th>`) ||
		!strings.Contains(html, `depth>=isolationDepth`) ||
		!strings.Contains(html, `panelOpenBeforeUnresolved`) ||
		!strings.Contains(html, `unresolved-mode`) ||
		!strings.Contains(html, `document.querySelector("#sourceCode code").textContent=node.source`) ||
		!strings.Contains(html, `id="codeBack"`) {
		t.Fatal("call-stack source panel was not embedded")
	}
	if !strings.Contains(html, `<title>Code call graph</title>`) ||
		!strings.Contains(html, `${node.language} · ${node.kind}`) ||
		!strings.Contains(html, `function nodeDisplayName(node)`) ||
		!strings.Contains(html, `Search callable, package/module, or file`) {
		t.Fatal("mixed-language viewer metadata was not embedded")
	}
	if !strings.Contains(html, `id="helpButton"`) ||
		!strings.Contains(html, `<dialog id="helpModal">`) ||
		!strings.Contains(html, `.showModal()`) ||
		strings.Contains(html, `Ctrl-drag to zoom · Ctrl-click to reset zoom`) {
		t.Fatal("modal toolbar help was not embedded")
	}
	if !strings.Contains(html, `button:focus-visible`) ||
		!strings.Contains(html, `@media(prefers-reduced-motion:reduce)`) ||
		!strings.Contains(html, `font:14px/1.4 system-ui`) {
		t.Fatal("viewer accessibility and typography improvements were not embedded")
	}
}

func TestRenderViewerIncludesMetaflowFilterWhenPresent(t *testing.T) {
	graph := &callgraph.Graph{
		Nodes: []callgraph.Function{{ID: "flow.start", EntryKind: "metaflow-step"}},
	}
	rendered, err := renderPage(graph)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), `<option value="metaflow-step">Metaflow steps</option>`) {
		t.Fatal("Metaflow filter was omitted for a Metaflow graph")
	}
}

// renderPage renders the viewer page for graph as a view does.
func renderPage(graph *callgraph.Graph) ([]byte, error) {
	data, err := marshalGraph(graph)
	if err != nil {
		return nil, err
	}
	return renderViewer(graph, data, `{"version":1,"instance":"i","root":""}`)
}
