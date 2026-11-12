import { getJSON, postJSON } from '../net/live.ts';
import { isRecord, type Result } from '../session/events.ts';

// The server keeps the settings in one file, so each browser gets the same
// settings. It also owns the rules: the defaults, the valid speeds, and the
// color schemes. internal/settings/settings.go has the same fields.
export interface Settings {
  version: number;
  // Jump over the time that the agent waits for the user during playback.
  skipWaiting: boolean;
  // The name of a color scheme in options.colorSchemes.
  colorScheme: string;
  // The state of the page when it opens.
  autoRotate: boolean;
  followEdit: boolean;
  speed: number;
  // The custom themes of the viewer. The schemes of the server are
  // read-only; colorScheme can name either.
  themes: Scheme[];
  // The theme of each working tree, by its absolute root path. A tree
  // without an entry uses colorScheme.
  projectThemes: Record<string, string>;
}

// themeFor returns the theme name for the working tree at root.
export function themeFor(settings: Settings, root: string): string {
  return (root && settings.projectThemes[root]) || settings.colorScheme;
}

// Scheme is a color scheme of the graph. colors are #rrggbb strings.
export interface Scheme {
  name: string;
  label: string;
  colors: string[];
}

export interface Options {
  defaults: Settings;
  speeds: number[];
  colorSchemes: Scheme[];
}

function bool(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === 'boolean' ? value : null;
}

function numbers(value: unknown): number[] {
  return Array.isArray(value) ? value.filter((item): item is number => typeof item === 'number' && Number.isFinite(item)) : [];
}

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

export const HEX_COLOR = /^#[0-9a-f]{6}$/;
export const MAX_THEME_COLORS = 12;

function parseScheme(value: unknown): Scheme | null {
  if (!isRecord(value)) return null;
  const name = value['name'];
  const label = value['label'];
  const colors = strings(value['colors']);
  if (typeof name !== 'string' || name === '' || typeof label !== 'string' || colors.length === 0) return null;
  return { name, label, colors };
}

// parseThemes reads the custom themes and drops a theme that is not valid,
// that uses the name of a scheme, or that repeats a name.
function parseThemes(value: unknown, schemes: readonly Scheme[]): Scheme[] {
  if (!Array.isArray(value)) return [];
  const themes: Scheme[] = [];
  for (const item of value) {
    const theme = parseScheme(item);
    if (!theme || theme.colors.length > MAX_THEME_COLORS || !theme.colors.every((color) => HEX_COLOR.test(color))) continue;
    if (schemes.some((s) => s.name === theme.name) || themes.some((s) => s.name === theme.name)) continue;
    themes.push(theme);
  }
  return themes;
}

// readSettings reads each field of a settings object. A field that is
// missing or not valid is null, so the caller decides what to do.
function readSettings(value: unknown, speeds: readonly number[], schemes: readonly Scheme[]): { [K in keyof Settings]: Settings[K] | null } {
  if (!isRecord(value)) return { version: null, skipWaiting: null, colorScheme: null, autoRotate: null, followEdit: null, speed: null, themes: null, projectThemes: null };
  const themes = parseThemes(value['themes'], schemes);
  const scheme = value['colorScheme'];
  const speed = value['speed'];
  const known = (name: string) => schemes.some((item) => item.name === name) || themes.some((item) => item.name === name);
  const picks = value['projectThemes'];
  const projectThemes: Record<string, string> = {};
  if (isRecord(picks)) {
    for (const [root, name] of Object.entries(picks)) {
      if (root.startsWith('/') && typeof name === 'string' && known(name)) projectThemes[root] = name;
    }
  }
  return {
    version: value['version'] === 1 ? 1 : null,
    skipWaiting: bool(value, 'skipWaiting'),
    colorScheme: typeof scheme === 'string' && known(scheme) ? scheme : null,
    autoRotate: bool(value, 'autoRotate'),
    followEdit: bool(value, 'followEdit'),
    speed: typeof speed === 'number' && speeds.includes(speed) ? speed : null,
    themes,
    projectThemes,
  };
}

// parseOptions reads the options of the server. It fails if a list is
// empty or if the defaults do not pass their own rules.
export function parseOptions(value: unknown): Result<Options> {
  if (!isRecord(value)) return { ok: false, error: 'settings options are not an object' };
  const speeds = numbers(value['speeds']);
  const colorSchemes = Array.isArray(value['colorSchemes']) ? value['colorSchemes'].map(parseScheme).filter((item) => item !== null) : [];
  if (speeds.length === 0) return { ok: false, error: 'settings options have no speeds' };
  if (colorSchemes.length === 0) return { ok: false, error: 'settings options have no color schemes' };
  const read = readSettings(value['defaults'], speeds, colorSchemes);
  const { version, skipWaiting, colorScheme, autoRotate, followEdit, speed } = read;
  if (version === null || skipWaiting === null || colorScheme === null || autoRotate === null || followEdit === null || speed === null) {
    return { ok: false, error: 'settings options have no valid defaults' };
  }
  return { ok: true, value: { defaults: { version, skipWaiting, colorScheme, autoRotate, followEdit, speed, themes: read.themes ?? [], projectThemes: read.projectThemes ?? {} }, speeds, colorSchemes } };
}

// parseSettings reads a settings object. A field that is missing or not
// valid gets its default from the options.
export function parseSettings(value: unknown, options: Options): Settings {
  const read = readSettings(value, options.speeds, options.colorSchemes);
  const defaults = options.defaults;
  return {
    version: read.version ?? defaults.version,
    skipWaiting: read.skipWaiting ?? defaults.skipWaiting,
    colorScheme: read.colorScheme ?? defaults.colorScheme,
    autoRotate: read.autoRotate ?? defaults.autoRotate,
    followEdit: read.followEdit ?? defaults.followEdit,
    speed: read.speed ?? defaults.speed,
    themes: read.themes ?? [],
    projectThemes: read.projectThemes ?? {},
  };
}

// loadSettings gets the settings and the options from the server.
export async function loadSettings(): Promise<Result<{ settings: Settings; options: Options }>> {
  const result = await getJSON('/api/settings');
  if (!result.ok) return result;
  if (!isRecord(result.value)) return { ok: false, error: '/api/settings: answer is not an object' };
  const options = parseOptions(result.value['options']);
  if (!options.ok) return options;
  return { ok: true, value: { settings: parseSettings(result.value['settings'], options.value), options: options.value } };
}

// saveSettings sends the settings to the server and returns the settings
// that the server kept, or the server's error text.
export async function saveSettings(settings: Settings, options: Options): Promise<Result<Settings>> {
  const result = await postJSON('/api/settings', settings);
  return result.ok ? { ok: true, value: parseSettings(result.value, options) } : result;
}
