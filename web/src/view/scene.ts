import * as THREE from 'three';
import type { LayoutResult } from '../graph/layout.ts';
import { type Graph, HI_CALLS_LO, LO_CALLS_HI } from '../graph/load.ts';
import { mulberry32 } from '../graph/layout.ts';
import { packageColors, readColor, readPalette } from '../settings/palette.ts';
import { Orbit } from './camera.ts';
import { makeReticle, type Reticle, tintReticle } from './reticle.ts';

const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
// Lit edges share this many pulse sprites, so big graphs need no sprite per edge.
const PULSE_POOL = 96;

// FrameInput is what the scene draws in one frame.
export interface FrameInput {
  dt: number;
  time: number;
  // Eased glow per node, 0 to 1.
  heat: Float32Array;
  // Distance of each node from the edit target; pulses run outward along it.
  hops: Int32Array;
  editNode: number;
  // Node that the reticle moves to, -1 to hide it.
  reticleNode: number;
  // Reticle size relative to its locked size.
  lockFactor: number;
  locked: boolean;
  reticleColor: THREE.Color;
  ripple: boolean;
  hover: number;
  selected: number;
  follow: boolean;
  rotate: boolean;
}

// Anchors that the group labels follow: a module's ring under its cluster,
// a file's centroid.
export interface Groups {
  modules: THREE.Vector3[];
  files: THREE.Vector3[];
}

const RING_SEGMENTS = 48;
// A node glows toward these while it is hot; its package color is the cold end.
const HOT_HALO = new THREE.Color().setRGB(1, 0.35, 0.49);
const HOT_CORE = new THREE.Color().setRGB(1, 0.88, 0.91);
const WHITE = new THREE.Color(1, 1, 1);
// Brightness of an edge at its caller end; the callee end is full, so an edge
// brightens in the direction of the call.
const CALLER_END = 0.3;
// An edge stops this far into its end nodes, as a share of the core sprite
// size, so it meets the core's rim instead of crossing to its center.
const CORE_TRIM = 0.3;
// Length of a focused edge's arrowhead per unit of camera distance, so it
// keeps its size on screen at every zoom, and its width per unit of length.
const ARROW_SIZE = 0.009;
const ARROW_WIDTH = 0.8;

interface NodeSprites {
  halo: THREE.Sprite;
  core: THREE.Sprite;
}

function radialTexture(stops: Array<[number, string]>): THREE.CanvasTexture {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 128;
  const context = canvas.getContext('2d');
  if (context) {
    const gradient = context.createRadialGradient(64, 64, 0, 64, 64, 64);
    for (const [offset, color] of stops) gradient.addColorStop(offset, color);
    context.fillStyle = gradient;
    context.fillRect(0, 0, 128, 128);
  }
  return new THREE.CanvasTexture(canvas);
}

function ringTexture(): THREE.CanvasTexture {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 128;
  const context = canvas.getContext('2d');
  if (context) {
    context.strokeStyle = '#fff';
    context.lineWidth = 3;
    context.shadowColor = '#fff';
    context.shadowBlur = 8;
    context.beginPath();
    context.arc(64, 64, 54, 0, Math.PI * 2);
    context.stroke();
  }
  return new THREE.CanvasTexture(canvas);
}

function sprite(texture: THREE.Texture, color: number, opacity: number): THREE.Sprite {
  return new THREE.Sprite(
    new THREE.SpriteMaterial({ map: texture, color, transparent: true, opacity, blending: THREE.AdditiveBlending, depthWrite: false }),
  );
}

function disposeTree(object: THREE.Object3D): void {
  object.traverse((child) => {
    if (child instanceof THREE.Sprite) {
      // Every sprite shares one geometry, so only the material is its own.
      child.material.dispose();
    } else if (child instanceof THREE.Line || child instanceof THREE.Points || child instanceof THREE.Mesh) {
      child.geometry.dispose();
      const materials = Array.isArray(child.material) ? child.material : [child.material];
      for (const material of materials) material.dispose();
    }
  });
}

export class GraphScene {
  readonly orbit = new Orbit();
  readonly renderer: THREE.WebGLRenderer | null;
  private readonly scene = new THREE.Scene();
  private readonly stage: HTMLElement;
  private width = 1;
  private height = 1;
  private readonly texHalo = radialTexture([[0, 'rgba(255,255,255,1)'], [0.16, 'rgba(255,255,255,.55)'], [0.45, 'rgba(255,255,255,.14)'], [1, 'rgba(255,255,255,0)']]);
  private readonly texCore = radialTexture([[0, 'rgba(255,255,255,1)'], [0.42, 'rgba(255,255,255,1)'], [0.58, 'rgba(255,255,255,.3)'], [1, 'rgba(255,255,255,0)']]);
  private readonly fog = new THREE.FogExp2(0x04060b, 0.011);
  private graphGroup = new THREE.Group();
  private nodes: NodeSprites[] = [];
  // The package color of each node, and its lighter core.
  private haloBase: THREE.Color[] = [];
  private coreBase: THREE.Color[] = [];
  private vectors: THREE.Vector3[] = [];
  private sizes: number[] = [];
  private edges: Array<[number, number]> = [];
  private calls: number[] = [];
  private callsColor = new THREE.Color();
  private calledByColor = new THREE.Color();
  private focusPositions = new Float32Array(0);
  private focusColors = new Float32Array(0);
  private focusGeometry: THREE.BufferGeometry | null = null;
  private focusCount = 0;
  private arrowPositions = new Float32Array(0);
  private arrowColors = new Float32Array(0);
  private arrowGeometry: THREE.BufferGeometry | null = null;
  private arrowCount = 0;
  private readonly along = new THREE.Vector3();
  private readonly from = new THREE.Vector3();
  private readonly to = new THREE.Vector3();
  private readonly side = new THREE.Vector3();
  private readonly wing = new THREE.Vector3();
  private edgeColors = new Float32Array(0);
  private edgeGeometry: THREE.BufferGeometry | null = null;
  private readonly pulses: THREE.Sprite[] = [];
  private readonly ripples: THREE.Sprite[] = [];
  private readonly reticle: Reticle;
  private readonly selection: Reticle;
  private readonly reticleColor = new THREE.Color(0xff5a7e);
  readonly reticlePosition = new THREE.Vector3();
  private reticleShown = false;
  private readonly projected = new THREE.Vector3();
  groups: Groups = { modules: [], files: [] };

  constructor(canvas: HTMLCanvasElement, stage: HTMLElement) {
    this.stage = stage;
    let renderer: THREE.WebGLRenderer | null = null;
    try {
      renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
    } catch {
      // Without WebGL the panes and scrubber still work; the page says so.
      renderer = null;
    }
    this.renderer = renderer;
    renderer?.setClearColor(0x04060b, 1);
    this.scene.fog = this.fog;
    this.scene.add(this.graphGroup);
    for (let i = 0; i < PULSE_POOL; i++) {
      const pulse = sprite(this.texCore, 0xffe3ea, 0.9);
      pulse.visible = false;
      this.scene.add(pulse);
      this.pulses.push(pulse);
    }
    const ring = ringTexture();
    for (let i = 0; i < 2; i++) {
      const ripple = sprite(ring, 0xff5a7e, 0);
      ripple.visible = false;
      this.scene.add(ripple);
      this.ripples.push(ripple);
    }
    this.reticle = makeReticle(0xff5a7e, true);
    this.reticle.group.visible = false;
    this.scene.add(this.reticle.group);
    this.selection = makeReticle(0xe6f1ff, false);
    this.selection.group.visible = false;
    this.scene.add(this.selection.group);
    new ResizeObserver(() => this.resize()).observe(stage);
    this.resize();
  }

  get size(): { width: number; height: number } {
    return { width: this.width, height: this.height };
  }

  get nodeCount(): number {
    return this.nodes.length;
  }

  resize(): void {
    const rect = this.stage.getBoundingClientRect();
    this.width = Math.max(1, rect.width | 0);
    this.height = Math.max(1, rect.height | 0);
    this.renderer?.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer?.setSize(this.width, this.height, false);
    this.orbit.camera.aspect = this.width / this.height;
    this.orbit.camera.updateProjectionMatrix();
  }

  // setGraph replaces the drawn graph. keepCamera keeps the view when the same
  // folder is reindexed.
  setGraph(graph: Graph, result: LayoutResult, keepCamera: boolean): void {
    this.scene.remove(this.graphGroup);
    disposeTree(this.graphGroup);
    const group = new THREE.Group();
    this.graphGroup = group;
    this.scene.add(group);
    const { positions, floor, radius } = result;
    const n = graph.n;
    this.vectors = Array.from({ length: n }, (_, i) => new THREE.Vector3(positions[i * 3], positions[i * 3 + 1], positions[i * 3 + 2]));
    this.sizes = graph.deg.map((degree) => 1 + Math.min(degree, 6) * 0.17);
    this.edges = graph.edges;
    this.calls = graph.calls;
    this.callsColor = readColor('--edge-calls', 0xffc857);
    this.calledByColor = readColor('--edge-called-by', 0xffffff);
    if (!keepCamera) {
      this.orbit.fit(radius);
      this.orbit.reset();
    }
    const spread = this.orbit.home / 44;
    this.fog.density = 0.011 / spread;

    const grid = new THREE.GridHelper(Math.max(180, radius * 6), 60, 0x1d6674, 0x113742);
    grid.position.y = floor;
    const gridMaterials = Array.isArray(grid.material) ? grid.material : [grid.material];
    for (const material of gridMaterials) {
      material.transparent = true;
      material.opacity = 0.55;
    }
    group.add(grid);

    const drops = new Float32Array(n * 6);
    this.vectors.forEach((v, i) => drops.set([v.x, v.y, v.z, v.x, floor, v.z], i * 6));
    const dropGeometry = new THREE.BufferGeometry();
    dropGeometry.setAttribute('position', new THREE.BufferAttribute(drops, 3));
    group.add(new THREE.LineSegments(dropGeometry, new THREE.LineBasicMaterial({ color: 0x4fe3f5, transparent: true, opacity: 0.07, blending: THREE.AdditiveBlending, depthWrite: false })));

    const rnd = mulberry32(11);
    const stars = new Float32Array(420 * 3);
    for (let i = 0; i < 420; i++) {
      const r = (70 + rnd() * 80) * spread;
      const theta = rnd() * Math.PI * 2;
      const phi = Math.acos(2 * rnd() - 1);
      stars.set([r * Math.sin(phi) * Math.cos(theta), r * Math.cos(phi), r * Math.sin(phi) * Math.sin(theta)], i * 3);
    }
    const starGeometry = new THREE.BufferGeometry();
    starGeometry.setAttribute('position', new THREE.BufferAttribute(stars, 3));
    group.add(new THREE.Points(starGeometry, new THREE.PointsMaterial({ color: 0x4fe3f5, size: 0.35 * spread, transparent: true, opacity: 0.35, blending: THREE.AdditiveBlending, depthWrite: false, fog: false })));

    const colors = packageColors(graph.modules, readPalette());
    this.haloBase = graph.modOf.map((m) => colors[m]);
    this.coreBase = this.haloBase.map((color) => color.clone().lerp(WHITE, 0.3));
    this.groups = this.drawGroups(graph, group, colors);

    const edgePositions = new Float32Array(this.edges.length * 6);
    this.edges.forEach(([a, b], k) => {
      // Trimmed at the resting core size; a hot node's larger core covers the rest.
      this.trimmed(a, b, this.sizes[a] * CORE_TRIM, this.sizes[b] * CORE_TRIM);
      edgePositions.set([this.from.x, this.from.y, this.from.z, this.to.x, this.to.y, this.to.z], k * 6);
    });
    this.edgeColors = new Float32Array(this.edges.length * 6);
    const edgeGeometry = new THREE.BufferGeometry();
    edgeGeometry.setAttribute('position', new THREE.BufferAttribute(edgePositions, 3));
    edgeGeometry.setAttribute('color', new THREE.BufferAttribute(this.edgeColors, 3));
    this.edgeGeometry = edgeGeometry;
    group.add(new THREE.LineSegments(edgeGeometry, new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false })));

    // The edges of the hovered or selected node draw again after the node
    // glow, with normal blending and no depth test, so their colors survive
    // the additive halos that cover short edges.
    this.focusPositions = new Float32Array(this.edges.length * 6);
    this.focusColors = new Float32Array(this.focusPositions.length);
    const focusGeometry = new THREE.BufferGeometry();
    focusGeometry.setAttribute('position', new THREE.BufferAttribute(this.focusPositions, 3));
    focusGeometry.setAttribute('color', new THREE.BufferAttribute(this.focusColors, 3));
    focusGeometry.setDrawRange(0, 0);
    this.focusGeometry = focusGeometry;
    const focusLines = new THREE.LineSegments(focusGeometry, new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, depthTest: false, depthWrite: false }));
    focusLines.renderOrder = 1;
    // Its bounds change every frame.
    focusLines.frustumCulled = false;
    group.add(focusLines);
    // Up to two arrowheads per edge, three vertices each.
    this.arrowPositions = new Float32Array(this.edges.length * 2 * 9);
    this.arrowColors = new Float32Array(this.arrowPositions.length);
    const arrowGeometry = new THREE.BufferGeometry();
    arrowGeometry.setAttribute('position', new THREE.BufferAttribute(this.arrowPositions, 3));
    arrowGeometry.setAttribute('color', new THREE.BufferAttribute(this.arrowColors, 3));
    arrowGeometry.setDrawRange(0, 0);
    this.arrowGeometry = arrowGeometry;
    const arrows = new THREE.Mesh(arrowGeometry, new THREE.MeshBasicMaterial({ vertexColors: true, transparent: true, depthTest: false, depthWrite: false, side: THREE.DoubleSide }));
    arrows.renderOrder = 1;
    arrows.frustumCulled = false;
    group.add(arrows);

    this.nodes = this.vectors.map((v) => {
      const halo = sprite(this.texHalo, 0x4fe3f5, 0.25);
      const core = sprite(this.texCore, 0xd0faff, 1);
      halo.position.copy(v);
      core.position.copy(v);
      group.add(halo, core);
      return { halo, core };
    });
    if (!this.reticleShown) this.reticlePosition.set(0, 0, 0);
  }

  // drawGroups rings each module's cluster just below it. Module centers sit on
  // a sphere, so rings on the shared floor would overlap.
  private drawGroups(graph: Graph, group: THREE.Group, colors: THREE.Color[]): Groups {
    const modules = graph.modules.map(() => ({ x: 0, z: 0, y: 0, r: 0, count: 0 }));
    const files = graph.files.map(() => new THREE.Vector3());
    const fileCount = new Array<number>(graph.files.length).fill(0);
    this.vectors.forEach((v, i) => {
      const m = modules[graph.modOf[i]];
      m.x += v.x;
      m.z += v.z;
      m.count++;
      files[graph.fileOf[i]].add(v);
      fileCount[graph.fileOf[i]]++;
    });
    for (const m of modules) {
      m.x /= m.count;
      m.z /= m.count;
    }
    files.forEach((v, f) => v.divideScalar(fileCount[f]));
    // The ring encloses 80% of a module's nodes, and sits under 80% of them:
    // the rest are pulled out by calls into other modules and would make
    // every ring overlap or hang far below its cluster.
    const distances = modules.map(() => [] as number[]);
    const heights = modules.map(() => [] as number[]);
    this.vectors.forEach((v, i) => {
      const m = modules[graph.modOf[i]];
      distances[graph.modOf[i]].push(Math.hypot(v.x - m.x, v.z - m.z));
      heights[graph.modOf[i]].push(v.y);
    });
    modules.forEach((m, k) => {
      const d = distances[k].sort((a, b) => a - b);
      const h = heights[k].sort((a, b) => a - b);
      m.r = d[Math.floor(0.8 * (d.length - 1))];
      m.y = h[Math.floor(0.2 * (h.length - 1))];
    });
    const rings = new Float32Array(modules.length * RING_SEGMENTS * 6);
    const ringColors = new Float32Array(rings.length);
    modules.forEach((m, k) => {
      const r = Math.max(1.5, m.r + 1);
      for (let s = 0; s < RING_SEGMENTS; s++) {
        const a0 = (s / RING_SEGMENTS) * Math.PI * 2;
        const a1 = ((s + 1) / RING_SEGMENTS) * Math.PI * 2;
        rings.set([m.x + r * Math.cos(a0), m.y - 1, m.z + r * Math.sin(a0), m.x + r * Math.cos(a1), m.y - 1, m.z + r * Math.sin(a1)], (k * RING_SEGMENTS + s) * 6);
        colors[k].toArray(ringColors, (k * RING_SEGMENTS + s) * 6);
        colors[k].toArray(ringColors, (k * RING_SEGMENTS + s) * 6 + 3);
      }
    });
    const ringGeometry = new THREE.BufferGeometry();
    ringGeometry.setAttribute('position', new THREE.BufferAttribute(rings, 3));
    ringGeometry.setAttribute('color', new THREE.BufferAttribute(ringColors, 3));
    group.add(new THREE.LineSegments(ringGeometry, new THREE.LineBasicMaterial({ vertexColors: true, transparent: true, opacity: 0.55, blending: THREE.AdditiveBlending, depthWrite: false })));
    return { modules: modules.map((m) => new THREE.Vector3(m.x, m.y - 1, m.z)), files };
  }

  // trimmed sets this.from and this.to to edge a–b shortened by trimA and
  // trimB, and this.along to its direction. It returns false when the trims
  // meet, and then both points sit at the middle.
  private trimmed(a: number, b: number, trimA: number, trimB: number): boolean {
    const va = this.vectors[a];
    const vb = this.vectors[b];
    const length = va.distanceTo(vb);
    if (length <= trimA + trimB) {
      this.from.lerpVectors(va, vb, 0.5);
      this.to.copy(this.from);
      return false;
    }
    this.along.subVectors(vb, va).divideScalar(length);
    this.from.copy(va).addScaledVector(this.along, trimA);
    this.to.copy(vb).addScaledVector(this.along, -trimB);
    return true;
  }

  // pushFocusEdge draws edge a–b (a < b) trimmed to the current node cores,
  // with an arrowhead at each callee end.
  private pushFocusEdge(a: number, b: number, dir: number, color: THREE.Color, eye: THREE.Vector3): void {
    if (!this.trimmed(a, b, this.nodes[a].core.scale.x * CORE_TRIM, this.nodes[b].core.scale.x * CORE_TRIM)) return;
    const { from, to } = this;
    this.focusPositions.set([from.x, from.y, from.z, to.x, to.y, to.z], this.focusCount * 6);
    this.focusColors.set([color.r, color.g, color.b, color.r, color.g, color.b], this.focusCount * 6);
    this.focusCount++;
    if (dir & LO_CALLS_HI) this.pushArrow(to, 1, color, eye);
    if (dir & HI_CALLS_LO) this.pushArrow(from, -1, color, eye);
  }

  // pushArrow draws a solid triangle with its tip at tip, pointing along
  // sign × this.along, spread across the view so it faces the camera.
  private pushArrow(tip: THREE.Vector3, sign: number, color: THREE.Color, eye: THREE.Vector3): void {
    const size = eye.distanceTo(tip) * ARROW_SIZE;
    this.side.subVectors(eye, tip).cross(this.along).normalize().multiplyScalar((size * ARROW_WIDTH) / 2);
    this.wing.copy(tip).addScaledVector(this.along, -sign * size);
    const o = this.arrowCount * 9;
    this.arrowPositions.set([tip.x, tip.y, tip.z, this.wing.x + this.side.x, this.wing.y + this.side.y, this.wing.z + this.side.z, this.wing.x - this.side.x, this.wing.y - this.side.y, this.wing.z - this.side.z], o);
    for (let v = 0; v < 3; v++) color.toArray(this.arrowColors, o + v * 3);
    this.arrowCount++;
  }

  position(i: number): THREE.Vector3 | null {
    return this.vectors[i] ?? null;
  }

  toScreen(point: THREE.Vector3): { x: number; y: number; z: number } {
    this.projected.copy(point).project(this.orbit.camera);
    return { x: (this.projected.x * 0.5 + 0.5) * this.width, y: (-this.projected.y * 0.5 + 0.5) * this.height, z: this.projected.z };
  }

  // nearest returns the node drawn closest to a client point within maxPx, or -1.
  nearest(clientX: number, clientY: number, maxPx: number): number {
    const rect = this.stage.getBoundingClientRect();
    const px = clientX - rect.left;
    const py = clientY - rect.top;
    let best = -1;
    let bestDistance = maxPx * maxPx;
    for (let i = 0; i < this.vectors.length; i++) {
      const s = this.toScreen(this.vectors[i]);
      if (s.z > 1) continue;
      const d = (s.x - px) ** 2 + (s.y - py) ** 2;
      if (d < bestDistance) {
        bestDistance = d;
        best = i;
      }
    }
    return best;
  }

  render(f: FrameInput): void {
    const goal = f.reticleNode >= 0 ? this.vectors[f.reticleNode] : undefined;
    if (goal) {
      if (!this.reticleShown) this.reticlePosition.copy(goal);
      this.reticlePosition.lerp(goal, 1 - Math.exp(-f.dt * 9));
    }
    this.reticleShown = goal !== undefined;
    if (f.rotate) this.orbit.az += f.dt * 0.06;
    this.orbit.ease(f.follow && this.reticleShown ? this.reticlePosition : null, f.dt);
    this.orbit.place();
    if (!this.renderer) return;
    const camera = this.orbit.camera;

    for (let i = 0; i < this.nodes.length; i++) {
      const h = f.heat[i];
      const { halo, core } = this.nodes[i];
      const emphasis = i === f.hover || i === f.selected ? 1 : 0;
      halo.material.color.copy(this.haloBase[i]).lerp(HOT_HALO, h);
      core.material.color.copy(this.coreBase[i]).lerp(HOT_CORE, h);
      const beat = i === f.editNode ? 1 + 0.12 * Math.sin(f.time * 6) * h : 1;
      core.scale.setScalar(this.sizes[i] * (1 + 0.5 * h + 0.35 * emphasis));
      halo.scale.setScalar(this.sizes[i] * (4.2 + 4 * h + 1.5 * emphasis) * beat);
      halo.material.opacity = Math.min(1, 0.22 + 0.5 * h + 0.25 * emphasis);
    }

    let pulse = 0;
    this.focusCount = this.arrowCount = 0;
    for (let e = 0; e < this.edges.length; e++) {
      const [a, b] = this.edges[e];
      const lit = Math.min(f.heat[a], f.heat[b]);
      const dir = this.calls[e];
      const focus = a === f.selected || a === f.hover ? a : b === f.selected || b === f.hover ? b : -1;
      const r = lerp(0.062, 0.95, lit);
      const g = lerp(0.178, 0.32, lit);
      const bl = lerp(0.192, 0.46, lit);
      const ka = dir & HI_CALLS_LO ? 1 : CALLER_END;
      const kb = dir & LO_CALLS_HI ? 1 : CALLER_END;
      this.edgeColors.set([r * ka, g * ka, bl * ka, r * kb, g * kb, bl * kb], e * 6);
      if (focus >= 0) {
        // A mutual call counts as a call of the focused node.
        const color = dir & (focus === a ? LO_CALLS_HI : HI_CALLS_LO) ? this.callsColor : this.calledByColor;
        this.pushFocusEdge(a, b, dir, color, camera.position);
      }
      if (lit > 0.35 && pulse < this.pulses.length) {
        const sprite = this.pulses[pulse++];
        const from = f.hops[a] <= f.hops[b] ? a : b;
        const to = from === a ? b : a;
        const u = (f.time * 0.55 + e * 0.3719) % 1;
        sprite.visible = true;
        sprite.position.lerpVectors(this.vectors[from], this.vectors[to], u);
        sprite.scale.setScalar(0.7);
        sprite.material.opacity = 0.9 * Math.sin(u * Math.PI);
      }
    }
    for (let i = pulse; i < this.pulses.length; i++) this.pulses[i].visible = false;
    const colors = this.edgeGeometry?.getAttribute('color');
    if (colors) colors.needsUpdate = true;
    for (const [geometry, vertices] of [[this.focusGeometry, this.focusCount * 2], [this.arrowGeometry, this.arrowCount * 3]] as const) {
      if (!geometry) continue;
      geometry.setDrawRange(0, vertices);
      geometry.getAttribute('position').needsUpdate = true;
      geometry.getAttribute('color').needsUpdate = true;
    }

    const center = f.editNode >= 0 ? this.vectors[f.editNode] : undefined;
    this.ripples.forEach((ripple, i) => {
      if (f.ripple && center) {
        const r = (f.time * 0.8 + i * 0.5) % 1;
        ripple.visible = true;
        ripple.position.copy(center);
        ripple.scale.setScalar(2 + r * 13);
        ripple.material.opacity = (1 - r) ** 2 * 0.55;
      } else {
        ripple.visible = false;
      }
    });

    const reticle = this.reticle;
    reticle.group.visible = this.reticleShown;
    if (this.reticleShown) {
      const d = camera.position.distanceTo(this.reticlePosition);
      reticle.group.position.copy(this.reticlePosition);
      reticle.group.quaternion.copy(camera.quaternion);
      reticle.group.scale.setScalar(d * 0.055 * f.lockFactor);
      reticle.outer.rotation.z = f.time * (f.locked ? 0.9 : 3);
      reticle.inner.rotation.z = -f.time * 0.6;
      this.reticleColor.lerp(f.reticleColor, 1 - Math.exp(-f.dt * 6));
      tintReticle(reticle, this.reticleColor);
    }

    const selected = f.selected >= 0 ? this.vectors[f.selected] : undefined;
    this.selection.group.visible = selected !== undefined;
    if (selected) {
      this.selection.group.position.copy(selected);
      this.selection.group.quaternion.copy(camera.quaternion);
      this.selection.group.scale.setScalar(camera.position.distanceTo(selected) * 0.032);
      this.selection.outer.rotation.z = -f.time * 0.5;
    }
    this.renderer.render(this.scene, camera);
  }
}
