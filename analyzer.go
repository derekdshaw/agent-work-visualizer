package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

type Graph struct {
	Root       string           `json:"root"`
	Files      []GraphFile      `json:"files"`
	Nodes      []Function       `json:"nodes"`
	Edges      []Edge           `json:"edges"`
	Unresolved []UnresolvedCall `json:"unresolved"`
	Stats      Stats            `json:"stats"`
}

type GraphFile struct {
	Path       string `json:"path"`
	Module     string `json:"module"`
	Language   string `json:"language"`
	Dependency bool   `json:"dependency,omitempty"`
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
	Dependency    bool     `json:"dependency,omitempty"`
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
	Files                 int `json:"files"`
	ProjectFiles          int `json:"projectFiles"`
	DependencyFiles       int `json:"dependencyFiles"`
	Definitions           int `json:"definitions"`
	ProjectDefinitions    int `json:"projectDefinitions"`
	DependencyDefinitions int `json:"dependencyDefinitions"`
	Calls                 int `json:"calls"`
	Resolved              int `json:"resolved"`
	Unresolved            int `json:"unresolved"`
}

type Options struct {
	IncludeTests bool
	// bazel, when set, keeps the first Bazel discovery so later analyses of
	// the same root skip the bazel query.
	bazel *bazelCache
}

type bazelCache struct {
	done    bool
	sources *bazelSources
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

type sourceFile struct {
	path       string
	language   string
	dependency bool
}

type operationLog struct {
	name    string
	started time.Time
	stop    chan struct{}
	done    chan struct{}
}

type analyzer struct {
	root        string
	graph       Graph
	files       map[string]*fileState
	nodesByID   map[string]*Function
	rawCalls    []rawCall
	flowClasses map[string]bool
	golang      *goAnalysis
	java        *javaAnalysis
	rust        *rustAnalysis
	typescript  *typescriptAnalysis
	workspace   string
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

func Analyze(root string, options Options) (*Graph, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", absoluteRoot)
	}
	log.Printf("Analyzing %s", absoluteRoot)
	analysisStarted := time.Now()

	a := newAnalyzer(absoluteRoot)
	discoveryLog := beginOperation("Discovering source files")
	files, err := collectSourceFiles(absoluteRoot, options)
	discoveryLog.finish(err)
	if err != nil {
		return nil, err
	}
	counts := sourceFileCounts(files)
	pythonFiles := counts["python"]
	javaFiles := counts["java"]
	log.Printf(
		"Discovered %d source files (%d Python, %d Go, %d Java, %d Rust, %d TypeScript)",
		len(files), pythonFiles, counts["go"], javaFiles, counts["rust"], counts["typescript"],
	)
	var bazel *bazelSources
	if javaFiles > 0 && options.bazel != nil && options.bazel.done {
		bazel = options.bazel.sources
	} else if javaFiles > 0 {
		bazelLog := beginOperation("Discovering Bazel Java dependencies")
		bazel, err = discoverBazelSources(absoluteRoot, options)
		bazelLog.finish(err)
		if err != nil {
			return nil, err
		}
		if options.bazel != nil {
			*options.bazel = bazelCache{done: true, sources: bazel}
		}
	}
	var protoFiles []string
	if bazel != nil {
		a.workspace = bazel.workspace
		existing := make(map[string]bool, len(files))
		for _, file := range files {
			existing[file.path] = true
		}
		for _, path := range bazel.java {
			if !existing[path] {
				files = append(files, sourceFile{path: path, language: "java", dependency: true})
			}
		}
		addedJavaFiles := len(files) - len(existing)
		protoFiles = bazel.proto
		sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
		log.Printf("Bazel discovery added %d Java dependencies and found %d proto files", addedJavaFiles, len(protoFiles))
	}
	if len(protoFiles) > 0 {
		protoLog := beginOperation("Parsing proto services")
		err = a.collectProtoServices(protoFiles)
		protoLog.finish(err)
		if err != nil {
			return nil, err
		}
	}
	if err := a.analyzeSourceFiles(files); err != nil {
		return nil, err
	}
	if pythonFiles > 0 {
		resolvePythonLog := beginOperation("Resolving Python calls")
		a.resolveCalls()
		resolvePythonLog.finish(nil)
	}
	if counts["go"] > 0 {
		resolveGoLog := beginOperation("Resolving Go calls")
		a.resolveGoCalls()
		resolveGoLog.finish(nil)
	}
	if javaFiles > 0 {
		resolveJavaLog := beginOperation("Resolving Java calls")
		a.resolveJavaCalls()
		a.addJavaDispatchEdges()
		a.markJavaFrameworkEntrypoints()
		resolveJavaLog.finish(nil)
	}
	if counts["rust"] > 0 {
		resolveRustLog := beginOperation("Resolving Rust calls")
		a.resolveRustCalls()
		resolveRustLog.finish(nil)
	}
	if counts["typescript"] > 0 {
		resolveTypescriptLog := beginOperation("Resolving TypeScript calls")
		a.resolveTypescriptCalls()
		resolveTypescriptLog.finish(nil)
	}
	finishLog := beginOperation("Finalizing graph")
	a.finish()
	finishLog.finish(nil)
	log.Printf("Analysis complete (%s)", time.Since(analysisStarted).Round(time.Millisecond))
	return &a.graph, nil
}

func newAnalyzer(root string) *analyzer {
	return &analyzer{
		root: root,
		// The viewer reads nodes, edges, and unresolved as JSON arrays. A nil slice encodes as null.
		graph:       Graph{Root: root, Nodes: []Function{}, Edges: []Edge{}, Unresolved: []UnresolvedCall{}},
		files:       make(map[string]*fileState),
		nodesByID:   make(map[string]*Function),
		flowClasses: make(map[string]bool),
		golang:      newGoAnalysis(),
		java:        newJavaAnalysis(),
		rust:        newRustAnalysis(),
		typescript:  newTypescriptAnalysis(),
	}
}

func beginOperation(name string) *operationLog {
	started := time.Now()
	log.Printf("%s", name)
	operation := &operationLog{
		name:    name,
		started: started,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		defer close(operation.done)
		for {
			select {
			case <-ticker.C:
				log.Printf("%s still running (%s elapsed)", name, time.Since(started).Round(time.Second))
			case <-operation.stop:
				return
			}
		}
	}()
	return operation
}

func (operation *operationLog) finish(err error) {
	close(operation.stop)
	<-operation.done
	status := "complete"
	if err != nil {
		status = "failed"
	}
	log.Printf("%s %s (%s)", operation.name, status, time.Since(operation.started).Round(time.Millisecond))
}

func sourceFileCounts(files []sourceFile) map[string]int {
	counts := make(map[string]int)
	for _, file := range files {
		counts[file.language]++
	}
	return counts
}

func (a *analyzer) analyzeSourceFiles(files []sourceFile) error {
	operation := beginOperation(fmt.Sprintf("Parsing %d source files", len(files)))
	progressInterval := max(1, len(files)/20)
	for index, file := range files {
		displayPath := sourceDisplayPath(a.root, file.path)
		if index == 0 || index%progressInterval == 0 || index == len(files)-1 {
			log.Printf("Parsing source files: %d/%d (%s)", index+1, len(files), displayPath)
		}
		fileStarted := time.Now()
		slowFileLog := time.AfterFunc(5*time.Second, func() {
			log.Printf("Still parsing source file %d/%d: %s (%s elapsed)",
				index+1, len(files), displayPath, time.Since(fileStarted).Round(time.Second))
		})
		err := a.analyzeFile(file)
		slowFileLog.Stop()
		if err != nil {
			operation.finish(err)
			return err
		}
	}
	operation.finish(nil)
	return nil
}

func (a *analyzer) analyzeFile(file sourceFile) error {
	switch file.language {
	case "python":
		return a.analyzePythonFile(file.path)
	case "go":
		return a.analyzeGoFile(file.path)
	case "java":
		return a.analyzeJavaFile(file.path, file.dependency)
	case "rust":
		return a.analyzeRustFile(file.path)
	case "typescript":
		return a.analyzeTypescriptFile(file.path)
	}
	return nil
}

func sourceDisplayPath(root string, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return relative
}

func collectSourceFiles(root string, options Options) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && shouldSkipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		language := sourceLanguage(entry.Name())
		if language == "" {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if !options.IncludeTests && isTestPath(relative, language) {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isGenerated(source) || language == "java" && isGeneratedJava(source) {
			return nil
		}
		files = append(files, sourceFile{path: path, language: language})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect source files: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

// sourceLanguage returns the analyzer language for a file name, or "" when the
// extension is not analyzed.
func sourceLanguage(name string) string {
	switch filepath.Ext(name) {
	case ".py":
		return "python"
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".rs":
		return "rust"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	}
	return ""
}

func shouldSkipDirectory(name string) bool {
	switch name {
	case ".agent-env", ".direnv", ".git", ".hg", ".nox", ".svn", ".mypy_cache", ".pytest_cache", ".ruff_cache",
		".tox", ".venv", "__pycache__", ".gradle", "build", "dist", "node_modules", "out", "site-packages", "target", "vendor", "venv":
		return true
	default:
		return false
	}
}

func isTestPath(path string, language string) bool {
	name := filepath.Base(path)
	if language == "python" && (strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py")) {
		return true
	}
	if language == "go" && strings.HasSuffix(name, "_test.go") {
		return true
	}
	if language == "java" && (strings.HasSuffix(name, "Test.java") || strings.HasSuffix(name, "Tests.java") || strings.HasSuffix(name, "IT.java")) {
		return true
	}
	if language == "rust" && strings.HasSuffix(name, "_test.rs") {
		return true
	}
	if language == "typescript" &&
		(strings.HasSuffix(name, ".test.ts") || strings.HasSuffix(name, ".test.tsx") ||
			strings.HasSuffix(name, ".test.mts") || strings.HasSuffix(name, ".test.cts") ||
			strings.HasSuffix(name, ".spec.ts") || strings.HasSuffix(name, ".spec.tsx") ||
			strings.HasSuffix(name, ".spec.mts") || strings.HasSuffix(name, ".spec.cts")) {
		return true
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == "test" || part == "tests" || language == "go" && part == "testdata" || language == "java" && part == "it" {
			return true
		}
	}
	return false
}

func isGenerated(source []byte) bool {
	lines := bytes.Split(source, []byte("\n"))
	if len(lines) > 10 {
		lines = lines[:10]
	}
	header := strings.ToLower(string(bytes.Join(lines, []byte("\n"))))
	return strings.Contains(header, "generated by") ||
		strings.Contains(header, "code generated") ||
		strings.Contains(header, "@generated") ||
		strings.Contains(header, "automatically generated") ||
		strings.Contains(header, "auto-generated")
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

func (a *analyzer) analyzePythonFile(path string) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(tree_sitter.NewLanguage(tree_sitter_python.Language())); err != nil {
		return fmt.Errorf("configure Python parser: %w", err)
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
	state := &fileState{
		module:  moduleName(relative),
		pkgInit: filepath.Base(relative) == "__init__.py",
		imports: make(map[string]importRef),
	}
	a.files[relative] = state
	a.graph.Files = append(a.graph.Files, GraphFile{Path: relative, Module: state.module, Language: "python"})
	a.graph.Stats.Files++
	a.graph.Stats.ProjectFiles++
	a.walk(root, source, relative, state, nil, "", nil, false)
	return nil
}

func firstInvalidNode(node *tree_sitter.Node) *tree_sitter.Node {
	if node.IsError() || node.IsMissing() {
		return node
	}
	if !node.HasError() {
		return nil
	}
	for i := uint(0); i < node.ChildCount(); i++ {
		if bad := firstInvalidNode(node.Child(i)); bad != nil {
			return bad
		}
	}
	return node
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

func (a *analyzer) walk(
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
			string(source[node.StartByte():minInt(len(source), int(body.StartByte()))]),
			"FlowSpec",
		)
		if isFlow {
			a.flowClasses[qualified(state.module, name)] = true
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

func (a *analyzer) collectFunction(
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
	qualifiedName := qualified(parts...)
	point := node.StartPosition()
	end := node.EndPosition()
	id := fmt.Sprintf("%s:%d:%s", file, point.Row+1, qualifiedName)
	function := Function{
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
	a.graph.Stats.ProjectDefinitions++

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

func (a *analyzer) collectImport(node *tree_sitter.Node, source []byte, state *fileState) {
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

func (a *analyzer) collectCall(
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

func (a *analyzer) resolveCalls() {
	byQualified := make(map[string]*Function)
	byModuleName := make(map[string]*Function)
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if function.Language != "python" {
			continue
		}
		a.nodesByID[function.ID] = function
		byQualified[function.QualifiedName] = function
		if function.Parent == "" && function.Class == "" {
			byModuleName[qualified(function.Module, function.Name)] = function
		}
	}
	edges := make(map[string]*Edge)
	lineSets := make(map[string]map[uint]bool)
	for _, call := range a.rawCalls {
		target := a.resolveCall(call, byQualified, byModuleName)
		if target == nil {
			if call.caller != "" {
				a.graph.Unresolved = append(a.graph.Unresolved, UnresolvedCall{
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
			edge = &Edge{Source: call.caller, Target: target.ID, Kind: call.kind}
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

func (a *analyzer) unresolvedCategory(call rawCall) string {
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

func (a *analyzer) resolveCall(
	call rawCall,
	byQualified map[string]*Function,
	byModuleName map[string]*Function,
) *Function {
	caller := a.nodesByID[call.caller]
	state := a.files[call.file]
	expression := strings.TrimSpace(call.expression)
	if caller != nil && (strings.HasPrefix(expression, "self.") || strings.HasPrefix(expression, "cls.")) {
		name := strings.TrimPrefix(strings.TrimPrefix(expression, "self."), "cls.")
		if caller.Class != "" {
			return byQualified[qualified(caller.Module, caller.Class, name)]
		}
		return nil
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
		if imported, ok := state.imports[expression]; ok {
			if imported.name != "" {
				return byModuleName[qualified(imported.module, imported.name)]
			}
		}
		if target := byModuleName[qualified(state.module, expression)]; target != nil {
			return target
		}
		if a.flowClasses[qualified(state.module, expression)] {
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
		return byQualified[qualified(targetParts...)]
	}
	if target := byQualified[qualified(state.module, expression)]; target != nil {
		return target
	}
	return byQualified[expression]
}

func (a *analyzer) markFlowEntrypoints(module string, class string) {
	prefix := qualified(module, class) + "."
	for index := range a.graph.Nodes {
		function := &a.graph.Nodes[index]
		if strings.HasPrefix(function.QualifiedName, prefix) && contains(function.Decorators, "step") {
			function.Entrypoint = true
			function.EntryKind = "metaflow-step"
		}
	}
}

func (a *analyzer) finish() {
	sort.Slice(a.graph.Files, func(i, j int) bool {
		return a.graph.Files[i].Path < a.graph.Files[j].Path
	})
	sort.Slice(a.graph.Nodes, func(i, j int) bool {
		return a.graph.Nodes[i].ID < a.graph.Nodes[j].ID
	})
	sort.Slice(a.graph.Edges, func(i, j int) bool {
		left := a.graph.Edges[i].Source + "\x00" + a.graph.Edges[i].Target + "\x00" + a.graph.Edges[i].Kind
		right := a.graph.Edges[j].Source + "\x00" + a.graph.Edges[j].Target + "\x00" + a.graph.Edges[j].Kind
		return left < right
	})
	sort.Slice(a.graph.Unresolved, func(i, j int) bool {
		left := fmt.Sprintf("%s:%09d:%s", a.graph.Unresolved[i].File, a.graph.Unresolved[i].Line, a.graph.Unresolved[i].Expression)
		right := fmt.Sprintf("%s:%09d:%s", a.graph.Unresolved[j].File, a.graph.Unresolved[j].Line, a.graph.Unresolved[j].Expression)
		return left < right
	})
}

func qualified(parts ...string) string {
	var nonempty []string
	for _, part := range parts {
		if part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, ".")
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func marshalGraph(graph *Graph) ([]byte, error) {
	data, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("encode graph: %w", err)
	}
	if bytes.Contains(data, []byte("</script")) {
		return nil, errors.New("encoded graph contains an unsafe script terminator")
	}
	return data, nil
}
