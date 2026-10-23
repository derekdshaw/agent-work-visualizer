# Agent Work Visualizer

`agent-work-visualizer` is a local web server that shows a Claude Code agent
working on a code base in real time. The page draws the call graph of a
Python, Go, Java, Rust, or TypeScript tree in 3D, locks a reticle onto the
callable that the agent edits, lights its dependents hop by hop, and types the
edit's diff into a code pane. Every session is recorded as a JSONL event log
that replays with a scrubber. The server and the page need no network access.

## Build and run

Building needs Go 1.24 or newer, a C compiler for the Tree-sitter bindings,
and Node 22.18 or newer for the web build.

> If your `package-lock.json` and `package.json` are out of sync do the following first before building.

```sh
npm --prefix web install --package-lock-only
```
Normal build process is as follows. The `ci` build syncs `node_modules` to what the `package-lock.json` specifies.

```sh
npm --prefix web ci
npm --prefix web run build      # writes internal/server/web/dist, which go build embeds
go build -o bin/agent-work-visualizer .
bin/agent-work-visualizer /path/to/project
```

```text
agent-work-visualizer [ROOT] [--port N]
```

Open `http://127.0.0.1:7777`. Options may appear before or after ROOT, and
ROOT is optional: without it, the page starts with no graph and you open a
working tree from the title bar. A binary built without the web build serves a
page that explains the build steps.

Virtual environments, caches, build outputs, and files with recognized
generated-code markers are excluded from the graph. Tests are always included,
because an agent reads and edits tests.

## Develop

Run the server and the Vite dev server side by side, then open
`http://localhost:5173`. Vite forwards `/api`, `/events`, and `/gate` to the
server on port 7777.

```sh
go run . /path/to/project
npm --prefix web run dev
```

```sh
go test ./...
npm --prefix web test           # node --test: reducer, hops, layout, log parsing
npm --prefix web run typecheck
```

## Layout

```text
main.go                    CLI: parse arguments, register languages, run the server
internal/callgraph         graph model shared by every package
internal/analyzer          file discovery, language routing, graph merge, single-file outlines
internal/lang/<language>   one Tree-sitter analyzer per language (python, golang, java, rust, typescript)
internal/lang/langutil     parsing and progress helpers shared by the analyzers
internal/transcript        follows Claude Code session transcripts and parses tool calls and results
internal/activity          attributes tool events to the callables they read or changed
internal/session           the JSONL session log: event schema, timeline, stored logs
internal/server            HTTP routes, event stream, pause gate, working trees, embedded page
web/src/session            event types and the pure fold(events, t) reducer
web/src/graph              graph loading, hop distances, force layout (in a worker)
web/src/view               three.js scene, camera, reticle
web/src/ui                 stream, blast radius, code pane, scrubber, panes, working tree
```

## The page

- **Graph.** Each node is a callable, grouped into clusters by directory,
  and within a directory into sub-clusters by file. A ring under each
  directory's cluster carries its name; file names appear for the directory
  under the pointer and the one the agent edits.
  Drag to orbit, right-drag, Shift-drag, or drag two fingers to pan, scroll
  or pinch to zoom, click a node for its details. Reset view also clears the pan.
  Auto-rotate pauses while you interact, and follow mode eases the camera
  toward the active node. Glow is additive sprites with fog for depth.
- **Reticle.** A read moves the reticle onto the callable that the agent read
  (ACQUIRING). An edit makes it scan the target's busiest neighbors, then lock
  on and shrink onto the target. Its color follows the phase: coral while
  editing, cyan while a command runs, lime once it passes.
- **Blast radius.** The edit target glows first, its direct callers and callees
  half a second after the diff starts, and nodes two hops out a second later.
  Edges light by the dimmer of their two ends, and pulses travel outward from
  the target. Every callable edited earlier in the session keeps a faint trace.
- **Agent stream.** One row per event: requests, the agent's plans, reads,
  edits, tool calls, subagents, and commands with their result. Subagent rows
  carry the subagent's ID.
- **Code pane.** Tabs for the files edited so far, the full path of the current
  file, and the edit's diff typed out line by line. The status bar shows the
  latest command and the phase.
- **Scrubber.** Coral bursts mark edits. Drag to any time; everything on the
  page is computed from the events up to that time, so scrubbing and replay
  need no other state. The speed button cycles 1×, 2×, 4×, 8×, and 16×
  (Shift-click goes back down) and is remembered per browser. The previous and
  next buttons jump to the start of an edit, so its lock-on and diff play from
  the beginning. LIVE returns to the live edge.
- **Session.** The title is Claude Code's session title. The picker replays an
  earlier session log of the same working tree.
- **Working tree.** Type or pick a path and select OPEN. The server indexes it
  without a restart; on failure the page keeps the current graph and shows the
  error. Recent paths, Claude Code session folders from the last 7 days, and the
  same folder in each git worktree are offered. A chip appears when an agent
  works in another worktree of the same repository.
- **Panels.** Drag either divider, or focus it and use Left/Right (Shift for
  larger steps); Home or a double click resets it. Widths persist. Below 980 px
  the panels stack under the graph.
- `prefers-reduced-motion` turns off auto-rotate, ripples, and the cursor blink.

A transcript records a tool call only once it has finished, so the page cannot
show the model's text while it is generated. The diff therefore types out the
real edit text at a fixed rate, about 90 characters a second, after the reticle
locks on.

## Session logs

The server writes one log per opened working tree to
`$XDG_STATE_HOME/agent-work-visualizer/sessions/` (default
`~/.local/state/agent-work-visualizer/sessions/`). The first line is a header,
and each later line an event:

```text
{"t":0,"type":"session","root":"/repo","branch":"main","t0":"2026-10-13T21:00:00Z","gapCap":30}
{"t":4.2,"at":"…","type":"read","agent":"main","path":"pkg/a.go","line":10,"nodeIds":["pkg/a.go\u0000pkg.Run"]}
{"t":6.0,"at":"…","type":"edit","agent":"main","path":"pkg/a.go","nodeIds":["pkg/a.go\u0000pkg.Run"],"hunks":[…]}
```

Event types are `title`, `prompt`, `plan`, `step`, `agent`, `read`, `edit`,
`check_start`, and `check_end`. Paths are relative to the root, and node IDs
are the file and qualified name of a callable, which survive line shifts. `t`
counts seconds on the session timeline; an idle gap counts at most `gapCap`
seconds, so an agent that waits an hour for you, or a session replayed from
yesterday, does not leave the timeline empty. The page streams the log over
server-sent events with the line number as the event ID, so a reconnecting page
resumes where it stopped. Each server start replays the newest session of the
tree into a new log, so the picker can list near-duplicate logs.

## Live agent view

The server reads the session transcripts that Claude Code writes under
`~/.claude/projects`. If you set `CLAUDE_CONFIG_DIR` for Claude Code, set the
same value for the server. It then reads `$CLAUDE_CONFIG_DIR/projects`.

The server follows sessions whose working directory is ROOT, a directory inside
ROOT, or a parent of ROOT. At startup, it replays the newest session in ROOT or
in a directory inside ROOT, together with its subagents. If no such session
exists, it replays the newest session in a parent of ROOT. It follows sessions
that start later from their first line. Reads and edits count only for files
under ROOT.

Each Read, Edit, or Write marks the callables that contain its lines. A Read
marks the lines that the agent read, an Edit or Write the changed lines, and a
Write that creates a file every line. The server moves line numbers forward
through later edits of the same file, so an earlier read stays on the correct
callable. Two seconds after the last edit to a supported source file, the server
analyzes ROOT again and the page redraws the graph without moving the nodes it
already placed. If the analysis fails, the page keeps the previous graph and
shows the error.

The server listens on the loopback interface only, because the session logs
contain the source that the agent changed. It rejects a request whose Host
header is not `127.0.0.1` or `localhost`, and a POST from another origin.

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

### Pause the agent

Pause agent holds the next tool call of each followed session until you resume. This needs a PreToolUse hook, which is a command that Claude Code
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

Select PAUSE AGENT in the title bar to pause and RESUME AGENT to resume; the
button shows how many calls the gate holds. If you use `--port`, change 7777 in
the URL. The gate holds a call for at most
50 minutes and then denies it. The curl `--max-time` value must be longer than
this limit, and the hook `timeout` must be longer than `--max-time`. Every
session runs the hook, but the gate holds only the sessions that the server
follows. If the server does not run, curl fails and the call runs. A pause does
not stop a tool call that already runs, or text that the agent writes.

A pause stays in effect after you open another working tree, and the gate then
holds only the sessions of the new tree. Resume also releases the calls that
the gate holds for a previous tree.

The server does not see changes that shell commands make, for example `sed -i`,
formatters, code generation, or `git checkout`. Replayed calls from before such
a change can show the wrong callable. The transcript format is internal to
Claude Code and can change between versions.

## Fidelity

The regression fixtures cover Python 3.12 generic type parameters, sync and
async functions, methods, decorators, nested functions, lambdas,
comprehensions, match statements, exception groups, import forms, Metaflow
transitions, syntax rejection, and deterministic ordering.
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
