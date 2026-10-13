# Codegraph Live: implementation guide

Hand this file and `prototype.html` to Claude Code. The prototype is a single-file, fully simulated mock of the interface: a 3D code graph, a streaming diff and a scrubbable session timeline. Your job is to replace the simulation with real data from the existing `code-graph-visualizer` project.

Open `prototype.html` in a browser first. It runs standalone (three.js comes from a CDN script tag).

## Goal

Show an LLM editing code in real time, with the edit landing on the right node of the code graph:

- Graph nodes glow. The node being edited is marked by a lock-on reticle.
- Dependents light up hop by hop (the blast radius).
- A diff streams into the code pane as the model writes.
- The session is recorded as events and can be scrubbed and replayed.

## First, read the existing repo

Before writing anything, find out and write down in a short note:

1. What does the current graph data look like (nodes, edges, kinds, ids)? What produces it (SCIP, tree-sitter, language server, something else)?
2. How is the graph currently rendered? Reuse any layout or data loading that already works.
3. Where could an LLM edit event come from? Candidates: a Claude Code hook (PreToolUse/PostToolUse on Edit/Write), a file watcher plus git diff, or a wrapper around the agent loop.

Do not invent a data shape that fights the existing one. Adapt the adapter layer (below), not the UI.

## Architecture (keep it this simple)

```
agent / hooks ──events──▶ server (tiny) ──WebSocket──▶ browser
code indexer ──graph.json─▶ server ──HTTP──────────────▶ browser
```

- **Frontend:** Vite + TypeScript + `three`. No UI framework, no state library. The prototype's DOM updates are plain `textContent`.
- **Server:** one small process (Node, Go or Rust, whichever the repo already uses). It serves `graph.json`, appends events to a JSONL log and broadcasts them on one WebSocket. No database.
- **Session store:** the JSONL event log is the source of truth. Replay is reading the same file.

## The one idea worth keeping: state is a function of time

In the prototype, everything on screen comes from `stateAt(t)` plus the event log: the log rows, the typed diff, the heat on each node and the reticle target. That is what makes the scrubber work for free. Keep it:

- Live mode: `t` = time since session start, events arrive over the WebSocket.
- Replay mode: `t` comes from the scrubber, events come from the JSONL file.
- Both feed the same reducer: `fold(events, t) -> ViewState`.

Do not build separate live and replay code paths.

## Working tree: choose where the code lives, at any time

The title bar has a **Working tree** field (path input, recent paths, an OPEN button and a status chip). The path is session state, not a startup flag, so the user can point the viewer at a different checkout or `git worktree` without restarting. The code pane shows the full path of the file being edited under the tabs.

Behavior to implement for real:

1. User submits a path. The client sends `{ type: 'set_root', path }` over the WebSocket and shows `REINDEXING…`.
2. The server validates the path (see security below), runs or loads the indexer for that root and replies `{ type: 'root_ready', path, nodes, edges }`. The client swaps in the new graph, re-runs layout and shows `INDEXED · N NODES`. On failure, reply `{ type: 'root_error', path, message }` and keep the old graph.
3. Every event path is **relative to the root**. The client joins root + relative path only for display. Never put absolute paths in events.
4. The first line of each session JSONL file is a header, so replays know which tree they belong to:
   `{ "type": "session", "root": "D:\\r\\code-graph-visualizer", "branch": "main", "t0": "2026-10-13T21:00:00Z" }`
5. If the agent works in a different worktree than the one being viewed, the server should say so. A mismatch chip beats silently showing the wrong graph.
6. Persist the last root and a short recents list. The prototype keeps them in `localStorage`, which is fine for a client-only setting.

Security (the server will read arbitrary paths): resolve and canonicalize the path, reject anything that is not an existing directory, and, if the server is reachable by anyone other than you, restrict roots to an allowlist from config. Bind to `127.0.0.1` by default.

If you also want a **file tree browser** (expandable folders for the working tree), that is a separate panel and not in the prototype. Tell Claude Code before it starts so the left panel gets a second tab for it.

## Resizable panes

Both side panels have a draggable divider. In the prototype, `bindSplit()` and `applyPanes()` implement it:

- Drag a divider to resize. Double-click or press Home to reset to the default width.
- Dividers are focusable `role="separator"` elements. Left and Right arrows resize by 16 px (64 px with Shift), and `aria-valuenow` tracks the width.
- The graph keeps at least 320 px. Widths are clamped to 200 to 640 px (stream) and 260 to 1100 px (code).
- Widths persist in `localStorage`. The graph canvas resizes through a `ResizeObserver`, so no extra wiring is needed.
- Below 980 px the dividers disappear and the panels stack under the graph.

For reading more code, drag the right divider (the code pane is on the right in this layout). If you want the agent stream on the right and the code on the left, it is a one-line change to the grid column order.

## Event schema (proposed, adjust to what the repo can emit)

All events carry `t` (seconds since session start) and are one JSON object per line.

```ts
type Ev =
  | { t: number; type: 'session';     root: string; branch?: string }   // header, first line of the file
  | { t: number; type: 'read';        nodeId: string; path: string }
  | { t: number; type: 'plan';        text: string }
  | { t: number; type: 'edit_start';  nodeId: string; path: string; startLine: number;
      context: string[]; removed: string[] }          // lines shown before and being replaced
  | { t: number; type: 'edit_delta';  nodeId: string; added: string }   // streamed text, appended
  | { t: number; type: 'edit_end';    nodeId: string }
  | { t: number; type: 'touch';       nodeId: string }                  // a dependent was changed too
  | { t: number; type: 'check_start'; command: string }
  | { t: number; type: 'check_end';   ok: boolean };
```

Graph snapshot (`graph.json`):

```ts
type Graph = {
  nodes: { id: string; label: string; module: string; kind: string }[];
  edges: { a: string; b: string }[];
};
```

## Prototype to real: what maps to what

All names are in `prototype.html`.

| Prototype piece | Replace with |
|---|---|
| `MODS`, `names`, `idx`, `E`, `ADJ` | Load `graph.json`. Keep `ADJ` (adjacency list) and `DEG` (degree). |
| Force layout loop (`ITER = 280`) | Keep for small graphs. For large ones, precompute layout server-side or use a worker, and cache positions. |
| `TASKS`, `LOG`, `stateAt(t)` | The reducer over real events. Keep the phase idea: read, plan, edit, check, clean. |
| `hopsFrom(target)` | Keep as is. Compute from the node of the current `edit_start`. |
| `heatTarget(hop, localTime)` | Keep the shape. Drive it from event times, not fixed offsets: hop 1 ramps after the first `edit_delta`, hop 2 after `touch` events or a delay. |
| `renderCode` typed diff | Drive from `edit_start` + the accumulated `edit_delta` text. Drop the fake character budget. |
| `renderLog` | One row per event. |
| Node sprites, edge colors, pulses, ripples, reticle | Keep unchanged. This is the visual part. |
| `nearest()` screen-space picking, hover tip, node card | Keep unchanged. |
| Simulated badge in title bar | Remove once live data is wired. |
| Working tree field, `openTree()`, fake `REINDEXING…` timer | `set_root` / `root_ready` / `root_error` messages (see Working tree). Keep the field, recents and status chip. |
| `bindSplit()`, `applyPanes()` | Keep as is. |

## Suggested file layout

```
src/
  main.ts            boot, wire modules, rAF loop
  graph/load.ts      fetch graph.json, build adjacency, hopsFrom()
  graph/layout.ts    force layout (or load cached positions)
  session/events.ts  Ev types, JSONL parse
  session/reduce.ts  fold(events, t) -> ViewState   (pure, unit-testable)
  view/scene.ts      three.js scene: nodes, edges, floor, pulses, ripples
  view/reticle.ts    lock-on reticle
  view/camera.ts     orbit, zoom, follow
  ui/panes.ts        stream log, blast list, code pane, scrubber
  ui/split.ts        resizable dividers (bindSplit / applyPanes)
  ui/tree.ts         working-tree field, recents, status chip
  net/live.ts        WebSocket client
server/              tiny server: serve graph.json, tail JSONL, broadcast
  root.ts            validate + switch working tree, trigger indexing
```

Unit tests go on `reduce.ts` and `hopsFrom`. Everything else is visual.

## Behaviors to preserve

- Reticle scans neighbor nodes, then locks on to the target, scaling in from large to small. It is a billboard that keeps constant screen size at any zoom.
- Glow is additive sprites (soft halo plus a bright core) with fog for depth. No postprocessing pass is needed.
- Edge brightness is `min(heat(a), heat(b))`. Pulses travel from the lower-hop node outward along lit edges.
- Camera: drag to orbit, wheel and pinch to zoom, auto-rotate pauses while the user interacts, follow mode eases the orbit center toward the active node.
- Panes stack below the graph on narrow screens.
- Respect `prefers-reduced-motion` (auto-rotate off, no blinking cursor).

## Gotchas already hit in the prototype

1. **Negative time on the first frame.** `requestAnimationFrame`'s timestamp can be slightly earlier than `performance.now()` taken at start. Clamp `dt` to `>= 0` and wrap time into `[0, TOTAL)` before indexing anything.
2. **Do not depend on a CDN in the artifact viewer.** The hosted preview inlines three.js. In the real app, bundle `three` via npm.
3. **Pointer events:** distinguish click from drag with a small movement threshold (the prototype uses 6 px) or selecting nodes will fight orbiting.
4. **Portrait aspect:** the camera distance is scaled up when `aspect < 1.1`, otherwise the graph is cropped on phones.

## Scaling notes (the prototype has 61 nodes)

- Sprites per node and a vertex-color line set are fine up to a few thousand nodes. Beyond that, switch nodes to one `THREE.Points` with a custom shader and instance the pulses.
- Only recolor edges whose endpoints changed heat, not all of them every frame.
- Run layout once, off the main thread, and cache.
- Screen-space picking loops over every node on pointer move. Throttle it, or use a spatial grid, for large graphs.

## Definition of done

1. `npm run dev` shows the real project's graph, rotating and orbitable.
2. Running a Claude Code edit in a watched repo produces events, and the reticle locks onto the correct node.
3. The diff pane streams the real edit text.
4. Dependents light up by real graph distance.
5. A finished session replays from its JSONL file with a working scrubber.
6. `reduce.ts` has tests for phase boundaries, heat per hop and scrubbing to arbitrary times.
7. Changing the working tree reloads the graph without a page refresh, and an invalid path leaves the current graph untouched with a visible error.
8. Both side panels resize by mouse and keyboard, and widths survive a reload.

## Prompt to paste into Claude Code

> Read `IMPLEMENTATION.md` and open `prototype.html`. First inspect this repo and tell me, in a short note, what the existing graph data and rendering look like and where edit events could come from. Then implement the plan using the smallest architecture that works: Vite + TypeScript + three, one WebSocket, a JSONL event log, and a pure `fold(events, t)` reducer shared by live and replay. Keep the prototype's visual behavior. Include the working-tree switcher and resizable panes from the prototype. Ask me before adding any dependency beyond `three` and a dev server.
