# Plan 06: Shared panel dividers

This plan changes `web/src/ui/split.ts` so that one function binds any
divider. A divider can be vertical (it changes a width) or horizontal (it
changes a height). The 3D view keeps its two dividers. The 2D view uses the
same function in plan 11 for its column width and for the line between the
stream and the code pane.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing.

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.

## Task 1: Put the size rule in a pure function

### Problem

`bindPanes` (`web/src/ui/split.ts:15`) knows only the left and the right
panel of the 3D layout. Its clamp rules are inside the event handlers, so no
test covers them.

### Result

`web/src/ui/split.ts` exports `clampSize(size, range, total, others, minRest)`.
It returns the size in `range` that leaves at least `minRest` px for the
area that the divider does not control. `others` is the sum of the other
fixed sizes on the same axis, including the dividers.

### Procedure

1. Add `clampSize` to `web/src/ui/split.ts`.
2. Use it in place of the clamp calls in `applyPanes`.

### Tests

1. Make `web/src/ui/split.test.ts`.
2. Test a size below the range, in the range, and above the range.
3. Test a small `total` where the range minimum and `minRest` cannot both
   apply. Make sure that `minRest` wins. Make sure that the result is never
   negative.

## Task 2: Bind any divider

### Result

`web/src/ui/split.ts` exports `bindDivider(options)`. The options are:

| Option | Meaning |
| --- | --- |
| `handle` | The divider element. |
| `container` | The element whose size the divider shares. |
| `axis` | `'x'` for a width, `'y'` for a height. |
| `side` | `'start'` if the panel is before the divider, `'end'` if after. |
| `cssVar` | The CSS variable on `container` that holds the size. |
| `range` | The minimum and maximum size in px. |
| `initial` | The size for a reset. |
| `load` | A function that returns the saved size, or `null`. |
| `save` | A function that saves the size. |
| `minRest` | The minimum size in px of the area that is left. |
| `others` | A function that returns the other fixed sizes on the axis. |

- A drag moves the divider. The drag uses pointer capture.
- Left and Right (or Up and Down for `'y'`) move the divider 16 px. With
  Shift they move 64 px.
- Home and a double click reset the size to `initial`.
- The handle has `role="separator"`, the correct `aria-orientation`, and
  `aria-valuenow`, `aria-valuemin`, and `aria-valuemax`.
- `bindDivider` does not use `localStorage` itself. The caller saves the
  size with `load` and `save`, so two dividers can share one key. Read and
  write `localStorage` in try/catch.
- `bindDivider` returns an object with `apply()` and `dispose()`. `apply()`
  sets the size again, for example after a window resize. `dispose()`
  removes the event listeners.

### Procedure

1. Add `bindDivider` to `web/src/ui/split.ts`.
2. Change `bindPanes` to call `bindDivider` two times, for `splitL` and
   `splitR`.
3. Keep the current keys and defaults: `awv.panes`, left 290 px in
   [200, 640], right 390 px in [260, 1100], and a minimum of 320 px for the
   graph.
4. A saved `awv.panes` value has the shape `{ l, r }`. Keep this shape, so
   the widths that viewers saved before still apply. The `load` and `save`
   functions of the left divider read and write `l`. The functions of the
   right divider read and write `r`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Validation

All steps must pass.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. On `main`, open the page, drag both dividers, and reload. Then switch to
   this branch and reload. The panel widths are the same.
3. Drag each divider. The panel width follows the pointer.
4. Focus each divider with Tab. Press Left and Right, then Shift+Left. The
   width changes by 16 px, then by 64 px.
5. Press Home on a divider, and double-click a divider. The width goes back
   to the default.
6. Make the window narrow until the graph is 320 px wide. A drag cannot make
   the graph narrower.
7. Make the window narrower than 980 px. The dividers go away and the panels
   stack, as on `main`.
