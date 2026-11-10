# Plan 08: Package of each callable

This plan adds a `package` field to each node of `GET /api/graph`. The 2D
view uses it for its outer cards. In a workspace, such as a Rust workspace
with many crates, a package is a member of the workspace. In other trees, a
package is a directory, as `module` is now.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...` and make sure that all tests pass.

## Task 1: Find the package of each file

### Problem

`module` is the directory of a callable (`internal/server/graph.go:56`). In
a Rust workspace, one crate has `src` and `tests` directories, so one crate
becomes many modules. A rule of "the nearest manifest" is also wrong: this
repository has `go.mod` at the root, so all Go code would be in one package.

### Result

`internal/analyzer` exports `Packages(root string, files []string) map[string]string`.
It maps each file path (relative to `root`) to its package path. The rule:

1. These files are manifests: `Cargo.toml`, `package.json`, `go.mod`,
   `pyproject.toml`, `setup.py`, `pom.xml`, `build.gradle`, and
   `build.gradle.kts`. Two manifests have the same kind if they have the
   same file name. `build.gradle` and `build.gradle.kts` are one kind.
2. Find the manifests in the directories that contain the files and in
   their parent directories, up to `root`. Do not count a manifest in
   `root` itself. For each directory that holds a manifest, also look in
   the other subdirectories of its parent directory. A member of a
   workspace can hold no source file, so the files alone do not show it.
3. A kind is a workspace kind if two or more subdirectories of one
   directory hold a manifest of that kind. The package of a file is the
   nearest directory above it that holds a manifest of any workspace kind.
   A file with no such directory above it uses its own directory.
4. A tree can have more than one workspace kind, for example crates below
   `crates/` and npm packages below `packages/`. Each file gets the package
   that is nearest to it. No kind wins over another kind.
5. If there is no workspace kind, the package of a file is its directory,
   or `.` for a file in `root`.

### Procedure

1. Add `Packages` to `internal/analyzer/analyzer.go`, or to a new file
   `internal/analyzer/packages.go`.
2. Check each directory one time only. Keep a map of the directories that
   you checked.
3. Use `os.Stat` on each manifest name. Do not walk the full tree, so a
   large `node_modules` or `target` directory costs nothing.
4. For the other subdirectories of a parent, use one `os.ReadDir` of that
   parent and `os.Stat` in each subdirectory. Read each parent one time
   only for each kind.

### Tests

1. Make `internal/analyzer/packages_test.go`. Use `t.TempDir()` for each
   case.
2. Add a table test with these cases:

| Case | Files and manifests | Expected packages |
| --- | --- | --- |
| Go repo with a web folder | `go.mod`, `web/package.json`, `main.go`, `internal/a/a.go`, `web/src/x.ts` | `.`, `internal/a`, `web/src` |
| One crate | `Cargo.toml`, `src/main.rs`, `src/parser/pack.rs`, `tests/parse.rs` | `src`, `src/parser`, `tests` |
| Rust workspace | `Cargo.toml`, `crates/a/Cargo.toml`, `crates/b/Cargo.toml`, `crates/a/src/lib.rs`, `crates/a/tests/t.rs`, `crates/b/src/x/y.rs` | `crates/a`, `crates/a`, `crates/b` |
| npm workspace | `package.json`, `packages/ui/package.json`, `packages/api/package.json`, `packages/ui/src/a.ts`, `scripts/b.ts` | `packages/ui`, `scripts` |
| Two workspace kinds | `Cargo.toml`, `crates/a/Cargo.toml`, `crates/b/Cargo.toml`, `packages/ui/package.json`, `packages/api/package.json`, `crates/a/src/lib.rs`, `crates/b/src/x/y.rs`, `packages/ui/src/a.ts`, `packages/api/index.ts` | `crates/a`, `crates/b`, `packages/ui`, `packages/api` |
| Crates below a subdirectory | `backend/Cargo.toml`, `backend/crates/kaju-api/Cargo.toml`, `backend/crates/kaju-web/Cargo.toml`, `backend/crates/kaju-api/src/config.rs`, `backend/crates/kaju-api/src/routes/auth.rs`, `backend/crates/kaju-web/src/routes/routines.rs`, `backend/migrations/init.rs`, `frontend/web/static/js/app.js` | `backend/crates/kaju-api`, `backend/crates/kaju-api`, `backend/crates/kaju-web`, `backend`, `frontend/web/static/js` |
| No manifests | `a/b.py`, `c.py` | `a`, `.` |

3. Run `go test ./internal/analyzer/`.

## Task 2: Send the package with each node

### Result

- `graphNode` (`internal/server/graph.go:21`) has
  `Package string \`json:"package"\``.
- `slimGraph` sets it from `Packages`.
- `GraphNode` in `web/src/graph/load.ts` has `package: string`. The parser
  uses `module` if `package` is missing, so an old log or server still
  works.

### Procedure

1. Change `slimGraph` (`internal/server/graph.go:43`) to take the root
   path. Collect the file paths of the nodes. Call `Packages` one time.
2. Change the call in `internal/server/view.go:137` and the calls in
   `internal/server/server_test.go` (lines 126 and 136).
3. Add the field to `GraphNode` and to the parser in
   `web/src/graph/load.ts`.
4. In `README.md`, in the section that describes `GET /api/graph` or the
   graph, add one sentence about `package`.

### Tests

1. In `internal/server/server_test.go`, add a test of `slimGraph` on a
   temporary Rust workspace. Make sure that each node has the package of its
   crate.
2. In `web/src/graph/load.test.ts`, add a test for a payload without
   `package`. Expect `package` to equal `module`.
3. Run `go test ./...` and `npm --prefix web test`.

## Validation

All steps must pass.

1. `go test ./...` and `npm --prefix web test` pass.
2. Start the server on this repository. Run
   `curl -s http://127.0.0.1:7777/api/graph | head -c 2000`. Each node has a
   `package` field. For `internal/server/view.go`, the package is
   `internal/server`.
3. Start the server on a Rust workspace with two or more crates under
   `crates/`. If you have none, make one with `cargo new --lib crates/a`,
   `cargo new --lib crates/b`, and a root `Cargo.toml` that lists them as
   members. The package of each node is its crate directory, for example
   `crates/a`.
4. The 3D view shows no change. It still groups by `module`.
