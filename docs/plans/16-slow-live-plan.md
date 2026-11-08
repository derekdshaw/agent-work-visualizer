# Plan 16: Slow speeds during a live session

This plan adds the speeds 0.75×, 0.5×, and 0.25×. During a live session, a
slow speed lets the viewer watch the work of the agent more slowly, with no
pause. The page keeps all the events that arrive while the playhead is
behind the live edge. The viewer can then:

- go back to 1× and see the events at their own speed, behind the live
  edge;
- select 2× or faster, so that the playhead catches up with the live edge;
- select LIVE, so that the playhead goes to the live edge at once.

A slow speed also operates in a replay.

While the playhead is behind the live edge, the page shows only the work up
to the playhead. A new graph waits until the playhead gets to the time at
which it arrived. The file view shows the file as it was at the playhead.

The speed button also gets arrow keys, so a keyboard user can select every
speed.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing. See "Other plans" at the end for the changes that apply if plans
03 or 15 are merged first.

## How the page operates now

The page already keeps every event of the live session in
`live.events`. The playhead (`simT`) is a time on the session timeline. The
page computes all of its state from the events up to the playhead (`fold`).
Thus, the "buffer" is not new data. It is the part of the timeline between
the playhead and the live edge.

In `frame` (`web/src/main.ts:645` to `:656`):

```ts
if (!replay && followLive && !scrubbing) simT = liveEnd(current);
else if (playing && !scrubbing) {
  simT += dt * speed;
  ...
  if (simT >= total) {
    simT = total;
    if (replay) playing = false;
    else followLive = true;
  }
}
```

- While the page follows the live edge, the playhead is the live edge, and
  the speed has no effect.
- When the playhead is behind the live edge and plays, it moves at the
  speed. At 2× or faster, it catches up. When it gets to the live edge, the
  page follows the live edge again.

A pause, then play, already puts the playhead behind the live edge. This
plan adds a way to get behind the live edge with no pause, and shows how
far behind the playhead is.

## Decisions

- **Speeds.** The list is 0.25×, 0.5×, 0.75×, 1×, 2×, 4×, 8×, and 16×.
- **A slow speed leaves the live edge.** If the page follows the live edge
  and the viewer selects a speed below 1×, the page stops following the live
  edge. The playhead starts at the current live edge and moves at the slow
  speed. Playback does not pause.
- **1× behind the live edge stays behind.** The playhead moves at the same
  rate as the live edge, so the distance stays the same. The live edge
  stops when no event arrives for longer than the gap cap of the log
  (`header.gapCap`, 30 s by default; see `liveEnd`). Thus, when the agent
  stops, the playhead gets to the live edge at most `gapCap` seconds plus
  the distance later. Then the page follows the live edge again, as now.
  (`skipWaiting` does not help here: a wait becomes a span only when the
  next prompt arrives, `waitingSpans` in `web/src/session/reduce.ts`.)
- **2× and faster catch up**, as now.
- **LIVE** goes to the live edge, as now. If the speed is below 1×, LIVE
  also sets the speed to 1×, because a slow speed has no effect at the live
  edge.
- **Slow speeds are not stored.** The speed button stores only a speed of
  1× or more in `awv.speed`. When the viewer selects a slow speed,
  `awv.speed` keeps its last value. Thus, a reload never starts at a slow
  speed.
- **The speed button does not wrap.** Click, Up, or Right goes one step
  faster, up to 16×. Shift-click, Down, or Left goes one step slower, down
  to 0.25×. With eight speeds, a wrap from 16× to 0.25× would surprise the
  viewer. This replaces the wrap from 16× to 1×.
- **The agent does not slow down.** A slow speed changes only the page.
  PAUSE AGENT still holds the agent.
- **No future work on the page.** While the playhead is behind the live
  edge, the page does not show a change that is after the playhead:
  - **Graph.** The server sends a new graph at the real time. The page
    fetches it at once, because the server keeps only the newest graph. But
    the page applies it only when the playhead gets to the session time at
    which it arrived.
  - **File view.** The server sends the file as it is on the disk now. The
    page removes the edits that are after the playhead from that text. Each
    edit event has the `structuredPatch` hunks of the edit, so the page can
    reverse them exactly.
  - **Stream, diff, heat, and reticle.** These come from `fold`, so they
    are already correct.
- **A graph does not go back.** If the viewer moves the playhead back
  before a graph that the page applied, the page keeps that graph. The page
  does not keep old graphs, because each graph can be large (about 660 kB
  for a tree of 3,000 callables). This is the behavior of the page now. The
  file view goes back correctly, because it comes from the events.
- **A change outside the agent's edits.** If the file on the disk does not
  match the edits (for example, a formatter or `git checkout` changed it),
  the page cannot reverse the edits. It then shows the current text with a
  note. It does not show text that can be wrong with no note.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: The speed rules in a pure module

### Result

`web/src/session/speed.ts` exports:

- `SPEEDS = [0.25, 0.5, 0.75, 1, 2, 4, 8, 16]`.
- `stepSpeed(speed, direction)`: the next speed up (`direction` 1) or down
  (`direction` -1). It stops at the two ends. A speed that is not in the
  list gives 1.
- `speedLabel(speed)`: `0.25×`, `0.5×`, `0.75×`, `1×`, `2×`, and so on.
- `storedSpeed(value)`: the speed from an `awv.speed` value, if it is in
  `SPEEDS` and is 1 or more, else 1.
- `lag(simT, liveEdge)`: the seconds that the playhead is behind the live
  edge, never below 0.

- `keyStep(key)`: 1 for `ArrowUp` and `ArrowRight`, -1 for `ArrowDown`
  and `ArrowLeft`, else 0.

### Tests

1. Make `web/src/session/speed.test.ts`.
2. Test `stepSpeed` up from 0.25 to 16, and that it stays at 16. Test it
   down from 16 to 0.25, and that it stays at 0.25.
3. Test `speedLabel` for each speed.
4. Test `storedSpeed` with `'0.5'`, `'4'`, `'3'`, `''`, and `'abc'`. Expect
   1, 4, 1, 1, and 1.
5. Test `keyStep` for the four arrow keys and for `Enter`.

## Task 2: Select a slow speed

### Result

In `web/src/main.ts`:

- `SPEEDS` comes from `web/src/session/speed.ts`. The speed button uses
  `stepSpeed`, `speedLabel`, and `storedSpeed`.
- The button handler writes `awv.speed` only when the new speed is 1 or
  more.
- If the page follows the live edge (`!replay && followLive`) and the new
  speed is below 1, set `simT` to `liveEnd(live)`, set `followLive` to
  `false`, and keep `playing` true.
- The LIVE handler sets the speed to 1 if it is below 1, and calls
  `setSpeedUI()`.
- A `keydown` handler on the speed button steps the speed with `keyStep`.
  It calls `preventDefault()` for an arrow key, so the page does not
  scroll. Enter and Space still do what a click does. The same rules apply
  as for a click: the store rule, and the rule for a slow speed at the live
  edge.
- The button has `aria-keyshortcuts="ArrowUp ArrowDown ArrowRight
  ArrowLeft"`.
- `setSpeedUI` sets `data-slow` on the button for a speed below 1, as it
  sets `data-fast` for a speed above 1. The title says: "Playback speed
  0.5×. Click or Up for faster, Shift-click or Down for slower."
- `web/src/style.css` gives `.btn.speed[data-slow]` a cyan border and cyan
  text. (`data-fast` is coral.)

The code in `frame` does not change. It already moves a playhead that is
behind the live edge at the speed, and it already follows the live edge
again when the playhead gets there.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Task 3: Show the distance to the live edge

### Problem

When the playhead is behind the live edge, the viewer must know it. Else a
viewer can think that the agent does the work that the page shows now.

### Result

- During a live session, while the playhead is more than 1 s behind the
  live edge, the LIVE button shows the distance: `LIVE −0:12`. Use `fmt`
  for the time. The button text is `LIVE` at other times.
- The LIVE button has `aria-pressed="false"` while the playhead is behind
  the live edge, as now. Its `aria-label` says "Go to the live edge, 12
  seconds ahead".
- The REC label in the title bar still shows the time of the playhead.
- The distance does not show in a replay.

### Procedure

1. In `frame`, compute `lag(simT, total)` for a live session. `total` is
   the live edge (`liveEnd(current)`) in a live session.
2. Change the LIVE button text and label only when the shown value changes,
   so that `frame` does not write the DOM in each frame.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Task 4: Hold a new graph until the playhead gets to it

### Result

- `refreshGraph` in `web/src/main.ts` fetches the new graph at once, as
  now.
- If the page follows the live edge, or shows a replay, it applies the
  graph at once, as now.
- Else (a live session with the playhead behind the live edge), it adds
  `{ at: liveEnd(live), payload }` to a list `heldGraphs`. `at` is the
  session time at which the graph arrived.
- In `frame`, after the playhead moves: find the newest item in
  `heldGraphs` with `at <= simT`. If there is one, apply it with
  `applyGraph(payload, true)`, and remove it and all items before it.
- When the page follows the live edge again (the playhead gets to the live
  edge, or the viewer clicks LIVE), apply the newest held graph at once and
  clear `heldGraphs`.
- A new view, OPEN of another working tree, or another session in the
  picker clears `heldGraphs`.
- `heldGraphs` keeps at most 20 items. When it is full, remove the oldest
  item. The page then shows an older graph for a longer time, but never a
  graph from after the playhead.

### Procedure

1. Put the choice of the graph in a pure function in
   `web/src/session/held.ts`: `takeHeld(held, simT)` returns the item to
   apply (or `null`) and the items that stay.
2. Use it in `frame` and in `refreshGraph`.

### Tests

1. Make `web/src/session/held.test.ts`.
2. Test `takeHeld` with three items at 10, 20, and 30 s, and `simT` 5, 20,
   and 35. Expect no item; the item at 20 (and the item at 30 stays); the
   item at 30 (and none stay).
3. Test the limit of 20 items.

## Task 5: Show the file at the playhead

### Problem

`openFile` (`web/src/main.ts:174`) fetches the text of the file from the
disk. While the playhead is behind the live edge, that text can have edits
that are after the playhead.

### Result

`web/src/session/fileAt.ts` exports `textAt(text, edits, t)`:

- `text` is the text from the disk.
- `edits` is the list of edit events of the file that are in `text`: the
  events of that path that the page had when it sent the fetch.
- It removes each edit with a time after `t`, from the newest to the
  oldest. For each edit, it reverses the hunks from the last to the first:
  at `newStart`, it replaces the context and `+` lines with the context and
  `-` lines.
- Before it reverses a hunk, it makes sure that the context and `+` lines
  are in the text at `newStart`. If they are not, it returns
  `{ ok: false }`.
- If it removes the edit that made the file (`created: true`), it returns
  `{ ok: true, exists: false }`.
- Else it returns `{ ok: true, exists: true, text }`.

In `web/src/main.ts`:

- `openFile` keeps the edits of the path that the page has when it sends
  the fetch.
- When a new edit of the active file arrives, fetch the file again, so the
  text and the list of edits agree.
- In a live session, the file view uses `textAt(fileText, edits, simT)`.
  This includes a playhead that is behind the live edge after a pause or a
  scrub.
- In a replay, the file view shows the disk text, as now. The disk text of
  an old session has the changes of later sessions, which are not in the
  events of the replay, so `textAt` cannot reverse them.
  - If the result is `exists: false`, the view says: "This file does not
    exist yet at this time."
  - If the result is `ok: false`, the view shows the disk text, with a
    note above it: "This file changed outside the agent's edits. This is
    the current text, not the text at this time."
- Compute `textAt` again only when the number of edits after `simT`
  changes. Add that number to the signature in `renderFile`
  (`web/src/ui/panes.ts`), so that the view draws again when it changes.
- The focus lines of a callable come from the graph. If the text at the
  playhead has fewer lines than `endLine`, show the text with no focus.
- There is a short time in which the agent wrote the file but its edit
  event did not arrive yet. A fetch in that time gets text with an edit that
  is not in the list, so the view can show that edit. The new event then
  causes a new fetch, which corrects the view. This time is about one poll
  of the transcript.

### Tests

1. Make `web/src/session/fileAt.test.ts`.
2. Make a text, apply two edits with known hunks, and test that `textAt`
   at a time before both edits gives the first text, between the edits
   gives the text after the first edit, and after both edits gives the
   last text.
3. Test an edit with two hunks in one file.
4. Test a text that a third party changed. Expect `ok: false`.
5. Test a file that an edit made (`created: true`) at a time before the
   edit. Expect `exists: false`.

## Task 6: Documents

### Procedure

1. In `README.md`, in the **Scrubber** item, change the speed text: "The
   speed button steps through 0.25×, 0.5×, 0.75×, 1×, 2×, 4×, 8×, and 16×.
   Click or Up goes faster, and Shift-click or Down goes slower. While the
   playhead is behind the live edge, the graph and the file view show the
   work up to the playhead, not the work after it. During a live session, a
   speed below 1× moves the playhead behind the live edge, and LIVE shows
   how far behind it is. Select 1× to play at the speed of the agent, 2× or
   faster to catch up, or LIVE to go to the live edge. The last speed of 1×
   or more is remembered per browser."

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository. Open the page. In the developer
   tools, remove `awv.speed` from `localStorage`, if it exists. Reload. The
   speed is 1× and LIVE is pressed.
3. Start a live session on this repository. Ask Claude Code to read three
   files and edit two functions.
4. While the agent works, Shift-click the speed button. The speed goes to
   0.75×, and the button is cyan. Playback does not pause. LIVE is not
   pressed. After a few seconds, LIVE shows a distance such as `LIVE −0:03`.
5. Shift-click two more times. The speed goes to 0.5×, then to 0.25×.
   Shift-click again. The speed stays at 0.25×. The distance on LIVE grows
   faster. Each edit types out more slowly than at 1×.
6. Click the speed button until 1×. The distance on LIVE stays almost the
   same while the agent works. The events show in the order and at the
   intervals at which the agent did them.
7. Wait until the agent stops and waits for your next prompt. Do not type.
   Within about 30 s plus the distance, the playhead gets to the live edge.
   LIVE is pressed again and shows `LIVE`.
8. Ask Claude Code for more work. Select 0.25× again. When the distance is
   more than 10 s, click the speed button to 2×. The distance becomes
   smaller. When it gets to 0, LIVE is pressed, and the speed stays 2×.
9. Select 0.5× again. When the distance is more than 5 s, click LIVE. The
   playhead goes to the live edge at once, and the speed is 1×.
10. Select 0.5×. Reload. The speed is the last speed of 1× or more (not
    0.5×), and LIVE is pressed.
11. Click the speed button until 16×. Click again. The speed stays at 16×.
12. Pick an older session in the session picker. Select 0.25×. The replay
    plays at 0.25×. LIVE shows no distance.
13. Pause a live session with the play button. Wait 5 s. Click play. The
    playhead plays at the current speed, behind the live edge, and LIVE
    shows the distance. (This is the old behavior. It must still operate.)
14. Use only the keyboard: Tab to the speed button. Press Down until the
    speed is 0.25×. Press Down again. The speed stays at 0.25×. Press Up
    until 16×. Left and Right do the same as Down and Up. The page does not
    scroll.
15. During a live session, select 0.25×. Ask Claude Code to add a new
    function to an existing Go file. The function does not show in the
    graph until the playhead gets to the edit and about 2 s more (the time
    the server takes to analyze the tree). Then it shows.
16. While the playhead is still behind, open the tab of that Go file. The
    file view does not show the new function. When the playhead passes the
    edit, the function shows in the file view.
17. Drag the playhead back before the edit. The file view no longer shows
    the function. The graph still shows the node (see the decision "A graph
    does not go back").
18. Ask Claude Code to make a new file. Select 0.25×, and open the tab of
    the new file before the playhead gets to its creation. The view says
    "This file does not exist yet at this time."
19. With your own editor, add three empty lines at the top of a file that
    the agent edited. Drag the playhead to before the agent's edit. The view shows the current text
    with the note "This file changed outside the agent's edits."
20. Pick an older session in the picker and open a file. The view shows
    the current text with no note, as before this plan.

## Other plans

- **Plan 03 (shared timeline).** If plan 03 is merged first, the speed
  state is in the `Playback` class in `web/src/ui/playback.ts`. Put the
  rules of Task 2 there, and use `web/src/session/speed.ts` from it. Change
  the speed list in plan 03 ("The speed list stays 1×, 2×, 4×, 8×, and
  16×") and its validation step 10 to match this plan. The timeline canvas
  of plan 03 can shade the part of the track between the playhead and the
  live edge, to show the buffer.
- **Plan 04 (shared code pane).** If plan 04 is merged first, the file
  fetch is in `CodePane.open`. Keep the list of edits there, and call
  `textAt` in `CodePane.render`, which already gets the state `s`.
- **Plan 15 (settings).** The `speed` setting keeps the valid values 1, 2,
  4, 8, and 16. A slow speed is not a setting, as it is not stored in
  `awv.speed`.
