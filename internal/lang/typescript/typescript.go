package typescript

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langutil"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// Language analyzes TypeScript sources. It holds no state, so one value can
// analyze any number of trees.
type Language struct{}

func New() Language {
	return Language{}
}

func (Language) Name() string {
	return "typescript"
}

func (Language) Extensions() []string {
	return []string{".ts", ".tsx", ".mts", ".cts"}
}

func (Language) SkipDirs() []string {
	return []string{"node_modules", "dist", "out"}
}

func (Language) IsTestFile(relativePath string) bool {
	name := filepath.Base(relativePath)
	for _, extension := range []string{".ts", ".tsx", ".mts", ".cts"} {
		if strings.HasSuffix(name, ".test"+extension) || strings.HasSuffix(name, ".spec"+extension) {
			return true
		}
	}
	return langutil.InDirectory(relativePath, "test", "tests")
}

func (Language) Analyze(root string, files []string) (*callgraph.Graph, error) {
	a := &analysis{
		root:      root,
		nodesByID: make(map[string]*callgraph.Function),
		files:     make(map[string]*typescriptFile),
	}
	if err := langutil.EachFile(root, files, a.analyzeTypescriptFile); err != nil {
		return nil, err
	}
	a.resolveTypescriptCalls()
	return &a.graph, nil
}

type analysis struct {
	root      string
	graph     callgraph.Graph
	nodesByID map[string]*callgraph.Function
	files     map[string]*typescriptFile
	calls     []typescriptCall
}

type typescriptFile struct {
	module  string
	imports map[string]string
}

type typescriptCall struct {
	caller      string
	expression  string
	file        string
	line        uint
	constructor bool
}

func (a *analysis) analyzeTypescriptFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	language := tree_sitter_typescript.LanguageTypescript()
	if filepath.Ext(path) == ".tsx" {
		language = tree_sitter_typescript.LanguageTSX()
	}
	tree, err := langutil.Parse(path, source, tree_sitter.NewLanguage(language))
	if err != nil {
		return err
	}
	defer tree.Close()
	root := tree.RootNode()
	relative, err := filepath.Rel(a.root, path)
	if err != nil {
		return fmt.Errorf("relative path for %s: %w", path, err)
	}
	relative = filepath.ToSlash(relative)
	state := &typescriptFile{
		module:  typescriptModuleName(relative),
		imports: make(map[string]string),
	}
	a.files[relative] = state
	a.graph.Files = append(a.graph.Files, callgraph.File{Path: relative, Module: state.module, Language: "typescript"})
	a.graph.Stats.Files++
	a.walkTypescript(root, source, relative, state, "", "")
	return nil
}

func typescriptModuleName(relative string) string {
	module := strings.TrimSuffix(filepath.ToSlash(relative), filepath.Ext(relative))
	module = strings.TrimSuffix(module, "/index")
	if module == "index" {
		return ""
	}
	return strings.ReplaceAll(module, "/", ".")
}

func (a *analysis) walkTypescript(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *typescriptFile,
	class string,
	parentID string,
) {
	switch node.Kind() {
	case "export_statement":
		if strings.HasPrefix(strings.TrimSpace(node.Utf8Text(source)), "export default ") {
			for i := uint(0); i < node.NamedChildCount(); i++ {
				child := node.NamedChild(i)
				if child.Kind() == "function_declaration" {
					a.collectTypescriptFunction(child, child, source, file, state, class, parentID, "", true)
					return
				}
			}
		}
	case "import_statement":
		collectTypescriptImport(node.Utf8Text(source), state)
		return
	case "class_declaration":
		name := node.ChildByFieldName("name")
		body := node.ChildByFieldName("body")
		if name == nil || body == nil {
			return
		}
		for i := uint(0); i < body.NamedChildCount(); i++ {
			a.walkTypescript(body.NamedChild(i), source, file, state, name.Utf8Text(source), parentID)
		}
		return
	case "function_declaration":
		a.collectTypescriptFunction(node, node, source, file, state, class, parentID, "", false)
		return
	case "method_definition":
		a.collectTypescriptFunction(node, node, source, file, state, class, parentID, "", false)
		return
	case "variable_declarator":
		name := node.ChildByFieldName("name")
		value := node.ChildByFieldName("value")
		if name != nil && value != nil && (value.Kind() == "arrow_function" || value.Kind() == "function_expression") {
			a.collectTypescriptFunction(value, node, source, file, state, class, parentID, name.Utf8Text(source), false)
			return
		}
	case "call_expression":
		function := node.ChildByFieldName("function")
		if function != nil {
			a.graph.Stats.Calls++
			a.calls = append(a.calls, typescriptCall{
				caller:     parentID,
				expression: strings.TrimSpace(function.Utf8Text(source)),
				file:       file,
				line:       node.StartPosition().Row + 1,
			})
		}
	case "new_expression":
		constructor := node.ChildByFieldName("constructor")
		if constructor != nil {
			a.graph.Stats.Calls++
			a.calls = append(a.calls, typescriptCall{
				caller:      parentID,
				expression:  strings.TrimSpace(constructor.Utf8Text(source)),
				file:        file,
				line:        node.StartPosition().Row + 1,
				constructor: true,
			})
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		a.walkTypescript(node.NamedChild(i), source, file, state, class, parentID)
	}
}

func (a *analysis) collectTypescriptFunction(
	functionNode *tree_sitter.Node,
	sourceNode *tree_sitter.Node,
	source []byte,
	file string,
	state *typescriptFile,
	class string,
	parentID string,
	assignedName string,
	exportDefault bool,
) {
	nameNode := functionNode.ChildByFieldName("name")
	name := assignedName
	if name == "" && nameNode != nil {
		name = nameNode.Utf8Text(source)
	}
	if name == "" && exportDefault {
		name = "default"
	}
	parameters := functionNode.ChildByFieldName("parameters")
	if parameters == nil {
		parameters = functionNode.ChildByFieldName("parameter")
	}
	body := functionNode.ChildByFieldName("body")
	if name == "" || body == nil {
		return
	}
	prefix := state.module
	if parentID != "" {
		if parent := a.nodesByID[parentID]; parent != nil {
			prefix = parent.QualifiedName
		}
	} else if class != "" {
		prefix = langutil.Qualified(state.module, class)
	}
	qualifiedName := langutil.Qualified(prefix, name)
	point := sourceNode.StartPosition()
	end := sourceNode.EndPosition()
	signatureEnd := body.StartByte()
	if signatureEnd < sourceNode.StartByte() {
		signatureEnd = sourceNode.EndByte()
	}
	signature := strings.TrimSpace(string(source[sourceNode.StartByte():signatureEnd]))
	function := callgraph.Function{
		ID:            fmt.Sprintf("%s:%d:%s", file, point.Row+1, qualifiedName),
		Name:          name,
		QualifiedName: qualifiedName,
		Module:        state.module,
		File:          file,
		Directory:     filepath.ToSlash(filepath.Dir(file)),
		Line:          point.Row + 1,
		Column:        point.Column + 1,
		EndLine:       end.Row + 1,
		Signature:     signature,
		Source:        sourceNode.Utf8Text(source),
		Class:         class,
		Parent:        parentID,
		Async:         strings.Contains(signature, "async"),
		Language:      "typescript",
		Kind:          "function",
		Modifiers:     typescriptModifiers(signature),
		Parameters:    typescriptParameters(parameters, source),
	}
	if function.Directory == "." {
		function.Directory = ""
	}
	if class != "" {
		function.Kind = "method"
	}
	if exportDefault && !slices.Contains(function.Modifiers, "default") {
		function.Modifiers = append(function.Modifiers, "default")
	}
	a.graph.Nodes = append(a.graph.Nodes, function)
	a.nodesByID[function.ID] = &a.graph.Nodes[len(a.graph.Nodes)-1]
	a.graph.Stats.Definitions++
	for i := uint(0); i < body.NamedChildCount(); i++ {
		a.walkTypescript(body.NamedChild(i), source, file, state, class, function.ID)
	}
}

func typescriptModifiers(signature string) []string {
	var modifiers []string
	for _, modifier := range []string{"export", "default", "public", "protected", "private", "static", "async"} {
		if strings.Contains(signature, modifier+" ") {
			modifiers = append(modifiers, modifier)
		}
	}
	return modifiers
}

func typescriptParameters(parameters *tree_sitter.Node, source []byte) []string {
	if parameters == nil {
		return nil
	}
	if parameters.Kind() != "formal_parameters" {
		return []string{strings.TrimSpace(parameters.Utf8Text(source))}
	}
	var values []string
	for i := uint(0); i < parameters.NamedChildCount(); i++ {
		values = append(values, strings.TrimSpace(parameters.NamedChild(i).Utf8Text(source)))
	}
	return values
}

func collectTypescriptImport(text string, state *typescriptFile) {
	text = strings.TrimSpace(strings.TrimSuffix(text, ";"))
	fromIndex := strings.LastIndex(text, " from ")
	if fromIndex < 0 {
		return
	}
	clause := strings.TrimSpace(strings.TrimPrefix(text[:fromIndex], "import "))
	source := strings.Trim(strings.TrimSpace(text[fromIndex+6:]), `"'`)
	module := resolveTypescriptModule(state.module, source)
	if strings.HasPrefix(clause, "type ") {
		clause = strings.TrimSpace(strings.TrimPrefix(clause, "type "))
	}
	if strings.HasPrefix(clause, "* as ") {
		state.imports[strings.TrimSpace(strings.TrimPrefix(clause, "* as "))] = module
		return
	}
	if open := strings.IndexByte(clause, '{'); open >= 0 {
		close := strings.LastIndexByte(clause, '}')
		if close > open {
			for _, item := range strings.Split(clause[open+1:close], ",") {
				parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item), "type ")))
				if len(parts) == 0 {
					continue
				}
				alias := parts[0]
				if len(parts) == 3 && parts[1] == "as" {
					alias = parts[2]
				}
				state.imports[alias] = langutil.Qualified(module, parts[0])
			}
		}
		clause = strings.TrimSpace(strings.TrimSuffix(clause[:open], ","))
	}
	if clause != "" {
		state.imports[strings.TrimSpace(clause)] = langutil.Qualified(module, "default")
	}
}

func resolveTypescriptModule(current string, imported string) string {
	if !strings.HasPrefix(imported, ".") {
		return strings.ReplaceAll(imported, "/", ".")
	}
	currentPath := strings.ReplaceAll(current, ".", "/")
	base := filepath.Dir(currentPath)
	resolved := filepath.ToSlash(filepath.Clean(filepath.Join(base, imported)))
	resolved = strings.TrimPrefix(resolved, "./")
	return strings.ReplaceAll(resolved, "/", ".")
}

func (a *analysis) resolveTypescriptCalls() {
	byQualified := make(map[string]*callgraph.Function)
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if function.Language == "typescript" {
			a.nodesByID[function.ID] = function
			byQualified[function.QualifiedName] = function
		}
	}
	edges := make(map[string]*callgraph.Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.calls {
		target := a.resolveTypescriptCall(call, byQualified)
		if target == nil {
			if call.caller != "" {
				a.graph.Unresolved = append(a.graph.Unresolved, callgraph.UnresolvedCall{
					Caller: call.caller, Expression: call.expression, File: call.file, Line: call.line,
					Category: a.typescriptUnresolvedCategory(call), Language: "typescript",
				})
			}
			continue
		}
		if call.caller == "" {
			continue
		}
		key := call.caller + "\x00" + target.ID + "\x00call"
		if edges[key] == nil {
			edges[key] = &callgraph.Edge{Source: call.caller, Target: target.ID, Kind: "call"}
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

func (a *analysis) resolveTypescriptCall(call typescriptCall, byQualified map[string]*callgraph.Function) *callgraph.Function {
	caller := a.nodesByID[call.caller]
	state := a.files[call.file]
	expression := strings.TrimSpace(call.expression)
	expression = strings.ReplaceAll(expression, "?.", ".")
	expression = strings.TrimSuffix(expression, "!")
	if caller != nil && strings.HasPrefix(expression, "this.") && caller.Class != "" {
		return byQualified[langutil.Qualified(caller.Module, caller.Class, strings.TrimPrefix(expression, "this."))]
	}
	if !strings.Contains(expression, ".") {
		if caller != nil {
			prefix := caller.QualifiedName
			for {
				if target := byQualified[langutil.Qualified(prefix, expression)]; target != nil {
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
			if call.constructor {
				return byQualified[langutil.Qualified(imported, "constructor")]
			}
			if target := byQualified[imported]; target != nil {
				return target
			}
			if strings.HasSuffix(imported, ".default") {
				module := strings.TrimSuffix(imported, ".default")
				for _, target := range byQualified {
					if target.Module == module && slices.Contains(target.Modifiers, "default") {
						return target
					}
				}
			}
		}
		if call.constructor {
			return byQualified[langutil.Qualified(state.module, expression, "constructor")]
		}
		return byQualified[langutil.Qualified(state.module, expression)]
	}
	root, suffix, _ := strings.Cut(expression, ".")
	if imported := state.imports[root]; imported != "" {
		if call.constructor {
			return byQualified[langutil.Qualified(imported, suffix, "constructor")]
		}
		return byQualified[langutil.Qualified(imported, suffix)]
	}
	if call.constructor {
		return byQualified[langutil.Qualified(state.module, expression, "constructor")]
	}
	if target := byQualified[langutil.Qualified(state.module, expression)]; target != nil {
		return target
	}
	return byQualified[expression]
}

func (a *analysis) typescriptUnresolvedCategory(call typescriptCall) string {
	root := strings.Split(call.expression, ".")[0]
	if state := a.files[call.file]; state != nil && state.imports[root] != "" {
		return "external"
	}
	return "dynamic"
}
