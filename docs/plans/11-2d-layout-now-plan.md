# Plan 11: 2D layout and Now panel

This plan gives the 2D view its page layout. The left column holds the Now
panel, the agent stream, and the code pane. The board fills the rest of the
width. The column width and the line between the stream and the code pane
are resizable. The Now panel is a new shared component. The 2D view is
still behind `?view=2d`.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 10, and plan 06 for the dividers.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Make the Now panel

### Problem

The 3D view tells what the agent does with the lock label and the run chip
on the graph. The 2D view has no such label. A reader needs one place that
says where the agent is and what it does now.

### Result

`web/src/ui/now.ts` has a `NowPanel` class with `render(events, s, label)`.
It shows, from top to bottom:

1. The label `NOW` and a phase chip with the name and color of
   `PHASE_LOOKS` (plan 05).
2. A path: package › file › callable, for the target of the state. The
   package part has the package color. If the latest read or edit names no
   callable, the path ends at the file. If there is no file yet, the panel
   says "The agent has not opened a file yet."
3. The latest thing the agent said, thought, ran, or was asked, with a
   label: `YOU ASKED`, `SAID`, `THINKING`, `TOOL`, or `RAN`. A thought shows
   from the time it began (`since`). The text has at most three lines.
4. The latest command and its state: running (cyan), passed (lime), or
   failed (coral).

The panel draws again only when its content changes.

### Tests

1. Make `web/src/ui/now.test.ts`. Put the choice of the latest item in a
   pure function `latestNarrative(events, t)`. Test that a thought that
   began before `t` but ends after `t` is the latest item.

## Task 2: Arrange the page for the 2D view

### Result

- When the 2D view is active, `.main` has the class `v2d`. The grid is:
  left column, divider, board.
- The left column holds, from top to bottom: the Now panel, the agent
  stream, a horizontal divider, and the code pane (tabs, path, code,
  status).
- The 3D right column and its divider do not show in the 2D view.
- The shell moves the existing stream and code pane elements into the left
  column. It does not make second copies. Thus, the open tabs and the
  stream stay the same when the view changes.
- The column width uses `bindDivider` (plan 06). It saves to the key
  `awv.panes2d`. It has a default of 400 px, and a range of 300 to 760 px. The
  board keeps at least 420 px.
- The line between the stream and the code pane uses `bindDivider` with
  `axis: 'y'`. It saves to the key `awv.split2d`. It has and a default of 40% of the column
  for the stream.
- On a screen narrower than 980 px, the parts stack in this order: the Now
  panel, the board (at least 440 px high), the stream, and the code pane.
  The dividers do not show.

### Procedure

1. Add `NowPanel` markup to `web/index.html`, hidden by default.
2. Add `applyLayout(name)` to the shell in `web/src/main.ts`. Call it from
   `setView` (plan 07).
3. Add the CSS for `.main.v2d` and its narrow-screen rules to
   `web/src/style.css`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository with the test session. Open
   `/?view=2d`.
3. The left column shows the Now panel, the stream, and the code pane. The
   board is on the right.
4. Replay the session. The Now panel shows the package, file, and callable
   that the agent reads. Its phase chip changes color with the phase. During
   a thought, it shows THINKING and the thought.
5. During a command, the Now panel shows the command as running. After the
   result, it shows passed or failed.
6. Each edit opens a tab in the code pane, and the diff types out, as in the
   3D view.
7. Drag the column divider. The board gets narrower or wider and fills its
   space again. Reload. The width stays.
8. Drag the divider between the stream and the code pane. Reload. The
   position stays.
9. Use the keyboard on both dividers, as in step 4 of plan 06.
10. Make the window 400 px wide. The parts stack. The page has no horizontal
    scroll.
11. Open `/?view=3d`. The 3D layout is the same as before this plan. Open
    `/?view=2d` again. The tabs that were open are still open.
