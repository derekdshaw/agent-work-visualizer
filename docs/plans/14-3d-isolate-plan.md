# Plan 14: Isolate a node in the 3D view

This plan adds an ISOLATE button to the node card of the 3D view. When the
viewer isolates a node, the graph shows only that node, its callers, and its
callees. All other nodes and their edges are hidden. The module rings and
the labels of the shown nodes stay bright. The other rings and labels dim.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing. If plan 07 is merged first, the 3D code of `web/src/main.ts` is in
`web/src/view/graph3d.ts`. Make the changes there.

## Decisions

- **One hop.** The isolated set is the node and the nodes one hop away:
  its callers and its callees. A later plan can add more hops.
- **Edges.** An edge shows only if both of its ends are in the isolated
  set. Thus, a call between a caller and a callee of the node also shows.
- **No zoom.** Isolation does not move the camera. The viewer zooms if
  necessary.
- **Playback.** Isolation operates during playback, during a live session,
  and while the session is paused. It does not pause the session, because
  the isolated set comes from the graph, not from the time.
- **Agent outside the set.** If the agent reads or edits a hidden node, the
  node stays hidden. The reticle and the lock label still show at the
  position of the node, so the viewer sees that the agent moved out of the
  set. Follow camera operates as before.
- **Live analysis.** A new analysis of the same tree keeps the isolation,
  if the isolated node is still in the graph. The set is calculated again
  from the new graph. Another working tree or another view clears the
  isolation.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Calculate the isolated set

### Result

`web/src/graph/load.ts` exports `isolationOf(graph, node)`. It returns:

| Field | Type | Meaning |
| --- | --- | --- |
| `node` | `number` | The isolated node. |
| `shown` | `Uint8Array` | 1 for the node and for each node in `graph.adj[node]`, else 0. Length `graph.n`. |
| `modules` | `Set<number>` | The modules (`graph.modOf`) of the shown nodes. |
| `files` | `Set<number>` | The files (`graph.fileOf`) of the shown nodes. |

`graph.adj` already holds the callers and the callees of each node, with no
direction. Do not make a second adjacency list.

### Tests

1. In `web/src/graph/load.test.ts`, make a graph of five nodes in two
   modules: `a` calls `b`, `c` calls `a`, `b` calls `c`, and `d` calls `e`.
2. Test `isolationOf` for `a`. Expect `a`, `b`, and `c` to show, and `d`
   and `e` to be hidden.
3. Test that `modules` and `files` hold only the modules and files of `a`,
   `b`, and `c`.
4. Test a node with no edges. Expect only that node to show.

## Task 2: Hide the other nodes in the scene

### Result

- `FrameInput` in `web/src/view/scene.ts` has a new field `isolation`: the
  result of `isolationOf`, or `null`.
- A hidden node has `halo.visible` and `core.visible` set to `false`.
- A hidden edge gets the color black in `edgeColors`. The edge material
  uses additive blending, so a black edge draws nothing.
- The isolated node is a focus node, the same as the hovered and the
  selected node. Its edges show in the focus colors (`--edge-calls` and
  `--edge-called-by`) with arrowheads, so the viewer sees which shown nodes
  are callers and which are callees.
- The focus edges of a hovered or selected node show only if both ends are
  shown.
- Pulse sprites run only on shown edges. A ripple does not show on a hidden
  node.
- `nearest()` skips hidden nodes. Thus, the pointer cannot hover or select
  a node that the viewer cannot see.
- The reticle and the selection marker do not change.

### Procedure

1. Add `isolation` to `FrameInput`.
2. In `render`, read `isolation.shown` in the node loop and in the edge
   loop.
3. Add an `isolation` argument to `nearest()`, or keep the last value from
   `render`. Use the same value in both places.

## Task 3: Dim the other rings and labels

### Result

- **Rings.** A module that has a shown node keeps its ring color. The ring
  of each other module has 25% of its color. The rings use additive
  blending, so a darker color makes a dimmer ring.
- **Module labels.** The label of a module that has a shown node does not
  change. The other module labels get the class `dim`, with
  `opacity: .3`.
- **File labels.** While a node is isolated, the file labels of
  `isolation.files` show. The other file labels are hidden. This replaces
  the usual rule (file labels of the hovered and the edited module) until
  the isolation ends.
- With no isolation, all rings and labels are as before.

### Procedure

1. In `GraphScene`, keep the ring color buffer and the base color of each
   module. Add `setIsolation(modules: ReadonlySet<number> | null)`. It
   writes the ring colors again and sets `needsUpdate`. Call it only when
   the isolation changes, not in each frame.
2. Add an `isolation` argument to `GroupLabels.render` in
   `web/src/ui/labels.ts`. Toggle `dim` on the module labels, and use
   `isolation.files` for the file labels.
3. Add `.gmod.dim{opacity:.3}` to `web/src/style.css`.

## Task 4: The card button

### Result

- The node card has a button after the node information. The button text
  is `ISOLATE` or `SHOW ALL`. It has the look of the other buttons on the
  stage, and `aria-pressed` that is `true` while the card node is the
  isolated node.
- The card shows the selected node. If no node is selected but a node is
  isolated, the card shows the isolated node. Thus, the button to end the
  isolation is always on the screen.

| The viewer does this | Result |
| --- | --- |
| Clicks ISOLATE on the card of a node | Isolates that node. The button changes to SHOW ALL. |
| Clicks SHOW ALL | Ends the isolation. The button changes to ISOLATE. |
| Selects another shown node | The card shows that node, with ISOLATE. A click isolates that node in place of the first one. |
| Clicks an empty part of the stage | Clears the selection. The card shows the isolated node, with SHOW ALL. |
| Clicks × on the card of the isolated node | Ends the isolation and closes the card. |
| Clicks × on the card of another node | Clears the selection. The card shows the isolated node. |
| Presses Esc | Ends the isolation. Does not change the selection. Esc in a text field or a list (for example the session picker) does not end the isolation. |
| Clicks Reset view | Does not change the isolation. Reset view controls only the camera. |

### Procedure

1. In `web/index.html`, add the button to `#card`.
2. In `web/src/main.ts`, add the variable `isolated` (a node index, or
   `-1`) and the value `isolation` (`isolationOf(graph, isolated)`, or
   `null`). Calculate `isolation` again only when `isolated` or the graph
   changes.
3. Change `showCard` to show `selected`, or `isolated` if `selected` is
   `-1`.
4. Pass `isolation` to `scene.render` and to `labels.render`. Call
   `scene.setIsolation` when `isolation` changes.
5. In `applyGraph`, if the view is the same, find the isolated node by its
   ID in the new graph (`next.index`), as the code does for `heat`. If the
   node is not in the new graph, or the view changed, set `isolated` to
   `-1`. Do not change how `applyGraph` treats `selected`.
6. Add a `keydown` handler for Esc on `window`. Ignore the key when the
   event target is an `input`, a `select`, or a `textarea`, or when no node
   is isolated.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository with the test session. Open the
   page.
3. Click a node that has callers and callees. The card shows ISOLATE.
4. Click ISOLATE. Only the node, its callers, and its callees show. The
   edges to the callees are yellow (`--edge-calls`) and the edges to the
   callers are white (`--edge-called-by`), with arrowheads. The camera does
   not move.
5. The rings and module labels of the modules of the shown nodes are
   bright. The other rings and module labels are dim. The file labels of
   the shown nodes show. No other file labels show.
6. Point at the empty places where hidden nodes were. No node gets hover.
   Click there. No hidden node is selected, and the card shows the isolated
   node with SHOW ALL.
7. Click a shown caller. The card shows the caller with ISOLATE. Click
   ISOLATE. The isolated set changes to the caller and its neighbors.
8. Click SHOW ALL. All nodes, edges, rings, and labels are as before
   step 4.
9. Isolate a node again. Press Esc. All nodes show again.
10. Isolate a node. Click × on the card. All nodes show and the card
    closes.
11. Isolate a node that the test session does not edit. Replay the session.
    The playback does not stop. When the agent edits a hidden node, the
    node stays hidden, and the reticle and the lock label show at its
    position.
12. Isolate a node that the session edits. Replay the session. The node
    glows with heat, and pulses run on its shown edges.
13. Start a live session on this repository. Isolate a node. Ask Claude
    Code to add a call to that node from another function. About 2 s after
    the edit, the server analyzes the tree again. The isolation stays, and
    the new caller shows.
14. Isolate a node. Open another working tree. The isolation ends. Open
    this repository again. All nodes show.
15. Use only the keyboard: Tab to the ISOLATE button, press Enter. The node
    is isolated. Press Enter again on SHOW ALL. All nodes show.
16. Open a working tree with more than 800 NODES. Isolate a node. Record
    5 s of auto-rotate in the performance panel. No frame takes more than
    16 ms.
