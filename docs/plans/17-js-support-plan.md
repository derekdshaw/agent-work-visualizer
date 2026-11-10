# Plan 17: JavaScript support

This plan adds JavaScript files to the TypeScript analyzer, which already
parses them. A tree such as `~/r/kaju`, which has a Rust backend and a
JavaScript web frontend, then shows both parts in the graph.

Swift is not in this plan. Plan 18 adds Swift to the Rust server after
plan 15.1. The Swift grammar is on crates.io with its generated parser,
but the Go module of the grammar has no generated parser at a tagged
version.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing. Plan 08 is merged, so the 2D view gets the `package` of each new
node with no other change.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...` and make sure that all tests pass.
3. Build the server and start it on `~/r/kaju`. Note the node count in the
   title bar. All of the nodes are Rust nodes. Keep this count for the
   validation.
4. If plan 15.1 is open, do not start this plan. Plan 15.1 does not permit
   a change to the Go code while it is open. If plan 15.1 is merged, make
   the same change in `backend/src/lang/typescript.rs` and
   `backend/src/analyzer.rs` in the same pull request, and run
   `scripts/graph-diff.sh ~/r/kaju`.

## Decisions

- **One analyzer for TypeScript and JavaScript.** A project can have both,
  and a TypeScript file can import a JavaScript file. The calls between
  them resolve only if one analyzer sees both. Do not make a second
  `Language` for JavaScript.
- **The language of a node is its file type.** A node from a `.js` file
  has `Language: "javascript"`, so the graph and the log show the correct
  language. `Name()` stays `"typescript"`, because it names the analyzer.
- **Minified files are not source.** A file whose name ends in `.min.js`
  is skipped as a generated file. These files have one line of many
  thousands of characters and no useful callables.

## Task 1: JavaScript files in the TypeScript analyzer

### Problem

`Extensions()` in `internal/lang/typescript/typescript.go:29` claims only
`.ts`, `.tsx`, `.mts`, and `.cts`. The Tree-sitter TypeScript grammar
parses JavaScript, because TypeScript is a superset of JavaScript. The
analyzer only has to claim the files.

### Result

- `Extensions()` also returns `.js`, `.jsx`, `.mjs`, and `.cjs`.
- A `.jsx` file is parsed with the TSX grammar, as a `.tsx` file is.
- A node from a `.js`, `.jsx`, `.mjs`, or `.cjs` file has
  `Language: "javascript"`.
- `IsTestFile` also accepts `.test.js`, `.spec.js`, and the same for the
  other three extensions.
- An import of a `.js` file from a `.ts` file, and the opposite, resolves.
  `import { a } from "./helper.js"` resolves to `helper.ts` or `helper.js`,
  whichever exists. TypeScript projects that emit ES modules use this
  form.
- CommonJS imports resolve: `const x = require("./x")` binds `x` as a
  namespace import, and `const { a, b: c } = require("./x")` binds `a` and
  `c` as named imports.
- `collectSourceFiles` in `internal/analyzer/analyzer.go` skips a file
  whose name ends in `.min.js`, in the same place that it skips a
  generated file.

### Procedure

1. Add the four extensions to `Extensions()` and `IsTestFile`.
2. In `analyzeTypescriptFile`, select the TSX grammar for `.jsx` as well
   as `.tsx`. Set the language of each node from the extension of its
   file. Find where the analyzer sets `Language` on a `callgraph.Function`
   and pass the file extension to it.
3. In the import resolver, strip a `.js`, `.mjs`, or `.cjs` suffix from a
   relative import path before the lookup, so `./helper.js` matches the
   module `helper`.
4. Add `require` to `collectTypescriptImport`. Only `require` with one
   string literal counts. A `require` with a variable or a template string
   stays unresolved.
5. In `collectSourceFiles`, skip a name that ends in `.min.js`. Put the
   check beside the `isGenerated` check.
6. Update `README.md`: the language list in the introduction, the layout
   table, the "TypeScript analysis recognizes" list, and the Fidelity
   section.

### Tests

Add to `internal/lang/typescript/typescript_test.go`, with
`langtest.WriteFixture` as the other tests do:

1. A `.js` file with a function and a class, called from a `.ts` file
   through `import { helper } from "./helper.js"`. Expect the edges and
   expect `Language == "javascript"` on the nodes of the `.js` file.
2. A `.jsx` file with a function that returns JSX. Expect one node and no
   error.
3. A `.cjs` file with `const { helper } = require("./helper")` and a call
   to `helper()`. Expect the edge.
4. A `.min.js` file with a function on one line. Expect no node for it.
5. A `.test.js` file. Expect no node unless `IncludeTests` is set.

Run `go test ./internal/analyzer/ ./internal/lang/typescript/`.

## Validation

All steps must pass.

1. `go test ./...` and `npm --prefix web test` pass.
2. Start the server on `~/r/kaju`. The node count is higher than the count
   from "Before you start". The output of `GET /api/graph` has nodes with
   `"language":"javascript"` from `frontend/web/static/js/app.js` and
   `routine-reorder.js`. No node has a file in `frontend/web/static/vendor`.
   The two Rust crates are unchanged.
3. Start the server on this repository. The node count and the graph are
   unchanged, because `web/src` has only TypeScript.
4. Ask Claude Code to read `frontend/web/static/js/app.js` in `~/r/kaju`.
   The reticle finds a callable in it.
