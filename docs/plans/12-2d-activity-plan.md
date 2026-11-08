# Plan 12: 2D activity

This plan makes the 2D board show what the agent does, as it does it. A
reticle marks the callable that the agent works on. The blast radius shows
on the chips. Recent reads and edits glow and fade. The package and the file
of the agent stand out, and the other packages dim. The board also follows
live changes: a new analysis, another working tree, and another session.
The 2D view is still behind `?view=2d`.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 11.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Reticle

### Result

- Four corner brackets, 11 px long and 3 px thick, around the target chip,
  with a glow in the same color (`drop-shadow(0 0 5px currentColor)`).
- The color is `reticleTone(s)` (plan 05).
- On a read, the brackets move to the new chip in 0.3 s
  (`cubic-bezier(.2, .8, .2, 1)`) and stay 4 px outside it.
- On an edit, the brackets start 12 px outside the chip. They close to 4 px
  over `LOCK_T`, so they lock on.
- When the target changes during playback, one ring grows from the chip and
  fades in 0.9 s (a ping). The ring starts at the chip rectangle: a 2 px
  border in the reticle color, corner radius 8 px, opacity 0.9. It grows to
  1.9 times that size and ends at opacity 0, with `ease-out`. Remove the
  ring element at `animationend`.
- If the target chip is scrolled out of its file card, the brackets hide.
- With `prefers-reduced-motion`, the brackets move with no transition and no
  ping shows. The chips and the cards change with no transition.

### Procedure

1. Add the reticle element to `web/src/view2d/board.ts`.
2. Place it from the chip rectangle relative to the board, in each frame.

## Task 2: Blast radius, heat, and spotlight

### Result

- **Blast radius.** The blast radius starts at the latest edit that names a
  callable. At the hop 1 time of `blastStage` (plan 09), the callables 1 hop
  away get a coral outline: 1.5 px, solid, offset 1 px. At the hop 2 time,
  the callables 2 hops away get a dashed outline: 1 px, coral at 60%, offset
  1 px. Use `hopsFrom` in `web/src/graph/load.ts`.
- **Heat.** A chip fills with cyan after a read and with coral after an
  edit. The fill fades over `HEAT_FADE` seconds of session time (plan 09).
  Draw the fill as a pseudo-element over the whole chip: `--cyan` at 55%
  for a read, `--coral` at 60% for an edit. Its opacity is
  `0.25 + 0.75 × level` while `level` (from `heat`, plan 09) is above 0,
  and 0 after that. Squares get the same fill.
- **Spotlight.** The package card of the agent's file stands out, and the
  file card of the agent stands out in it:

  | Card | Border | Fill | Glow |
  | --- | --- | --- | --- |
  | Package | Package color at 75% | Package color at 8% | A 1 px ring at 35%, and a 28 px glow at 14% |
  | File | Package color | `--void` at 90% | A 1 px ring at 60%, and a 22 px glow at 30% |

  The other package cards have 42% opacity. The opacity changes in 0.4 s;
  the borders, fills, and glows change in 0.3 s. A file with no callable in
  the graph (for example a Markdown file) highlights no card.
- A legend line above the board shows: read, edited, 1 hop, 2 hops. Each
  item is an 18 × 12 px sample chip with that mark, then the word, in
  `--mono` 11 px `--mute`. The fit note of plan 10 is on the same line, at
  the right.

### Procedure

1. Add the classes and CSS for the outlines, the heat fill, and the
   spotlight.
2. Set the heat as a CSS variable on each chip. Change it only when its
   value changes by 0.01 or more.

## Task 3: Follow the agent

### Result

- When the target changes, the board scrolls the target chip into view in
  its file card, if that card scrolls.
- If the viewer scrolled a file card in the last 4 s, the board does not
  scroll it.
- The target always has a name (plan 10), so the reticle is on a named chip.

## Task 4: Live changes

### Result

| Change | What the board does |
| --- | --- |
| New analysis of the same tree (graph version changes) | Keeps the card rectangles if `layoutKey` (plan 09) is the same. A new callable shows in its file card. A new or removed file lays the cards out again. |
| OPEN of another working tree | Builds the board again from the new graph. |
| Another session in the picker | Shows the new session. Nothing else changes, because the board reads only `fold`. |
| LIVE | Follows the live edge, as the 3D view does. |

### Procedure

1. In `Graph2D.setGraph`, compare `layoutKey` with the last key.
2. If the key is the same, add or remove chips in the file cards. Do not
   move the cards.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository with the test session. Open
   `/?view=2d` and replay the session.
3. On each read, the reticle moves to the callable and a ping shows. On
   each edit, the reticle closes onto the callable.
4. The reticle is cyan on a read, coral during an edit, cyan during a
   command, and lime after a pass.
5. After an edit, its callers and callees get a coral outline a short time
   after the diff starts. Callables 2 hops away get a dashed outline after
   that.
6. A chip that the agent read glows cyan and fades in about 20 s of session
   time. An edited chip glows coral.
7. The package and file of the agent stand out. The other packages are dim.
8. Open a working tree with more than 800 NODES and a session for it,
   if you have one. Scroll a file card that the agent does not use. It does
   not scroll back for 4 s.
9. Start a live session on this repository. Ask Claude Code to add a
   function to an existing file. About 2 s after the edit, the new chip
   shows. No card moves.
10. Ask Claude Code to make a new Go file with a function. The cards lay out
    again and the new file shows.
11. Open another working tree, then this repository again. The board
    changes each time.
12. Pick an older session in the session picker. The board shows that
    session. Select LIVE. The board shows the live session.
13. Turn on reduced motion in the operating system. Reload. The reticle
    moves with no animation and no ping shows.
14. Open `/?view=3d`. The 3D view is the same as before this plan.
