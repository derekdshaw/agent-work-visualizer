# Plan 09: Session state and 2D model

This plan adds the pure logic of the 2D view. It does not draw anything. It
adds read counts and times to the session state, and it adds modules that
build the card tree, lay out the cards, and rank the callables. Tests with
synthetic graphs of 1,000 callables cover all of it.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 08. The modules are pure, so this plan can go at the same time as
plan 07.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.

## Task 1: Add read and edit history to the session state

### Problem

`ViewState` (`web/src/session/reduce.ts:30`) has `edited`, a set of the
edited nodes. The 2D view also needs how many times the agent read each
node, and when the agent last read and last edited each node.

### Result

`ViewState` has three new fields. `fold` sets them from the events at or
before `t`:

- `reads: ReadonlyMap<string, number>`: the number of reads of each node.
- `lastRead: ReadonlyMap<string, number>`: the time of the last read.
- `lastEdit: ReadonlyMap<string, number>`: the time of the last edit.

A read or an edit counts for each ID in its `nodeIds`.

### Procedure

1. Add the fields to `ViewState`.
2. In `fold` (`web/src/session/reduce.ts:134`), fill the maps in the event
   loop.

### Tests

1. In `web/src/session/reduce.test.ts`, add a test with two reads of one
   node and one edit of it. Fold at a time between the reads, and after the
   edit. Check all three maps.

## Task 2: Build the card tree

### Result

`web/src/view2d/model.ts` exports `buildTree(graph)`. It returns packages,
each with files, each with callables:

- Packages come from `node.package` (plan 08). They are in name order.
- Files are in path order in each package.
- Callables are in line order in each file. Inline tests go last, in line
  order. An inline test is a callable whose display name starts with
  `tests.`.
- The display name of a callable is its qualified name without the parts
  that spell its file path. Compare the parts in order with the parts of
  the file path, without the extension. Remove a qualified-name part when it
  is equal to the next path part. Skip the path parts that are not equal.
  Keep at least the last part. For
  example: `crates/a/src/index.rs` and `crates.a.src.index.hold_lock` give
  `hold_lock`. `src/models/container.rs` and
  `models.container.HashIndex.new` give `HashIndex.new`.
- An inline test shows its display name without the `tests.` prefix.
- The file label is the file name. If another file in the same package has
  the same name (for example `mod.rs`), the label is the last two parts of
  the path (`parser/mod.rs`).

### Tests

1. Make `web/src/view2d/model.test.ts`.
2. Test the two display name examples above, and a Go method
   (`internal/server/view.go`, `internal.server.rootView.setGraph` gives
   `rootView.setGraph`).
3. Test that inline tests go last.
4. Test the file label rule with two `mod.rs` files.

## Task 3: Lay out the cards

### Problem

The board must fill its area with no gap and no scroll of the page. A large
package must get more area than a small one. Cards must not jump when the
agent adds a function.

### Result

`web/src/view2d/treemap.ts` exports:

- `layout(items, rect, minArea)`: an ordered squarified treemap. It keeps
  the input order. It fills `rect` with no overlap. Each item gets at least
  `minArea` px², if `rect` is large enough for all of the minimums.
- `weigh(tree, chipWidth)`: the weight of each file and package.
  `chipWidth(callable)` is the width of a named chip at scale 1. The content
  area of a file is the sum of `(chipWidth + CHIP_GAP) × CHIP_PITCH` for
  its callables, times `CONTENT_SLACK`. With `s` the square root of the
  content area, the file weight is `(s + FILE_FRAME_X) × (s + FILE_FRAME_Y)`.
  With `s` the square root of the sum of its file weights, the package
  weight is `(s + PKG_FRAME_X) × (s + PKG_FRAME_Y)`.
- `layoutKey(tree, width, height)`: a string of the file paths and the
  board size.

Put the constants in `web/src/view2d/dims.ts`. Plan 10 uses the same
module for the board CSS, so the weights and the drawn cards agree.

| Name | Value | Meaning |
| --- | --- | --- |
| `BOARD_PAD` | 12 px | Board padding. |
| `PKG_GAP` | 10 px | Gap between package cards. |
| `FILE_GAP` | 6 px | Gap between file cards. |
| `PKG_MIN` | 240 × 110 px | Minimum package card. |
| `FILE_MIN` | 130 × 62 px | Minimum file card. |
| `CHIP_H` | 24 px | Chip height at scale 1. |
| `CHIP_GAP` | 5 px | Gap between chips at scale 1. |
| `CHIP_PITCH` | 29 px | `CHIP_H + CHIP_GAP`: the height of one row of chips. |
| `CONTENT_SLACK` | 1.3 | Extra area for the space at the ends of rows that do not fill. |
| `FILE_FRAME_X` | 22 px | The width of a file card outside its chips: padding, border, and chip area padding. |
| `FILE_FRAME_Y` | 44 px | The height of a file card outside its chips: padding, border, header, and the gap under it, with a small margin. |
| `PKG_FRAME_X` | 26 px | The width of a package card outside its file cards: padding, border, and file gaps. |
| `PKG_FRAME_Y` | 52 px | The height of a package card outside its file cards: padding, borders, header, and the gap under it, with a small margin. |

If plan 10 changes a padding, a border, or a header height, change the
frame values here too. If they do not agree, the weights give some file
cards too little area and others too much. Validation step 5 of plan 10
measures the drawn sizes against these values.

The stability rule: the board lays out the cards again only when
`layoutKey` changes. Thus, a new callable in a file does not move any card.
A new file, a removed file, or a new board size moves the cards.

### Procedure

1. Make `web/src/view2d/dims.ts` with the constants above.
2. Make `web/src/view2d/treemap.ts`.
3. Raise the weight of a small item until it gets its minimum area. Do this
   in at most three passes. Then lay out.

### Tests

1. Make `web/src/view2d/treemap.test.ts`.
2. Make a synthetic tree of 12 packages, 90 files, and 1,000 callables. Use
   a seeded random generator (`mulberry32` from `web/src/graph/layout.ts`).
3. Lay it out in 1200 × 700 px. Make sure that:
   - the cells cover the area, with a total area error below 1%;
   - no two cells overlap;
   - each cell has at least its minimum area;
   - the cells keep the input order.
4. Add a callable to one file. Make sure that `layoutKey` does not change.
5. Add a file. Make sure that `layoutKey` changes.

## Task 4: Rank the callables

### Result

`web/src/view2d/rank.ts` exports:

- `TARGET_RANK = 1e6` and `rank(id, s)`. The rank is `TARGET_RANK` for the
  target of `s`, plus 1000 for each edit, plus 10 for each read (at most 99
  reads count).
- `nameOrder(ids, rank)`: the IDs in the order in which they get a name. The
  highest rank comes first. Equal ranks keep the source order.
- `SQUARE_SIZES = [13, 10, 8, 6]`: the square sizes in px, from large to
  small.
- `HEAT_FADE = 20` and `heat(id, s)`: `{ kind, level }`. `level` falls from
  1 to 0 over `HEAT_FADE` seconds after the last read or edit. `kind` is
  `'edit'` if the edit heat is at least the read heat, else `'read'`.
- `blastStage(s)`: which hops of the latest edit show. Hop 1 shows at
  `LOCK_T + 0.5` s after the edit. Hop 2 shows at `LOCK_T + 1.5` s. These are
  the times of `heatTarget` in `web/src/session/reduce.ts`.

### Tests

1. Make `web/src/view2d/rank.test.ts`.
2. Test that the target comes first, then edited callables, then read
   callables, then the rest in source order.
3. Test `heat` at 0 s, 10 s, and 25 s after a read.
4. Test `blastStage` before and after each hop time.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. The new tests run in less than 2 s in total.
3. `web/src/view2d/` has no import of `three` and no use of `document`.
4. The page looks and acts the same as before this plan. Open the page and
   replay the test session to make sure.
