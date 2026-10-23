import assert from 'node:assert/strict';
import { test } from 'node:test';
import { layout, type LayoutInput } from './layout.ts';

function input(n: number, previous: Float32Array | null = null): LayoutInput {
  const edges: Array<[number, number]> = [];
  for (let i = 1; i < n; i++) edges.push([Math.floor(i / 2), i]);
  const modOf = Array.from({ length: n }, (_, i) => i % 7);
  return { n, edges, modOf, moduleCount: 7, fileOf: modOf.map((m, i) => m * 3 + (i % 3)), fileCount: 21, previous, seed: 11 };
}

function finite(positions: Float32Array): boolean {
  return positions.every((value) => Number.isFinite(value));
}

test('layout is deterministic, centered, and above the floor', () => {
  const first = layout(input(80));
  const second = layout(input(80));
  assert.deepEqual([...first.positions], [...second.positions]);
  assert.ok(finite(first.positions));
  let minY = Infinity;
  const mean = [0, 0, 0];
  for (let i = 0; i < 80; i++) {
    minY = Math.min(minY, first.positions[i * 3 + 1]);
    for (let d = 0; d < 3; d++) mean[d] += first.positions[i * 3 + d] / 80;
  }
  assert.ok(first.floor < minY);
  assert.ok(mean.every((value) => Math.abs(value) < 1e-3));
  assert.ok(first.radius > 0);
});

test('a warm start keeps placed nodes near where they were', () => {
  const cold = layout(input(60));
  const previous = new Float32Array(61 * 3).fill(Number.NaN);
  previous.set(cold.positions);
  const warm = layout(input(61, previous));
  assert.ok(finite(warm.positions));
  let moved = 0;
  for (let i = 0; i < 60; i++) {
    moved += Math.hypot(...[0, 1, 2].map((d) => warm.positions[i * 3 + d] - cold.positions[i * 3 + d])) / 60;
  }
  assert.ok(moved < 3, `nodes moved ${moved} on average`);
});

test('a large graph uses the grid and stays finite', () => {
  const result = layout(input(1200));
  assert.ok(finite(result.positions));
  assert.ok(result.radius > layout(input(80)).radius);
});

test('a file packs tighter than its module, a module tighter than the graph', () => {
  // 2 modules × 3 files × 6 nodes, no edges: grouping comes from the pulls alone.
  const n = 36;
  const fileOf = Array.from({ length: n }, (_, i) => Math.floor(i / 6));
  const modOf = fileOf.map((f) => Math.floor(f / 3));
  const { positions } = layout({ n, edges: [], modOf, moduleCount: 2, fileOf, fileCount: 6, previous: null, seed: 11 });
  const sums = { file: [0, 0], module: [0, 0], cross: [0, 0] };
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const d = Math.hypot(...[0, 1, 2].map((k) => positions[i * 3 + k] - positions[j * 3 + k]));
      const bucket = fileOf[i] === fileOf[j] ? sums.file : modOf[i] === modOf[j] ? sums.module : sums.cross;
      bucket[0] += d;
      bucket[1]++;
    }
  }
  const mean = (bucket: number[]) => bucket[0] / bucket[1];
  assert.ok(mean(sums.file) < mean(sums.module), `file ${mean(sums.file)} vs module ${mean(sums.module)}`);
  assert.ok(mean(sums.module) < mean(sums.cross), `module ${mean(sums.module)} vs cross ${mean(sums.cross)}`);
});
