import assert from 'node:assert/strict';
import { test } from 'node:test';
import { type Options, parseOptions, parseSettings, themeFor } from './settings.ts';

const defaults = { version: 1, skipWaiting: true, colorScheme: 'default', autoRotate: true, followEdit: true, speed: 1, themes: [], projectThemes: {} };
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
  const full = { version: 1, skipWaiting: false, colorScheme: 'warm', autoRotate: false, followEdit: false, speed: 4, themes: [], projectThemes: { '/a': 'warm' } };
  assert.deepEqual(parseSettings(full, options), full);
});

test('project picks keep valid entries and themeFor falls back to the default', () => {
  const parsed = parseSettings({ version: 1, colorScheme: 'warm', projectThemes: { '/a': 'default', '/b': 'nosuch', c: 'default', '/d': 3 } }, options);
  assert.deepEqual(parsed.projectThemes, { '/a': 'default' });
  assert.equal(themeFor(parsed, '/a'), 'default');
  assert.equal(themeFor(parsed, '/b'), 'warm');
  assert.equal(themeFor(parsed, ''), 'warm');
});

test('themes keep valid entries and colorScheme can name one', () => {
  const themes = [
    { name: 'mine', label: 'Mine', colors: ['#112233'] },
    { name: 'default', label: 'Clash', colors: ['#112233'] },
    { name: 'bad', label: 'Bad', colors: ['red'] },
  ];
  const parsed = parseSettings({ version: 1, colorScheme: 'mine', themes }, options);
  assert.deepEqual(parsed.themes, [themes[0]]);
  assert.equal(parsed.colorScheme, 'mine');
});

test('a colorScheme that names a dropped theme gets its default', () => {
  const parsed = parseSettings({ version: 1, colorScheme: 'bad', themes: [{ name: 'bad', label: 'Bad', colors: ['red'] }] }, options);
  assert.equal(parsed.colorScheme, 'default');
  assert.deepEqual(parsed.themes, []);
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
