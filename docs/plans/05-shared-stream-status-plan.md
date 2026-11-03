# Plan 05: Shared stream, status, and format helpers

This plan splits what is left of `web/src/ui/panes.ts` into small modules:
the agent stream, the blast radius list, the phase colors and names, and
the format helpers. After this plan, `panes.ts` does not exist. The visible
behavior does not change.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Plan 04 (it moves the code pane out of `Panes` first).

## Before you start

1. Make a new branch from `main`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`. Make
   sure that they pass.

## Task 1: Move the format helpers

### Problem

`fmt`, `el`, `firstLine`, and `describe` are in `web/src/ui/panes.ts`. The
2D view and the Now panel need them too.

### Result

- `web/src/ui/format.ts` exports `fmt`, `el`, `firstLine`, and `describe`.
- `describe` returns the same rows as now: tag, tone, and text.

### Procedure

1. Make `web/src/ui/format.ts`. Move the four functions into it.
2. Change the imports in the other modules.

### Tests

1. Make `web/src/ui/format.test.ts`.
2. Test `fmt` for 0 s, 61 s, and 3661 s. Expect `00:00`, `01:01`, and
   `1:01:01`.
3. Test `describe` for each event type. Make sure that a subagent event has
   the subagent ID in its text and that a title has no row.

## Task 2: Move the phase looks

### Problem

`renderStatus` has a table of phase colors and names. The 3D reticle in
`main.ts` (line 683) picks colors with its own rules. The 2D view and the
Now panel need the same colors and names.

### Result

- `web/src/ui/phase.ts` exports `PHASE_LOOKS`: for each `Phase`, a CSS color
  variable and a name (`IDLE`, `READING`, `PLANNING`, `EDITING`,
  `VERIFYING`, `CLEAN`, `FAILED`). While a diff types out, the name of the
  `edit` phase is `STREAMING`, as now.
- `web/src/ui/phase.ts` exports `reticleTone(s)`. It returns `cyan` before
  the lock, `lime` when the phase is `clean`, `cyan` when the phase is
  `check`, and `coral` in all other cases. This is the rule of `main.ts`
  line 683.
- The status line of the code pane and the 3D reticle use these exports.

### Procedure

1. Make `web/src/ui/phase.ts`.
2. In `web/src/ui/codePane.ts`, use `PHASE_LOOKS` in `renderStatus`.
3. In `web/src/main.ts`, use `reticleTone` for `reticleColor`.

### Tests

1. Make `web/src/ui/phase.test.ts`. Test `reticleTone` for each phase, locked
   and not locked.

## Task 3: Move the stream and the blast list

### Result

- `web/src/ui/stream.ts` has a `Stream` class with `render(events, s,
  label)`. It is the code of `renderLog`, with `LOG_ROWS` and
  `THINK_LOOKAHEAD`.
- `web/src/ui/blast.ts` has a `BlastList` class with `render(items, heat,
  empty)`. It is the code of `renderBlast`. Only the 3D view uses it.
- `web/src/ui/panes.ts` is removed.

### Procedure

1. Make the two modules. Move the code.
2. In `web/src/main.ts`, make one `Stream` and one `BlastList`. Remove
   `Panes`.
3. Remove `web/src/ui/panes.ts`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Validation

All steps must pass. The behavior must be the same as on `main`.

1. `npm --prefix web test` and `npm --prefix web run typecheck` pass.
2. `web/src/ui/panes.ts` does not exist.
3. Start the server on this repository with the test session. Replay it.
4. The stream shows the same rows as on `main`: ASK, PLAN, THINK, READ,
   EDIT, RUN, OK or FAIL. A thought shows from the time it began.
5. The blast radius list shows the callers and callees of the edit target.
   They light in order.
6. The status line shows the phase name and color. During a command it
   shows VERIFYING in cyan. After a pass it shows CLEAN in lime.
7. The 3D reticle is cyan on a read, coral during an edit, and lime after a
   pass.
