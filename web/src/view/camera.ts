import * as THREE from 'three';

const clamp = (value: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, value));

// Orbit holds the prototype's camera: azimuth and elevation around a center
// that eases toward the active node in follow mode. The default distance
// scales with the graph radius, so big graphs start fully in view.
export class Orbit {
  readonly camera = new THREE.PerspectiveCamera(48, 1, 0.1, 2000);
  readonly center = new THREE.Vector3();
  // Panned offset of the center from where it would ease to otherwise.
  readonly offset = new THREE.Vector3();
  private readonly axis = new THREE.Vector3();
  az = 0.6;
  el = 0.38;
  dist = 44;
  home = 44;
  private readonly az0 = 0.6;
  private readonly el0 = 0.38;

  // fit sets the default distance for a graph of the given radius.
  fit(radius: number): void {
    this.home = Math.max(44, radius * 2.3);
    this.dist = this.home;
  }

  reset(): void {
    this.az = this.az0;
    this.el = this.el0;
    this.dist = this.home;
    this.offset.set(0, 0, 0);
  }

  // pan shifts the view in the screen plane by a pointer movement in pixels.
  pan(dx: number, dy: number): void {
    const k = this.dist * 0.0015;
    this.offset.addScaledVector(this.axis.setFromMatrixColumn(this.camera.matrixWorld, 0), -dx * k);
    this.offset.addScaledVector(this.axis.setFromMatrixColumn(this.camera.matrixWorld, 1), dy * k);
  }

  rotate(dx: number, dy: number): void {
    this.az -= dx * 0.006;
    this.el = clamp(this.el + dy * 0.006, -0.15, 1.35);
  }

  zoom(factor: number): void {
    this.dist = clamp(this.dist * factor, this.home * 0.3, this.home * 2.5);
  }

  // ease moves the center toward goal, or toward the origin without one.
  ease(goal: THREE.Vector3 | null, dt: number): void {
    const k = 1 - Math.exp(-dt * 3);
    const gx = (goal ? goal.x * 0.5 : 0) + this.offset.x;
    const gy = (goal ? goal.y * 0.5 : 0) + this.offset.y;
    const gz = (goal ? goal.z * 0.5 : 0) + this.offset.z;
    this.center.x += (gx - this.center.x) * k;
    this.center.y += (gy - this.center.y) * k;
    this.center.z += (gz - this.center.z) * k;
  }

  place(): void {
    const camera = this.camera;
    const ce = Math.cos(this.el);
    // A portrait viewport crops the graph unless the camera backs off.
    const d = this.dist * (camera.aspect < 1.1 ? 1.1 / camera.aspect : 1);
    camera.position.set(
      this.center.x + d * Math.sin(this.az) * ce,
      this.center.y + d * Math.sin(this.el),
      this.center.z + d * Math.cos(this.az) * ce,
    );
    camera.lookAt(this.center);
    camera.updateMatrixWorld();
  }

  describe(): string {
    const az = Math.round((((this.az * 180) / Math.PI) % 360 + 360) % 360);
    return `AZ ${az}° EL ${Math.round((this.el * 180) / Math.PI)}° · ${Math.round(this.dist)} u`;
  }
}
