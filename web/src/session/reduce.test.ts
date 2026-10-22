import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { EditEvent, Ev, Hunk } from './events.ts';
import { countAt, fold, heatTarget, IDLE_AFTER, LOCK_T, MAX_TYPE, MIN_TYPE, nextEditTime, type NodeFlags, previousEditTime, typingDuration } from './reduce.ts';

const base = { at: '2026-01-02T03:04:05Z', agent: 'main' };

// 270 added characters type out in 3 seconds.
const hunks: Hunk[] = [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 4, lines: [' fn a() {', '-  old', `+${'x'.repeat(135)}`, `+${'y'.repeat(135)}`] }];

function edit(t: number, nodeIds: string[], path = 'a.rs', lines = hunks): EditEvent {
  return { ...base, t, type: 'edit', path, nodeIds, hunks: lines, created: false };
}

const session: Ev[] = [
  { ...base, t: 0, type: 'prompt', text: 'fix it' },
  { ...base, t: 1, type: 'title', text: 'Fix the parser' },
  { ...base, t: 2, type: 'read', path: 'a.rs', line: 1, nodeIds: ['A'] },
  { ...base, t: 3, type: 'plan', text: 'split it' },
  edit(5, ['B', 'C']),
  { ...base, t: 12, type: 'check_start', text: 'cargo check' },
  { ...base, t: 14, type: 'check_end', ok: true },
  { ...base, t: 15, type: 'read', path: 'b.rs', line: 9, nodeIds: ['D'] },
  edit(16, ['D'], 'b.rs'),
  // An edit without callables, such as a config file, leaves the radius on D.
  edit(20, [], 'vite.config.ts'),
  { ...base, t: 30, type: 'check_start', text: 'cargo test' },
  { ...base, t: 31, type: 'check_end', ok: false },
];

function flags(partial: Partial<NodeFlags>): NodeFlags {
  return { hop: 99, reading: false, touched: false, edited: false, ...partial };
}

test('phase boundaries follow the latest event', () => {
  const cases: Array<[number, string]> = [
    [-1, 'idle'],
    [0, 'plan'],
    // A title does not change the phase.
    [1.5, 'plan'],
    [1.999, 'plan'],
    [2, 'read'],
    [3, 'plan'],
    [5, 'edit'],
    [11.999, 'edit'],
    [12, 'check'],
    [14, 'clean'],
    [15, 'read'],
    [16, 'edit'],
    [30, 'check'],
    [31, 'fail'],
    [31 + IDLE_AFTER, 'fail'],
    [31 + IDLE_AFTER + 0.001, 'idle'],
  ];
  for (const [t, phase] of cases) assert.equal(fold(session, t).phase, phase, `phase at ${t}`);
});

test('the reticle acquires reads and locks onto edits', () => {
  assert.equal(fold(session, 1).target, null);
  const reading = fold(session, 2.5);
  assert.equal(reading.target, 'A');
  assert.equal(reading.locked, false);
  assert.equal(reading.sinceTarget, 0.5);
  const editing = fold(session, 6);
  assert.equal(editing.target, 'B');
  assert.equal(editing.locked, true);
  // A read after an edit moves the reticle, and the edit stays the latest edit.
  const after = fold(session, 15.5);
  assert.equal(after.target, 'D');
  assert.equal(after.locked, false);
  assert.equal(after.edit?.event.nodeIds[0], 'B');
});

test('an edit types out after the lock', () => {
  assert.equal(typingDuration(edit(0, ['B'])), 3);
  assert.equal(typingDuration(edit(0, ['B'], 'a.rs', [])), MIN_TYPE);
  assert.equal(typingDuration(edit(0, ['B'], 'a.rs', [{ ...hunks[0], lines: [`+${'z'.repeat(5000)}`] }])), MAX_TYPE);
  assert.equal(fold(session, 4.9).edit, null);
  assert.equal(fold(session, 5 + LOCK_T / 2).edit?.progress, 0);
  assert.ok(Math.abs((fold(session, 5 + LOCK_T + 1.5).edit?.progress ?? 0) - 0.5) < 1e-9);
  assert.equal(fold(session, 5 + LOCK_T + 3.01).edit?.progress, 1);
  assert.equal(fold(session, 11).edit?.progress, 1);
});

test('heat per hop ramps out from the edit target and dims when settled', () => {
  const at = (t: number, node: Partial<NodeFlags>) => heatTarget(fold(session, t), flags(node));
  // Locking on: only the target glows.
  assert.equal(at(5.5, { hop: 0 }), 0.7);
  assert.equal(at(5.5, { hop: 1 }), 0);
  assert.equal(at(5.5, { hop: 2 }), 0);
  // Typing: hop 1 follows half a second after the lock, hop 2 a second later.
  assert.equal(at(5 + LOCK_T + 0.6, { hop: 0 }), 1);
  assert.equal(at(5 + LOCK_T + 0.6, { hop: 1 }), 0.75);
  assert.equal(at(5 + LOCK_T + 0.6, { hop: 2 }), 0);
  assert.equal(at(5 + LOCK_T + 1.6, { hop: 2 }), 0.5);
  assert.equal(at(5 + LOCK_T + 1.6, { hop: 3 }), 0);
  // Another node of the same edit glows a little less than the target.
  assert.equal(at(5 + LOCK_T + 0.6, { hop: 1, touched: true }), 0.8);
  // Typed out, then checking: the target holds.
  assert.equal(at(11, { hop: 0 }), 0.8);
  assert.equal(at(13, { hop: 0 }), 0.8);
  assert.equal(at(13, { hop: 2 }), 0.5);
  // A passed check dims the radius.
  assert.equal(at(14.5, { hop: 0 }), 0.45);
  assert.equal(at(14.5, { hop: 1 }), 0.35);
  assert.equal(at(14.5, { hop: 2 }), 0.2);
  // A read target glows, more while reading.
  assert.equal(at(15.5, { reading: true }), 0.55);
  // An edited node keeps a trace far from the current edit.
  assert.equal(at(25, { hop: 99, edited: true }), 0.22);
  // A file-level edit does not reset the radius of the last node edit.
  assert.equal(at(21, { hop: 0 }), 0.8);
  assert.equal(at(4, { hop: 0 }), 0);
});

test('scrubbing to any time equals folding the events up to that time', () => {
  // A later fold must not leave state behind for an earlier one.
  const late = fold(session, 40);
  for (let t = -2; t <= 60; t += 0.37) {
    const prefix = session.filter((event) => event.t <= t);
    assert.deepEqual(fold(session, t), fold(prefix, t), `state at ${t}`);
    assert.equal(fold(session, t).count, prefix.length, `count at ${t}`);
  }
  assert.deepEqual(fold(session, 40), late);
});

test('files list the most recently edited last', () => {
  const again: Ev[] = [edit(1, ['A'], 'a.rs'), edit(2, ['B'], 'b.rs'), edit(3, ['A'], 'a.rs')];
  assert.deepEqual(fold(again, 5).files, ['b.rs', 'a.rs']);
});

test('the state collects titles, edited nodes, files, and checks', () => {
  const s = fold(session, 13);
  assert.equal(s.title, 'Fix the parser');
  assert.deepEqual([...s.edited], ['B', 'C']);
  assert.deepEqual(s.files, ['a.rs']);
  assert.deepEqual(s.check, { text: 'cargo check', state: 'running' });
  assert.deepEqual(fold(session, 14).check, { text: 'cargo check', state: 'ok' });
  const end = fold(session, 32);
  assert.deepEqual(end.files, ['a.rs', 'b.rs', 'vite.config.ts']);
  assert.equal(end.edit?.event.path, 'vite.config.ts');
  assert.equal(end.nodeEdit?.event.nodeIds[0], 'D');
  assert.deepEqual([...end.edited], ['B', 'C', 'D']);
  assert.deepEqual(end.check, { text: 'cargo test', state: 'fail' });
});

test('countAt finds the boundary', () => {
  for (const t of [-1, 0, 0.5, 2, 5, 31, 100]) {
    assert.equal(countAt(session, t), session.filter((event) => event.t <= t).length, `count at ${t}`);
  }
  assert.equal(countAt([], 5), 0);
});

test('edit jumps step through the edits', () => {
  // Edits are at 5, 16, and 20.
  assert.equal(nextEditTime(session, -1), 5);
  assert.equal(nextEditTime(session, 5), 16);
  assert.equal(nextEditTime(session, 12), 16);
  assert.equal(nextEditTime(session, 20), null);
  assert.equal(previousEditTime(session, 25), 20);
  // Just after an edit starts, back goes to the edit before it.
  assert.equal(previousEditTime(session, 20.2), 16);
  assert.equal(previousEditTime(session, 16), 5);
  assert.equal(previousEditTime(session, 5.3), null);
  assert.equal(nextEditTime([], 0), null);
});
