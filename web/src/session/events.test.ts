import assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseLine, parseLog } from './events.ts';

// These lines match what internal/session writes; TestEventJSONFieldNames pins the Go side.
const log = [
  '{"t":0,"type":"session","root":"/r","branch":"main","t0":"2026-01-02T03:04:05Z","gapCap":30}',
  '{"t":1.5,"at":"2026-01-02T03:04:05Z","type":"read","agent":"main","path":"a.py","line":3,"nodeIds":["a.py\\u0000a.f"]}',
  '{"t":2,"at":"2026-01-02T03:04:05Z","type":"edit","agent":"main","path":"a.py","nodeIds":["k"],"hunks":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" a","+b"]}],"created":true}',
  '{"t":3,"at":"2026-01-02T03:04:05Z","type":"check_end","agent":"main","ok":false}',
  '{"t":4,"at":"2026-01-02T03:04:05Z","type":"plan","agent":"abc","text":"Fix it"}',
  '{"t":5,"at":"2026-01-02T03:04:05Z","type":"read","agent":"main","path":"README.md","line":1}',
  'not json',
  '{"t":6,"type":"mystery"}',
  '',
].join('\n');

test('parseLog reads the header, the events, and reports bad lines', () => {
  const parsed = parseLog(log);
  assert.deepEqual(parsed.header, { type: 'session', root: '/r', branch: 'main', t0: '2026-01-02T03:04:05Z', gapCap: 30 });
  assert.deepEqual(
    parsed.events.map((event) => event.type),
    ['read', 'edit', 'check_end', 'plan', 'read'],
  );
  assert.deepEqual(parsed.events[0], { t: 1.5, at: '2026-01-02T03:04:05Z', agent: 'main', type: 'read', path: 'a.py', line: 3, nodeIds: ['a.py\u0000a.f'] });
  assert.deepEqual(parsed.events[1], {
    t: 2,
    at: '2026-01-02T03:04:05Z',
    agent: 'main',
    type: 'edit',
    path: 'a.py',
    nodeIds: ['k'],
    hunks: [{ oldStart: 1, oldLines: 1, newStart: 1, newLines: 2, lines: [' a', '+b'] }],
    created: true,
  });
  assert.deepEqual(parsed.events[2], { t: 3, at: '2026-01-02T03:04:05Z', agent: 'main', type: 'check_end', ok: false });
  // A file-level read has no nodes.
  const fileLevel = parsed.events[4];
  assert.ok(fileLevel.type === 'read');
  assert.deepEqual(fileLevel.nodeIds, []);
  assert.equal(parsed.errors.length, 2);
  assert.ok(parsed.errors[0].startsWith('line 7:'));
  assert.ok(parsed.errors[1].includes('mystery'));
});

test('parseLine rejects values that are not objects', () => {
  assert.equal(parseLine('[1,2]').ok, false);
  assert.equal(parseLine('null').ok, false);
});
