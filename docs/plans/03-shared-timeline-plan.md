# Plan 03: Shared timeline and transport

This plan replaces the scrubber at the bottom of the page with a new
timeline. The 3D view uses it at once. The 2D view uses it after plan 07,
because the timeline is part of the page shell and not part of a view.

The new timeline is one canvas with four marks: edits, commands, thinking,
and waits for the user. It keeps every behavior of the current scrubber.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...` and make sure that all tests pass.
3. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
4. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Put the timeline marks in a pure module

### Problem

`Panes.renderBursts` (`web/src/ui/panes.ts:376`) calculates the marks and
writes them to the DOM in one step. No test covers the edit labels or the
readout. The playback logic is in the frame loop of `web/src/main.ts`
(lines 645 to 658), so no test covers it either.

### Result

- `web/src/ui/timelineModel.ts` has only pure functions. It does not use the
  DOM.
- Tests cover each function.

### Procedure

1. Make `web/src/ui/timelineModel.ts`.
2. Add `commandSpans(events)`. Pair each `check_start` with the next
   `check_end`. Return `{ from, to, ok }`. If no `check_end` follows, set
   `to` to `null` and `ok` to `null`.
3. Add `editMarks(events)`. Return `{ t, width, path }` for each edit. Set
   `width` to `LOCK_T + typingDuration(event)`.
4. Add `editLabels(marks, total)`. Move the label logic of
   `renderBursts` here. An edit that starts within 12% of the track after
   the first edit of a group joins that group. The label is the file name of
   the first edit, plus ` +N` for the other edits.
5. Add `axisTicks(total, width)`. Use steps of 15, 30, 60, 120, 300, 600,
   1200, 1800, and 3600 seconds. Use the smallest step that puts at least
   64 px between two ticks.
6. Add `timeToX(t, total, width)` and `xToTime(x, total, width)`. Keep the
   7 px inset at each end, as the current track does.
7. Add `nearestEdit(marks, x, total, width)`. Return the edit within 8 px of
   `x` (`READOUT_SLOP`), or `null`.

### Tests

1. Make `web/src/ui/timelineModel.test.ts`.
2. Test `commandSpans` with a command that passes, a command that fails, and
   a command with no result.
3. Test `editLabels` with three edits close together. Expect one label
   `a.ts +2`. Test two edits far apart. Expect two labels.
4. Test `axisTicks` for a total of 120 s and of 5400 s at 1000 px. Make
   sure that no two ticks are closer than 64 px.
5. Test that `xToTime(timeToX(t))` returns `t`.
6. Run `npm --prefix web test`. Make sure that all tests pass.

## Task 2: Put the playback state in a module

### Problem

The play state, the speed, the live edge, and the scrub state are loose
variables in `web/src/main.ts` (lines 112 to 116 and 427 to 487). A second
view cannot use them, and no test covers them.

### Result

- `web/src/ui/playback.ts` has a `Playback` class with this state: `t`,
  `playing`, `followLive`, `scrubbing`, and `speed`.
- `Playback.advance(dt, total, waits, replay, liveEnd)` does the work that
  lines 645 to 658 of `main.ts` do now.
- `Playback.previousEdit(events)` and `Playback.nextEdit(events)` use
  `previousEditTime` and `nextEditTime` from `web/src/session/reduce.ts`.
  Thus, `REWIND_GRACE` still applies.
- The speed list stays 1×, 2×, 4×, 8×, and 16×. The speed persists under the
  key `awv.speed`. Shift-click steps the speed down.
- `settings/playback.ts` `skipWaiting` still applies.

### Procedure

1. Make `web/src/ui/playback.ts`. Move the state and the logic from
   `main.ts` into the `Playback` class.
2. Keep the `localStorage` read and write in try/catch, as `main.ts` does
   now.
3. In `main.ts`, make one `Playback` instance. Use it in `frame` and in the
   button handlers.

### Tests

1. Make `web/src/ui/playback.test.ts`.
2. Test that `advance` skips a wait span when `skipWaiting` is true.
3. Test that `advance` stops at the end of a replay.
4. Test that `advance` goes back to the live edge at the end of a live
   session.
5. Test that two calls of `previousEdit` within `REWIND_GRACE` go back two
   edits.

## Task 3: Draw the timeline

### Problem

The current track is a range input on top of DOM bands. It does not show
commands. Many bands and labels make many DOM nodes.

### Result

- `web/src/ui/timeline.ts` has a `Timeline` class. It draws one canvas in
  the footer.
- From top to bottom, the canvas shows:
  1. Edits: a coral bar from top to bottom at the start of each edit, with
     a light coral band for the lock-on and typing time.
  2. Commands: a cyan band 7 px high from the start to the result. A failed
     command is coral.
  3. Thinking: a violet band 8 px high (`--think`).
  4. Waits for the user: gray diagonal lines over the full height.
  5. The grouped edit labels under the marks.
  6. The time axis with `mm:ss` labels (`fmt` from `web/src/ui/panes.ts`).
- The time after the playhead is darker. The playhead is a 2 px line in
  `--ink`.
- When the pointer is near an edit, a readout shows the path of the edit.
- The canvas element has `role="slider"`, `tabindex="0"`,
  `aria-valuemin`, `aria-valuemax`, `aria-valuenow`, and
  `aria-valuetext`. The value text gives the time and the latest edit
  before it, for example `01:23 of 05:00, after edit of load.ts`. The edit
  labels are only on the canvas, so this text gives them to a screen reader.
- Keys on the slider: Left and Right move 5 s. With Shift they move 30 s.
  Home goes to 0. End goes to the end. Page Up goes to the previous edit and
  Page Down goes to the next edit, with the rules of `Playback`.
- A drag on the canvas sets the time. The drag uses pointer capture. During
  the drag, `Playback.scrubbing` is true. Pointer up, pointer cancel, and
  loss of focus end the drag, as `main.ts` lines 475 to 479 do now.
- A drag to the end of a live session goes back to the live edge
  (`followLive` becomes true), as `main.ts` line 484 does now.
- The canvas has `touch-action: none`, so a touch drag moves the playhead
  and does not scroll the page.
- The canvas reads the color variables again when the `data-color-scheme`
  attribute of `<html>` changes. Use a `MutationObserver`. Thus, a change of
  color scheme also changes the timeline, as `settings/colors.css` says.
- The canvas draws again only when its input changes: the event count, the
  total, the time, the size, the pointer position, or the colors.
- The canvas uses the device pixel ratio, with a maximum of 2.

### Procedure

1. Make `web/src/ui/timeline.ts`. Use the functions from Task 1.
2. Read the colors from the CSS variables `--coral`, `--cyan`, `--lime`,
   `--think`, `--mute`, and `--ink`. Do not write color values in the code.
3. Use a `ResizeObserver` on the canvas parent to set the canvas size.
4. Give `Timeline` a method `render(events, total, t, waits, thinks)`.
5. Give the constructor a callback `seek(t)` and a callback
   `scrub(active)`.

### Tests

The functions that the canvas uses have tests in Task 1. Do the manual test
in "Validation".

## Task 4: Use the timeline in the page

### Procedure

1. In `web/index.html`, remove the `.track` element, the `#seek` input, and
   the `DRAG TO SCRUB` hint. Put a `<canvas id="timeline">` in their place.
2. Keep the play, previous, next, speed, time, and LIVE controls. Add a
   small legend: edit, command, thinking, waiting for you.
3. In `web/src/style.css`, remove the rules for `.track`, `.burst`, `.tlab`,
   `.wait`, `.think`, and `.tread` (lines 130 to 142). Add rules for the
   canvas and the legend.
4. In `web/src/ui/panes.ts`, remove `renderBursts`, `editMarks`, and the
   readout code in the constructor. Remove `track` and `seek` from
   `PaneElements`.
5. In `web/src/main.ts`, make one `Timeline`. Call `render` from `frame`.
6. In `README.md`, change the **Scrubber** item of "The page". Say what the
   four marks are and which keys move the playhead.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build` and `go build -o bin/agent-work-visualizer .`.

## Validation

Do these steps after the pull request is merged, or on its branch. All steps
must pass.

1. `npm --prefix web test`, `npm --prefix web run typecheck`, and
   `go test ./...` pass.
2. Start the server on this repository with the test session. Open
   `http://127.0.0.1:7777`.
3. The timeline shows coral edit bars, cyan command bands, violet thinking
   bands, and gray lines where the agent waited for you.
4. Point at an edit bar. The readout shows the path of the edited file.
5. Two edits close together show one label with `+1`.
6. Drag on the timeline. The graph, the stream, and the code pane follow the
   pointer.
7. Click the timeline, then press Right. The time moves 5 s. Press
   Shift+Right. The time moves 30 s. Press Home, then End. Press Page Up and
   Page Down. The time goes to the previous edit and to the next edit.
8. Open the accessibility tree of the browser, or use a screen reader. Make
   sure that the timeline is a slider. Its value text names the time and the
   latest edit.
9. Click previous edit two times fast. The time goes back two edits.
10. Click the speed button until 16×. Shift-click it. The speed goes to 8×.
    Reload the page. The speed is still 8×.
11. Play a replay that has a wait. The playhead jumps over the wait.
12. Select LIVE. The playhead goes to the live edge and follows new events.
    Drag the playhead back, then drag it to the right end. The LIVE button
    is pressed again and the playhead follows new events.
13. On a touch screen, or with touch emulation in the developer tools, drag
    on the timeline. The playhead moves and the page does not scroll.
14. In the developer tools console, run
    `document.documentElement.dataset.colorScheme = 'x'` after you add an
    `x` scheme to `settings/colors.css`. The timeline colors change with no
    reload.
15. Make the window 400 px wide. The timeline fits with no horizontal page
    scroll.
16. In the browser developer tools, the network panel shows no requests to
    other hosts.
