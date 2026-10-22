import { isRecord, type Result } from '../session/events.ts';

// The answer of GET /api/graph. Node IDs are the IDs that session events use.
export interface GraphNode {
  id: string;
  label: string;
  qualifiedName: string;
  // The node's directory, "." for the root.
  module: string;
  kind: string;
  language: string;
  file: string;
  line: number;
  endLine: number;
}

export interface GraphEdge {
  // a calls b.
  a: string;
  b: string;
  kind: string;
}

export interface GraphPayload {
  // The session log that /events streams for this graph.
  view: string;
  root: string;
  branch: string;
  version: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

// Graph indexes a payload by node position. Edges are undirected pairs with
// a < b, because the blast radius follows calls both ways.
export interface Graph {
  payload: GraphPayload;
  n: number;
  index: Map<string, number>;
  adj: number[][];
  deg: number[];
  edges: Array<[number, number]>;
  modules: string[];
  modOf: number[];
}

// FAR is the hop count of a node that the target does not reach.
export const FAR = 99;

function text(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  return typeof value === 'string' ? value : '';
}

function count(record: Record<string, unknown>, key: string): number {
  const value = record[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function parseNode(value: unknown): GraphNode | null {
  if (!isRecord(value) || !text(value, 'id')) return null;
  return {
    id: text(value, 'id'),
    label: text(value, 'label'),
    qualifiedName: text(value, 'qualifiedName'),
    module: text(value, 'module') || '.',
    kind: text(value, 'kind'),
    language: text(value, 'language'),
    file: text(value, 'file'),
    line: count(value, 'line'),
    endLine: count(value, 'endLine'),
  };
}

function parseEdge(value: unknown): GraphEdge | null {
  if (!isRecord(value) || !text(value, 'a') || !text(value, 'b')) return null;
  return { a: text(value, 'a'), b: text(value, 'b'), kind: text(value, 'kind') };
}

export function parseGraph(value: unknown): Result<GraphPayload> {
  if (!isRecord(value) || !Array.isArray(value['nodes']) || !Array.isArray(value['edges'])) {
    return { ok: false, error: 'graph payload has no node and edge lists' };
  }
  return {
    ok: true,
    value: {
      view: text(value, 'view'),
      root: text(value, 'root'),
      branch: text(value, 'branch'),
      version: count(value, 'version'),
      nodes: value['nodes'].map(parseNode).filter((node): node is GraphNode => node !== null),
      edges: value['edges'].map(parseEdge).filter((edge): edge is GraphEdge => edge !== null),
    },
  };
}

export function buildGraph(payload: GraphPayload): Graph {
  const n = payload.nodes.length;
  const index = new Map<string, number>();
  payload.nodes.forEach((node, i) => index.set(node.id, i));
  const adj: number[][] = Array.from({ length: n }, () => []);
  const edges: Array<[number, number]> = [];
  const seen = new Set<number>();
  for (const edge of payload.edges) {
    const a = index.get(edge.a);
    const b = index.get(edge.b);
    if (a === undefined || b === undefined || a === b) continue;
    const lo = Math.min(a, b);
    const hi = Math.max(a, b);
    const key = lo * n + hi;
    if (seen.has(key)) continue;
    seen.add(key);
    edges.push([lo, hi]);
    adj[lo].push(hi);
    adj[hi].push(lo);
  }
  const modules: string[] = [];
  const moduleIndex = new Map<string, number>();
  const modOf = payload.nodes.map((node) => {
    let m = moduleIndex.get(node.module);
    if (m === undefined) {
      m = modules.length;
      moduleIndex.set(node.module, m);
      modules.push(node.module);
    }
    return m;
  });
  return { payload, n, index, adj, deg: adj.map((list) => list.length), edges, modules, modOf };
}

// hopsFrom returns the graph distance from node s to every node, FAR for a
// node that s does not reach.
export function hopsFrom(adj: number[][], s: number): Int32Array {
  const hops = new Int32Array(adj.length).fill(FAR);
  if (s < 0 || s >= adj.length) return hops;
  hops[s] = 0;
  const queue = [s];
  for (let head = 0; head < queue.length; head++) {
    const a = queue[head];
    for (const b of adj[a]) {
      if (hops[b] === FAR) {
        hops[b] = hops[a] + 1;
        queue.push(b);
      }
    }
  }
  return hops;
}

export async function fetchGraph(): Promise<Result<GraphPayload>> {
  try {
    const response = await fetch('/api/graph', { cache: 'no-store' });
    if (!response.ok) return { ok: false, error: `GET /api/graph: ${response.status}` };
    const body: unknown = await response.json();
    return parseGraph(body);
  } catch (error) {
    return { ok: false, error: error instanceof Error ? error.message : String(error) };
  }
}
