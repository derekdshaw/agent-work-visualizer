import assert from 'node:assert/strict';
import { test } from 'node:test';
import { buildGraph, FAR, type GraphPayload, HI_CALLS_LO, hopsFrom, LO_CALLS_HI, parseGraph } from './load.ts';

function node(id: string, module = '.') {
  return { id, label: id, qualifiedName: id, module, kind: 'function', language: 'go', file: 'a.go', line: 1, endLine: 2 };
}

const payload: GraphPayload = {
  view: 'v',
  root: '/r',
  branch: 'main',
  version: 1,
  nodes: [node('a'), node('b'), node('c', 'pkg'), node('d', 'pkg'), node('e', 'other')],
  edges: [
    { a: 'a', b: 'b', kind: 'call' },
    { a: 'b', b: 'a', kind: 'call' },
    { a: 'b', b: 'c', kind: 'call' },
    { a: 'c', b: 'c', kind: 'call' },
    { a: 'c', b: 'd', kind: 'dispatch' },
    { a: 'd', b: 'missing', kind: 'call' },
  ],
};

test('buildGraph indexes nodes and keeps undirected unique edges', () => {
  const graph = buildGraph(payload);
  assert.equal(graph.n, 5);
  assert.deepEqual(graph.edges, [
    [0, 1],
    [1, 2],
    [2, 3],
  ]);
  assert.deepEqual(graph.deg, [1, 2, 2, 1, 0]);
  assert.deepEqual(graph.modules, ['.', 'pkg', 'other']);
  assert.deepEqual(graph.modOf, [0, 0, 1, 1, 2]);
  assert.equal(graph.index.get('d'), 3);
});

test('buildGraph keeps the call direction of each edge', () => {
  assert.deepEqual(buildGraph(payload).calls, [LO_CALLS_HI | HI_CALLS_LO, LO_CALLS_HI, LO_CALLS_HI]);
  assert.deepEqual(buildGraph({ ...payload, edges: [{ a: 'd', b: 'c', kind: 'call' }] }).calls, [HI_CALLS_LO]);
});

test('hopsFrom counts graph distance both ways along calls', () => {
  const graph = buildGraph(payload);
  assert.deepEqual([...hopsFrom(graph.adj, 2)], [2, 1, 0, 1, FAR]);
  assert.deepEqual([...hopsFrom(graph.adj, 0)], [0, 1, 2, 3, FAR]);
  assert.deepEqual([...hopsFrom(graph.adj, 4)], [FAR, FAR, FAR, FAR, 0]);
  assert.deepEqual([...hopsFrom(graph.adj, -1)], [FAR, FAR, FAR, FAR, FAR]);
});

test('parseGraph narrows the payload', () => {
  const parsed = parseGraph({ ...payload, nodes: [...payload.nodes, { label: 'no id' }, 7], edges: [{ a: 'a' }, ...payload.edges] });
  assert.ok(parsed.ok);
  assert.equal(parsed.value.nodes.length, 5);
  assert.equal(parsed.value.edges.length, 6);
  assert.equal(parseGraph({ nodes: [] }).ok, false);
  assert.equal(parseGraph(null).ok, false);
});
