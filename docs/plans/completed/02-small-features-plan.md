# Plan 02: Small improvements

This plan has four tasks. They come from items 3 to 6 of the audit of the
server. Each task has a problem, a result, a procedure, and tests. The tasks
are independent. Each task is one commit.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...` and make sure that all tests pass.
3. Run `npm --prefix web test` and make sure that all tests pass.

## Task 1: Do not analyze the folder again after the startup replay

### Problem

When the server opens a folder, it analyzes the folder. Then it replays the
newest session. The replay contains old edits. After a poll that finds an
edit to a source file, the server analyzes the folder again 2 seconds later
(`internal/server/view.go:310`):

```go
if mapped.Kind == "edit" && a.Supports(mapped.File) {
```

Thus, each start does a full analysis two times. The log of the server shows
`Analyzing ...` two times, with no new edits between them.

### Result

- At startup, the server analyzes the folder one time only.
- An edit that occurs after the analysis starts causes a new analysis.
  This includes an edit that occurs during the first analysis.

### Procedure

1. Open `internal/server/view.go`.
2. In `openRoot` (line 85), record the time before the analysis:
   `started := time.Now()`. Put it before `a.Analyze` (line 89).
3. Add the parameter `since time.Time` to `watch` (line 292).
4. In `openRoot`, give `started` to `watch`.
5. In `watch`, change the condition at line 310. Signal an edit only if
   `mapped.Time.After(since)`.
6. Change the comment of `watch`. Say that it ignores edits that occur
   before `since`, because the analysis already contains them.

### Tests

1. Open `internal/server/server_test.go`.
2. In `TestWatchAppendsEventsAndSignalsSourceEdits` (line 516), give
   `time.Time{}` to `watch`. Thus, the test does not change.
3. Add the test `TestWatchIgnoresEditsBeforeTheAnalysis`:
   - Write a transcript with an edit of `app.py` at `transcripttest.Time`.
   - Start `watch` with `since` set to `transcripttest.Time.Add(time.Second)`.
   - Wait until the log contains the edit (use `waitForLog`).
   - Wait 100 ms. Then make sure that the `edits` channel is empty.
   - Append an edit of `app.py` with a time after `since`. The helpers in
     `transcripttest` set `timestamp` to `transcripttest.Time`. Thus, set the
     `timestamp` key of the two lines to `transcripttest.Time.Add(time.Minute)`.
   - Make sure that the `edits` channel gets a value in 2 seconds.
4. Run `go test ./internal/server/`. Make sure that all tests pass.
5. Do a manual test:
   1. Build the server and start it on this repository.
   2. Look at the log of the server for 10 seconds.
   3. Make sure that `Analyzing` shows one time only.
   4. Ask Claude Code to edit a source file in the repository.
   5. Make sure that `Analyzing` shows again approximately 2 seconds later.

## Task 2: Count a piped file read only for the lines that the agent saw

### Problem

The Bash parser splits a command at `|` (`internal/transcript/bash.go:418`).
Then it counts `cat` and `nl` as reads of the full file (`bash.go:65-68`).
Thus, `nl -ba x.go | sed -n '10,20p'` marks all functions in `x.go` as read.
The agent saw only lines 10 to 20.

This occurs in approximately 5 of 270 file reads in local transcripts.

### Result

| Command | Lines that the parser counts |
|---|---|
| `cat f` | all lines |
| `cat f \|\| true` | all lines |
| `cat f \| head -5` | 1 to 5 |
| `nl -ba f \| sed -n '10,20p'` | 10 to 20 |
| `cat f \| tail -3` | the last 3 lines |
| `cat f \| grep x` | none |
| `cat f g \| head -5` | none |

### Procedure

1. Open `internal/transcript/bash.go`.
2. Add the type `shellCommand struct { words []string; piped bool }`.
   `piped` is true when the output of the command goes into a pipe.
3. Change `shellCommands` (line 335) to return `[]shellCommand`.
4. In `shellCommands`, change the case for `|` (line 418):
   - For `||`, skip the second `|` and end the command. Set `piped` to false.
   - For `|` and `|&`, end the command and set `piped` to true.
   - For `;`, `&`, `(`, and `)`, do not change the behavior.
5. In `bashReads` (line 37), use the index of each command.
6. For `cat`, `nl`, `less`, `more`, `bat`, and `batcat`, do these steps:
   1. If the command is not piped, count all lines. This is the current
      behavior.
   2. If the command is piped and names more than one file, count no lines.
   3. If the command is piped, find the next command. Remove its variable
      assignments, as `bashReads` does now.
   4. If the next command is `head`, use `lineOption` to get the range.
   5. If the next command is `tail`, use `tailOption` to get the range.
   6. If the next command is `sed`, use `sedPrintRanges` to get the range.
      If `sedPrintRanges` returns false, count no lines.
   7. For all other next commands, count no lines.
7. Do not change `head`, `tail`, `sed`, `rg`, or `grep` when they come before
   a pipe. The test case `sed -n 3,8p a | head -2` gives lines 3 to 8 now.
   This result counts a few lines too many. This is acceptable.

### Tests

1. Open `internal/transcript/bash_test.go`.
2. Add one row to the table in `TestBashReadsNameFilesAndLines` (line 37) for
   each command in the table of the result. Use `pkg/a.go` (30 lines) for `f`
   and `pkg/b.go` for `g`.
3. Make sure that all of the old rows stay the same.
4. Run `go test ./internal/transcript/`. Make sure that all tests pass.

## Task 3: Let the file route show only the files of the session

### Problem

The server listens only on `127.0.0.1`. It refuses other hosts and other
origins, thus web pages cannot read from it. But any process on the computer
can send requests to it, and the server does not ask for a login.

On a computer that has more than one user, another user can do these steps:

1. Send `curl -X POST 127.0.0.1:7777/api/root -d '{"path":"~"}'`. The server
   expands `~` to the home folder of the user who started the server.
2. Send `curl '127.0.0.1:7777/api/file?path=.ssh/id_ed25519'`.
   `serveFile` (`internal/server/server.go:255`) opens the folder with
   `os.OpenRoot(root)` and sends any file in it.

This is not a problem on a computer that has one user.

### Result

- `GET /api/file` sends a file only if the current view knows the file. The
  view knows a file if a graph node is in the file, or if an event of the
  current log names the file.
- For all other paths, `GET /api/file` sends status 404.
- The code pane shows each file that it shows now in the live view.

  CAUTION: When you replay an old session, the code pane cannot show the
  full text of a file that is not a source file. An example is `README.md`.
  The diff of the edit continues to show.

### Procedure

1. Open `internal/server/view.go`.
2. Add the field `files map[string]bool` to `rootView`. The lock `mu`
   protects it.
3. In `newView` (line 62), make the map.
4. In `setGraph` (line 136), add the `File` of each node to `files`. Keep the
   files that are in `files` already, because the log can name them.
5. In `appendEvents` (line 204), add the `Path` of each event that has one.
6. Add the method `knows(path string) bool`. It reads `files` with a read
   lock.
7. Open `internal/server/server.go`.
8. In `serveFile` (line 255), get the view one time: `view := s.current()`.
9. Before `os.OpenRoot`, call `view.knows(path)`. If it returns false, send
   status 404 with the text `no such file in the session`.
10. Do not change the other checks. `os.OpenRoot` continues to refuse `..`
    and symbolic links that go out of the folder.

### Tests

1. Open `internal/server/server_test.go`.
2. In `TestServeFileStaysInsideTheFolder` (line 74), give the view a graph
   with a node in `pkg/a.py`. `testView` sets an empty graph, thus call
   `view.setGraph` with a `callgraph.Graph` that has one `Function`. Set its
   `File` to `pkg/a.py`. The test continues to get the file.
3. Add a file `notes.txt` to the folder of the test. Make sure that
   `GET /api/file?path=notes.txt` gives status 404.
4. Append an edit event for `notes.txt` with `appendEvents`. Make sure that
   `GET /api/file?path=notes.txt` now sends the file.
5. Run `go test ./internal/server/`. Make sure that all tests pass.
6. Do a manual test:
   1. Start the server on this repository and open the page.
   2. Click a node. Make sure that the code pane shows its file.
   3. Make the file `probe.txt` in the root of the repository.
   4. Run `curl -s -o /dev/null -w '%{http_code}' '127.0.0.1:7777/api/file?path=probe.txt'`.
      Make sure that the result is `404`.
   5. Remove `probe.txt`.

### Out of scope

- A login or a token. This plan does not stop a local user who opens a
  folder and reads its source files. A token in the URL can stop this, but
  it changes how you open the page.

## Task 4: Read only the first lines to find generated files

### Problem

Each analysis looks for a "generated" mark in the first 10 lines of each
file (`internal/analyzer/analyzer.go:194`). But it splits the full file into
lines first:

```go
lines := bytes.Split(source, []byte("\n"))
```

Thus, each analysis makes one slice entry for each line of each file. Then
the language analyzer reads each file again.

### Result

- `isGenerated` splits only the first 10 lines.
- The analysis finds the same files as before.

### Procedure

1. Open `internal/analyzer/analyzer.go`.
2. In `isGenerated` (line 194), replace `bytes.Split(source, []byte("\n"))`
   with `bytes.SplitN(source, []byte("\n"), 11)`.
3. Keep the line `if len(lines) > 10 { lines = lines[:10] }`. The eleventh
   part of `SplitN` contains the remaining text of the file.

### Tests

1. Open `internal/analyzer/analyzer_test.go`. It is in the package
   `analyzer_test`, thus it cannot call `isGenerated`. Test it through
   `Analyze`.
2. Find the test that writes `gen.one` (line 89). Add two fixture files:
   - A file with the mark on line 10. Make sure that `Analyze` skips it.
   - A file with the mark on line 11. Make sure that `Analyze` keeps it.
3. Run `go test ./internal/analyzer/`. Make sure that all tests pass.
4. Run `go test ./...`. Make sure that all tests pass.

## Done when

- All four tasks are complete and each task is one commit.
- `go vet ./...`, `go test ./...`, and `npm --prefix web test` pass.
- The manual tests of Task 1 and Task 3 give the results in this plan.
