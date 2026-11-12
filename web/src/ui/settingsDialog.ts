import * as THREE from 'three';
import { packageColors } from '../settings/palette.ts';
import { HEX_COLOR, MAX_THEME_COLORS, type Options, type Scheme, type Settings } from '../settings/settings.ts';
import { type Result } from '../session/events.ts';

export interface SettingsDialogElements {
  dialog: HTMLDialogElement;
  form: HTMLFormElement;
  // The title bar button that opens the dialog. The focus goes back to it
  // when the dialog closes.
  opener: HTMLButtonElement;
  main: HTMLElement;
  skipWaiting: HTMLInputElement;
  // The theme of the open working tree, and the default theme, each with
  // the swatch row of its selected theme under it.
  projectTheme: HTMLSelectElement;
  projectSwatches: HTMLElement;
  defaultTheme: HTMLSelectElement;
  defaultSwatches: HTMLElement;
  addTheme: HTMLButtonElement;
  editTheme: HTMLButtonElement;
  deleteTheme: HTMLButtonElement;
  autoRotate: HTMLInputElement;
  motionNote: HTMLElement;
  followEdit: HTMLInputElement;
  speed: HTMLSelectElement;
  status: HTMLElement;
  reset: HTMLButtonElement;
  cancel: HTMLButtonElement;
  save: HTMLButtonElement;
  editor: HTMLElement;
  editorLabel: HTMLInputElement;
  editorName: HTMLElement;
  editorRows: HTMLElement;
  addColor: HTMLButtonElement;
  preview: HTMLElement;
  editorDone: HTMLButtonElement;
  editorCancel: HTMLButtonElement;
}

export interface SettingsDialogHooks {
  // onSave sends the settings to the server.
  onSave(settings: Settings): Promise<Result<Settings>>;
  // preview shows the colors on the graph while the editor is open. The
  // page puts the saved scheme back when the dialog closes.
  preview(colors: readonly string[]): void;
}

// dropIndex returns the index that the dragged row from gets when the
// pointer is at y: the number of the other rows whose middle is above y.
// middles holds the middle y of each row, the dragged row too.
export function dropIndex(middles: readonly number[], from: number, y: number): number {
  return middles.filter((middle, i) => i !== from && middle < y).length;
}

// SettingsDialog is the modal settings form. open fills the controls from
// the settings and the options of the server. The theme section lists the
// schemes of the server and the custom themes, and an editor view makes and
// changes custom themes. Save sends everything in one POST; Cancel and Esc
// close with no change.
export class SettingsDialog {
  private readonly el: SettingsDialogElements;
  private readonly hooks: SettingsDialogHooks;
  private options: Options | null = null;
  // The packages of the open graph, in sorted name order, for the hover
  // text of the swatches and the preview of the editor, and the root path
  // of the tree, for its theme pick.
  private packages: string[] = [];
  private root = '';
  // The themes and the project picks while the dialog is open. Save sends
  // them; Cancel drops them. defaultSelection is the global default list,
  // kept here so a rebuild does not lose it.
  private themes: Scheme[] = [];
  private projectThemes: Record<string, string> = {};
  private defaultSelection = 'default';
  // The theme in the editor, and its state before the editor opened.
  private editing = -1;
  private before: Scheme | null = null;

  constructor(el: SettingsDialogElements, reducedMotion: boolean, hooks: SettingsDialogHooks) {
    this.el = el;
    this.hooks = hooks;
    el.motionNote.hidden = !reducedMotion;
    el.cancel.addEventListener('click', () => el.dialog.close());
    el.reset.addEventListener('click', () => {
      // Reset shows the default controls. The themes are data, not
      // controls, so Reset keeps them.
      if (this.options) this.fill({ ...this.options.defaults, themes: this.themes });
    });
    el.dialog.addEventListener('close', () => el.opener.focus());
    el.defaultTheme.addEventListener('change', () => {
      this.defaultSelection = el.defaultTheme.value;
      this.showSwatches(el.defaultSwatches, this.defaultSelection);
      this.showSwatches(el.projectSwatches, this.projectName());
      this.updateThemeButtons();
    });
    el.projectTheme.addEventListener('change', () => {
      // The pick lives in the map at once, so a rebuild keeps it.
      if (this.root) {
        if (el.projectTheme.value) this.projectThemes[this.root] = el.projectTheme.value;
        else delete this.projectThemes[this.root];
      }
      this.showSwatches(el.projectSwatches, this.projectName());
      this.updateThemeButtons();
    });
    el.addTheme.addEventListener('click', () => this.addNewTheme());
    el.editTheme.addEventListener('click', () => {
      const index = this.themes.findIndex((theme) => theme.name === this.projectName());
      if (index >= 0) this.openEditor(index);
    });
    el.deleteTheme.addEventListener('click', () => this.deleteSelected());
    el.editorLabel.addEventListener('input', () => this.renameEditing());
    el.addColor.addEventListener('click', () => {
      const theme = this.themes[this.editing];
      if (!theme || theme.colors.length >= MAX_THEME_COLORS) return;
      theme.colors.push(theme.colors[theme.colors.length - 1]);
      this.renderEditor();
    });
    el.editorDone.addEventListener('click', () => this.closeEditor(true));
    el.editorCancel.addEventListener('click', () => this.closeEditor(false));
    el.form.addEventListener('submit', (event) => {
      event.preventDefault();
      void (async () => {
        el.save.disabled = true;
        el.status.textContent = '';
        const result = await this.hooks.onSave(this.read());
        el.save.disabled = false;
        if (result.ok) el.dialog.close();
        else el.status.textContent = result.error;
      })();
    });
  }

  open(settings: Settings, options: Options, packages: readonly string[], root: string): void {
    this.options = options;
    this.packages = [...packages].sort();
    this.root = root;
    this.themes = settings.themes.map((theme) => ({ ...theme, colors: [...theme.colors] }));
    this.projectThemes = { ...settings.projectThemes };
    this.editing = -1;
    this.el.main.hidden = false;
    this.el.editor.hidden = true;
    this.buildSpeeds(options);
    this.fill(settings);
    this.el.status.textContent = '';
    this.el.dialog.showModal();
    this.el.skipWaiting.focus();
  }

  // projectName is the theme that the open working tree shows: its pick,
  // or the global default. The New, Edit, and Delete buttons act on it.
  private projectName(): string {
    return (this.root && this.projectThemes[this.root]) || this.defaultSelection;
  }

  private allSchemes(): Scheme[] {
    return this.options ? [...this.options.colorSchemes, ...this.themes] : [...this.themes];
  }

  // packagesFor names the packages that get color index of count colors.
  // packageColors gives the colors to the packages in sorted name order and
  // repeats the list when it runs out.
  private packagesFor(index: number, count: number): string {
    const names: string[] = [];
    for (let i = index; i < this.packages.length; i += count) names.push(this.packages[i]);
    return names.length ? `Packages: ${names.join(', ')}` : 'No package uses this color yet.';
  }

  // buildSchemes fills both lists with the schemes of the server, then the
  // custom themes. The project list has "Use the global default" first.
  private buildSchemes(): void {
    const all = this.allSchemes();
    const option = (value: string, text: string) => {
      const item = document.createElement('option');
      item.value = value;
      item.textContent = text;
      return item;
    };
    if (!all.some((scheme) => scheme.name === this.defaultSelection)) this.defaultSelection = all[0]?.name ?? 'default';
    this.el.defaultTheme.replaceChildren(...all.map((scheme) => option(scheme.name, scheme.label)));
    this.el.defaultTheme.value = this.defaultSelection;
    this.el.projectTheme.replaceChildren(option('', 'Use the global default'), ...all.map((scheme) => option(scheme.name, scheme.label)));
    const pick = this.root ? this.projectThemes[this.root] : undefined;
    this.el.projectTheme.value = pick && all.some((scheme) => scheme.name === pick) ? pick : '';
    this.el.projectTheme.disabled = this.root === '';
    this.showSwatches(this.el.defaultSwatches, this.defaultSelection);
    this.showSwatches(this.el.projectSwatches, this.projectName());
    this.updateThemeButtons();
  }

  // showSwatches draws the colors of the named theme in a swatch row. Each
  // swatch names the packages that get that color.
  private showSwatches(row: HTMLElement, name: string): void {
    const scheme = this.allSchemes().find((item) => item.name === name);
    row.replaceChildren();
    if (!scheme) return;
    scheme.colors.forEach((color, index) => {
      const sample = document.createElement('span');
      sample.className = 'sw';
      sample.style.background = color;
      sample.title = this.packagesFor(index, scheme.colors.length);
      row.appendChild(sample);
    });
  }

  // updateThemeButtons disables Edit and Delete when the project shows a
  // scheme of the server, because only custom themes change.
  private updateThemeButtons(): void {
    const custom = this.themes.some((theme) => theme.name === this.projectName());
    this.el.editTheme.disabled = !custom;
    this.el.deleteTheme.disabled = !custom;
  }

  private buildSpeeds(options: Options): void {
    this.el.speed.replaceChildren();
    for (const speed of options.speeds) {
      const option = document.createElement('option');
      option.value = String(speed);
      option.textContent = `${speed}×`;
      this.el.speed.appendChild(option);
    }
  }

  private fill(settings: Settings): void {
    this.el.skipWaiting.checked = settings.skipWaiting;
    this.defaultSelection = settings.colorScheme;
    this.buildSchemes();
    this.el.autoRotate.checked = settings.autoRotate;
    this.el.followEdit.checked = settings.followEdit;
    this.el.speed.value = String(settings.speed);
  }

  private read(): Settings {
    const projectThemes = { ...this.projectThemes };
    return {
      version: this.options?.defaults.version ?? 1,
      skipWaiting: this.el.skipWaiting.checked,
      colorScheme: this.defaultSelection,
      autoRotate: this.el.autoRotate.checked,
      followEdit: this.el.followEdit.checked,
      speed: Number(this.el.speed.value),
      themes: this.themes,
      projectThemes,
    };
  }

  // ---- Themes ------------------------------------------------------------

  // addNewTheme makes a theme with the colors of the theme that the project
  // shows, picks it for the project, and opens the editor for it. Without a
  // working tree, it becomes the global default instead.
  private addNewTheme(): void {
    const all = this.allSchemes();
    const source = all.find((scheme) => scheme.name === this.projectName()) ?? all[0];
    if (!source) return;
    let n = 1;
    while (this.themes.some((theme) => theme.label === `Theme ${n}`)) n++;
    const label = `Theme ${n}`;
    const taken = (name: string) => all.some((scheme) => scheme.name === name);
    let name = label.toLowerCase().replaceAll(' ', '-');
    for (let i = 2; taken(name); i++) name = `theme-${n}-${i}`;
    this.themes.push({ name, label, colors: [...source.colors] });
    if (this.root) this.projectThemes[this.root] = name;
    else this.defaultSelection = name;
    this.buildSchemes();
    this.openEditor(this.themes.length - 1);
  }

  // deleteSelected removes the theme that the project shows, and each
  // project pick that named it. A global default that named it falls back
  // to the first scheme.
  private deleteSelected(): void {
    const name = this.projectName();
    const index = this.themes.findIndex((theme) => theme.name === name);
    if (index < 0) return;
    this.themes.splice(index, 1);
    for (const [root, pick] of Object.entries(this.projectThemes)) {
      if (pick === name) delete this.projectThemes[root];
    }
    if (this.defaultSelection === name) this.defaultSelection = this.options?.colorSchemes[0]?.name ?? 'default';
    this.buildSchemes();
  }

  // renameEditing takes the label of the editor. The name follows the
  // label: lower case, with - for the other characters, and unique.
  private renameEditing(): void {
    const theme = this.themes[this.editing];
    if (!theme) return;
    const label = this.el.editorLabel.value.trim().slice(0, 40) || 'Theme';
    theme.label = label;
    const previous = theme.name;
    const others = (this.options ? [...this.options.colorSchemes] : []).concat(this.themes.filter((t) => t !== theme));
    const base = label.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replaceAll(/^-+|-+$/g, '').slice(0, 28) || 'theme';
    let name = base;
    for (let i = 2; others.some((scheme) => scheme.name === name); i++) name = `${base}-${i}`;
    theme.name = name;
    this.el.editorName.textContent = name;
    // The picks and the global default follow a rename.
    for (const [root, pick] of Object.entries(this.projectThemes)) {
      if (pick === previous) this.projectThemes[root] = name;
    }
    if (this.defaultSelection === previous) this.defaultSelection = name;
  }

  // ---- The editor --------------------------------------------------------

  private openEditor(index: number): void {
    const theme = this.themes[index];
    this.editing = index;
    this.before = { ...theme, colors: [...theme.colors] };
    this.el.editorLabel.value = theme.label;
    this.el.editorName.textContent = theme.name;
    this.el.main.hidden = true;
    this.el.editor.hidden = false;
    this.renderEditor();
    this.el.editorLabel.focus();
  }

  // closeEditor returns to the settings view. Without keep, the theme goes
  // back to its state before the editor opened.
  private closeEditor(keep: boolean): void {
    const theme = this.themes[this.editing];
    if (!keep && theme && this.before) {
      // The picks follow the name back too.
      for (const [root, pick] of Object.entries(this.projectThemes)) {
        if (pick === theme.name) this.projectThemes[root] = this.before.name;
      }
      if (this.defaultSelection === theme.name) this.defaultSelection = this.before.name;
      this.themes[this.editing] = this.before;
    }
    const current = this.themes[this.editing];
    this.editing = -1;
    this.before = null;
    this.el.editor.hidden = true;
    this.el.main.hidden = false;
    this.buildSchemes();
    this.hooks.preview(current?.colors ?? []);
  }

  // renderEditor draws the color rows and the package preview, and shows
  // the colors on the graph.
  private renderEditor(): void {
    const theme = this.themes[this.editing];
    if (!theme) return;
    this.el.addColor.disabled = theme.colors.length >= MAX_THEME_COLORS;
    this.el.editorRows.replaceChildren();
    theme.colors.forEach((color, index) => {
      this.el.editorRows.appendChild(this.colorRow(theme, color, index));
    });
    this.renderPreview(theme);
    this.hooks.preview(theme.colors);
  }

  private colorRow(theme: Scheme, color: string, index: number): HTMLElement {
    const row = document.createElement('div');
    row.className = 'colorrow';
    const handle = document.createElement('button');
    handle.type = 'button';
    handle.className = 'btn handle';
    handle.textContent = '⠿';
    handle.title = 'Drag to reorder. The arrow keys move the color too.';
    handle.setAttribute('aria-label', `Move color ${index + 1} of ${theme.colors.length}`);
    this.bindDrag(handle, row, index);
    handle.addEventListener('keydown', (event) => {
      const to = event.key === 'ArrowUp' ? index - 1 : event.key === 'ArrowDown' ? index + 1 : -1;
      if (to < 0 || to >= theme.colors.length) return;
      event.preventDefault();
      this.moveColor(index, to);
    });
    const input = document.createElement('input');
    input.type = 'color';
    input.value = color;
    input.title = this.packagesFor(index, theme.colors.length);
    const hex = document.createElement('span');
    hex.className = 'hex';
    hex.textContent = color;
    input.addEventListener('input', () => {
      const value = input.value.toLowerCase();
      if (!HEX_COLOR.test(value)) return;
      theme.colors[index] = value;
      hex.textContent = value;
      this.renderPreview(theme);
      this.hooks.preview(theme.colors);
    });
    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'btn trash';
    remove.title = 'Remove this color';
    remove.setAttribute('aria-label', 'Remove this color');
    remove.textContent = '🗑';
    remove.disabled = theme.colors.length === 1;
    remove.addEventListener('click', () => {
      theme.colors.splice(index, 1);
      this.renderEditor();
    });
    row.append(handle, input, hex, remove);
    return row;
  }

  private moveColor(from: number, to: number): void {
    const theme = this.themes[this.editing];
    if (!theme) return;
    const [color] = theme.colors.splice(from, 1);
    theme.colors.splice(to, 0, color);
    this.renderEditor();
    const handles = this.el.editorRows.querySelectorAll('button.handle');
    const handle = handles[to];
    if (handle instanceof HTMLButtonElement) handle.focus();
  }

  // bindDrag reorders the rows with the pointer events, so it works with
  // touch too. A line between the rows shows where the drag will drop.
  private bindDrag(handle: HTMLButtonElement, row: HTMLElement, index: number): void {
    handle.addEventListener('pointerdown', (down) => {
      down.preventDefault();
      handle.setPointerCapture(down.pointerId);
      let to = index;
      const rows = Array.from(this.el.editorRows.children);
      const others = rows.filter((_, i) => i !== index);
      // The line goes under the row that the color will follow, or over the
      // first row when the color goes to the top.
      const mark = (at: number) => {
        const moves = at !== index;
        const after = moves && at > 0 ? others[at - 1] : null;
        const before = moves && at === 0 ? others[0] : null;
        rows.forEach((r) => {
          r.classList.toggle('drop', r === after);
          r.classList.toggle('dropbefore', r === before);
        });
        row.classList.toggle('dragging', moves);
      };
      const find = (y: number) =>
        dropIndex(
          rows.map((r) => {
            const box = r.getBoundingClientRect();
            return box.top + box.height / 2;
          }),
          index,
          y,
        );
      const move = (event: PointerEvent) => {
        to = find(event.clientY);
        mark(to);
      };
      const up = () => {
        handle.removeEventListener('pointermove', move);
        handle.removeEventListener('pointerup', up);
        handle.removeEventListener('pointercancel', cancel);
        if (to !== index) this.moveColor(index, to);
        else mark(index);
      };
      const cancel = () => {
        to = index;
        up();
      };
      handle.addEventListener('pointermove', move);
      handle.addEventListener('pointerup', up);
      handle.addEventListener('pointercancel', cancel);
    });
  }

  // renderPreview lists the packages of the open graph with the color that
  // each gets from the theme, with the lightness variation of the palette.
  private renderPreview(theme: Scheme): void {
    this.el.preview.replaceChildren();
    if (this.packages.length === 0) {
      this.el.preview.textContent = 'Open a working tree to see its packages here.';
      return;
    }
    const palette = theme.colors.map((color) => new THREE.Color(color));
    const colors = packageColors(this.packages, palette);
    this.packages.forEach((name, i) => {
      const item = document.createElement('div');
      item.className = 'pkgrow';
      const sample = document.createElement('span');
      sample.className = 'sw';
      sample.style.background = `#${colors[i].getHexString()}`;
      const text = document.createElement('span');
      text.textContent = name;
      item.append(sample, text);
      this.el.preview.appendChild(item);
    });
  }
}
