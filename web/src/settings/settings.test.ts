import assert from 'node:assert/strict';
import { test } from 'node:test';
import { type Options, parseOptions, parseSettings } from './settings.ts';

const defaults = { version: 1, skipWaiting: true, colorScheme: 'default', autoRotate: true, followEdit: true, speed: 1 };
const options: Options = {
  defaults,
  speeds: [1, 2, 4, 8, 16],
  colorSchemes: [
    { name: 'default', label: 'Default', colors: ['#3987e5', '#d95926'] },
    { name: 'warm', label: 'Warm', colors: ['#ff0000'] },
  ],
};

test('parseOptions reads a full object', () => {
  const parsed = parseOptions({ ...options, colorSchemes: [...options.colorSchemes, { name: 7, colors: [] }] });
  assert.ok(parsed.ok);
  assert.deepEqual(parsed.value, options);
});

test('parseOptions fails without speeds, schemes, or valid defaults', () => {
  assert.equal(parseOptions({ ...options, speeds: [] }).ok, false);
  assert.equal(parseOptions({ ...options, colorSchemes: [] }).ok, false);
  assert.equal(parseOptions({ ...options, defaults: { ...defaults, speed: 3 } }).ok, false);
  assert.equal(parseOptions(null).ok, false);
});

test('a full object keeps each value', () => {
  const full = { version: 1, skipWaiting: false, colorScheme: 'warm', autoRotate: false, followEdit: false, speed: 4 };
  assert.deepEqual(parseSettings(full, options), full);
});

test('a speed that is not valid gets its default', () => {
  const parsed = parseSettings({ ...defaults, colorScheme: 'warm', speed: 3 }, options);
  assert.equal(parsed.speed, 1);
  assert.equal(parsed.colorScheme, 'warm');
});

test('a missing field gets its default', () => {
  assert.deepEqual(parseSettings({ version: 1, autoRotate: false }, options), { ...defaults, autoRotate: false });
});

test('a color scheme that the server does not know gets its default', () => {
  assert.equal(parseSettings({ colorScheme: 'nosuch' }, options).colorScheme, 'default');
});

test('null gives the defaults', () => {
  assert.deepEqual(parseSettings(null, options), defaults);
});
