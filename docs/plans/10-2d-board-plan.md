# Plan 10: 2D board

This plan draws the 2D board: a card for each package, a card for each file
in it, and a chip for each callable in a file. Each file shows the names
that fit, then small squares for the other callables. The board shows the
state of the session at the playhead, but it has no motion yet. The board
is still behind `?view=2d`.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 09.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Draw the cards and the chips

### Result

`web/src/view2d/board.ts` has a `Board` class. It makes these elements:

- **Package card.** A border and a top line in the package color. A header
  with the last part of the package path in capitals, the rest of the path
  in gray, and the counts (`17 files · 273 fn`).
- **File card.** A dark card with a header: the file label (plan 09) and
  the counts of reads and edits so far, in cyan and coral.
- **Chip.** A rounded box with the display name in the monospace font. For a
  Go or Rust method, the type part (`HashIndex.`) is gray and the method
  name is light. An inline test has a dashed border.

The package colors come from `packageColors` in
`web/src/settings/palette.ts`, in package name order. The chip borders are
gray, not the package color, so an orange package does not look like the
coral of an edit. The chip background is the package color at 8% opacity.

The cards are absolute boxes at the rectangles of plan 09. The board fills
its host. The page does not scroll.

Use the font stacks of the page (`--ui` and `--mono` in
`web/src/style.css`). Do not load a web font. Measure the chip widths after
`document.fonts.ready`.

### Dimensions

The values marked † come from `web/src/view2d/dims.ts` (plan 09). Set them
from that module as CSS variables on the board. Do not type them again in
the CSS. `k` is the chip scale of Task 2. The values marked `× k` change
with the scale. The other values do not.

Each package card sets `--pc` to its color as an `r, g, b` triple. The CSS
uses `rgba(var(--pc), a)` for the package color at opacity `a`. For a gray
at an opacity, use `color-mix(in srgb, var(--mute) N%, transparent)`.

**Package card**

| Part | Value |
| --- | --- |
| Border | 1 px, package color at 28% |
| Top border | 2 px, package color at 80% |
| Corner radius | 8 px |
| Fill | package color at 4% |
| Padding | 8 px |
| Gap between the header and the file cards | 6 px |
| Header: last part of the path | `--ui`, 600, 13 px, capitals, letter spacing 0.12 em, package color |
| Header: rest of the path | `--mono`, 400, 11 px, `--mute` |
| Header: counts | `--mono`, 400, 10 px, `--mute`, tabular numbers, at the right end |
| Narrower than 240 px | Hide the rest of the path and the counts (a container query) |

**File card**

| Part | Value |
| --- | --- |
| Border | 1 px, `--mute` at 16% |
| Corner radius | 6 px |
| Fill | `--void` at 55% |
| Padding | 6 px top, 8 px sides, 7 px bottom |
| Gap between the header and the chips | 5 px |
| Header: file label | `--mono`, 500, 11 px, `--ink` at 85% |
| Header: counts | `--mono`, 10 px, tabular numbers, at the right end. Reads in `--cyan`, edits in `--coral`, 8 px apart |
| Header: `scroll` after the label | `--mute`, 400 |
| Chip area padding | 2 px |
| Narrower than 170 px | Hide the counts (a container query) |

**Chip (named)**

| Part | Value |
| --- | --- |
| Height | `CHIP_H` † × k |
| Gap between chips | `CHIP_GAP` † × k |
| Side padding | 9 px × k |
| Corner radius | 5 px |
| Border | 1 px, `--mute` at 22% |
| Fill | package color at 8% |
| Text | `--mono`, 400, 11 px × k, line height 22 px × k, `--ink` at 78%, one line, ellipsis |
| Type part of a method (`HashIndex.`) | `--mute` |
| Inline test | Dashed border, fill package color at 3% |
| Pointer on the chip, or focus | Text `--ink`, fill package color at 24% |

**Square** (a callable that has no name)

| Part | Value |
| --- | --- |
| Size | The current value of `SQUARE_SIZES` (plan 09) for that file card |
| Gap | 5 px at 13 px squares, 3 px at smaller squares |
| Corner radius | 3 px |
| Border, fill | The same as a named chip |
| Text | None. The tooltip of plan 13 gives the name. |

**State marks** (Task 3). They apply to named chips and to squares.

| Mark | Named chip | Square |
| --- | --- | --- |
| Read once | A 2 px cyan line at the bottom, 2 px from the edge, 5 px in from each side, at 55% | The same line, 1 px from the bottom, 2 px in from each side |
| Read two or more times | The same line at 100% | The same line at 100% |
| Edited | A 3 px coral bar on the inside of the left edge. Text `--ink` | The same bar |
| Target | Fill package color at 45%, border `--ink` at 50%, text `--ink` | Not possible: the target always has a name |

The state marks do not change the size of a chip. Use `box-shadow`,
`outline`, and pseudo-elements, not borders or padding.

### Procedure

1. Make `web/src/view2d/board.ts`.
2. Make `web/src/view2d/board.css`, or add a section to
   `web/src/style.css`. Use only the CSS variables of the page for colors.
3. In `web/src/view/graph2d.ts`, replace the placeholder text with a
   `Board`.

### Tests

1. Run `npm --prefix web typecheck`.

## Task 2: Fit the names

### Result

- **Scale.** The board looks for the largest chip scale from 0.85 to 1.35
  at which every file shows every name. If no scale in that range is
  sufficient, the scale is 0.92 and each file names what it can.
- **Names, then squares.** In each file, the callables in the order of
  `nameOrder` (plan 09) get a name while the names fit. The other
  callables are squares. The named chips come first, in source order. The
  squares follow, in source order.
- **Square size.** The squares start at 13 px. If they do not fit with no
  names, they shrink to 10, 8, then 6 px.
- **Scroll.** If even 6 px squares do not fit, that file card scrolls. Its
  header shows `scroll` after the file name.
- **The target always has a name**, also in a card that scrolls.
- **Note.** A line above the board says how many files show squares and how
  many scroll, for example "Names where they fit; 12 files show the rest as
  squares and 2 scroll."
- **Cost.** The board fills a file card again only when the ranks of its
  callables change, or when the layout changes. A frame with no change in
  rank costs no layout work.

### Procedure

1. Add `fit()` and `fill(fileCard, rank)` to `Board`.
2. Use a binary search on the number of names in each file.
3. Set `contain: layout` on each file card, so that a fill does not lay
   out the full page.

### Tests

1. The rules come from plan 09 and have tests there. Do the manual test in
   "Validation".

## Task 3: Show the state at the playhead

### Result

`Graph2D.frame` gives the board the `ViewState`. The board shows the state
marks of Task 1 (read line, edit bar, target fill) and the read and edit
counts in each file header.

A move of the playhead back in time removes the marks of the later events,
because all of this comes from `fold`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. Start the server on this repository with the test session. Open
   `/?view=2d`.
3. The board shows one card for each package. In this repository, the
   packages are directories, for example `internal/server`.
4. Each package card holds its files. Each file card shows names, and
   squares if the names do not all fit. No card is smaller than 130 × 62 px.
   No name is cut off at the bottom of its card.
5. In the browser developer tools, measure a chip at the current scale `k`
   (the `--k` variable on the board). Its height is 24 × k px. Measure a
   13 px square, a file card padding, and a package card border. They match
   the Dimensions tables.
6. The page does not scroll. No part of the board is cut off at the bottom.
7. Replay the session. The cards of the files that the agent read get cyan
   lines and read counts. The edited callable gets a coral bar.
8. Drag the playhead back to the start. The marks go away.
9. Make the window smaller and larger. The board fills the space again.
10. Open a working tree with more than 800 NODES. Most file cards show
    squares. A few file cards may scroll. The text of the note matches the
    board.
11. In the browser developer tools, the network panel shows no requests to
    other hosts.
12. In the performance panel, record 5 s of playback on the large working
    tree. No frame takes more than 16 ms.
13. Open `/?view=3d`. The 3D view is the same as before this plan.
