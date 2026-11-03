# Plan 13: 2D interaction and view toggle

This plan makes the 2D board respond to the viewer, and then shows a toggle
for the two views in the title bar. A pointer on a callable shows its calls.
A click on a callable opens its file at that callable. A click on a file or
package card moves the highlight there. After this plan, the 2D view is a
normal part of the page.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 12.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Show the calls of a callable

### Result

- When the pointer is on a chip, a tooltip shows: the full display name,
  `file:line–endLine`, the number of lines, the number of calls and of
  callers, and the reads and edits so far.
- The board draws a curved line from the chip to each callee and to each
  caller. The lines to callees are `--edge-calls`. The lines to callers are
  `--edge-called-by`. The lines are in an SVG layer over the board.
- The chip under the pointer gets a 2 px `--ink` outline. The callees and
  the callers get 1.5 px outlines in the colors of their lines. All
  outlines have a 1 px offset. All other chips have 28% opacity.
- When the pointer leaves the chip, the lines and the fade go away.
- The board does not draw lines at other times.

### Tooltip

| Part | Value |
| --- | --- |
| Position | `position: fixed`. 14 px to the right of the pointer and 18 px below it. |
| Near the right edge | Move it left, so that it stays 8 px inside the window. |
| Near the bottom edge | If it does not fit below the pointer, put it 10 px above the pointer. |
| Pointer events | None, so it never takes the pointer from the board. |
| Size | At most 320 px wide. A long name breaks at any character. |
| Box | Padding 8 px × 10 px, fill `--panel` at 97%, 1 px border `--cyan` at 30%, corner radius 4 px |
| Text | `--mono`, 11 px, line height 1.55, `--mute`. The name is on the first line, in `--ink`, 500. The count of calls is in `--edge-calls`; the count of callers is in `--edge-called-by`. |
| Layer | Over the board, the links, and the floating bar |

### Links

- Each line is a quadratic curve from the center of the chip to the center
  of the other chip.
- The control point is on the perpendicular through the midpoint of the
  two centers, at a distance of `min(60, 0.2 × length)` px from it. Each
  curve starts at the chip under the pointer, so all curves bend to the same
  side of their direction.
- If a callable is both a callee and a caller, draw only the callee line.
  Give its chip the callee outline.
- The stroke is 1.5 px, at 85% of its color, with no fill.
- The SVG layer covers the full board, has no pointer events, and is under
  the reticle. Draw it again only when the chip under the pointer changes
  or the board size changes.

## Task 2: Clicks

### Result

| Click on | Result |
| --- | --- |
| A chip | Selects the callable (`hooks.select`), keeps its lines and fade, and opens its file in the code pane at the callable (`hooks.openFile(file, { line, endLine })`). |
| The same chip again | Clears the selection. |
| A file card, outside its chips | Moves the highlight to the file and its package. Opens the file in the code pane with no focus lines. |
| A package card, outside its file cards | Moves the highlight to the package. |
| The same card again, Esc, or FOLLOW AGENT | Moves the highlight back to the agent. |

- While the viewer holds the highlight, a bar floats at the lower right of
  the board: `Showing <path>` and a FOLLOW AGENT button. The bar floats over
  the board, so it does not change the size of the board.
- The bar: `position: absolute`, 12 px from the right and bottom edges of
  the board, at most the board width less 24 px. Padding 6 px, and 12 px on
  the left. Fill `--panel` at 96%, 1 px border `--cyan` at 30%, corner
  radius 4 px, shadow `0 6px 24px` black at 50%. Text `--mono` 11 px. The
  path is `--ink` 500, one line, with an ellipsis at the end. The button
  has the look of the transport buttons.
- The agent's reticle, heat, and blast outlines still show while the viewer
  holds the highlight. They are dim if they are in a dim package.
- A selection made in one view stays when the viewer changes the view,
  because the shell keeps it (plan 07).
- A new edit opens its tab in the code pane by the rules of plan 04. Follow
  edit decides if the new tab becomes active.

## Task 3: Keyboard

### Problem

A large tree has about 1,000 chips. One tab stop for each chip makes the
board too slow to use with a keyboard.

### Result

- Each package card is one tab stop.
- In a package card, the arrow keys move the focus through its file cards,
  in reading order. Enter on a file card moves the focus to its first chip.
- In a file card, the arrow keys move the focus through its chips. Esc moves
  the focus back to the file card, and then to the package card.
- Enter or Space on a chip, a file card, or a package card does what a click
  does.
- Only one element in each group has `tabindex="0"`. The others have
  `tabindex="-1"` (a roving tab index).
- The focused element has a 2 px `--cyan` outline. The offset is 2 px on a
  card and 1 px on a chip.
- A screen reader reads a chip as its display name, file, and line. It reads
  a card as "Highlight" and its path.

## Task 4: The view toggle

### Result

- The title bar has a toggle with two buttons, `3D` and `2D`, with
  `aria-pressed`.
- A click calls `setView` (plan 07). The choice persists under `awv.view`.
- `?view=2d` and `?view=3d` still override the stored choice.
- A change of view keeps the playback time, the open tabs, the selection,
  and Follow edit.

### Procedure

1. Add the toggle to `web/index.html` and its handler to `web/src/main.ts`.
2. In `README.md`, add a **2D view** item to "The page". Describe the cards,
   names and squares, the reticle, the heat, the spotlight, the clicks, and
   the keyboard. Add `web/src/view2d` to the list in "Layout".

## Validation

This is the full check of the 2D view. All steps must pass.

1. `npm --prefix web test`, `npm --prefix web run typecheck`, and
   `go test ./...` pass.
2. Start the server on this repository with the test session. Open
   `http://127.0.0.1:7777`.
3. Select `2D` in the title bar. The 2D board shows with the left column of
   plan 11. Reload. The 2D view is still selected.
4. Point at a chip. The tooltip, the lines, the outlines, and the fade show.
   Move the pointer away. They go away. Point at a chip near the right edge
   and at a chip near the bottom edge of the board. The tooltip stays fully
   in the window and does not cover the chip.
5. Click a chip. The code pane opens its file, scrolled to the callable,
   with the focus color on its lines.
6. Click a file card outside its chips. The file and its package stand out.
   The other packages are dim. The code pane opens the file. The floating
   bar shows the path.
7. Click a package card outside its file cards. The package stands out.
8. Click the same card again. The highlight goes back to the agent. Do it
   again with Esc, and again with FOLLOW AGENT.
9. Replay the session while you hold the highlight on another package. The
   reticle and the heat still show in the agent's package, dim.
10. Use only the keyboard: Tab to a package card, arrows to a file card,
    Enter, arrows to a chip, Enter. The code pane opens the file at the
    callable. Esc goes back up.
11. Select a chip, then select `3D`. The same node is selected in the 3D
    view. The tabs, the time, and Follow edit are the same.
12. Open `/?view=3d` with `2D` stored. The 3D view shows.
13. Do steps 3 to 13 of the validation of plan 12 again in this build.
14. Do the full 3D check of plan 07 again in this build.
15. Open the browser developer tools. Take a heap snapshot. Change between
    `3D` and `2D` ten times with the toggle, with no reload. Take a second
    snapshot. The heap is not more than 10% larger. The console shows no
    warning about too many WebGL contexts.
16. Make the window 400 px wide. The 2D view stacks, the toggle fits in the
    title bar, and the page has no horizontal scroll.
17. In the browser developer tools, the network panel shows no requests to
    other hosts.
