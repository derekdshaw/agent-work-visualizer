// Force layout from the prototype: module clusters on a sphere, repulsion
// between nodes, springs along edges. It is pure so it runs in a worker and in
// tests alike.

export interface LayoutInput {
  n: number;
  edges: Array<[number, number]>;
  modOf: number[];
  moduleCount: number;
  fileOf: number[];
  fileCount: number;
  // Positions from an earlier layout, x y z per node, NaN for a node without
  // one. Null starts cold. A warm start keeps the picture stable after the
  // graph is reindexed.
  previous: Float32Array | null;
  seed: number;
}

export interface LayoutResult {
  // x y z per node, centered on the origin.
  positions: Float32Array;
  // Height of the floor grid, below every node.
  floor: number;
  // Distance of the farthest node from the origin.
  radius: number;
}

const COLD_ITERATIONS = 280;
const WARM_ITERATIONS = 90;
// Above this many nodes, repulsion only acts within CUTOFF, found through a grid.
const EXACT_LIMIT = 900;
const CUTOFF = 14;
const Y_SQUASH = 0.85;
// Pull toward the module center. Edges across modules stretch clusters, so it
// must be strong enough that module rings stay mostly apart.
const MODULE_PULL = 0.1;
// Pull toward the centroid of the node's file, so a file reads as a sub-cluster
// inside its module cluster.
const FILE_PULL = 0.06;

export function mulberry32(seed: number): () => number {
  let a = seed;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// moduleCenters spreads the module clusters over a sphere whose size grows
// with the cube root of the node count, so big graphs keep the prototype's
// spacing between nodes.
function moduleCenters(moduleCount: number, scale: number): number[][] {
  const count = Math.max(1, moduleCount);
  return Array.from({ length: count }, (_, k) => {
    const kk = k + 0.5;
    const phi = Math.acos(1 - (2 * kk) / count);
    const theta = Math.PI * (1 + Math.sqrt(5)) * kk;
    return [16 * scale * Math.sin(phi) * Math.cos(theta), 11 * scale * Math.cos(phi), 16 * scale * Math.sin(phi) * Math.sin(theta)];
  });
}

export function layout(input: LayoutInput): LayoutResult {
  const { n, edges, modOf, fileOf } = input;
  const scale = Math.max(1, Math.cbrt(n / 60));
  const centers = moduleCenters(input.moduleCount, scale);
  const rnd = mulberry32(input.seed);
  const P = new Float64Array(n * 3);
  const placed = new Uint8Array(n);
  const previous = input.previous;
  if (previous) {
    for (let i = 0; i < n; i++) {
      if (Number.isFinite(previous[i * 3])) {
        P[i * 3] = previous[i * 3];
        P[i * 3 + 1] = previous[i * 3 + 1] / Y_SQUASH;
        P[i * 3 + 2] = previous[i * 3 + 2];
        placed[i] = 1;
      }
    }
  }
  const adj: number[][] = Array.from({ length: n }, () => []);
  for (const [a, b] of edges) {
    adj[a].push(b);
    adj[b].push(a);
  }
  for (let i = 0; i < n; i++) {
    if (placed[i]) continue;
    // A new node starts beside its placed neighbors, or in its module cluster.
    const near = adj[i].filter((j) => placed[j]);
    const base = near.length ? [0, 1, 2].map((d) => near.reduce((sum, j) => sum + P[j * 3 + d], 0) / near.length) : centers[modOf[i] % centers.length];
    const spread = near.length ? 2 : 6 * scale;
    for (let d = 0; d < 3; d++) P[i * 3 + d] = base[d] + (rnd() - 0.5) * spread;
  }

  const warm = previous !== null;
  const iterations = warm ? WARM_ITERATIONS : COLD_ITERATIONS;
  const F = new Float64Array(n * 3);
  const fileSum = new Float64Array(input.fileCount * 3);
  const fileSize = new Float64Array(input.fileCount);
  for (let i = 0; i < n; i++) fileSize[fileOf[i]]++;
  for (let it = 0; it < iterations; it++) {
    F.fill(0);
    fileSum.fill(0);
    for (let i = 0; i < n; i++) for (let d = 0; d < 3; d++) fileSum[fileOf[i] * 3 + d] += P[i * 3 + d];
    if (n <= EXACT_LIMIT) repelExact(P, F, n);
    else repelGrid(P, F, n);
    for (const [a, b] of edges) {
      const dx = P[b * 3] - P[a * 3];
      const dy = P[b * 3 + 1] - P[a * 3 + 1];
      const dz = P[b * 3 + 2] - P[a * 3 + 2];
      const r = Math.sqrt(dx * dx + dy * dy + dz * dz) + 1e-4;
      const f = (r - 4.6) * 0.05;
      F[a * 3] += (dx / r) * f;
      F[a * 3 + 1] += (dy / r) * f;
      F[a * 3 + 2] += (dz / r) * f;
      F[b * 3] -= (dx / r) * f;
      F[b * 3 + 1] -= (dy / r) * f;
      F[b * 3 + 2] -= (dz / r) * f;
    }
    const cool = (1 - (it / iterations) * 0.7) * (warm ? 0.6 : 1);
    for (let i = 0; i < n; i++) {
      const c = centers[modOf[i] % centers.length];
      const f = fileOf[i];
      for (let d = 0; d < 3; d++) F[i * 3 + d] += (c[d] - P[i * 3 + d]) * MODULE_PULL + (fileSum[f * 3 + d] / fileSize[f] - P[i * 3 + d]) * FILE_PULL;
      const m = Math.hypot(F[i * 3], F[i * 3 + 1], F[i * 3 + 2]);
      const s = m > 1.5 ? 1.5 / m : 1;
      for (let d = 0; d < 3; d++) P[i * 3 + d] += F[i * 3 + d] * s * 0.5 * cool;
    }
  }

  const mean = [0, 0, 0];
  for (let i = 0; i < n; i++) for (let d = 0; d < 3; d++) mean[d] += P[i * 3 + d] / n;
  const positions = new Float32Array(n * 3);
  let minY = 0;
  let radius = 0;
  for (let i = 0; i < n; i++) {
    const x = P[i * 3] - mean[0];
    const y = (P[i * 3 + 1] - mean[1]) * Y_SQUASH;
    const z = P[i * 3 + 2] - mean[2];
    positions.set([x, y, z], i * 3);
    minY = Math.min(minY, y);
    radius = Math.max(radius, Math.hypot(x, y, z));
  }
  return { positions, floor: minY - 4, radius };
}

function push(F: Float64Array, i: number, dx: number, dy: number, dz: number, r2: number): void {
  const r = Math.sqrt(r2);
  const f = 18 / r2;
  F[i * 3] += (dx / r) * f;
  F[i * 3 + 1] += (dy / r) * f;
  F[i * 3 + 2] += (dz / r) * f;
}

function repelExact(P: Float64Array, F: Float64Array, n: number): void {
  for (let i = 0; i < n; i++) {
    for (let j = i + 1; j < n; j++) {
      const dx = P[i * 3] - P[j * 3];
      const dy = P[i * 3 + 1] - P[j * 3 + 1];
      const dz = P[i * 3 + 2] - P[j * 3 + 2];
      const r2 = dx * dx + dy * dy + dz * dz + 0.3;
      push(F, i, dx, dy, dz, r2);
      push(F, j, -dx, -dy, -dz, r2);
    }
  }
}

function cellKey(x: number, y: number, z: number): number {
  return ((x + 512) * 1024 + (y + 512)) * 1024 + (z + 512);
}

function repelGrid(P: Float64Array, F: Float64Array, n: number): void {
  const grid = new Map<number, number[]>();
  const cells = new Int32Array(n * 3);
  for (let i = 0; i < n; i++) {
    for (let d = 0; d < 3; d++) cells[i * 3 + d] = Math.floor(P[i * 3 + d] / CUTOFF);
    const key = cellKey(cells[i * 3], cells[i * 3 + 1], cells[i * 3 + 2]);
    const list = grid.get(key);
    if (list) list.push(i);
    else grid.set(key, [i]);
  }
  const cutoff2 = CUTOFF * CUTOFF;
  for (let i = 0; i < n; i++) {
    for (let dx = -1; dx <= 1; dx++) {
      for (let dy = -1; dy <= 1; dy++) {
        for (let dz = -1; dz <= 1; dz++) {
          const list = grid.get(cellKey(cells[i * 3] + dx, cells[i * 3 + 1] + dy, cells[i * 3 + 2] + dz));
          if (!list) continue;
          for (const j of list) {
            if (j === i) continue;
            const ex = P[i * 3] - P[j * 3];
            const ey = P[i * 3 + 1] - P[j * 3 + 1];
            const ez = P[i * 3 + 2] - P[j * 3 + 2];
            const r2 = ex * ex + ey * ey + ez * ez + 0.3;
            if (r2 < cutoff2) push(F, i, ex, ey, ez, r2);
          }
        }
      }
    }
  }
}
