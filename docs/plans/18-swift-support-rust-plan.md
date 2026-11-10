# Plan 18: Swift support in the Rust server

This plan adds a Swift analyzer to the Rust server. A tree such as
`~/r/kaju`, which has a Swift iOS app in `frontend/ios`, then shows the
Swift callables and the calls between them.

The Go server does not get Swift. The Go module of the grammar,
`github.com/alex-pinkus/tree-sitter-swift`, has no generated `parser.c` at
a tagged version. The generated files are only on tags with the suffix
`-with-generated-files`, which are not Go module versions. The crate
`tree-sitter-swift` on crates.io has the generated parser, so the Rust
server can add the grammar with one line in `Cargo.toml`.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 15.1. This plan writes code in `backend/`, which plan 15.1 makes. Do
not start this plan before plan 15.1 is merged.

Plan 17 is not necessary. Plan 17 adds JavaScript to the Go server and to
the Rust server. The two plans change different files.

## Before you start

1. Make a new branch from `main`.
2. Run `cargo test --manifest-path backend/Cargo.toml` and
   `go test ./...`. Make sure that all tests pass.
3. Run `cargo info tree-sitter-swift`. The version must be 0.7.4 or newer.
   The crate must depend on `tree-sitter-language` 0.1, which the
   `tree-sitter` 0.25 crate of plan 15.1 accepts.
4. Build the Rust binary. Start it on `~/r/kaju`. Note the node count in
   the title bar. Keep this count for the validation.

## Decisions

- **Rust only.** The Swift analyzer exists in the Rust server and not in
  the Go server. `scripts/graph-diff.sh` compares the two servers. On a
  tree with Swift files, run it with a LANGUAGE argument, or remove the
  Swift nodes from the Rust output before the comparison. Add this to the
  script: when the environment variable `GRAPH_DIFF_SKIP` names a language,
  the script removes the nodes of that language and the edges between
  them from both outputs.
- **No file imports.** Swift has no file imports. An `import` statement
  names a module, such as `Foundation`, and all files of one target share
  one namespace. The analyzer resolves a call by name across all Swift
  files, and selects the nearest callable when more than one has the
  name. See "Call resolution" in Task 2.
- **No Xcode project parsing.** The analyzer does not read `.xcodeproj`,
  `.pbxproj`, `project.yml`, or `Package.swift` to find targets. The
  nearest-callable rule keeps the targets of one tree apart in most trees.
- **No `super`.** A call through `super` stays unresolved, as the other
  languages leave inheritance unresolved.
- **Fail closed.** A file with a Tree-sitter `ERROR` node stops the
  analysis with the same error text as the other languages. The Swift
  grammar is less complete than the other five grammars. If the grammar
  rejects a correct Swift file in `~/r/kaju`, stop and report the file
  and the construct. A rule that skips one file is a change to the
  fail-closed rule, and needs its own decision.
- **Same names as the other analyzers.** The kinds are `function` and
  `method`. The ID is `FILE:LINE:QUALIFIED_NAME`. The edge kind is `call`.
  The 3D view, the 2D view, the activity mapper, and the session log then
  need no change.
- **`Package.swift` is a manifest.** `Packages` in `backend/src/analyzer.rs`
  and in `internal/analyzer/packages.go` get the kind `swiftpm` for
  `Package.swift`. This is the one change to the Go code in this plan. It
  keeps the package of a file the same in both servers.

## Pieces of code

Each row is one file that this plan adds or changes.

| File | Change |
| --- | --- |
| `backend/Cargo.toml` | Add `tree-sitter-swift = "0.7.4"` to `[dependencies]`. |
| `backend/src/lang/swift.rs` | New. The `Language` for Swift. Task 2. |
| `backend/src/lang/mod.rs` | Add `pub mod swift;`. |
| `backend/src/main.rs` | Register `swift::Language` after `typescript::Language`. |
| `backend/examples/dump_graph.rs` | Register `swift::Language` in the same position. |
| `backend/src/analyzer.rs` | Add `"Package.swift"` with the kind `swiftpm` to the manifest kinds of `packages`. Task 1. |
| `internal/analyzer/packages.go` | Add `"Package.swift": "swiftpm"` to `manifestKinds`. Task 1. |
| `internal/analyzer/packages_test.go` | Add the Swift Package Manager case. Task 1. |
| `scripts/graph-diff.sh` | Add `GRAPH_DIFF_SKIP`. Task 3. |
| `README.md` | The language list, the layout table, a "Swift analysis recognizes" list, the limits paragraph, the Fidelity section, and a sentence in "Build and run" that says that only the Rust binary reads Swift. Task 3. |
| `THIRD_PARTY_NOTICES` | The license of `tree-sitter-swift` (MIT). Task 3. |

The web code does not change. `web/src` reads the `language` of a node
only as text.

## Task 1: The Swift Package Manager manifest

### Result

`Package.swift` is a manifest of the kind `swiftpm` in both servers. A
tree with two or more local packages, each with a `Package.swift`, is a
workspace. The package of a Swift file is then its Swift package.

### Procedure

1. Add `"Package.swift": "swiftpm"` to `manifestKinds` in
   `internal/analyzer/packages.go`.
2. Add the same entry to the manifest kinds in `backend/src/analyzer.rs`.

### Tests

1. Add this case to `internal/analyzer/packages_test.go` and to the
   `packages` tests in `backend/src/analyzer.rs`:

   | Files and manifests | Expected packages |
   | --- | --- |
   | `Package.swift`, `Packages/a/Package.swift`, `Packages/b/Package.swift`, `Packages/a/Sources/A/a.swift`, `Packages/b/Sources/B/b.swift`, `Sources/App/main.swift` | `Packages/a`, `Packages/b`, `Sources/App` |

2. Run `go test ./internal/analyzer/` and
   `cargo test --manifest-path backend/Cargo.toml packages`.

## Task 2: The Swift analyzer

### Result

`backend/src/lang/swift.rs` has `Language`, which implements the
`Language` trait of `analyzer.rs`.

| Method | Value |
| --- | --- |
| `name` | `"swift"` |
| `extensions` | `[".swift"]` |
| `skip_dirs` | `[".build", ".swiftpm", "DerivedData", "Pods", "Carthage"]` |
| `is_test_file` | True for a path in a directory named `Tests` or with a name that ends in `Tests`, and for a file with a name that ends in `Tests.swift` or `Test.swift`. |
| `analyze` | Parses each file in sequence with `util::parse`, collects the callables and the calls, then resolves the calls. |

#### Grammar nodes

The grammar `tree-sitter-swift` 0.7.4 has these nodes. Check them with
`tree-sitter parse` on a fixture before you write the walker.

| Node | Meaning |
| --- | --- |
| `source_file` | The root. |
| `class_declaration` | A `class`, `struct`, `enum`, `actor`, or `extension`. The field `declaration_kind` is the keyword. The field `name` is the type name. The field `body` is a `class_body` or an `enum_class_body`. |
| `protocol_declaration` | A `protocol`. Its `protocol_body` has requirements with no body. The analyzer does not make nodes for them. |
| `function_declaration` | A `func`. The field `name` is a `simple_identifier`. The field `body` is a `function_body`. A `function_declaration` with no `body` is a protocol requirement. |
| `init_declaration` | An `init`. Its name is `init`. |
| `deinit_declaration` | A `deinit`. Its name is `deinit`. |
| `modifiers` with `property_modifier` | The text `static` or `class` before a `func`. |
| `computed_property` | A property with a `get` body. Not a callable. The walker goes into its body to find calls. |
| `lambda_literal` | A closure. Not a callable. The walker goes into its body to find calls. |
| `call_expression` | A call. The first named child is the callee. The last is a `call_suffix` with `value_arguments`. |
| `navigation_expression` | `a.b`. The first named child is the receiver. The last is the member name. |
| `self_expression` | `self`. |
| `super_expression` | `super`. |
| `constructor_expression` | `Foo()` or `Foo(x: 1)` where `Foo` is a `user_type`. |
| `simple_identifier`, `type_identifier`, `user_type` | Names. |

#### Callables

| Swift | Kind | Name | Qualified name | Class | Modifiers |
| --- | --- | --- | --- | --- | --- |
| `func f()` at the top level | `function` | `f` | `f` | empty | none |
| `func f()` in type `T` | `method` | `f` | `T.f` | `T` | none |
| `static func f()` or `class func f()` in `T` | `method` | `f` | `T.f` | `T` | `static` or `class` |
| `init()` in `T` | `method` | `init` | `T.init` | `T` | none |
| `deinit` in `T` | `method` | `deinit` | `T.deinit` | `T` | none |
| `func f()` in `extension T` | `method` | `f` | `T.f` | `T` | none |
| `func f()` in nested type `Outer.Inner` | `method` | `f` | `Outer.Inner.f` | `Outer.Inner` | none |
| `func g()` nested in function `f` | the kind of `f` | `g` | `f.g` or `T.f.g` | the class of `f` | none |

A protocol requirement, a computed property, a closure, and a property
with a default value are not callables.

The `module` of a callable is the directory of its file with `/` changed
to `.`, as `typescript_module_name` does, but without the file name. The
qualified name does not start with the module, because two files of one
target share one namespace. Thus two files can have one qualified name.
The ID stays unique, because it starts with the file and the line.

#### Calls

The walker collects each `call_expression` and each
`constructor_expression` with the ID of the callable that contains it. A
call at the top level of a file, outside all callables, is not collected.

| Call | Receiver form | Name |
| --- | --- | --- |
| `f()` | none | `f` |
| `self.f()` | `self` | `f` |
| `Self.f()` | `Self` | `f` |
| `T.f()` where `T` starts with an upper-case letter | type `T` | `f` |
| `T()` or `T(x: 1)` | constructor | `T` |
| `x.f()` where `x` starts with a lower-case letter | value | `f` |
| `super.f()` | `super` | `f` |
| `a.b.f()` | the form of `b` | `f` |

#### Call resolution

After all files are collected, the analyzer resolves each call with
these steps. The first step that finds one callable gives the target.

1. Receiver `self`, `Self`, or none inside a type `T`: the callables
   named `NAME` with class `T`. A method of `T` in an extension counts.
   For `none`, if `T` has no such method, continue with step 4.
2. Receiver type `T`: the callables named `NAME` with class `T` and the
   modifier `static` or `class`. If there are none, the callables named
   `NAME` with class `T`.
3. Constructor `T`: the callables named `init` with class `T`. If there
   are none, the call stays unresolved. A struct with no `init` has a
   memberwise initializer, which is not in the source.
4. Receiver `none` outside a type, or after step 1 found nothing: the
   callables named `NAME` with no class.
5. Receiver `value` or `super`: the call stays unresolved.

If a step finds more than one callable, the analyzer selects in this
order: the callables in the same file as the call, then the callables in
the same directory, then the callables in the nearest directory above.
If two or more callables remain, for example two overloads with different
argument labels in one file, the call stays unresolved, as an ambiguous
Java overload does.

Each resolved call becomes an edge of kind `call` from the caller to the
target. The same pair gets one edge.

### Procedure

1. Add the crate to `backend/Cargo.toml`. Run `cargo build` and make sure
   that the C parser compiles. `parser.c` is 23 MB; the first build of the
   crate takes some seconds.
2. Add `backend/src/lang/swift.rs` and `pub mod swift;`. Use
   `backend/src/lang/rust.rs` as the model. A Rust `impl` block is the
   nearest thing to a Swift extension.
3. Write `analyze`. For each file: read the bytes, parse with
   `util::parse` and `tree_sitter_swift::LANGUAGE`, make the relative
   path with `/`, then walk the root. Read node text as bytes and change
   it with `String::from_utf8_lossy`, as plan 15.1 says.
4. Write the walker. Keep a stack of the enclosing types and the ID of
   the enclosing callable. On a `class_declaration`, push the type name,
   or `Outer.Inner` in a nested type, walk the body, then pop. On an
   `extension`, push the extended type name. On a `protocol_declaration`,
   do not push and do not collect the requirements. On a
   `function_declaration` with a body, an `init_declaration`, or a
   `deinit_declaration`, make the callable, then walk the body with its
   ID as the enclosing callable. On a `call_expression` or a
   `constructor_expression` inside a callable, collect the call.
5. Write the resolver. Index the callables by name in a
   `HashMap<String, Vec<usize>>`. Apply the steps above.
6. Register `Language` in `main.rs` and in `examples/dump_graph.rs`.

### Tests

Add a `#[cfg(test)] mod tests` to `swift.rs`. Use `write_fixture`,
`analyze`, `find_function`, and `assert_edge` from `lang/test_util.rs`.

| Test | Fixture | Expectation |
| --- | --- | --- |
| Free functions across files | `a.swift` has `func a() { b() }`; `b.swift` has `func b() {}`. | Two `function` nodes. Edge `a` to `b`. |
| Struct members | `struct Foo { init() {} func m() { self.n() } func n() {} static func s() {} }` and `func use() { let f = Foo(); f.m(); Foo.s() }`. | Nodes `Foo.init`, `Foo.m`, `Foo.n`, `Foo.s` with kind `method`. Edges `Foo.m` to `Foo.n`, `use` to `Foo.init`, `use` to `Foo.s`. No edge for `f.m()`. |
| Extension in another file | `foo.swift` has `struct Foo { func m() { helper() } }`; `foo+ext.swift` has `extension Foo { func helper() {} }`. | Edge `Foo.m` to `Foo.helper`. |
| Nested function and nested type | `func outer() { func inner() {} inner() }` and `enum Outer { struct Inner { func f() {} } }`. | Nodes `outer`, `outer.inner`, `Outer.Inner.f`. Edge `outer` to `outer.inner`. |
| Nearest callable | `a/x.swift` has `func shared() {}` and `func callA() { shared() }`; `b/y.swift` has `func shared() {}` and `func callB() { shared() }`. | Edge `callA` to `shared` in `a/x.swift`. Edge `callB` to `shared` in `b/y.swift`. |
| Ambiguous overload | `func f(a: Int) {}`, `func f(b: Int) {}`, `func g() { f(a: 1) }` in one file. | No edge from `g`. |
| Protocol requirement | `protocol P { func req() }` and `struct S: P { func req() {} }`. | One node `S.req`. No node for `P.req`. |
| Closure and computed property | `var v: Int { helper(); return 1 }` in a struct, and `let c = { helper() }` in a function. | No node for `v` or `c`. Edge from the function to `helper`. The call in `v` is not collected. |
| Test file | `Tests/FooTests.swift` and `KajuTests/BarTests.swift`. | No node without `include_tests`. Nodes with it. |
| Syntax error | `func broken( {` | An error with the file name and `syntax error`. |
| Bytes that are not UTF-8 | A comment with the byte `0xff`. | The same nodes as without the byte. |

Run `cargo test --manifest-path backend/Cargo.toml swift`.

## Task 3: Documents and the graph check

### Result

- `scripts/graph-diff.sh` accepts `GRAPH_DIFF_SKIP=swift`.
- `README.md` says which binary reads Swift, and what the Swift analysis
  recognizes and does not recognize.
- `THIRD_PARTY_NOTICES` has the license of the grammar.

### Procedure

1. In `scripts/graph-diff.sh`, after the `jq` step, remove the nodes whose
   `language` is `$GRAPH_DIFF_SKIP` and the edges between two removed
   nodes from both outputs. An empty variable removes nothing.
2. In `README.md`:
   - In the introduction, change the language list to "Python, Go, Java,
     Rust, TypeScript, and Swift (Rust server only)".
   - In "Build and run", add one sentence: the Rust binary reads Swift
     files, and the Go binary does not.
   - In the layout table, add `backend/src/lang/swift.rs`.
   - In "Analysis model", add a "Swift analysis recognizes" list: `.swift`
     files; free functions, methods, initializers, static and class
     methods, nested functions and nested types; methods in extensions;
     unqualified, `self`, `Self`, type, and constructor calls; resolution
     by name with the nearest callable.
   - In the limits paragraph, add: Swift analysis does not read Xcode
     projects or `Package.swift` for targets, does not resolve calls on
     values or through `super`, does not expand macros or result builders,
     and does not perform type inference.
   - In "Fidelity", add one sentence that lists the Swift fixtures.
3. Add the MIT license of `tree-sitter-swift` to `THIRD_PARTY_NOTICES`,
   in the form of the other grammar entries.

## Validation

All steps must pass.

1. `cargo test --manifest-path backend/Cargo.toml`, `go test ./...`, and
   `npm --prefix web test` pass.
2. Start the Rust binary on `~/r/kaju`. The node count is higher than the
   count from "Before you start". The output of `GET /api/graph` has nodes
   with `"language":"swift"` and files in `frontend/ios/Kaju`. No node has
   a file in `frontend/ios/KajuTests` or `frontend/ios/KajuUITests`. The
   package of a Swift node is its directory, for example
   `frontend/ios/Kaju/Services`, because the app has no `Package.swift`.
   The two Rust crates are unchanged.
3. On the same tree, a method that a view calls on a service has an edge.
   Click a node in `frontend/ios/Kaju/Services`. The blast radius list
   shows the callers in `frontend/ios/Kaju/Views`.
4. Run `GRAPH_DIFF_SKIP=swift scripts/graph-diff.sh ~/r/kaju`. Expect no
   diff. Run `scripts/graph-diff.sh .` on this repository. Expect no diff.
5. Make a Swift Package Manager tree with `Packages/a/Package.swift` and
   `Packages/b/Package.swift`, each with one source file. Start the Rust
   binary on it. The package of each node is `Packages/a` or `Packages/b`.
6. Ask Claude Code to read a Swift file in `~/r/kaju` and to edit one
   method. The reticle finds the method, and the code pane shows the
   change.
