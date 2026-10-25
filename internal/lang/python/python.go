package python

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
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// Language analyzes Python sources. It holds no state, so one value can
// analyze any number of trees.
type Language struct{}

func New() Language {
	return Language{}
}

func (Language) Name() string {
	return "python"
}

func (Language) Extensions() []string {
	return []string{".py"}
}

func (Language) SkipDirs() []string {
	return []string{".nox", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".tox", ".venv", "__pycache__", "build", "dist", "site-packages", "venv"}
}

func (Language) IsTestFile(relativePath string) bool {
	name := filepath.Base(relativePath)
	return strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") || langutil.InDirectory(relativePath, "test", "tests")
}

func (Language) Analyze(root string, files []string) (*callgraph.Graph, error) {
	a := &analysis{
		root:        root,
		nodesByID:   make(map[string]*callgraph.Function),
		files:       make(map[string]*fileState),
		flowClasses: make(map[string]bool),
	}
	if err := langutil.EachFile(root, files, a.analyzePythonFile); err != nil {
		return nil, err
	}
	a.resolveCalls()
	return &a.graph, nil
}

type analysis struct {
	root        string
	graph       callgraph.Graph
	nodesByID   map[string]*callgraph.Function
	files       map[string]*fileState
	rawCalls    []rawCall
	flowClasses map[string]bool
}

type importRef struct {
	module string
	name   string
}

type fileState struct {
	module  string
	pkgInit bool
	imports map[string]importRef
}

type classState struct {
	name string
}

type rawCall struct {
	caller     string
	expression string
	file       string
	line       uint
	kind       string
	mainGuard  bool
}

var pythonBuiltins = map[string]bool{
	"BaseException": true, "BaseExceptionGroup": true, "Exception": true, "ExceptionGroup": true,
	"ArithmeticError": true, "AssertionError": true, "AttributeError": true, "BufferError": true,
	"EOFError": true, "FloatingPointError": true, "GeneratorExit": true, "ImportError": true,
	"IndexError": true, "KeyError": true, "KeyboardInterrupt": true, "LookupError": true,
	"MemoryError": true, "ModuleNotFoundError": true, "NameError": true, "NotImplementedError": true,
	"OSError": true, "OverflowError": true, "RecursionError": true, "ReferenceError": true,
	"RuntimeError": true, "StopAsyncIteration": true, "StopIteration": true, "SyntaxError": true,
	"IndentationError": true, "TabError": true,
	"SystemError": true, "SystemExit": true, "TypeError": true, "UnboundLocalError": true,
	"UnicodeError": true, "UnicodeDecodeError": true, "UnicodeEncodeError": true,
	"UnicodeTranslateError": true, "ValueError": true, "ZeroDivisionError": true, "BlockingIOError": true,
	"BrokenPipeError": true, "ChildProcessError": true, "ConnectionAbortedError": true,
	"ConnectionError": true, "ConnectionRefusedError": true, "ConnectionResetError": true,
	"FileExistsError": true, "FileNotFoundError": true, "InterruptedError": true,
	"IsADirectoryError": true, "NotADirectoryError": true, "PermissionError": true,
	"ProcessLookupError": true, "TimeoutError": true, "Warning": true, "BytesWarning": true,
	"DeprecationWarning": true, "EncodingWarning": true, "FutureWarning": true, "ImportWarning": true,
	"PendingDeprecationWarning": true, "ResourceWarning": true, "RuntimeWarning": true,
	"SyntaxWarning": true, "UnicodeWarning": true, "UserWarning": true, "__import__": true,
	"abs": true, "aiter": true, "all": true, "anext": true,
	"any": true, "ascii": true, "bin": true, "bool": true, "breakpoint": true, "bytearray": true,
	"bytes": true, "callable": true, "chr": true, "classmethod": true, "compile": true, "complex": true,
	"delattr": true, "dict": true, "dir": true, "divmod": true, "enumerate": true, "eval": true,
	"exec": true, "filter": true, "float": true, "format": true, "frozenset": true, "getattr": true,
	"globals": true, "hasattr": true, "hash": true, "help": true, "hex": true, "id": true,
	"input": true, "int": true, "isinstance": true, "issubclass": true, "iter": true, "len": true,
	"list": true, "locals": true, "map": true, "max": true, "memoryview": true, "min": true,
	"next": true, "object": true, "oct": true, "open": true, "ord": true, "pow": true,
	"print": true, "property": true, "range": true, "repr": true, "reversed": true, "round": true,
	"set": true, "setattr": true, "slice": true, "sorted": true, "staticmethod": true, "str": true,
	"sum": true, "super": true, "tuple": true, "type": true, "vars": true, "zip": true,
}

func (a *analysis) analyzePythonFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	tree, err := langutil.Parse(path, source, tree_sitter.NewLanguage(tree_sitter_python.Language()))
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
	state := &fileState{
		module:  moduleName(relative),
		pkgInit: filepath.Base(relative) == "__init__.py",
		imports: make(map[string]importRef),
	}
	a.files[relative] = state
	a.graph.Files = append(a.graph.Files, callgraph.File{Path: relative, Module: state.module, Language: "python"})
	a.graph.Stats.Files++
	a.walk(root, source, relative, state, nil, "", nil, false)
	return nil
}

func moduleName(relative string) string {
	module := strings.TrimSuffix(filepath.ToSlash(relative), ".py")
	module = strings.ReplaceAll(module, "/", ".")
	module = strings.TrimSuffix(module, ".__init__")
	if module == "__init__" {
		return ""
	}
	return module
}

func (a *analysis) walk(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *fileState,
	class *classState,
	parentID string,
	decorators []string,
	mainGuard bool,
) {
	switch node.Kind() {
	case "decorated_definition":
		var definition *tree_sitter.Node
		var names []string
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child.Kind() == "decorator" {
				names = append(names, decoratorName(child.Utf8Text(source)))
				for j := uint(0); j < child.NamedChildCount(); j++ {
					a.walk(child.NamedChild(j), source, file, state, class, parentID, nil, mainGuard)
				}
			} else {
				definition = child
			}
		}
		if definition != nil {
			a.walk(definition, source, file, state, class, parentID, names, mainGuard)
		}
		return
	case "import_statement", "import_from_statement":
		a.collectImport(node, source, state)
	case "class_definition":
		nameNode := node.ChildByFieldName("name")
		body := node.ChildByFieldName("body")
		if nameNode == nil || body == nil {
			return
		}
		name := nameNode.Utf8Text(source)
		classContext := &classState{name: name}
		isFlow := strings.Contains(
			string(source[node.StartByte():min(len(source), int(body.StartByte()))]),
			"FlowSpec",
		)
		if isFlow {
			a.flowClasses[langutil.Qualified(state.module, name)] = true
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child.Id() != nameNode.Id() && child.Id() != body.Id() {
				a.walk(child, source, file, state, class, parentID, nil, mainGuard)
			}
		}
		for i := uint(0); i < body.NamedChildCount(); i++ {
			a.walk(body.NamedChild(i), source, file, state, classContext, "", nil, mainGuard)
		}
		return
	case "function_definition":
		a.collectFunction(node, source, file, state, class, parentID, decorators)
		return
	case "if_statement":
		condition := node.ChildByFieldName("condition")
		if condition != nil && isMainGuard(condition.Utf8Text(source)) {
			mainGuard = true
		}
	}

	if node.Kind() == "call" {
		a.collectCall(node, source, file, parentID, mainGuard)
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		a.walk(node.NamedChild(i), source, file, state, class, parentID, decorators, mainGuard)
	}
}

func (a *analysis) collectFunction(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *fileState,
	class *classState,
	parentID string,
	decorators []string,
) {
	nameNode := node.ChildByFieldName("name")
	parameters := node.ChildByFieldName("parameters")
	body := node.ChildByFieldName("body")
	if nameNode == nil || parameters == nil || body == nil {
		return
	}
	name := nameNode.Utf8Text(source)
	parentName := ""
	if parentID != "" {
		parentName = a.nodesByID[parentID].QualifiedName
	}
	parts := []string{state.module}
	if class != nil {
		parts = append(parts, class.name)
	}
	if parentName != "" {
		parts = []string{parentName}
	}
	parts = append(parts, name)
	qualifiedName := langutil.Qualified(parts...)
	point := node.StartPosition()
	end := node.EndPosition()
	id := fmt.Sprintf("%s:%d:%s", file, point.Row+1, qualifiedName)
	function := callgraph.Function{
		ID:            id,
		Name:          name,
		QualifiedName: qualifiedName,
		Module:        state.module,
		File:          file,
		Directory:     filepath.ToSlash(filepath.Dir(file)),
		Line:          point.Row + 1,
		Column:        point.Column + 1,
		EndLine:       end.Row + 1,
		Signature:     name + parameters.Utf8Text(source),
		Source:        node.Utf8Text(source),
		Decorators:    decorators,
		Parent:        parentID,
		Async:         node.Child(0) != nil && node.Child(0).Kind() == "async",
		Language:      "python",
		Kind:          "function",
	}
	if function.Directory == "." {
		function.Directory = ""
	}
	if class != nil {
		function.Class = class.name
		function.Kind = "method"
	}
	for _, decorator := range decorators {
		if decorator == "step" {
			function.Entrypoint = true
			function.EntryKind = "metaflow-step"
		}
	}
	a.graph.Nodes = append(a.graph.Nodes, function)
	a.nodesByID[id] = &a.graph.Nodes[len(a.graph.Nodes)-1]
	a.graph.Stats.Definitions++

	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Id() != nameNode.Id() && child.Id() != body.Id() {
			a.walk(child, source, file, state, class, id, nil, false)
		}
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		a.walk(body.NamedChild(i), source, file, state, class, id, nil, false)
	}
}

func decoratorName(text string) string {
	text = strings.TrimSpace(strings.TrimPrefix(text, "@"))
	if index := strings.IndexByte(text, '('); index >= 0 {
		text = text[:index]
	}
	if index := strings.LastIndexByte(text, '.'); index >= 0 {
		text = text[index+1:]
	}
	return text
}

func (a *analysis) collectImport(node *tree_sitter.Node, source []byte, state *fileState) {
	text := strings.TrimSpace(node.Utf8Text(source))
	if strings.HasPrefix(text, "import ") {
		for _, item := range strings.Split(strings.TrimPrefix(text, "import "), ",") {
			fields := strings.Fields(strings.TrimSpace(item))
			module := fields[0]
			alias := strings.Split(module, ".")[0]
			if len(fields) == 3 && fields[1] == "as" {
				alias = fields[2]
			}
			aModule := module
			if alias == strings.Split(module, ".")[0] && strings.Contains(module, ".") {
				aModule = alias
			}
			state.imports[alias] = importRef{module: aModule}
		}
		return
	}
	if !strings.HasPrefix(text, "from ") {
		return
	}
	parts := strings.SplitN(strings.TrimPrefix(text, "from "), " import ", 2)
	if len(parts) != 2 {
		return
	}
	module := resolveRelativeModule(state.module, parts[0], state.pkgInit)
	names := strings.Trim(strings.TrimSpace(parts[1]), "()")
	for _, item := range strings.Split(names, ",") {
		fields := strings.Fields(strings.TrimSpace(item))
		if len(fields) == 0 || fields[0] == "*" {
			continue
		}
		alias := fields[0]
		if len(fields) == 3 && fields[1] == "as" {
			alias = fields[2]
		}
		state.imports[alias] = importRef{module: module, name: fields[0]}
	}
}

func resolveRelativeModule(current string, imported string, packageInit bool) string {
	dots := 0
	for dots < len(imported) && imported[dots] == '.' {
		dots++
	}
	if dots == 0 {
		return imported
	}
	packageParts := strings.Split(current, ".")
	if !packageInit && len(packageParts) > 0 {
		packageParts = packageParts[:len(packageParts)-1]
	}
	remove := dots - 1
	if remove > len(packageParts) {
		remove = len(packageParts)
	}
	packageParts = packageParts[:len(packageParts)-remove]
	suffix := strings.TrimPrefix(imported, strings.Repeat(".", dots))
	if suffix != "" {
		packageParts = append(packageParts, suffix)
	}
	return strings.Join(packageParts, ".")
}

func (a *analysis) collectCall(
	node *tree_sitter.Node,
	source []byte,
	file string,
	caller string,
	mainGuard bool,
) {
	function := node.ChildByFieldName("function")
	if function == nil {
		return
	}
	a.graph.Stats.Calls++
	expression := strings.TrimSpace(function.Utf8Text(source))
	line := node.StartPosition().Row + 1
	if (expression == "self.next" || expression == "cls.next") && caller != "" {
		arguments := node.ChildByFieldName("arguments")
		if arguments == nil {
			return
		}
		for i := uint(0); i < arguments.NamedChildCount(); i++ {
			target := arguments.NamedChild(i)
			if target.Kind() == "keyword_argument" {
				continue
			}
			a.rawCalls = append(a.rawCalls, rawCall{
				caller: caller, expression: target.Utf8Text(source), file: file, line: line, kind: "transition",
			})
		}
		return
	}
	a.rawCalls = append(a.rawCalls, rawCall{
		caller: caller, expression: expression, file: file, line: line, kind: "call", mainGuard: mainGuard,
	})
}

func isMainGuard(text string) bool {
	compact := strings.ReplaceAll(strings.ReplaceAll(text, " ", ""), "'", "\"")
	return strings.Contains(compact, "__name__==\"__main__\"") ||
		strings.Contains(compact, "\"__main__\"==__name__")
}

func (a *analysis) resolveCalls() {
	byQualified := make(map[string]*callgraph.Function)
	byModuleName := make(map[string]*callgraph.Function)
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if function.Language != "python" {
			continue
		}
		a.nodesByID[function.ID] = function
		byQualified[function.QualifiedName] = function
		if function.Parent == "" && function.Class == "" {
			byModuleName[langutil.Qualified(function.Module, function.Name)] = function
		}
	}
	edges := make(map[string]*callgraph.Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.rawCalls {
		target := a.resolveCall(call, byQualified, byModuleName)
		if target == nil {
			if call.caller != "" {
				a.graph.Unresolved = append(a.graph.Unresolved, callgraph.UnresolvedCall{
					Caller: call.caller, Expression: call.expression, File: call.file, Line: call.line,
					Category: a.unresolvedCategory(call), Language: "python",
				})
			}
			continue
		}
		if call.mainGuard {
			target.Entrypoint = true
			if target.EntryKind == "" {
				target.EntryKind = "main"
			}
		}
		if call.caller == "" {
			continue
		}
		key := call.caller + "\x00" + target.ID + "\x00" + call.kind
		edge := edges[key]
		if edge == nil {
			edge = &callgraph.Edge{Source: call.caller, Target: target.ID, Kind: call.kind}
			edges[key] = edge
			lineSets[key] = make(map[uint]bool)
		}
		edge.Count++
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

func (a *analysis) unresolvedCategory(call rawCall) string {
	expression := strings.TrimSpace(call.expression)
	if pythonBuiltins[expression] {
		return "builtin"
	}
	root, _, _ := strings.Cut(expression, ".")
	if state := a.files[call.file]; state != nil {
		if _, imported := state.imports[root]; imported {
			return "external"
		}
	}
	return "dynamic"
}

func (a *analysis) resolveCall(
	call rawCall,
	byQualified map[string]*callgraph.Function,
	byModuleName map[string]*callgraph.Function,
) *callgraph.Function {
	caller := a.nodesByID[call.caller]
	state := a.files[call.file]
	expression := strings.TrimSpace(call.expression)
	if caller != nil && (strings.HasPrefix(expression, "self.") || strings.HasPrefix(expression, "cls.")) {
		name := strings.TrimPrefix(strings.TrimPrefix(expression, "self."), "cls.")
		if caller.Class != "" {
			return byQualified[langutil.Qualified(caller.Module, caller.Class, name)]
		}
		return nil
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
		if imported, ok := state.imports[expression]; ok {
			if imported.name != "" {
				return byModuleName[langutil.Qualified(imported.module, imported.name)]
			}
		}
		if target := byModuleName[langutil.Qualified(state.module, expression)]; target != nil {
			return target
		}
		if a.flowClasses[langutil.Qualified(state.module, expression)] {
			a.markFlowEntrypoints(state.module, expression)
		}
		return nil
	}
	parts := strings.Split(expression, ".")
	if imported, ok := state.imports[parts[0]]; ok {
		targetParts := []string{imported.module}
		if imported.name != "" {
			targetParts = append(targetParts, imported.name)
		}
		targetParts = append(targetParts, parts[1:]...)
		return byQualified[langutil.Qualified(targetParts...)]
	}
	if target := byQualified[langutil.Qualified(state.module, expression)]; target != nil {
		return target
	}
	return byQualified[expression]
}

func (a *analysis) markFlowEntrypoints(module string, class string) {
	prefix := langutil.Qualified(module, class) + "."
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if strings.HasPrefix(function.QualifiedName, prefix) && slices.Contains(function.Decorators, "step") {
			function.Entrypoint = true
			function.EntryKind = "metaflow-step"
		}
	}
}
