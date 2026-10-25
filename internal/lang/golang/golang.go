package golang

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langutil"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// Language analyzes Go sources. It holds no state, so one value can
// analyze any number of trees.
type Language struct{}

func New() Language {
	return Language{}
}

func (Language) Name() string {
	return "go"
}

func (Language) Extensions() []string {
	return []string{".go"}
}

func (Language) SkipDirs() []string {
	return []string{"vendor"}
}

func (Language) IsTestFile(relativePath string) bool {
	name := filepath.Base(relativePath)
	return strings.HasSuffix(name, "_test.go") || langutil.InDirectory(relativePath, "test", "tests", "testdata")
}

func (Language) Analyze(root string, files []string) (*callgraph.Graph, error) {
	a := &analysis{
		root:                   root,
		nodesByID:              make(map[string]*callgraph.Function),
		files:                  make(map[string]*goFile),
		packagesByImportPath:   make(map[string]*goPackage),
		packagesByRelativePath: make(map[string]*goPackage),
	}
	if err := langutil.EachFile(root, files, a.analyzeGoFile); err != nil {
		return nil, err
	}
	a.resolveGoCalls()
	return &a.graph, nil
}

type analysis struct {
	root                   string
	graph                  callgraph.Graph
	nodesByID              map[string]*callgraph.Function
	files                  map[string]*goFile
	packagesByImportPath   map[string]*goPackage
	packagesByRelativePath map[string]*goPackage
	calls                  []goCall
}

type goFile struct {
	module      string
	packageName string
	imports     []goImport
}

type goPackage struct {
	module      string
	packageName string
}

type goImport struct {
	path  string
	alias string
}

type goCall struct {
	caller       string
	expression   string
	file         string
	line         uint
	module       string
	receiverName string
	receiverType string
}

var goBuiltins = map[string]bool{
	"append": true, "bool": true, "byte": true, "cap": true, "clear": true, "close": true,
	"comparable": true, "complex": true, "complex64": true, "complex128": true, "copy": true,
	"delete": true, "error": true, "false": true, "float32": true, "float64": true,
	"imag": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"iota": true, "len": true, "make": true, "max": true, "min": true, "new": true,
	"nil": true, "panic": true, "print": true, "println": true, "real": true, "recover": true,
	"rune": true, "string": true, "true": true, "uint": true, "uint8": true, "uint16": true,
	"uint32": true, "uint64": true, "uintptr": true,
}

func (a *analysis) analyzeGoFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	tree, err := langutil.ParseWithFallback(path, source, tree_sitter.NewLanguage(tree_sitter_go.Language()), renameGoNewExpressionCalls)
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
	packageName := goPackageName(root, source)
	if packageName == "" {
		return fmt.Errorf("%s:1:1: missing package clause", path)
	}
	state := &goFile{
		module:      goPackageModule(relative, packageName),
		packageName: packageName,
	}
	a.files[relative] = state
	a.graph.Files = append(a.graph.Files, callgraph.File{Path: relative, Module: state.module, Language: "go"})
	a.registerGoPackage(path, relative, state)
	a.graph.Stats.Files++
	a.walkGo(root, source, relative, state, "", "", "")
	return nil
}

// renameGoNewExpressionCalls returns a copy of source in which each call
// new(expression) has a different name of the same length. Tree-sitter Go
// 0.25 accepts only a type as the argument of new, but Go 1.26 also accepts an
// expression, for example new(int(n)). Byte offsets in the copy are equal to
// the offsets in source. The function returns nil if source has no such call.
func renameGoNewExpressionCalls(source []byte) []byte {
	files := token.NewFileSet()
	// If source has a syntax error, go/parser returns a partial syntax tree.
	// Renaming the calls in that tree stops a valid new(expression) call before
	// the syntax error from hiding the position of the error.
	file, _ := goparser.ParseFile(files, "", source, goparser.SkipObjectResolution)
	if file == nil {
		return nil
	}
	var rewritten []byte
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 || goTypeSyntax(call.Args[0]) {
			return true
		}
		if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "new" {
			if rewritten == nil {
				rewritten = append([]byte(nil), source...)
			}
			copy(rewritten[files.Position(name.Pos()).Offset:], "neW")
		}
		return true
	})
	return rewritten
}

// goTypeSyntax reports whether Tree-sitter Go can parse expression as a type.
func goTypeSyntax(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return goTypeName(expression)
	case *ast.ArrayType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.MapType, *ast.StructType:
		return true
	case *ast.StarExpr:
		return goTypeSyntax(expression.X)
	case *ast.ParenExpr:
		return goTypeSyntax(expression.X)
	case *ast.IndexExpr:
		return goTypeName(expression.X) && goTypeSyntax(expression.Index)
	case *ast.IndexListExpr:
		if !goTypeName(expression.X) {
			return false
		}
		for _, index := range expression.Indices {
			if !goTypeSyntax(index) {
				return false
			}
		}
		return true
	}
	return false
}

// goTypeName reports whether expression is a type name, with or without a
// package name. In Tree-sitter Go, only these forms can take type arguments.
func goTypeName(expression ast.Expr) bool {
	switch expression := expression.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr:
		_, ok := expression.X.(*ast.Ident)
		return ok
	}
	return false
}

func goPackageName(root *tree_sitter.Node, source []byte) string {
	for index := uint(0); index < root.NamedChildCount(); index++ {
		child := root.NamedChild(index)
		if child.Kind() == "package_clause" && child.NamedChildCount() > 0 {
			return child.NamedChild(0).Utf8Text(source)
		}
	}
	return ""
}

func goPackageModule(relative string, packageName string) string {
	directory := filepath.ToSlash(filepath.Dir(relative))
	if directory == "." {
		return packageName
	}
	return strings.ReplaceAll(directory, "/", ".")
}

func (a *analysis) registerGoPackage(path string, relative string, state *goFile) {
	goPackage := &goPackage{module: state.module, packageName: state.packageName}
	relativeDirectory := filepath.ToSlash(filepath.Dir(relative))
	if relativeDirectory == "." {
		relativeDirectory = ""
	}
	a.packagesByRelativePath[relativeDirectory] = goPackage
	importRoot, importPrefix := findGoImportRoot(filepath.Dir(path))
	if importRoot == "" {
		return
	}
	packageDirectory, err := filepath.Rel(importRoot, filepath.Dir(path))
	if err != nil {
		return
	}
	importPath := importPrefix
	if packageDirectory != "." {
		importPath = strings.TrimSuffix(importPrefix, "/") + "/" + filepath.ToSlash(packageDirectory)
	}
	a.packagesByImportPath[importPath] = goPackage
}

func findGoImportRoot(directory string) (string, string) {
	for {
		contents, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err == nil {
			for _, line := range strings.Split(string(contents), "\n") {
				fields := strings.Fields(line)
				if len(fields) < 2 || fields[0] != "module" {
					continue
				}
				modulePath := fields[1]
				if unquoted, unquoteErr := strconv.Unquote(modulePath); unquoteErr == nil {
					modulePath = unquoted
				}
				return directory, modulePath
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", ""
		}
		directory = parent
	}
}

func (a *analysis) walkGo(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *goFile,
	parentID string,
	receiverName string,
	receiverType string,
) {
	switch node.Kind() {
	case "import_spec":
		collectGoImport(node, source, state)
		return
	case "function_declaration":
		a.collectGoFunction(node, source, file, state, "", "")
		return
	case "method_declaration":
		receiver := node.ChildByFieldName("receiver")
		name, typeName := goReceiver(receiver, source)
		a.collectGoFunction(node, source, file, state, name, typeName)
		return
	case "call_expression":
		function := node.ChildByFieldName("function")
		if function != nil {
			a.graph.Stats.Calls++
			a.calls = append(a.calls, goCall{
				caller:       parentID,
				expression:   strings.TrimSpace(function.Utf8Text(source)),
				file:         file,
				line:         node.StartPosition().Row + 1,
				module:       state.module,
				receiverName: receiverName,
				receiverType: receiverType,
			})
		}
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		a.walkGo(node.NamedChild(index), source, file, state, parentID, receiverName, receiverType)
	}
}

func collectGoImport(node *tree_sitter.Node, source []byte, state *goFile) {
	pathNode := node.ChildByFieldName("path")
	if pathNode == nil {
		return
	}
	importPath := pathNode.Utf8Text(source)
	unquoted, err := strconv.Unquote(importPath)
	if err != nil {
		return
	}
	alias := ""
	if nameNode := node.ChildByFieldName("name"); nameNode != nil {
		alias = nameNode.Utf8Text(source)
	}
	if alias == "_" {
		return
	}
	state.imports = append(state.imports, goImport{path: unquoted, alias: alias})
}

func goReceiver(receiver *tree_sitter.Node, source []byte) (string, string) {
	if receiver == nil {
		return "", ""
	}
	for index := uint(0); index < receiver.NamedChildCount(); index++ {
		parameter := receiver.NamedChild(index)
		if parameter.Kind() != "parameter_declaration" {
			continue
		}
		name := ""
		if nameNode := parameter.ChildByFieldName("name"); nameNode != nil {
			name = nameNode.Utf8Text(source)
		}
		typeNode := parameter.ChildByFieldName("type")
		if typeNode == nil {
			return name, ""
		}
		return name, goBaseType(typeNode.Utf8Text(source))
	}
	return "", ""
}

func goBaseType(value string) string {
	value = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(value), "*"))
	if open := strings.IndexByte(value, '['); open >= 0 {
		value = value[:open]
	}
	if dot := strings.LastIndexByte(value, '.'); dot >= 0 {
		value = value[dot+1:]
	}
	return strings.TrimSpace(value)
}

func (a *analysis) collectGoFunction(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *goFile,
	receiverName string,
	receiverType string,
) {
	nameNode := node.ChildByFieldName("name")
	parameters := node.ChildByFieldName("parameters")
	body := node.ChildByFieldName("body")
	if nameNode == nil || parameters == nil || body == nil {
		return
	}
	name := nameNode.Utf8Text(source)
	qualifiedName := langutil.Qualified(state.module, receiverType, name)
	point := node.StartPosition()
	end := node.EndPosition()
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
		Signature:     strings.TrimSpace(string(source[node.StartByte():body.StartByte()])),
		Source:        node.Utf8Text(source),
		Class:         receiverType,
		Language:      "go",
		Kind:          "function",
		Parameters:    goParameters(parameters, source),
	}
	if function.Directory == "." {
		function.Directory = ""
	}
	if receiverType != "" {
		function.Kind = "method"
	}
	if receiverType == "" && name == "init" {
		function.Entrypoint = true
		function.EntryKind = "init"
	} else if state.packageName == "main" && receiverType == "" && name == "main" {
		function.Entrypoint = true
		function.EntryKind = "main"
	}
	a.graph.Nodes = append(a.graph.Nodes, function)
	a.nodesByID[function.ID] = &a.graph.Nodes[len(a.graph.Nodes)-1]
	a.graph.Stats.Definitions++
	for index := uint(0); index < body.NamedChildCount(); index++ {
		a.walkGo(body.NamedChild(index), source, file, state, function.ID, receiverName, receiverType)
	}
}

func goParameters(parameters *tree_sitter.Node, source []byte) []string {
	var values []string
	for index := uint(0); index < parameters.NamedChildCount(); index++ {
		values = append(values, strings.TrimSpace(parameters.NamedChild(index).Utf8Text(source)))
	}
	return values
}

func (a *analysis) resolveGoCalls() {
	byQualified := make(map[string]*callgraph.Function)
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if function.Language == "go" {
			a.nodesByID[function.ID] = function
			byQualified[function.QualifiedName] = function
		}
	}
	edges := make(map[string]*callgraph.Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.calls {
		target := a.resolveGoCall(call, byQualified)
		if target == nil {
			if call.caller != "" {
				a.graph.Unresolved = append(a.graph.Unresolved, callgraph.UnresolvedCall{
					Caller: call.caller, Expression: call.expression, File: call.file, Line: call.line,
					Category: a.goUnresolvedCategory(call), Language: "go",
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

func (a *analysis) resolveGoCall(call goCall, byQualified map[string]*callgraph.Function) *callgraph.Function {
	state := a.files[call.file]
	expression := strings.TrimSpace(call.expression)
	if !strings.Contains(expression, ".") {
		if target := byQualified[langutil.Qualified(call.module, expression)]; target != nil {
			return target
		}
		for _, goImport := range state.imports {
			if goImport.alias != "." {
				continue
			}
			if imported := a.importedPackage(goImport.path); imported != nil {
				if target := byQualified[langutil.Qualified(imported.module, expression)]; target != nil {
					return target
				}
			}
		}
		return nil
	}
	root, suffix, _ := strings.Cut(expression, ".")
	if root == call.receiverName && call.receiverType != "" {
		return byQualified[langutil.Qualified(call.module, call.receiverType, suffix)]
	}
	if target := byQualified[langutil.Qualified(call.module, root, suffix)]; target != nil {
		return target
	}
	for _, goImport := range state.imports {
		if a.importName(goImport) != root {
			continue
		}
		if imported := a.importedPackage(goImport.path); imported != nil {
			return byQualified[langutil.Qualified(imported.module, suffix)]
		}
	}
	return nil
}

func (a *analysis) importedPackage(importPath string) *goPackage {
	if goPackage := a.packagesByImportPath[importPath]; goPackage != nil {
		return goPackage
	}
	relative := strings.TrimPrefix(importPath, "./")
	return a.packagesByRelativePath[relative]
}

func (a *analysis) importName(goImport goImport) string {
	if goImport.alias != "" {
		return goImport.alias
	}
	if imported := a.importedPackage(goImport.path); imported != nil {
		return imported.packageName
	}
	return filepath.Base(goImport.path)
}

func (a *analysis) goUnresolvedCategory(call goCall) string {
	if goBuiltins[call.expression] {
		return "builtin"
	}
	state := a.files[call.file]
	root := strings.Split(call.expression, ".")[0]
	for _, goImport := range state.imports {
		if goImport.alias == "." || a.importName(goImport) == root {
			return "external"
		}
	}
	return "dynamic"
}
