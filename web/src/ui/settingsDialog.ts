import { type Options, type Settings } from '../settings/settings.ts';
import { type Result } from '../session/events.ts';

export interface SettingsDialogElements {
  dialog: HTMLDialogElement;
  form: HTMLFormElement;
  // The title bar button that opens the dialog. The focus goes back to it
  // when the dialog closes.
  opener: HTMLButtonElement;
  skipWaiting: HTMLInputElement;
  schemes: HTMLElement;
  autoRotate: HTMLInputElement;
  motionNote: HTMLElement;
  followEdit: HTMLInputElement;
  speed: HTMLSelectElement;
  status: HTMLElement;
  reset: HTMLButtonElement;
  cancel: HTMLButtonElement;
  save: HTMLButtonElement;
}

// SettingsDialog is the modal settings form. open fills the controls from
// the settings and the options of the server. Save gives the settings to
// onSave; an error keeps the dialog open and shows the text. Esc and Cancel
// close the dialog with no change.
export class SettingsDialog {
  private readonly el: SettingsDialogElements;
  private options: Options | null = null;

  constructor(el: SettingsDialogElements, reducedMotion: boolean, onSave: (s: Settings) => Promise<Result<Settings>>) {
    this.el = el;
    el.motionNote.hidden = !reducedMotion;
    el.cancel.addEventListener('click', () => el.dialog.close());
    el.reset.addEventListener('click', () => {
      if (this.options) this.fill(this.options.defaults);
    });
    el.dialog.addEventListener('close', () => el.opener.focus());
    el.form.addEventListener('submit', (event) => {
      event.preventDefault();
      void (async () => {
        el.save.disabled = true;
        el.status.textContent = '';
        const result = await onSave(this.read());
        el.save.disabled = false;
        if (result.ok) el.dialog.close();
        else el.status.textContent = result.error;
      })();
    });
  }

  open(settings: Settings, options: Options): void {
    this.options = options;
    this.buildSchemes(options);
    this.buildSpeeds(options);
    this.fill(settings);
    this.el.status.textContent = '';
    this.el.dialog.showModal();
    this.el.skipWaiting.focus();
  }

  // buildSchemes makes one radio button per color scheme, with a row of
  // color samples from the scheme's colors.
  private buildSchemes(options: Options): void {
    this.el.schemes.replaceChildren();
    for (const scheme of options.colorSchemes) {
      const label = document.createElement('label');
      label.className = 'setrow';
      const radio = document.createElement('input');
      radio.type = 'radio';
      radio.name = 'colorScheme';
      radio.value = scheme.name;
      const name = document.createElement('span');
      name.textContent = scheme.label;
      const samples = document.createElement('span');
      samples.className = 'swatches';
      for (const color of scheme.colors) {
        const sample = document.createElement('span');
        sample.className = 'sw';
        sample.style.background = color;
        samples.appendChild(sample);
      }
      label.append(radio, name, samples);
      this.el.schemes.appendChild(label);
    }
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
    for (const radio of this.el.schemes.querySelectorAll('input')) {
      radio.checked = radio.value === settings.colorScheme;
    }
    this.el.autoRotate.checked = settings.autoRotate;
    this.el.followEdit.checked = settings.followEdit;
    this.el.speed.value = String(settings.speed);
  }

  private read(): Settings {
    const scheme = this.el.schemes.querySelector('input:checked');
    return {
      version: this.options?.defaults.version ?? 1,
      skipWaiting: this.el.skipWaiting.checked,
      colorScheme: scheme instanceof HTMLInputElement ? scheme.value : 'default',
      autoRotate: this.el.autoRotate.checked,
      followEdit: this.el.followEdit.checked,
      speed: Number(this.el.speed.value),
    };
  }
}
