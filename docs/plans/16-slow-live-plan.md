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
- **The speed button does not wrap.** Click goes one step faster, up to
  16×. Shift-click goes one step slower, down to 0.25×. With eight speeds, a
  wrap from 16× to 0.25× would surprise the viewer. This replaces the wrap
  from 16× to 1×.
- **The agent does not slow down.** A slow speed changes only the page.
  PAUSE AGENT still holds the agent.
- **Not in this plan.** The server sends a new graph and the code pane
  reads the file from the disk at the real time, not at the time of the
  playhead. Thus, while the playhead is behind the live edge, the graph can
  show a callable that the agent adds later on the timeline, and the file
  view can show text that the agent writes later. This is already true
  after a pause or a scrub. The typed diff comes from the events, so it is
  correct.

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

### Tests

1. Make `web/src/session/speed.test.ts`.
2. Test `stepSpeed` up from 0.25 to 16, and that it stays at 16. Test it
   down from 16 to 0.25, and that it stays at 0.25.
3. Test `speedLabel` for each speed.
4. Test `storedSpeed` with `'0.5'`, `'4'`, `'3'`, `''`, and `'abc'`. Expect
   1, 4, 1, 1, and 1.

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
- `setSpeedUI` sets `data-slow` on the button for a speed below 1, as it
  sets `data-fast` for a speed above 1. The title says: "Playback speed
  0.5×. Click for faster, Shift-click for slower."
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

## Task 4: Documents

### Procedure

1. In `README.md`, in the **Scrubber** item, change the speed text: "The
   speed button steps through 0.25×, 0.5×, 0.75×, 1×, 2×, 4×, 8×, and 16×.
   Click goes faster and Shift-click goes slower. During a live session, a
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

## Other plans

- **Plan 03 (shared timeline).** If plan 03 is merged first, the speed
  state is in the `Playback` class in `web/src/ui/playback.ts`. Put the
  rules of Task 2 there, and use `web/src/session/speed.ts` from it. Change
  the speed list in plan 03 ("The speed list stays 1×, 2×, 4×, 8×, and
  16×") and its validation step 10 to match this plan. The timeline canvas
  of plan 03 can shade the part of the track between the playhead and the
  live edge, to show the buffer.
- **Plan 15 (settings).** The `speed` setting keeps the valid values 1, 2,
  4, 8, and 16. A slow speed is not a setting, as it is not stored in
  `awv.speed`.
