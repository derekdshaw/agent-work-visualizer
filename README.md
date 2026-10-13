# Agent Work Visualizer

`agent-work-visualizer` is a local web server that shows the work of a Claude
Code agent on the call graph of a Python, Go, Java, Rust, or TypeScript source
tree while the agent runs. A tree may contain any combination of the supported
languages. The server and its page need no language runtime, build tool,
browser extension, or network access.

## Build and run

Go 1.24 or newer and a C compiler are required to build the Tree-sitter
bindings.

```sh
go test ./...
go build -o bin/agent-work-visualizer .
bin/agent-work-visualizer /path/to/project
```

```text
agent-work-visualizer [ROOT] [--port N]
```

Open `http://127.0.0.1:7777` in a browser on the same computer. Options may
appear before or after ROOT. See [Live agent view](#live-agent-view) for what
the server follows and shows.

Virtual environments, caches, build outputs, and files with recognized
generated-code markers are excluded from the graph. Tests are always included,
because an agent reads and edits tests.

## Layout

```text
main.go                          CLI: parse arguments, register languages, run the server
internal/callgraph               graph model shared by every package
internal/analyzer                file discovery, language routing, graph merge, single-file outlines
internal/lang/<language>         one Tree-sitter analyzer per language (python, golang, java, rust, typescript)
internal/lang/langutil           parsing and progress helpers shared by the analyzers
internal/transcript              follows Claude Code session transcripts and parses tool results
internal/activity                attributes tool events to the callables they read or changed
internal/server                  HTTP routes, event stream, pause gate, folder picker, viewer page
```

## Analysis model

The analyzer uses the official Go Tree-sitter binding with the Python, Go,
Java, Rust, and TypeScript grammars. It collects definitions and imports before
resolving calls.

Python analysis recognizes:

- same-module and nested functions;
- imported, aliased, and module-qualified functions;
- `self` and `cls` method calls;
- `__main__`-guarded entrypoints;
- Metaflow `@step` entrypoints and `self.next(...)` transitions.

Go analysis recognizes:

- `.go` files, packages, generic functions, and value or pointer receiver methods;
- standard, aliased, and dot imports, with local packages matched through the
  nearest `go.mod` module path;
- same-package calls, calls into imported local packages, receiver calls, and
  method expressions when the source identifies one target;
- top-level `main` functions in `package main` and package `init` functions as
  entrypoints;
- Go 1.26 `new(expression)` calls, which the Tree-sitter Go grammar does not
  parse yet.

Java analysis recognizes:

- packages, explicit imports, wildcard imports, and static imports;
- methods, overloaded methods, constructors, compact record constructors, and
  annotation members;
- local, field, static, inherited, and constructor calls when the source makes
  one target uniquely identifiable;
- calls inside lambdas as calls from the enclosing method;
- `public static void main(String[])` entrypoints.

Rust analysis recognizes:

- `.rs` files, nested modules, free and nested functions, trait methods, and
  implementation methods;
- simple, aliased, and grouped `use` declarations;
- same-module calls, imported calls, associated functions, and `self`/`Self`
  method calls;
- top-level `main` entrypoints.

TypeScript analysis recognizes:

- `.ts`, `.tsx`, `.mts`, and `.cts` files;
- named, aliased, namespace, and default imports;
- function declarations, class methods and constructors, and functions assigned
  to variables;
- same-module and imported calls, static and `this` method calls, constructor
  calls, async functions, and TSX syntax.

Purple dispatch edges connect source-defined Java overrides to their resolved
base methods.

Repeated calls produce one edge with a count and distinct source lines. Calls
are never resolved across languages. Dynamic dispatch, callbacks, reflection,
monkeypatching, dependency injection, calls into external packages, and
ambiguous Java overloads remain explicitly unresolved. Java analysis does not
read Maven, Gradle, JDK, bytecode, or classpath metadata. Rust analysis does not
expand macros or perform type inference. TypeScript analysis does not use a
`tsconfig.json` or TypeScript type checker. Go analysis does not apply build
constraints or perform type inference, so calls through interface or variable
values remain unresolved.

Analysis is fail-closed. An unreadable file or a Tree-sitter `ERROR` or missing
node stops the analysis with a path, line, and column. The server does not
start, or keeps the previous graph, until every source file parses.

## Viewer

The page embeds graph data, Cytoscape.js 3.33.1, and 3d-force-graph 1.79.0.
The 3D view progressively expands around entrypoints and selected callables.
Search shows matching callables and their direct callers without expanding to
higher callers. A keyboard-accessible result list shows each match's source
location and direct-caller count; Up/Down and Enter select a result. The Modules
view lists every analyzed source module and every disconnected callable graph
component; selecting either opens its complete internal 3D graph. Selecting a
callable opens a focused 2D caller/callee view whose depth can reach the farthest
callable in its connected component. The
context toolbar's back button returns through each prior node selection. The
Labels toggle draws collision-managed compact callable names beside 3D nodes.
In the Metaflow-steps filter, depth follows only forward step transitions and
shows each included step's direct function dependencies without recursively
expanding those dependencies. Its upper depth limit is the final reachable
Metaflow step; other filters use the selected callable's connected component. The Unresolved tab
groups call sites into collapsed language-runtime/built-in, external/imported
API, and dynamic/unknown sections, with counts by expression and exact
`file:line`.
Ctrl-drag draws a rectangle and zooms to the enclosed nodes. Ctrl-click without
dragging resets the camera; wheel and pinch zoom remain available.
The circular help button at the toolbar's far-right edge opens a modal with
navigation, depth, split-pane, source-view, and visual-key guidance.

Node colors identify source directories. The expandable upper-left key lists
colors for the directories currently visible and distinguishes ordinary calls,
Metaflow transitions, and larger entrypoint nodes. Directory colors come from a
fixed 25-color perceptually separated palette and are never reused;
directories beyond the palette capacity in the active rendering use the key's
neutral overflow color.

Clicking a 3D node isolates it with its direct callers and callees. Isolation
adds 3D and 2D tabs; the 2D tab fills the visualization area and roots its
caller/callee layout at the selected node. Toolbar down/up arrows decrease or
increase the shared connected-component depth in either tab. Both tabs use the dark
theme. The key stays available and only shows node directories and relationship types
present in the active rendering. The isolated 3D nodes are automatically fit
to the resized viewport with 20 pixels of padding. Reset view exits isolation.
The 2D hierarchy assigns levels by hop distance regardless of call direction;
edge arrows still show caller-to-callee direction.
The selected node is bright white and substantially larger in 3D, and has a
bright outline in 2D.
Selecting another node in either view makes it the isolation root and first
call-stack row. The stack expands and contracts with the shared graph depth.

Selecting a node also opens a right-side call-stack split pane, reducing the
active graph viewport instead of covering it. A side-panel icon at the
toolbar's far-right edge, directly above the pane, collapses or expands it.
Dragging the pane's left divider resizes it; a focused divider also supports
Left/Right arrow keys. The table follows outgoing calls to the selected graph
depth and marks cycles. Its columns are Callable, Calls, and Location.
Selecting a row shows the embedded source for that callable; the back arrow
returns to the table. The pane is temporarily hidden in the Unresolved view and
restored when returning to the graph.

Renderer license notices are included in [LICENSE](LICENSE).

## Live agent view

The server shows the work of a Claude Code agent on the call graph while the
agent runs. It reads the session transcripts that Claude Code writes
under `~/.claude/projects`. If you set `CLAUDE_CONFIG_DIR` for Claude Code, set
the same value for the server. It then reads `$CLAUDE_CONFIG_DIR/projects`.

ROOT is the folder that the server follows. ROOT is optional. Without ROOT,
the page opens the folder picker and shows no graph until you choose a folder.

The graph always includes test files, because an agent reads and edits tests.
If a test file does not parse, the server does not start. This also applies to
Go `testdata` files.

Open `http://127.0.0.1:7777` in a browser on the same computer. The server
listens on the loopback interface only, because the page contains the source
of every callable. It also rejects a request whose Host header is not
`127.0.0.1` or `localhost`.

The server follows sessions whose working directory is ROOT, a directory inside
ROOT, or a parent of ROOT. At startup, it replays the newest session in ROOT or
in a directory inside ROOT, together with its subagents. If no such session
exists, it replays the newest session in a parent of ROOT. It follows sessions
that start later from their first line.
The page shows only Read, Edit, and Write calls on files under ROOT.

Each call marks the callables that contain its lines. A Read marks the lines
that the agent read. An Edit or Write marks the changed lines, and a Write that
creates a file marks every line. The server moves line numbers forward through
later edits of the same file, so an earlier read stays on the correct callable.

The page has these live views:

- The 3D view shows each callable that the agent touched, with up to 12 direct
  callers and callees. Yellow is the latest step, orange is edited, blue is
  read only, and gray is not touched.
- A green trail joins the last 30 steps of each agent in the 3D view.
- Layers shows one node for each source directory, colored by agent activity.
  Select a directory to open its callables.
- Activity lists every call, newest first, with the time, agent, tool,
  callables, and `file:line`. Select a callable to center it with its direct
  callers and callees in the 3D view.
- The timeline steps back, plays, and steps forward through the agent steps.
  The graph, the call stack, the changes, and the task panel show the state at
  the selected step. Live returns to the latest step.
- Follow adds the callables of the latest step to the graph. If no callable,
  scope, or search is selected, the camera centers the latest step. It zooms
  until the step and its nearby callers and callees fill about half of the
  view.
- The agent task panel shows the session title, the latest request, the latest
  tool call, and the subagents that work at that time.
- In the call stack, select an edited callable to show its changes in this
  session as an inline diff above its current source.

### Choose the folder

The folder button at the right end of the second toolbar row shows the folder
that the server follows. Select it to open the folder picker. The picker lists
these folders:

- The working directories of Claude Code sessions from the last 7 days, newest
  first.
- The same folder in each git worktree of the repository that contains the
  current folder. If a worktree does not have that folder, the picker lists the
  worktree. Without a current folder, the picker uses the directory where the
  server started.

You can also type a path. A path that starts with `~` starts in your home
directory. The server analyzes the new folder before it changes to it. Then
every open page reloads with the graph and history of the new folder. If the
analysis fails, the server keeps the current folder and the picker shows the
error. A pause stays in effect after the change, and the gate then holds only
the sessions of the new folder. Resume agent also releases the tool calls that
the gate holds for a previous folder.

### Pause the agent

Pause agent holds the next tool call of each followed session until you select
Resume agent. This needs a PreToolUse hook, which is a command that Claude Code
runs before each tool call. The hook sends the call to the `/gate` endpoint.
Add this entry to the `hooks.PreToolUse` list in `~/.claude/settings.json`:

```json
{
  "matcher": "*",
  "hooks": [
    {
      "type": "command",
      "command": "curl -sf --max-time 3300 -H 'Content-Type: application/json' --data-binary @- http://127.0.0.1:7777/gate || true",
      "timeout": 3600
    }
  ]
}
```

If you use `--port`, change 7777 in the URL. The gate holds a call for at most
50 minutes and then denies it. The curl `--max-time` value must be longer than
this limit, and the hook `timeout` must be longer than `--max-time`. Every
session runs the hook, but the gate holds only the sessions that the server
follows. If the server does not run, curl fails and the call runs. A pause does
not stop a tool call that already runs, or text that the agent writes.

Two seconds after the last edit to a supported source file, the server analyzes
ROOT again and the page loads the new graph. If the analysis fails, the page
keeps the previous graph and shows the error. If the server restarts, an open
page reloads itself.

The server does not see changes that shell commands make, for example `sed -i`,
formatters, code generation, or `git checkout`. Replayed calls from before such
a change can show the wrong callable. The transcript format is internal to
Claude Code and can change between versions.

## Fidelity

The regression fixtures cover Python 3.12 generic type parameters, sync and
async functions, methods, decorators, nested functions, lambdas,
comprehensions, match statements, exception groups, import forms, Metaflow
transitions, syntax rejection, deterministic ordering, and HTML escaping.
Java fixtures cover packages, imports, overloads, constructors, inheritance,
records, lambdas, entrypoints, exclusions, unresolved calls, mixed-language
trees, and syntax rejection.
Rust fixtures cover imports, aliases, nested modules, free and implementation
methods, entrypoints, exclusions, call resolution, and syntax rejection.
TypeScript fixtures cover named and default imports, declarations, assigned
functions, classes, constructors, async functions, TSX, exclusions, call
resolution, and syntax rejection.
Go fixtures cover generic functions, receiver methods, standard, aliased, and
dot imports, package-local call resolution, entrypoints, exclusions, unresolved
external calls, and syntax rejection.
