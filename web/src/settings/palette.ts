import * as THREE from 'three';

// Lightness steps for each pass through the palette, so every package keeps a
// distinct color past the end of the list.
const CYCLE_SHIFTS = [0, 0.14, -0.12, 0.24, -0.2];
const FALLBACK = 0x4fe3f5;

// readPalette returns the package colors of the current scheme in settings/colors.css.
export function readPalette(): THREE.Color[] {
  const list = getComputedStyle(document.documentElement).getPropertyValue('--package-colors');
  return list
    .split(',')
    .map((item) => item.trim())
    .filter((item) => item !== '' && CSS.supports('color', item))
    .map((item) => new THREE.Color(item));
}

// readColor returns a color variable of settings/colors.css, or fallback.
export function readColor(name: string, fallback: number): THREE.Color {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  return value && CSS.supports('color', value) ? new THREE.Color(value) : new THREE.Color(fallback);
}

// packageColors assigns palette slots in sorted name order, so a package keeps
// its color across reindexing while the set of packages stays the same.
export function packageColors(modules: readonly string[], palette: readonly THREE.Color[]): THREE.Color[] {
  const slots = palette.length ? palette : [new THREE.Color(FALLBACK)];
  const rank = new Map([...modules].sort().map((name, i) => [name, i]));
  return modules.map((name) => {
    const r = rank.get(name) ?? 0;
    const shift = CYCLE_SHIFTS[Math.floor(r / slots.length) % CYCLE_SHIFTS.length];
    return slots[r % slots.length].clone().offsetHSL(0, 0, shift);
  });
}
