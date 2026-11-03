# Plan 04: Shared code pane

This plan moves the code pane into one component that owns its state. The
code pane has the file tabs, the path bar, the file view, the typed diff,
and the status line. After this plan, any view can open a file at a callable
with one call. The visible behavior does not change.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Put the tab rules in a pure module

### Problem

The tab state is in loose variables in `web/src/main.ts` (lines 133 to 143):
`openFiles`, `dismissed`, `activeFile`, `activeDiff`, `editPath`,
`fileFocusOf`, and others. `openFile`, `selectTab`, and `closeTab` (lines
163 to 194) and part of `frame` (lines 709 to 721) change them. No test
covers these rules.

### Result

`web/src/ui/tabs.ts` has a `TabState` type and pure functions that keep the
current rules:

- `open(state, path, diff, focus?)`: add the tab if it is not open, select
  it, and remember the focus lines of the file.
- `select(state, path, editPath)`: select the tab. The tab shows the diff if
  `path` is `editPath`.
- `close(state, path)`: remove the tab and remember that the viewer closed
  it. If the tab was active, select the tab at the same position, or the
  last tab.
- `syncEdits(state, files, edit, follow)`: add a tab for each edited file
  that the viewer did not close. A new edit selects its tab only when
  `follow` is true or no tab is open. A new edit of a closed file opens its
  tab again.

### Procedure

1. Make `web/src/ui/tabs.ts`. Move the rules from `main.ts` into the
   functions above.
2. Each function returns a new state. Do not change the input state.

### Tests

1. Make `web/src/ui/tabs.test.ts`.
2. Test `close` on the active middle tab. Expect the next tab to become
   active.
3. Test `close` on the last tab. Expect the tab before it to become active.
4. Test that `syncEdits` does not open a closed file again for the same
   edit, but opens it for a new edit.
5. Test that `syncEdits` with `follow` false keeps the active tab.
6. Test that `open` with focus lines, then `select` of another tab, then
   `select` of the first tab keeps the focus lines.

## Task 2: Make the CodePane component

### Problem

`Panes` (`web/src/ui/panes.ts`) draws the tabs, the path, the file, the
diff, and the status. `main.ts` keeps the state and fetches the file text.
A second view would have to copy this code.

### Result

- `web/src/ui/codePane.ts` has a `CodePane` class.
- The constructor takes the elements `tabs`, `pathRoot`, `pathFile`, `code`,
  `check`, and `live`, and the reduced-motion flag.
- `CodePane.open(path, focus?)` opens the file and scrolls to the focus
  lines. It fetches `/api/file?path=…` with `getText` from
  `web/src/net/live.ts`. A late answer for an older request has no effect.
- `CodePane.render(s, root, follow)` does the work of lines 709 to 726 of
  `main.ts`.
- `CodePane.reset()` closes all tabs. Use it when the viewer opens another
  working tree.

### Procedure

1. Make `web/src/ui/codePane.ts`. Use `TabState` from Task 1.
2. Move `renderTabs`, `renderFile`, `renderPath`, `renderCode`, and
   `renderStatus` from `Panes` into `CodePane`. Keep their code and their
   signature checks.
3. Move the constants `CODE_ROW_LIMIT`, `FILE_ROW_LIMIT`, and `TYPE_STEPS`
   with them.
4. Remove these elements from `PaneElements`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Task 3: Use CodePane in the page

### Procedure

1. In `web/src/main.ts`, make one `CodePane`.
2. Remove the tab variables and `openFile`, `selectTab`, and `closeTab`.
3. Where a node click opens a file, call
   `codePane.open(node.file, { line: node.line, endLine: node.endLine })`.
4. In `frame`, call `codePane.render(s, graph.payload.root, followCamera)`.
5. `applyGraph` clears the tabs when the view changes (`main.ts` lines 221
   to 227, `if (!sameView)`). Call `codePane.reset()` there in place of
   that code. A new analysis of the same tree keeps the tabs, as now.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Validation

All steps must pass. The behavior must be the same as on `main`.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository with the test session. Open the page.
3. Replay the session. Each edit opens a tab for its file. The diff types
   out in that tab.
4. Click a node in the graph. A tab opens for its file. The pane scrolls to
   the callable, and its lines have the focus color.
5. Click the tab of the edited file. It shows the diff of the current edit.
6. Close a tab with its ×. It does not open again until a new edit changes
   that file.
7. Turn off Follow edit. Replay to a new edit. The active tab does not
   change, but the tab of the edited file is present.
8. Open another working tree. The tabs of the old tree go away. Pick another
   session in the picker. The tabs go away too, because the view changes.
9. During a live session, let the server analyze the tree again after an
   edit. The tabs stay.
10. `web/src/main.ts` has no variable named `openFiles` or `dismissed`.
