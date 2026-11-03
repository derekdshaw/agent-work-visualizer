# Plan 07: View host

This plan separates the page shell from the 3D view. The shell keeps the
session, the playback, the timeline, the code pane, the stream, and the
selection. A view draws the graph. After this plan, the shell can show the
3D view or a 2D view. The 2D view is only a placeholder here, and only the
hidden setting `?view=2d` shows it.

This is the largest refactor of the 2D work. Most of the change moves code.
The 3D view must look and act the same as on `main`.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plans 03, 04, 05, and 06.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.
4. On `main`, do the steps of "Validation" one time. Write down anything
   that does not pass on `main`, so that you do not count it as a
   regression.

## Task 1: Define the view interface and the shared selection

### Problem

`web/src/main.ts` mixes the shell and the 3D view. The selection is a node
index (`hover`, `selected`, lines 122 and 123). An index changes when the
graph changes, and a 2D view has no use for 3D indexes.

### Result

`web/src/view/host.ts` exports:

```ts
export interface Selection {
  // Node IDs, as session events use them. null when there is none.
  hover: string | null;
  selected: string | null;
}

export interface ViewFrame {
  dt: number;
  clock: number;
  state: ViewState;
  events: readonly Ev[];
  follow: boolean;
  selection: Selection;
}

export interface ViewHooks {
  select(id: string | null): void;
  hover(id: string | null): void;
  openFile(path: string, focus?: { line: number; endLine: number }): void;
}

export interface GraphView {
  readonly name: '3d' | '2d';
  mount(host: HTMLElement, hooks: ViewHooks): void;
  unmount(): void;
  setGraph(graph: Graph, sameView: boolean): Promise<void>;
  frame(input: ViewFrame): void;
}
```

### Procedure

1. Make `web/src/view/host.ts` with the types above.
2. In `web/src/main.ts`, keep the selection as node IDs. Change the code
   that reads `hover` and `selected` to use `graph.index` where it needs an
   index.
3. `applyGraph` clears the selection on each new graph (`main.ts` line 220).
   Keep this behavior.

### Tests

1. Run `npm --prefix web typecheck`.

## Task 2: Move the 3D view behind the interface

### Problem

The 3D code is spread through `main.ts`:

- the scene (line 26) and the group labels (line 43);
- the heat and the reticle logic in `frame` (lines 666 to 704);
- the overlays (lines 748 to 780);
- the pointer handlers (lines 498 to 572);
- the camera tools (lines 574 to 605).

### Result

- `web/src/view/graph3d.ts` has a `Graph3D` class that implements
  `GraphView`.
- `Graph3D` owns these parts:
  - the scene, the group labels, the heat array, and the hop ring;
  - the content of the blast list;
  - the reticle overlay, the tooltip, and the node card;
  - the focus chip, the run chip, and the camera readout;
  - the auto-rotate and reset buttons.
- The Follow edit button moves from `#stage .tools` to the transport row in
  the footer, after the speed button. It stays in the shell, because it
  controls the code pane in both views. Its look, its title, and its
  `aria-pressed` state do not change.
- The blast list element (`#blast`) and its label stay in the left panel of
  the shell (`#streamPane`). `Graph3D.mount` gets the element as a second
  host. It shows the list on mount and hides it on unmount.
- The markup of the 3D stage moves from `web/index.html` into a
  `<template id="view3d">`. `Graph3D.mount` clones the template into the
  host element. `Graph3D.unmount` removes it and stops all listeners.
- `Graph3D.unmount` calls `dispose` on the WebGL renderer and on the scene
  objects, so a switch of view does not leak GPU memory.

### Procedure

1. Make `web/src/view/graph3d.ts`. Move the code that the "Problem" lists.
2. Move the 3D stage children of `#stage` in `web/index.html` into the
   template. Keep `#stage` as the host element.
3. Do not move `#blast` or the agent stream. Give `#blast` and its label to
   `Graph3D.mount` as a second host element.
4. In `main.ts`, keep the session, the playback, the timeline, the code
   pane, the stream, the working tree form, the session picker, and the
   pause gate.
5. In `frame`, after `fold`, call `view.frame({...})`.
6. In `applyGraph`, call `view.setGraph(graph, sameView)`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Task 3: Select the view

### Result

- `web/src/settings/view.ts` exports `initialView()`. It returns `'2d'` if
  the page URL has `?view=2d`, `'3d'` if it has `?view=3d`, else the value
  of the `localStorage` key `awv.view`, else `'3d'`.
- `web/src/view/graph2d.ts` has a `Graph2D` placeholder that implements
  `GraphView`. It shows the text "2D view: not ready" in the host.
- The shell has `setView(name)`. It unmounts the current view, mounts the
  other view, gives it the current graph, and keeps the selection, the open
  tabs, and the playback time.
- No visible control calls `setView` yet. Plan 13 adds the toggle.

### Procedure

1. Make the two modules.
2. In `main.ts`, mount the view that `initialView()` returns.

### Tests

1. Make `web/src/settings/view.test.ts`. Test `initialView` with each URL
   form and with an empty storage. Give the function the URL and the storage
   as parameters, so the test does not need a browser.

## Validation

This is a full check of the 3D view. All steps must pass, with the same
result as on `main`.

1. `npm --prefix web test`, `npm --prefix web run typecheck`, and
   `go test ./...` pass.
2. Start the server on this repository with the test session. Open
   `http://127.0.0.1:7777`.
3. **Graph.** Clusters show by directory, with a ring and a name under each.
   Point at a directory. Its file names show.
4. **Camera.** Drag to orbit. Shift-drag to pan. Scroll to zoom. Use two
   fingers on a touch screen, if you have one. Select Reset view.
   Auto-rotate stops while you interact.
5. **Selection.** Point at a node. A tooltip shows its name and file. Click
   it. The node card shows, and the code pane opens the file at the
   callable. Close the card.
6. **Reticle.** Replay the session. A read moves the reticle (ACQUIRING). An
   edit scans the neighbors, then locks on. The colors follow plan 05.
7. **Blast radius.** The target glows first, then 1 hop, then 2 hops. The
   blast list lights in the same order. Earlier edits keep a faint trace.
8. **Follow edit.** The Follow edit button is in the transport row. With it
   on, each new edit moves the camera and the code pane. With it off, they
   stay.
9. **Timeline.** Do steps 3 to 12 of the validation of plan 03.
10. **Working tree.** Open another working tree. The graph and the tabs
    change. Open this repository again.
11. **Re-analysis.** During a live session, ask Claude Code to add a
    function. About 2 s after the edit, the new node shows. The other nodes
    do not move.
12. **Blast list.** The blast radius list is in the left panel, under the
    stream, as on `main`.
13. **Placeholder.** Open `http://127.0.0.1:7777/?view=2d`. The graph area
    shows "2D view: not ready". The blast list does not show. The timeline,
    the stream, the code pane, and the Follow edit button still work. Open
    `/?view=3d`. The 3D view shows again.
