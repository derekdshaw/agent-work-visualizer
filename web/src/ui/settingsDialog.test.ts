import assert from 'node:assert/strict';
import { test } from 'node:test';
import { dropIndex } from './settingsDialog.ts';

// Four rows, 20 px high, with their middles at 10, 30, 50, and 70.
const middles = [10, 30, 50, 70];

test('a drag up into the lower half of a row drops after that row', () => {
  // Row 3 to y 55, under the middle of row 2: the line shows under row 2,
  // so the color goes between rows 2 and 3, which is where it was.
  assert.equal(dropIndex(middles, 3, 55), 3);
  // Row 3 to y 35, under the middle of row 1: between rows 1 and 2.
  assert.equal(dropIndex(middles, 3, 35), 2);
});

test('a drag up above the middle of the first row drops at the top', () => {
  assert.equal(dropIndex(middles, 3, 5), 0);
});

test('a drag down into the lower half of a row drops after that row', () => {
  assert.equal(dropIndex(middles, 0, 55), 2);
  assert.equal(dropIndex(middles, 0, 90), 3);
});

test('a drag that stays near its own row does not move', () => {
  assert.equal(dropIndex(middles, 1, 25), 1);
  assert.equal(dropIndex(middles, 1, 45), 1);
});
