package java

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langutil"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

// Language analyzes Java sources. It holds no state, so one value can
// analyze any number of trees.
type Language struct{}

func New() Language {
	return Language{}
}

func (Language) Name() string {
	return "java"
}

func (Language) Extensions() []string {
	return []string{".java"}
}

func (Language) SkipDirs() []string {
	return []string{".gradle", "build", "out", "target"}
}

func (Language) IsTestFile(relativePath string) bool {
	name := filepath.Base(relativePath)
	if strings.HasSuffix(name, "Test.java") || strings.HasSuffix(name, "Tests.java") || strings.HasSuffix(name, "IT.java") {
		return true
	}
	return langutil.InDirectory(relativePath, "test", "tests", "it")
}

func (Language) Analyze(root string, files []string) (*callgraph.Graph, error) {
	a := &analysis{
		root:      root,
		files:     make(map[string]*javaFile),
		types:     make(map[string]*javaType),
		simple:    make(map[string][]string),
		callables: make(map[string]*javaCallable),
		methods:   make(map[string][]*javaCallable),
		aliases:   make(map[string]string),
	}
	if err := langutil.EachFile(root, files, a.analyzeJavaFile); err != nil {
		return nil, err
	}
	a.resolveJavaCalls()
	a.addJavaDispatchEdges()
	return &a.graph, nil
}

type analysis struct {
	root      string
	graph     callgraph.Graph
	files     map[string]*javaFile
	types     map[string]*javaType
	simple    map[string][]string
	callables map[string]*javaCallable
	methods   map[string][]*javaCallable
	calls     []javaCall
	aliases   map[string]string
}

type javaFile struct {
	packageName    string
	imports        map[string]string
	wildcard       []string
	staticImports  map[string][]string
	staticWildcard []string
}

type javaType struct {
	qualifiedName string
	simpleName    string
	packageName   string
	superclass    string
	fields        map[string]string
	recordTypes   []string
	recordNames   []string
	file          *javaFile
}

type javaCallable struct {
	id             string
	owner          string
	name           string
	parameterTypes []string
	varargs        bool
	constructor    bool
	file           *javaFile
}

type javaCall struct {
	caller        string
	owner         string
	name          string
	receiver      string
	arguments     []string
	expression    string
	file          string
	line          uint
	constructor   bool
	explicitSuper bool
	reference     bool
	kind          string
	state         *javaFile
	variables     map[string]string
}

func (a *analysis) analyzeJavaFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if isGeneratedJava(source) {
		return nil
	}
	tree, err := langutil.Parse(path, source, tree_sitter.NewLanguage(tree_sitter_java.Language()))
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
	state := parseJavaFile(root, source)
	a.files[relative] = state
	a.graph.Files = append(a.graph.Files, callgraph.File{Path: relative, Module: state.packageName, Language: "java"})
	a.graph.Stats.Files++
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		if isJavaTypeDeclaration(child.Kind()) {
			a.collectJavaType(child, source, relative, state, "")
		}
	}
	return nil
}

func parseJavaFile(root *tree_sitter.Node, source []byte) *javaFile {
	state := &javaFile{
		imports:       make(map[string]string),
		staticImports: make(map[string][]string),
	}
	for i := uint(0); i < root.NamedChildCount(); i++ {
		child := root.NamedChild(i)
		text := strings.TrimSpace(child.Utf8Text(source))
		switch child.Kind() {
		case "package_declaration":
			state.packageName = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "package"), ";"))
		case "import_declaration":
			value := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "import"), ";"))
			static := strings.HasPrefix(value, "static ")
			value = strings.TrimSpace(strings.TrimPrefix(value, "static "))
			if strings.HasSuffix(value, ".*") {
				owner := strings.TrimSuffix(value, ".*")
				if static {
					state.staticWildcard = append(state.staticWildcard, owner)
				} else {
					state.wildcard = append(state.wildcard, owner)
				}
				continue
			}
			index := strings.LastIndexByte(value, '.')
			if index < 0 {
				continue
			}
			name := value[index+1:]
			if static {
				owner := value[:index]
				state.staticImports[name] = append(state.staticImports[name], owner)
			} else {
				state.imports[name] = value
			}
		}
	}
	return state
}

func isJavaTypeDeclaration(kind string) bool {
	switch kind {
	case "class_declaration", "interface_declaration", "enum_declaration", "record_declaration", "annotation_type_declaration":
		return true
	default:
		return false
	}
}

func (a *analysis) collectJavaType(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *javaFile,
	parent string,
) {
	nameNode := node.ChildByFieldName("name")
	body := node.ChildByFieldName("body")
	if nameNode == nil || body == nil {
		return
	}
	name := nameNode.Utf8Text(source)
	owner := name
	if parent != "" {
		owner = parent + "." + name
	}
	qualifiedName := langutil.Qualified(state.packageName, owner)
	info := &javaType{
		qualifiedName: qualifiedName,
		simpleName:    name,
		packageName:   state.packageName,
		fields:        make(map[string]string),
		file:          state,
	}
	if superclass := node.ChildByFieldName("superclass"); superclass != nil {
		info.superclass = strings.TrimSpace(strings.TrimPrefix(superclass.Utf8Text(source), "extends"))
	}
	if node.Kind() == "record_declaration" {
		info.recordTypes, info.recordNames = javaParameters(node.ChildByFieldName("parameters"), source)
		for index, component := range info.recordNames {
			info.fields[component] = info.recordTypes[index]
		}
	}
	a.types[qualifiedName] = info
	a.simple[name] = append(a.simple[name], qualifiedName)
	_, annotations := javaModifiers(node, source)
	if slices.Contains(annotations, "Immutable") && strings.HasPrefix(name, "Abstract") {
		generated := langutil.Qualified(state.packageName, strings.TrimPrefix(name, "Abstract"))
		a.aliases[generated] = qualifiedName
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		if child.Kind() == "field_declaration" || child.Kind() == "constant_declaration" {
			collectJavaVariables(child, source, info.fields)
		}
	}
	for i := uint(0); i < body.NamedChildCount(); i++ {
		child := body.NamedChild(i)
		switch {
		case isJavaTypeDeclaration(child.Kind()):
			a.collectJavaType(child, source, file, state, owner)
		case child.Kind() == "method_declaration":
			a.collectJavaCallable(child, source, file, state, info, false, nil, nil)
		case child.Kind() == "constructor_declaration":
			a.collectJavaCallable(child, source, file, state, info, true, nil, nil)
		case child.Kind() == "compact_constructor_declaration":
			a.collectJavaCallable(child, source, file, state, info, true, info.recordTypes, info.recordNames)
		case child.Kind() == "annotation_type_element_declaration":
			a.collectJavaCallable(child, source, file, state, info, false, nil, nil)
		}
	}
}

func (a *analysis) collectJavaCallable(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *javaFile,
	owner *javaType,
	constructor bool,
	compactTypes []string,
	compactNames []string,
) {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := nameNode.Utf8Text(source)
	parameterTypes, parameterNames := javaParameters(node.ChildByFieldName("parameters"), source)
	if compactTypes != nil {
		parameterTypes = append([]string(nil), compactTypes...)
		parameterNames = append([]string(nil), compactNames...)
	}
	varargs := false
	if parameters := node.ChildByFieldName("parameters"); parameters != nil {
		varargs = strings.Contains(parameters.Utf8Text(source), "...")
	}
	callName := name
	kind := "method"
	if constructor {
		callName = "<init>"
		kind = "constructor"
	}
	id := fmt.Sprintf("java:%s#%s(%s)", owner.qualifiedName, callName, strings.Join(parameterTypes, ","))
	qualifiedName := fmt.Sprintf("%s.%s(%s)", owner.qualifiedName, callName, strings.Join(parameterTypes, ", "))
	point := node.StartPosition()
	end := node.EndPosition()
	modifiers, annotations := javaModifiers(node, source)
	signatureEnd := node.EndByte()
	if body := node.ChildByFieldName("body"); body != nil {
		signatureEnd = body.StartByte()
	}
	function := callgraph.Function{
		ID:            id,
		Name:          name,
		QualifiedName: qualifiedName,
		Module:        state.packageName,
		File:          file,
		Directory:     filepath.ToSlash(filepath.Dir(file)),
		Line:          point.Row + 1,
		Column:        point.Column + 1,
		EndLine:       end.Row + 1,
		Signature:     strings.TrimSpace(string(source[node.StartByte():signatureEnd])),
		Source:        node.Utf8Text(source),
		Decorators:    annotations,
		Class:         owner.qualifiedName,
		Entrypoint:    isJavaMain(node, source, name, parameterTypes, modifiers),
		Language:      "java",
		Kind:          kind,
		Modifiers:     modifiers,
		Parameters:    parameterTypes,
	}
	if function.Directory == "." {
		function.Directory = ""
	}
	if function.Entrypoint {
		function.EntryKind = "main"
	}
	a.graph.Nodes = append(a.graph.Nodes, function)
	a.graph.Stats.Definitions++
	callable := &javaCallable{
		id: id, owner: owner.qualifiedName, name: callName, parameterTypes: parameterTypes,
		varargs: varargs, constructor: constructor, file: state,
	}
	a.callables[id] = callable
	key := owner.qualifiedName + "\x00" + callName
	a.methods[key] = append(a.methods[key], callable)
	body := node.ChildByFieldName("body")
	if body == nil {
		return
	}
	variables := make(map[string]string, len(owner.fields)+len(parameterNames))
	for field, fieldType := range owner.fields {
		variables[field] = fieldType
	}
	for index, parameterName := range parameterNames {
		if index < len(parameterTypes) {
			variables[parameterName] = parameterTypes[index]
		}
	}
	a.collectJavaCalls(body, source, file, state, callable, variables)
}

func javaParameters(node *tree_sitter.Node, source []byte) ([]string, []string) {
	if node == nil {
		return nil, nil
	}
	var types []string
	var names []string
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		switch child.Kind() {
		case "formal_parameter":
			typeNode := child.ChildByFieldName("type")
			nameNode := child.ChildByFieldName("name")
			if typeNode == nil || nameNode == nil {
				continue
			}
			value := normalizeJavaType(typeNode.Utf8Text(source))
			if dimensions := child.ChildByFieldName("dimensions"); dimensions != nil {
				value += strings.ReplaceAll(dimensions.Utf8Text(source), " ", "")
			}
			types = append(types, value)
			names = append(names, nameNode.Utf8Text(source))
		case "spread_parameter":
			text := child.Utf8Text(source)
			index := strings.LastIndex(text, "...")
			if index < 0 {
				continue
			}
			before := strings.TrimSpace(text[:index])
			after := strings.TrimSpace(text[index+3:])
			fields := strings.Fields(after)
			if len(fields) == 0 {
				continue
			}
			types = append(types, normalizeJavaType(before)+"[]")
			names = append(names, fields[0])
		}
	}
	return types, names
}

func javaModifiers(node *tree_sitter.Node, source []byte) ([]string, []string) {
	known := map[string]bool{
		"abstract": true, "default": true, "final": true, "native": true, "private": true,
		"protected": true, "public": true, "static": true, "strictfp": true, "synchronized": true,
		"transient": true, "volatile": true, "sealed": true, "non-sealed": true,
	}
	var modifiers []string
	var annotations []string
	for i := uint(0); i < node.NamedChildCount(); i++ {
		child := node.NamedChild(i)
		if child.Kind() != "modifiers" {
			continue
		}
		for j := uint(0); j < child.ChildCount(); j++ {
			item := child.Child(j)
			text := strings.TrimSpace(item.Utf8Text(source))
			if strings.HasPrefix(text, "@") {
				name := strings.TrimPrefix(strings.SplitN(text, "(", 2)[0], "@")
				if index := strings.LastIndexByte(name, '.'); index >= 0 {
					name = name[index+1:]
				}
				annotations = append(annotations, name)
			} else if known[text] {
				modifiers = append(modifiers, text)
			}
		}
	}
	sort.Strings(modifiers)
	sort.Strings(annotations)
	return modifiers, annotations
}

func isJavaMain(
	node *tree_sitter.Node,
	source []byte,
	name string,
	parameters []string,
	modifiers []string,
) bool {
	typeNode := node.ChildByFieldName("type")
	if name != "main" || len(parameters) != 1 || typeNode == nil || normalizeJavaType(typeNode.Utf8Text(source)) != "void" {
		return false
	}
	if !slices.Contains(modifiers, "public") || !slices.Contains(modifiers, "static") {
		return false
	}
	parameter := eraseJavaType(parameters[0])
	return parameter == "String[]" || parameter == "java.lang.String[]"
}

func collectJavaVariables(node *tree_sitter.Node, source []byte, variables map[string]string) {
	typeNode := node.ChildByFieldName("type")
	if typeNode == nil {
		return
	}
	variableType := normalizeJavaType(typeNode.Utf8Text(source))
	for i := uint(0); i < node.NamedChildCount(); i++ {
		declarator := node.NamedChild(i)
		if declarator.Kind() != "variable_declarator" {
			continue
		}
		nameNode := declarator.ChildByFieldName("name")
		if nameNode == nil {
			continue
		}
		value := variableType
		if value == "var" {
			value = inferJavaExpressionType(declarator.ChildByFieldName("value"), source, variables)
		}
		variables[nameNode.Utf8Text(source)] = value
	}
}

func (a *analysis) collectJavaCalls(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *javaFile,
	caller *javaCallable,
	variables map[string]string,
) {
	switch node.Kind() {
	case "method_declaration", "constructor_declaration", "compact_constructor_declaration", "annotation_type_element_declaration":
		return
	case "local_variable_declaration":
		collectJavaVariables(node, source, variables)
	case "method_invocation":
		nameNode := node.ChildByFieldName("name")
		arguments := node.ChildByFieldName("arguments")
		if nameNode != nil && arguments != nil {
			receiver := ""
			if object := node.ChildByFieldName("object"); object != nil {
				receiver = object.Utf8Text(source)
			}
			a.addJavaCall(node, source, file, state, caller, variables, nameNode.Utf8Text(source), receiver, arguments, false, false)
		}
	case "object_creation_expression":
		typeNode := node.ChildByFieldName("type")
		arguments := node.ChildByFieldName("arguments")
		if typeNode != nil && arguments != nil {
			typeName := normalizeJavaType(typeNode.Utf8Text(source))
			a.addJavaCall(node, source, file, state, caller, variables, "<init>", typeName, arguments, true, false)
		}
		for i := uint(0); i < node.NamedChildCount(); i++ {
			child := node.NamedChild(i)
			if child.Kind() != "class_body" {
				a.collectJavaCalls(child, source, file, state, caller, variables)
			}
		}
		return
	case "explicit_constructor_invocation":
		constructor := node.ChildByFieldName("constructor")
		arguments := node.ChildByFieldName("arguments")
		if constructor != nil && arguments != nil {
			super := constructor.Kind() == "super"
			a.addJavaCall(node, source, file, state, caller, variables, "<init>", constructor.Utf8Text(source), arguments, true, super)
		}
	case "method_reference":
		parts := strings.Split(node.Utf8Text(source), "::")
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[1])
			constructor := name == "new"
			if constructor {
				name = "<init>"
			}
			a.calls = append(a.calls, javaCall{
				caller: caller.id, owner: caller.owner, name: name, receiver: strings.TrimSpace(parts[0]),
				expression: node.Utf8Text(source), file: file, line: node.StartPosition().Row + 1,
				constructor: constructor, reference: true, kind: "dispatch", state: state,
				variables: cloneStringMap(variables),
			})
			a.graph.Stats.Calls++
		}
	}
	for i := uint(0); i < node.NamedChildCount(); i++ {
		a.collectJavaCalls(node.NamedChild(i), source, file, state, caller, variables)
	}
}

func (a *analysis) addJavaCall(
	node *tree_sitter.Node,
	source []byte,
	file string,
	state *javaFile,
	caller *javaCallable,
	variables map[string]string,
	name string,
	receiver string,
	arguments *tree_sitter.Node,
	constructor bool,
	explicitSuper bool,
) {
	argumentTypes := make([]string, 0, arguments.NamedChildCount())
	for i := uint(0); i < arguments.NamedChildCount(); i++ {
		argumentTypes = append(argumentTypes, inferJavaExpressionType(arguments.NamedChild(i), source, variables))
	}
	a.calls = append(a.calls, javaCall{
		caller: caller.id, owner: caller.owner, name: name, receiver: receiver,
		arguments: argumentTypes, expression: node.Utf8Text(source), file: file,
		line: node.StartPosition().Row + 1, constructor: constructor,
		explicitSuper: explicitSuper, kind: "call", state: state, variables: cloneStringMap(variables),
	})
	a.graph.Stats.Calls++
}

func inferJavaExpressionType(node *tree_sitter.Node, source []byte, variables map[string]string) string {
	if node == nil {
		return ""
	}
	switch node.Kind() {
	case "string_literal", "string_template_expression":
		return "java.lang.String"
	case "character_literal":
		return "char"
	case "true", "false":
		return "boolean"
	case "decimal_integer_literal", "hex_integer_literal", "octal_integer_literal", "binary_integer_literal":
		text := strings.ToLower(node.Utf8Text(source))
		if strings.HasSuffix(text, "l") {
			return "long"
		}
		return "int"
	case "decimal_floating_point_literal", "hex_floating_point_literal":
		if strings.HasSuffix(strings.ToLower(node.Utf8Text(source)), "f") {
			return "float"
		}
		return "double"
	case "null_literal":
		return "null"
	case "identifier":
		return variables[node.Utf8Text(source)]
	case "array_access":
		array := node.ChildByFieldName("array")
		if array == nil {
			array = node.NamedChild(0)
		}
		return strings.TrimSuffix(inferJavaExpressionType(array, source, variables), "[]")
	case "object_creation_expression", "cast_expression":
		if typeNode := node.ChildByFieldName("type"); typeNode != nil {
			return normalizeJavaType(typeNode.Utf8Text(source))
		}
	}
	return ""
}

func normalizeJavaType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "...", "[]")
	return strings.Map(func(character rune) rune {
		if unicode.IsSpace(character) {
			return -1
		}
		return character
	}, value)
}

func eraseJavaType(value string) string {
	value = normalizeJavaType(value)
	var result strings.Builder
	depth := 0
	for _, character := range value {
		switch character {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

func cloneStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func (a *analysis) resolveJavaCalls() {
	edges := make(map[string]*callgraph.Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.calls {
		target := a.resolveJavaCall(call)
		if target == nil {
			a.graph.Unresolved = append(a.graph.Unresolved, callgraph.UnresolvedCall{
				Caller: call.caller, Expression: call.expression, File: call.file, Line: call.line,
				Category: a.javaUnresolvedCategory(call), Language: "java",
			})
			continue
		}
		key := call.caller + "\x00" + target.id + "\x00" + call.kind
		edge := edges[key]
		if edge == nil {
			edge = &callgraph.Edge{Source: call.caller, Target: target.id, Kind: call.kind}
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

func (a *analysis) resolveJavaCall(call javaCall) *javaCallable {
	owners := a.javaCallOwners(call)
	var candidates []*javaCallable
	seen := make(map[string]bool)
	for _, owner := range owners {
		for _, candidate := range a.javaMethodsForOwner(owner, call.name) {
			if !seen[candidate.id] {
				candidates = append(candidates, candidate)
				seen[candidate.id] = true
			}
		}
	}
	if call.reference {
		if len(candidates) == 1 {
			return candidates[0]
		}
		return nil
	}
	var compatible []*javaCallable
	for _, candidate := range candidates {
		if javaArgumentsCompatible(a, call.state, call.arguments, candidate) {
			compatible = append(compatible, candidate)
		}
	}
	if len(compatible) == 1 {
		return compatible[0]
	}
	return nil
}

func (a *analysis) javaCallOwners(call javaCall) []string {
	packageName := a.javaPackageForOwner(call.owner)
	if call.constructor {
		switch {
		case call.explicitSuper:
			if info := a.types[call.owner]; info != nil {
				if owner := a.resolveJavaType(info.file, info.packageName, info.superclass); owner != "" {
					return []string{owner}
				}
			}
		case call.receiver == "this":
			return []string{call.owner}
		default:
			if owner := a.resolveJavaType(call.state, packageName, call.receiver); owner != "" {
				return []string{owner}
			}
		}
		return nil
	}
	if call.receiver == "" {
		owners := []string{call.owner}
		owners = append(owners, call.state.staticImports[call.name]...)
		owners = append(owners, call.state.staticWildcard...)
		return owners
	}
	if call.receiver == "this" {
		return []string{call.owner}
	}
	if call.receiver == "super" {
		if info := a.types[call.owner]; info != nil {
			if owner := a.resolveJavaType(info.file, info.packageName, info.superclass); owner != "" {
				return []string{owner}
			}
		}
		return nil
	}
	receiver := call.receiver
	if strings.HasPrefix(receiver, "this.") {
		receiver = strings.TrimPrefix(receiver, "this.")
	}
	if receiverType := call.variables[receiver]; receiverType != "" {
		if owner := a.resolveJavaType(call.state, packageName, receiverType); owner != "" {
			return []string{owner}
		}
	}
	if owner := a.resolveJavaType(call.state, packageName, receiver); owner != "" {
		return []string{owner}
	}
	return nil
}

func (a *analysis) javaMethodsForOwner(owner string, name string) []*javaCallable {
	var methods []*javaCallable
	seenOwners := make(map[string]bool)
	seenSignatures := make(map[string]bool)
	for owner != "" && !seenOwners[owner] {
		seenOwners[owner] = true
		for _, method := range a.methods[owner+"\x00"+name] {
			signature := strings.Join(method.parameterTypes, "\x00")
			if !seenSignatures[signature] {
				methods = append(methods, method)
				seenSignatures[signature] = true
			}
		}
		if name == "<init>" {
			break
		}
		info := a.types[owner]
		if info == nil || info.superclass == "" {
			break
		}
		owner = a.resolveJavaType(info.file, info.packageName, info.superclass)
	}
	return methods
}

func javaArgumentsCompatible(a *analysis, state *javaFile, arguments []string, candidate *javaCallable) bool {
	parameterCount := len(candidate.parameterTypes)
	if candidate.varargs {
		if len(arguments) < parameterCount-1 {
			return false
		}
	} else if len(arguments) != parameterCount {
		return false
	}
	for index, argument := range arguments {
		parameterIndex := index
		if candidate.varargs && parameterIndex >= parameterCount-1 {
			parameterIndex = parameterCount - 1
		}
		if parameterIndex < 0 || parameterIndex >= parameterCount {
			return false
		}
		parameter := candidate.parameterTypes[parameterIndex]
		if candidate.varargs && index >= parameterCount-1 {
			parameter = strings.TrimSuffix(parameter, "[]")
		}
		if !javaTypesCompatible(a, state, a.javaPackageForOwner(candidate.owner), argument, parameter) {
			return false
		}
	}
	return true
}

func javaTypesCompatible(a *analysis, state *javaFile, packageName string, argument string, parameter string) bool {
	if argument == "" {
		return true
	}
	argument = eraseJavaType(argument)
	parameter = eraseJavaType(parameter)
	if isJavaTypeVariable(parameter) {
		return true
	}
	if argument == "null" {
		return !javaPrimitiveTypes[parameter]
	}
	left := a.resolveJavaType(state, packageName, argument)
	right := a.resolveJavaType(state, packageName, parameter)
	if left == "" {
		left = argument
	}
	if right == "" {
		right = parameter
	}
	if left == right || javaBoxingPairs[left] == right || javaBoxingPairs[right] == left {
		return true
	}
	return false
}

func isJavaTypeVariable(value string) bool {
	if strings.ContainsAny(value, ".[") || value == "" {
		return false
	}
	runes := []rune(value)
	return unicode.IsUpper(runes[0]) && (len(runes) == 1 || strings.HasSuffix(value, "T"))
}

func (a *analysis) resolveJavaType(state *javaFile, packageName string, value string) string {
	value = eraseJavaType(value)
	array := ""
	for strings.HasSuffix(value, "[]") {
		array += "[]"
		value = strings.TrimSuffix(value, "[]")
	}
	if value == "" || javaPrimitiveTypes[value] {
		return value + array
	}
	if strings.Contains(value, ".") {
		if alias := a.aliases[value]; alias != "" {
			return alias + array
		}
		if _, exists := a.types[value]; exists {
			return value + array
		}
		root := strings.SplitN(value, ".", 2)[0]
		if imported := state.imports[root]; imported != "" {
			resolved := imported + strings.TrimPrefix(value, root)
			if alias := a.aliases[resolved]; alias != "" {
				resolved = alias
			}
			return resolved + array
		}
		if unicode.IsLower([]rune(value)[0]) {
			return value + array
		}
	}
	if imported := state.imports[value]; imported != "" {
		if alias := a.aliases[imported]; alias != "" {
			imported = alias
		}
		return imported + array
	}
	samePackage := langutil.Qualified(packageName, value)
	if alias := a.aliases[samePackage]; alias != "" {
		return alias + array
	}
	if _, exists := a.types[samePackage]; exists {
		return samePackage + array
	}
	if javaLangTypes[value] {
		return "java.lang." + value + array
	}
	var matches []string
	for _, wildcard := range state.wildcard {
		candidate := langutil.Qualified(wildcard, value)
		if _, exists := a.types[candidate]; exists {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 1 {
		return matches[0] + array
	}
	if candidates := a.simple[value]; len(candidates) == 1 {
		return candidates[0] + array
	}
	return ""
}

func (a *analysis) javaUnresolvedCategory(call javaCall) string {
	values := []string{call.receiver}
	if call.constructor {
		values = []string{call.receiver}
	}
	for _, value := range values {
		root := strings.SplitN(strings.TrimPrefix(value, "this."), ".", 2)[0]
		if imported := call.state.imports[root]; imported != "" {
			if isJavaRuntime(imported) {
				return "builtin"
			}
			return "external"
		}
		if javaLangTypes[root] || isJavaRuntime(value) {
			return "builtin"
		}
		if variableType := call.variables[root]; variableType != "" {
			resolved := a.resolveJavaType(call.state, a.javaPackageForOwner(call.owner), variableType)
			if isJavaRuntime(resolved) {
				return "builtin"
			}
			typeRoot := strings.SplitN(eraseJavaType(variableType), ".", 2)[0]
			if imported := call.state.imports[typeRoot]; imported != "" {
				return "external"
			}
		}
	}
	if owners := call.state.staticImports[call.name]; len(owners) > 0 {
		if isJavaRuntime(owners[0]) {
			return "builtin"
		}
		return "external"
	}
	return "dynamic"
}

func isJavaRuntime(value string) bool {
	return strings.HasPrefix(value, "java.") || strings.HasPrefix(value, "javax.") || strings.HasPrefix(value, "jdk.")
}

func (a *analysis) javaPackageForOwner(owner string) string {
	if info := a.types[owner]; info != nil {
		return info.packageName
	}
	return ""
}

func (a *analysis) addJavaDispatchEdges() {
	functions := make(map[string]*callgraph.Function, len(a.graph.Nodes))
	for index := range a.graph.Nodes {
		functions[a.graph.Nodes[index].ID] = &a.graph.Nodes[index]
	}
	existing := make(map[string]bool, len(a.graph.Edges))
	for _, edge := range a.graph.Edges {
		existing[edge.Source+"\x00"+edge.Target+"\x00"+edge.Kind] = true
	}
	for _, override := range a.callables {
		function := functions[override.id]
		if function == nil || !slices.Contains(function.Decorators, "Override") {
			continue
		}
		info := a.types[override.owner]
		for info != nil && info.superclass != "" {
			superclass := a.resolveJavaType(info.file, info.packageName, info.superclass)
			var matches []*javaCallable
			for _, candidate := range a.methods[superclass+"\x00"+override.name] {
				if len(candidate.parameterTypes) == len(override.parameterTypes) {
					matches = append(matches, candidate)
				}
			}
			if len(matches) == 1 {
				key := matches[0].id + "\x00" + override.id + "\x00dispatch"
				if !existing[key] {
					a.graph.Edges = append(a.graph.Edges, callgraph.Edge{
						Source: matches[0].id, Target: override.id, Kind: "dispatch", Count: 1,
						Lines: []uint{function.Line},
					})
					existing[key] = true
				}
				break
			}
			info = a.types[superclass]
		}
	}
}

var javaPrimitiveTypes = map[string]bool{
	"boolean": true, "byte": true, "char": true, "double": true, "float": true,
	"int": true, "long": true, "short": true, "void": true,
}

var javaBoxingPairs = map[string]string{
	"boolean": "java.lang.Boolean", "byte": "java.lang.Byte", "char": "java.lang.Character",
	"double": "java.lang.Double", "float": "java.lang.Float", "int": "java.lang.Integer",
	"long": "java.lang.Long", "short": "java.lang.Short",
}

var javaLangTypes = map[string]bool{
	"Boolean": true, "Byte": true, "Character": true, "Class": true, "Double": true,
	"Enum": true, "Exception": true, "Float": true, "Integer": true, "Iterable": true,
	"Long": true, "Math": true, "Number": true, "Object": true, "Record": true,
	"RuntimeException": true, "Short": true, "String": true, "StringBuilder": true,
	"System": true, "Thread": true, "Throwable": true, "Void": true,
}

func isGeneratedJava(source []byte) bool {
	lines := bytes.Split(source, []byte("\n"))
	if len(lines) > 50 {
		lines = lines[:50]
	}
	header := string(bytes.Join(lines, []byte("\n")))
	return strings.Contains(header, "@Generated") ||
		strings.Contains(header, "@javax.annotation.Generated") ||
		strings.Contains(header, "@javax.annotation.processing.Generated")
}
