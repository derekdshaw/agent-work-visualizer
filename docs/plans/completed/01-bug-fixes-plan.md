# Plan 01: Bug fixes

This plan has two tasks. Each task has a problem, a result, a procedure, and
tests. Do the tasks in sequence. Each task is one commit.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...` and make sure that all tests pass.
3. Run `npm --prefix web test` and make sure that all tests pass.

## Task 1: Show each session one time in the session picker

### Problem

The server makes a new session log each time that it starts. The server also
makes a new session log each time that you open a folder. Each new log replays
the newest transcript from its start. Thus, many logs contain the same session.

`List` in `internal/session/session.go:314` shows all of these copies. On one
computer, 25 of 54 logs contained the same session. The session picker shows
25 entries that are almost the same.

### Result

The session picker shows one entry for each session. The entry is the newest
copy of the session. The newest copy replays the same transcript from its
start, thus it has all the events of the older copies.

The page hides the log of the live view and shows `LIVE` for it
(`web/src/main.ts:356`). Thus, the picker shows no copy of the live session.

### Procedure

1. Open `internal/session/session.go`.
2. Find the function `List` (line 314).
3. Before the loop over `names`, add a set of the sessions that `List` keeps:
   `seen := make(map[sessionKey]bool)`.
4. Add the type `sessionKey struct { root string; t0 int64 }`.
   Use `T0.UnixNano()` for `t0`. Do not use `time.Time` as a map key, because
   two equal times can have different locations.
5. In the loop, after `summarize` returns a summary for `root`, make the key.
6. If `seen` contains the key, go to the next name.
7. If `seen` does not contain the key, add the key and append the summary.

   NOTE: `names` is in the sequence newest first. Thus, the first summary of a
   key is the newest copy.

8. Make sure that `limit` counts only the summaries that `List` keeps.
9. Change the comment of `List`. Say that it keeps only the newest log of
   each root and start time (`T0`).

### Tests

1. Open `internal/session/session_test.go`.
2. Change `TestListAndRead` (line 174). The logs "First" and "Third" have the
   same `T0` (`start`). Give "First" a different start time, for example
   `start.Add(-time.Hour)`. Change the `T0` in `want` to agree.
3. Add the test `TestListKeepsTheNewestCopyOfEachSession`:
   - Write three logs for `/repo`: `20260101-000000-a` and `20260102-000000-b`
     with the same `T0`, and `20260103-000000-c` with a different `T0`.
   - Call `List(dir, "/repo", 10)`.
   - Make sure that the result has two summaries, in this sequence:
     `20260103-000000-c`, then `20260102-000000-b`.
   - Call `List(dir, "/repo", 1)`. Make sure that the result has only
     `20260103-000000-c`.
4. Run `go test ./internal/session/`. Make sure that all tests pass.
5. Do a manual test:
   1. Build the server: `npm --prefix web run build`, then
      `go build -o bin/agent-work-visualizer .`.
   2. Start the server on this repository two times. Stop it after each start.
   3. Start the server one more time and open `http://127.0.0.1:7777`.
   4. Open the session picker.
   5. Make sure that each session shows one time only.

### Out of scope

- Remove the duplicate log files from the disk. The logs use approximately
  5 MB, thus this is not necessary now.
- Stop the server from making the copies.

## Task 2: Remove the graph data that only the old report used

### Problem

The static HTML report is no longer in the project. But the language
analyzers and `internal/analyzer` still make data for it. The server does not
use this data. `slimGraph` in `internal/server/graph.go:43` reads only these
fields of a `Function`: `ID`, `Name`, `QualifiedName`, `Directory`, `Kind`,
`Language`, `File`, `Line`, and `EndLine`. It reads only `Source`, `Target`,
and `Kind` of an `Edge`.

This unused data has costs:

- Each analysis copies the source text of each function into `Source`. Thus,
  each graph refresh keeps one more copy of all the code in memory.
- `finish` in `internal/analyzer/analyzer.go:219` sorts the unresolved calls.
  For each comparison, it makes two strings with `fmt.Sprintf`.
- Each language has a function that puts unresolved calls into categories.
  No part of the server reads the categories.
- Tests in five languages examine this data. You must keep these tests
  correct when you change the code.

### Result

- The graph has only the data that the server and the call resolution use.
- `GET /api/graph` gives the same nodes and the same edges as before the
  change.
- Approximately 150 lines of code are removed.

### Fields to remove and fields to keep

The call resolution reads some fields. You must keep these fields. A probe
build found the fields that the code reads.

| Type | Remove | Keep |
|---|---|---|
| `Graph` | `Files`, `Unresolved`, `Stats` | `Root`, `Nodes`, `Edges` |
| `Function` | `Column`, `Signature`, `Source`, `Async`, `Parameters`, `Entrypoint`, `EntryKind` | `ID`, `Name`, `QualifiedName`, `Module`, `File`, `Directory`, `Line`, `EndLine`, `Language`, `Kind`, `Class`, `Parent`, `Decorators`, `Modifiers` |
| `Edge` | `Count`, `Lines` | `Source`, `Target`, `Kind` |
| Types | `File`, `UnresolvedCall`, `Stats` | |

These fields are necessary for the call resolution:

- `Class`, `Parent`, and `Module`: Python (`python.go:439`, `:510`), Rust
  (`rust.go:441`), and TypeScript (`typescript.go:398`).
- `Modifiers`: TypeScript (`typescript.go:264`, `:425`).
- `Decorators`: Java uses `@Override` to find dispatch edges
  (`java.go:963`).

### Procedure

1. Open `internal/callgraph/callgraph.go`.
2. Remove the fields and the types in the "Remove" column of the table.
3. Change the package comment. Remove "and the report renderer".
4. Run `go build ./...`. The compiler shows each line that uses a removed
   field.
5. For each error in `internal/lang/*`, do one of these steps:
   - If the line sets a removed field in a struct literal, remove the line.
   - If the line adds to `a.graph.Files`, `a.graph.Stats`, or
     `a.graph.Unresolved`, remove the line.
   - If the line adds to `Count` or `Lines` of an edge that exists, remove
     only that line. Keep the check that finds the edge that exists. This
     check prevents duplicate edges.
6. Remove each function that only makes the removed data:
   - `goUnresolvedCategory` (`golang.go:531`)
   - `javaUnresolvedCategory` (`java.go:905`)
   - `unresolvedCategory` (`python.go:486`)
   - `typescriptUnresolvedCategory` (`typescript.go:452`)
   - `rustUnresolvedCategory` (`rust.go:478`)
   - `markFlowEntrypoints` (`python.go:557`) and the `if` block that calls it
     (`python.go:537-539`). Then use `rg flowClasses internal/lang/python` to
     find other uses of `flowClasses`. If no other code reads it, remove it.
   - `goParameters` (`golang.go:424`), `typescriptParameters`
     (`typescript.go:285`), and `rustParameters` (`rust.go:324`)
   - `rustModifiers` (`rust.go:314`)
7. Do not remove `javaParameters` or `javaModifiers`. The Java analyzer uses
   their results for overloads and records (`java.go:233`, `:240`, `:283`).
   Do not remove `typescriptModifiers`, because TypeScript reads `Modifiers`.
8. In Python, remove the code that sets `Entrypoint` for a `__main__` guard
   (`python.go:456-461`). Then use `rg mainGuard internal/lang/python` to
   find other uses of `mainGuard`. If no other code reads it, remove it.
9. In `internal/analyzer/analyzer.go`:
   - In `Analyze`, remove `Unresolved` from the `Graph` literal (line 106).
   - In `merge`, keep only the lines for `Nodes` and `Edges`.
   - In `finish`, keep only the sorts of `Nodes` and `Edges`.
10. Run `go build ./...` again. Do steps 5 to 9 again until no errors occur.
11. Run `go vet ./...`. Make sure that it shows no problems.
12. Run `gofmt -l .`. Make sure that it shows no files.

### Tests

1. Run `go test ./...`. Tests that examine a removed field do not compile.
2. For each test that does not compile, remove only the part that examines a
   removed field. Keep the parts that examine nodes, edges, and qualified
   names. Approximately 50 lines in `internal/lang/*/*_test.go` and
   `internal/analyzer/analyzer_test.go` use these fields.
3. If a test examines only a removed field, remove the test.
4. Run `go test ./...` again. Make sure that all tests pass.
5. Make sure that `TestOutlineMatchesAnalyzeKeys` in
   `internal/analyzer/analyzer_test.go:151` passes. This test makes sure that
   the keys of the outline agree with the keys of the graph.
6. Compare the graph before and after the change:
   1. Build the server from `main` to `/tmp/awv-before`.
   2. Build the server from the new branch to `/tmp/awv-after`.
   3. Start each server on the same folder, on different ports:
      `/tmp/awv-before <folder> --port 7801` and
      `/tmp/awv-after <folder> --port 7802`.
   4. For each server, get the graph:
      `curl -s http://127.0.0.1:<port>/api/graph | jq -S '{nodes, edges}'`.
      Write each result to a file.
   5. Run `diff` on the two files. Make sure that it shows no difference.
   6. Do this comparison on this repository (Go and TypeScript). Also do it on
      a Rust repository and on a Python repository if you have them.

### Out of scope

- Change the call resolution rules.
- Remove the `Decorators` values that the Python analyzer sets. The field
  stays for Java, and these values do not cost much.

## Done when

- Both tasks are complete and each task is one commit.
- `go vet ./...`, `go test ./...`, and `npm --prefix web test` pass.
- The manual tests of Task 1 and Task 2 give the results in this plan.
