import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as THREE from 'three';
import { packageColors } from './palette.ts';

const palette = ['#3987e5', '#d95926', '#199e70'].map((hex) => new THREE.Color(hex));

test('every package gets its own color, past the end of the palette too', () => {
  const modules = Array.from({ length: 12 }, (_, i) => `pkg/${String(i).padStart(2, '0')}`);
  const colors = packageColors(modules, palette).map((color) => color.getHexString());
  assert.equal(new Set(colors).size, modules.length);
  assert.equal(colors[0], palette[0].getHexString());
});

test('a package keeps its color when the node order changes', () => {
  const first = packageColors(['b', 'a', 'c'], palette);
  const second = packageColors(['c', 'b', 'a'], palette);
  assert.equal(first[0].getHexString(), second[1].getHexString());
  assert.equal(first[1].getHexString(), palette[0].getHexString());
});

test('an empty palette falls back to one color', () => {
  assert.equal(packageColors(['a'], [])[0].getHexString(), '4fe3f5');
});
