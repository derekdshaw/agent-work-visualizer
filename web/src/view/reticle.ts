import * as THREE from 'three';

// The lock-on reticle from the prototype: a dashed or solid ring, an inner
// ring, and four ticks. It is a billboard; the scene turns it to face the
// camera and scales it with distance so it keeps its screen size.

export interface Reticle {
  group: THREE.Group;
  outer: THREE.Line;
  inner: THREE.Line;
  ticks: THREE.LineSegments;
  materials: Array<THREE.LineBasicMaterial | THREE.LineDashedMaterial>;
}

function circlePoints(radius: number, segments: number): THREE.Vector3[] {
  const points: THREE.Vector3[] = [];
  for (let i = 0; i <= segments; i++) {
    const angle = (i / segments) * Math.PI * 2;
    points.push(new THREE.Vector3(Math.cos(angle) * radius, Math.sin(angle) * radius, 0));
  }
  return points;
}

export function makeReticle(color: number, dashed: boolean): Reticle {
  const group = new THREE.Group();
  const outerMaterial = dashed
    ? new THREE.LineDashedMaterial({ color, dashSize: 0.14, gapSize: 0.1, transparent: true, depthTest: false, fog: false })
    : new THREE.LineBasicMaterial({ color, transparent: true, depthTest: false, fog: false });
  const outer = new THREE.Line(new THREE.BufferGeometry().setFromPoints(circlePoints(1, 72)), outerMaterial);
  if (dashed) outer.computeLineDistances();
  const innerMaterial = new THREE.LineBasicMaterial({ color, transparent: true, opacity: 0.9, depthTest: false, fog: false });
  const inner = new THREE.Line(new THREE.BufferGeometry().setFromPoints(circlePoints(0.6, 48)), innerMaterial);
  const tickPoints = [
    [1.15, 0], [1.5, 0], [-1.15, 0], [-1.5, 0], [0, 1.15], [0, 1.5], [0, -1.15], [0, -1.5],
  ].map(([x, y]) => new THREE.Vector3(x, y, 0));
  const tickMaterial = new THREE.LineBasicMaterial({ color, transparent: true, depthTest: false, fog: false });
  const ticks = new THREE.LineSegments(new THREE.BufferGeometry().setFromPoints(tickPoints), tickMaterial);
  for (const part of [outer, inner, ticks]) {
    part.renderOrder = 10;
    group.add(part);
  }
  return { group, outer, inner, ticks, materials: [outerMaterial, innerMaterial, tickMaterial] };
}

export function tintReticle(reticle: Reticle, color: THREE.Color): void {
  for (const material of reticle.materials) material.color.copy(color);
}
