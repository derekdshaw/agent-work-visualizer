package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

type rustAnalysis struct {
	files map[string]*rustFile
	calls []rustCall
}

type rustFile struct {
	module  string
	imports map[string]string
}

type rustCall struct {
	caller     string
	expression string
	file       string
	module     string
	line       uint
}

func newRustAnalysis() *rustAnalysis {
	return &rustAnalysis{files: make(map[string]*rustFile)}
}

func (a *analyzer) analyzeRustFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(tree_sitter.NewLanguage(tree_sitter_rust.Language())); err != nil {
		return fmt.Errorf("configure Rust parser: %w", err)
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return fmt.Errorf("parse %s: parser returned no tree", path)
	}
	defer tree.Close()
	root := tree.RootNode()
	if bad := firstInvalidNode(root); bad != nil {
		point := bad.StartPosition()
		kind := "syntax error"
		if bad.IsMissing() {
			kind = "missing syntax"
		}
		return fmt.Errorf("%s:%d:%d: %s (%s)", path, point.Row+1, point.Column+1, kind, bad.Kind())
	}
	relative, err := filepath.Rel(a.root, path)
	if err != nil {
		return fmt.Errorf("relative path for %s: %w", path, err)
	}
	relative = filepath.ToSlash(relative)
	state := &rustFile{module: rustModuleName(relative), imports: make(map[string]string)}
	a.rust.files[relative] = state
	a.graph.Files = append(a.graph.Files, GraphFile{Path: relative, Module: state.module, Language: "rust"})
	a.graph.Stats.Files++
	a.graph.Stats.ProjectFiles++
	a.walkRust(root, source, relative, state, "", "")
	return nil
}

func rustModuleName(relative string) string {
	module := strings.TrimSuffix(filepath.ToSlash(relative), ".rs")
	module = strings.TrimPrefix(module, "src/")
	module = strings.TrimSuffix(module, "/mod")
	if module == "main" || module == "lib" || module == "mod" {
		return ""
	}
	return strings.ReplaceAll(module, "/", ".")
}

func (a *analyzer) walkRust(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *rustFile,
	owner string,
	parentID string,
) {
	switch node.Kind() {
	case "use_declaration":
		collectRustUse(node.Utf8Text(source), state)
		return
	case "mod_item":
		name := node.ChildByFieldName("name")
		body := node.ChildByFieldName("body")
		if name == nil || body == nil {
			return
		}
		nested := *state
		nested.module = qualified(state.module, name.Utf8Text(source))
		for i := uint(0); i < body.NamedChildCount(); i++ {
			a.walkRust(body.NamedChild(i), source, file, &nested, owner, parentID)
		}
		return
	case "impl_item":
		typeNode := node.ChildByFieldName("type")
		body := node.ChildByFieldName("body")
		if typeNode == nil || body == nil {
			return
		}
		typeName := rustBaseType(typeNode.Utf8Text(source))
		for i := uint(0); i < body.NamedChildCount(); i++ {
			a.walkRust(body.NamedChild(i), source, file, state, typeName, parentID)
		}
		return
	case "trait_item":
		name := node.ChildByFieldName("name")
		body := node.ChildByFieldName("body")
		if name == nil || body == nil {
			return
		}
		for i := uint(0); i < body.NamedChildCount(); i++ {
			a.walkRust(body.NamedChild(i), source, file, state, name.Utf8Text(source), parentID)
		}
		return
	case "function_item":
		a.collectRustFunction(node, source, file, state, owner, parentID)
		return
	case "call_expression":
		function := node.ChildByFieldName("function")
		if function != nil {
			a.graph.Stats.Calls++
			a.rust.calls = append(a.rust.calls, rustCall{
				caller:     parentID,
				expression: strings.TrimSpace(function.Utf8Text(source)),
				file:       file,
				module:     state.module,
				line:       node.StartPosition().Row + 1,
			})
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		a.walkRust(node.NamedChild(i), source, file, state, owner, parentID)
	}
}

func (a *analyzer) collectRustFunction(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *rustFile,
	owner string,
	parentID string,
) {
	nameNode := node.ChildByFieldName("name")
	parameters := node.ChildByFieldName("parameters")
	body := node.ChildByFieldName("body")
	if nameNode == nil || parameters == nil || body == nil {
		return
	}
	name := nameNode.Utf8Text(source)
	prefix := state.module
	if parentID != "" {
		if parent := a.nodesByID[parentID]; parent != nil {
			prefix = parent.QualifiedName
		}
	} else if owner != "" {
		prefix = qualified(state.module, owner)
	}
	qualifiedName := qualified(prefix, name)
	point := node.StartPosition()
	end := node.EndPosition()
	header := string(source[node.StartByte():body.StartByte()])
	function := Function{
		ID:            fmt.Sprintf("%s:%d:%s", file, point.Row+1, qualifiedName),
		Name:          name,
		QualifiedName: qualifiedName,
		Module:        state.module,
		File:          file,
		Directory:     filepath.ToSlash(filepath.Dir(file)),
		Line:          point.Row + 1,
		Column:        point.Column + 1,
		EndLine:       end.Row + 1,
		Signature:     strings.TrimSpace(header),
		Source:        node.Utf8Text(source),
		Class:         owner,
		Parent:        parentID,
		Async:         strings.Contains(header, "async fn"),
		Language:      "rust",
		Kind:          "function",
		Modifiers:     rustModifiers(header),
		Parameters:    rustParameters(parameters, source),
	}
	if function.Directory == "." {
		function.Directory = ""
	}
	if owner != "" {
		function.Kind = "method"
	}
	if name == "main" && owner == "" && parentID == "" {
		function.Entrypoint = true
		function.EntryKind = "main"
	}
	a.graph.Nodes = append(a.graph.Nodes, function)
	a.nodesByID[function.ID] = &a.graph.Nodes[len(a.graph.Nodes)-1]
	a.graph.Stats.Definitions++
	a.graph.Stats.ProjectDefinitions++
	for i := uint(0); i < body.NamedChildCount(); i++ {
		a.walkRust(body.NamedChild(i), source, file, state, owner, function.ID)
	}
}

func rustBaseType(value string) string {
	value = strings.TrimSpace(value)
	if index := strings.IndexByte(value, '<'); index >= 0 {
		value = value[:index]
	}
	if index := strings.LastIndex(value, "::"); index >= 0 {
		value = value[index+2:]
	}
	return strings.TrimSpace(value)
}

func rustModifiers(header string) []string {
	var modifiers []string
	for _, modifier := range []string{"pub", "async", "const", "unsafe", "extern"} {
		if strings.Contains(header, modifier+" ") {
			modifiers = append(modifiers, modifier)
		}
	}
	return modifiers
}

func rustParameters(parameters *tree_sitter.Node, source []byte) []string {
	var values []string
	for i := uint(0); i < parameters.NamedChildCount(); i++ {
		values = append(values, strings.TrimSpace(parameters.NamedChild(i).Utf8Text(source)))
	}
	return values
}

func collectRustUse(text string, state *rustFile) {
	value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(text), "use "), ";"))
	if open := strings.Index(value, "::{"); open >= 0 && strings.HasSuffix(value, "}") {
		prefix := value[:open]
		for _, item := range strings.Split(value[open+3:len(value)-1], ",") {
			collectRustImport(prefix+"::"+strings.TrimSpace(item), state)
		}
		return
	}
	collectRustImport(value, state)
}

func collectRustImport(value string, state *rustFile) {
	parts := strings.Split(value, " as ")
	target := strings.TrimSpace(parts[0])
	alias := target
	if index := strings.LastIndex(alias, "::"); index >= 0 {
		alias = alias[index+2:]
	}
	if len(parts) == 2 {
		alias = strings.TrimSpace(parts[1])
	}
	state.imports[alias] = resolveRustPath(state.module, target)
}

func resolveRustPath(module string, path string) string {
	path = strings.ReplaceAll(strings.TrimSpace(path), "::", ".")
	switch {
	case path == "crate":
		return ""
	case strings.HasPrefix(path, "crate."):
		return strings.TrimPrefix(path, "crate.")
	case path == "self":
		return module
	case strings.HasPrefix(path, "self."):
		return qualified(module, strings.TrimPrefix(path, "self."))
	case path == "super" || strings.HasPrefix(path, "super."):
		parent := module
		if index := strings.LastIndexByte(parent, '.'); index >= 0 {
			parent = parent[:index]
		} else {
			parent = ""
		}
		return qualified(parent, strings.TrimPrefix(path, "super."))
	default:
		return path
	}
}

func (a *analyzer) resolveRustCalls() {
	byQualified := make(map[string]*Function)
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if function.Language == "rust" {
			a.nodesByID[function.ID] = function
			byQualified[function.QualifiedName] = function
		}
	}
	edges := make(map[string]*Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.rust.calls {
		target := a.resolveRustCall(call, byQualified)
		if target == nil {
			if call.caller != "" {
				a.graph.Unresolved = append(a.graph.Unresolved, UnresolvedCall{
					Caller: call.caller, Expression: call.expression, File: call.file, Line: call.line,
					Category: a.rustUnresolvedCategory(call), Language: "rust",
				})
			}
			continue
		}
		if call.caller == "" {
			continue
		}
		key := call.caller + "\x00" + target.ID + "\x00call"
		if edges[key] == nil {
			edges[key] = &Edge{Source: call.caller, Target: target.ID, Kind: "call"}
			lineSets[key] = make(map[uint]bool)
		}
		edges[key].Count++
		lineSets[key][call.line] = true
		a.graph.Stats.Resolved++
	}
	for key, edge := range edges {
		for line := range lineSets[key] {
			edge.Lines = append(edge.Lines, line)
		}
		sort.Slice(edge.Lines, func(i, j int) bool { return edge.Lines[i] < edge.Lines[j] })
		a.graph.Edges = append(a.graph.Edges, *edge)
	}
	a.graph.Stats.Unresolved = len(a.graph.Unresolved)
}

func (a *analyzer) resolveRustCall(call rustCall, byQualified map[string]*Function) *Function {
	caller := a.nodesByID[call.caller]
	state := a.rust.files[call.file]
	expression := strings.ReplaceAll(strings.TrimSpace(call.expression), "::", ".")
	expression = strings.ReplaceAll(expression, "::<", "<")
	if index := strings.IndexByte(expression, '<'); index >= 0 {
		expression = expression[:index]
	}
	if caller != nil && (strings.HasPrefix(expression, "self.") || strings.HasPrefix(expression, "Self.")) {
		if caller.Class != "" {
			name := strings.TrimPrefix(strings.TrimPrefix(expression, "self."), "Self.")
			return byQualified[qualified(caller.Module, caller.Class, name)]
		}
	}
	if !strings.Contains(expression, ".") {
		if caller != nil {
			prefix := caller.QualifiedName
			for {
				if target := byQualified[qualified(prefix, expression)]; target != nil {
					return target
				}
				index := strings.LastIndexByte(prefix, '.')
				if index < 0 {
					break
				}
				prefix = prefix[:index]
			}
		}
		if imported := state.imports[expression]; imported != "" {
			return byQualified[imported]
		}
		return byQualified[qualified(call.module, expression)]
	}
	root, suffix, _ := strings.Cut(expression, ".")
	if imported := state.imports[root]; imported != "" {
		return byQualified[qualified(imported, suffix)]
	}
	if strings.HasPrefix(expression, "crate.") {
		return byQualified[strings.TrimPrefix(expression, "crate.")]
	}
	if strings.HasPrefix(expression, "super.") {
		return byQualified[resolveRustPath(call.module, strings.Replace(expression, ".", "::", 1))]
	}
	if target := byQualified[qualified(call.module, expression)]; target != nil {
		return target
	}
	return byQualified[expression]
}

func (a *analyzer) rustUnresolvedCategory(call rustCall) string {
	root := strings.Split(strings.ReplaceAll(call.expression, "::", "."), ".")[0]
	if state := a.rust.files[call.file]; state != nil && state.imports[root] != "" {
		return "external"
	}
	return "dynamic"
}
