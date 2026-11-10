# Plan 15: Settings

This plan adds a settings dialog to the page. The server keeps the settings
in a file, so each browser gets the same settings. The settings are:

- Skip the waits during playback.
- The color scheme of the graph.
- The startup state: auto-rotate, Follow edit, and the playback speed.

The text uses Simplified Technical English (ASD-STE100). Code names, file
names, and commands are technical names. Do not change them.

## Depends on

Nothing. See "Other plans" at the end for the changes that apply if plans
07, 10, or 14 are merged first.

## Decisions

- **One file on the server.** The server keeps the settings in
  `$XDG_CONFIG_HOME/agent-work-visualizer/settings.json`, or in
  `~/.config/agent-work-visualizer/settings.json` if `XDG_CONFIG_HOME` is
  empty. Use this path on all operating systems, as `session.DefaultDir`
  does for the logs.
- **No file is necessary.** If the file does not exist, the server uses the
  defaults. The server writes the file only when the viewer saves.
- **Startup settings apply only at startup.** Auto-rotate, Follow edit, and
  the speed set the state when the page opens. The buttons on the page
  still change the state for that page. A button does not change the
  settings.
- **The last speed overrides the setting.** The speed button keeps the last
  speed in `localStorage` (`awv.speed`), as now. At startup, the page uses
  `awv.speed` if it has a valid value. Else it uses the `speed` setting.
  When the viewer saves a new `speed` in the dialog, the page removes
  `awv.speed`, so the new setting applies at the next startup.
- **Reduced motion wins.** If the operating system asks for reduced
  motion, auto-rotate is off at startup, as now.
- **The other settings apply at once.** Skip waits and the color scheme
  change the open page when the viewer saves.
- **Other open pages** read the settings again when they reload. The server
  does not send the settings to them.
- **The panel widths, the recent working trees, and the view (plan 13)**
  stay in `localStorage`. They are per browser, not settings.

## Before you start

1. Make a new branch from `main`.
2. Run `go test ./...`, `npm --prefix web test`, and
   `npm --prefix web run typecheck`. Make sure that they pass.
3. Make a test session. See "A test session" in `docs/plans/README.md`.

## Task 1: Read and write the settings file

### Result

A new package `internal/settings` has:

- `Settings`, with these JSON fields:

  | Field | Type | Default | Valid values |
  | --- | --- | --- | --- |
  | `version` | number | `1` | `1` |
  | `skipWaiting` | bool | `true` | `true`, `false` |
  | `colorScheme` | string | `"default"` | 1 to 32 characters of `a-z`, `0-9`, and `-` |
  | `autoRotate` | bool | `true` | `true`, `false` |
  | `followEdit` | bool | `true` | `true`, `false` |
  | `speed` | number | `1` | `1`, `2`, `4`, `8`, `16` |

- `Defaults()`: the defaults above.
- `DefaultPath()`: the path of the decision "One file on the server".
- `Load(path)`: returns the settings in the file.
  - If the file does not exist, it returns `Defaults()` and no error.
  - If the file is not valid JSON, if a value has the wrong type, or if
    `version` is not `1`, it returns `Defaults()` and an error. The server
    logs the error and continues with the defaults. A Save then replaces
    the file.
  - It ignores fields that it does not know.
  - A field that is missing or not valid gets its default. The other fields
    keep their values.
- `Validate(s)`: returns an error that names the first field that is not
  valid.
- `Save(path, s)`: writes the file. It makes the directory if necessary
  (mode `0755`). It writes a temporary file in the same directory, then
  renames it, so a failed write does not leave a damaged file. The file
  mode is `0644`. The JSON is indented, so a person can edit the file.

The server does not know the names of the color schemes, because the
schemes are in CSS. It accepts any name of the valid form. The page uses
`default` for a name that it does not know.

### Tests

1. Make `internal/settings/settings_test.go`. Use `t.TempDir()` for the
   files. Use `t.Setenv` for `XDG_CONFIG_HOME`.
2. Test `Load` with no file, with a full file, with a file that has one
   field not valid (`"speed": 3`), with an unknown field, with
   `"version": 2`, with a value of the wrong type (`"speed": "fast"`), and
   with a file that is not JSON. Expect `Defaults()` and an error for
   `version` 2, for the wrong type, and for the file that is not JSON.
3. Test that `Save`, then `Load`, gives the same settings.
4. Test that `Validate` rejects each value in the "Valid values" column
   that is not valid: `speed` 3, `colorScheme` `""`, `colorScheme`
   `"Bad Name"`, and `version` 2.
5. Test `DefaultPath` with and without `XDG_CONFIG_HOME`.

## Task 2: Serve the settings

### Result

- `Config` in `internal/server/server.go` has a new field `SettingsPath`.
  `main.go` sets it from `settings.DefaultPath()`.
- `Run` loads the settings once. If `Load` returns an error, `Run` logs
  it with `log.Printf` and continues with the defaults that `Load`
  returned. `liveServer` gets three fields:
  `settingsPath string`, `settings settings.Settings`, and
  `settingsMu sync.Mutex`. The existing `mu` guards only `view`, so the
  settings get their own mutex. The tests make a `liveServer` literal
  with a `settingsPath` in `t.TempDir()`.
- `GET /api/settings` returns the current settings as JSON.
- `POST /api/settings` takes a full `Settings` object. The decoder uses
  `DisallowUnknownFields`, so a field that the server does not know is an
  error. It decodes into a zero `Settings`, so a missing field fails
  `Validate`. A decode error returns 400 with the decoder's text, which
  names the field. The settings and the file do not change.
  - If `Validate` fails, it returns 400 with the error text. The settings
    and the file do not change.
  - If `Save` fails, it returns 500 with the error text. The settings in
    memory do not change.
  - Else it keeps the new settings and returns them as JSON.
- The cross-origin check in `routes` already applies to the POST. Do not
  add a second check.

### Tests

1. In `internal/server/server_test.go`, test GET with no file. Expect the
   defaults.
2. Test POST with valid settings, then GET. Expect the new settings. Expect
   the file to hold them.
3. Test POST with `speed` 3, and with an unknown field. Expect 400 with
   text that names the field, and expect GET to return the old settings.
4. Add `/api/settings` to the path list in
   `TestServeControlsRejectOtherOrigins`. It already sends an `Origin` of
   another site and of `null`, and expects 403.

## Task 3: Use the settings in the page

### Result

`web/src/settings/settings.ts` has:

- `interface Settings` with the fields of Task 1.
- `DEFAULT_SETTINGS`.
- `parseSettings(value: unknown): Settings`. It uses type guards, not `as`
  casts. A field that is missing or not valid gets its default.
- `loadSettings(): Promise<Settings>`. It calls `getJSON('/api/settings')`
  from `web/src/net/live.ts`. If the request fails, it returns
  `DEFAULT_SETTINGS`, so the page still opens with an older server.
- `saveSettings(s): Promise<Result<Settings>>`. It calls `postJSON`.

In `web/src/main.ts`:

- Code at the top level of the module sets the startup state
  (`autoRotate`, `followCamera`, the read of `awv.speed`, and the
  `aria-pressed` of the rotate button), and a fetch is asynchronous. So
  the settings cannot load before that code runs. Instead, `load()`
  calls `loadSettings()` one time, before `fetchGraph()`, and then calls
  a new `applyStartup(settings)`. The retry of `load()` after a failed
  graph fetch does not load the settings again. Nothing that the
  settings control is visible before the graph arrives, so this does
  not delay the page.
- `applyStartup` sets `autoRotate` to
  `settings.autoRotate && !reducedMotion`, sets `followCamera` (the
  Follow edit button) to `settings.followEdit`, and sets `aria-pressed`
  on both buttons from code. Remove `aria-pressed="true"` from
  `btnFollow` in `web/index.html`; `btnRot` already gets it from code.
- `applyStartup` sets `speed` to `settings.speed` if `awv.speed` holds no
  value in `SPEEDS`, then calls `setSpeedUI()`. Keep the top-level read
  of `awv.speed`, `SPEED_KEY`, and the write of `awv.speed` in the speed
  button handler, as now.
- `applyStartup` also sets `playback.skipWaiting` and calls
  `applyColorScheme`, as the next bullets describe.
- After a save that changes `speed`, remove `awv.speed` from
  `localStorage`. Read and write `localStorage` in try/catch, as now.
- `playback.skipWaiting` (`web/src/settings/playback.ts`) is set from
  `settings.skipWaiting`. Change the comment at the top of that file: the
  settings now set the value.
- `applyColorScheme(name)` sets `data-color-scheme` on `<html>`. For
  `default`, or for a name that is not in `COLOR_SCHEMES` (Task 5), it
  removes the attribute.

### Tests

1. Make `web/src/settings/settings.test.ts`. Test `parseSettings` with a
   full object, with `speed` 3, with a missing field, and with `null`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Task 4: The settings dialog

### Result

- The title bar has a SETTINGS button after PAUSE AGENT, with the look of
  the other title bar buttons.
- The button opens a modal `<dialog>`. Esc and Cancel close it with no
  change.
- The dialog has two groups:

  **Playback and graph** (applies when you save)

  | Control | Setting |
  | --- | --- |
  | Check box: "Skip the time the agent waits for you" | `skipWaiting` |
  | Radio buttons: one per color scheme, each with a row of color samples | `colorScheme` |

  **When the page opens**

  | Control | Setting |
  | --- | --- |
  | Check box: "Auto-rotate the graph" | `autoRotate` |
  | Check box: "Follow edit" | `followEdit` |
  | List: 1×, 2×, 4×, 8×, 16× | `speed` |

  An `<option>` cannot hold the color samples, so the color scheme uses
  radio inputs, one `<label>` each. The speed list is a `<select>`.

  A note under the second group says: "These apply the next time the page
  opens." A note next to the speed list says: "The speed button keeps your
  last speed. Saving a new speed here clears it." If the operating system
  asks for reduced motion, a note next to auto-rotate says that auto-rotate
  stays off.

- **Save** sends the settings to the server.
  - If the server accepts them, the dialog closes. The page applies
    `skipWaiting` and `colorScheme` at once.
  - If the server returns an error, the dialog stays open and shows the
    error text in a status line (`role="status"`).
- **Reset** sets the controls to `DEFAULT_SETTINGS`. It does not save.
  The viewer clicks Save to keep them.
- Each control has a `<label>`. The dialog has `aria-labelledby` on its
  title. The focus goes to the first control when the dialog opens, and
  back to the SETTINGS button when it closes.
- The dialog uses the colors and fonts of the page. It fits in a window
  that is 400 px wide.

### Procedure

1. Add the button and the `<dialog>` to `web/index.html`.
2. Make `web/src/ui/settingsDialog.ts` with a `SettingsDialog` class. Its
   constructor takes the elements and an `onSave(s)` callback. It has
   `open(s: Settings)`.
3. Add the CSS to `web/src/style.css`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Task 5: Color schemes

### Problem

`web/src/settings/colors.css` has only the default scheme. The graph reads
the package colors only in `GraphScene.setGraph`
(`web/src/view/scene.ts:261`). A change of scheme does not change the
graph until it is drawn again.

### Result

- `web/src/settings/palette.ts` exports `COLOR_SCHEMES`: a list of
  `{ name, label }`. The first item is `{ name: 'default', label:
  'Default' }`.
- A scheme block uses the selector `[data-color-scheme="<name>"]`, with
  no `:root`. `:root` matches only `<html>`, so a hidden sample element
  with the attribute would not match a `:root[...]` rule. Change the
  default block to `:root, [data-color-scheme="default"]`, so a sample
  element for the default scheme reads the default colors while another
  scheme is active. Change the comment at the end of the file to show the
  new form.
- `colors.css` has at least one more scheme. Each scheme must pass the
  checks that the comment at the top of the file gives for the default
  scheme:
  - Each color is visible on the `#04060b` background.
  - Adjacent colors are different for a viewer with a color vision
    deficiency (ΔE of 8 or more).
  - No color looks like the coral of an edit, or like `--edge-calls` or
    `--edge-called-by`.
  Write the measured values in a comment above the scheme, as the default
  scheme does.
- `GraphScene.recolor()` reads the palette again. It sets the halo and core
  colors of each node and the ring color of each module. It does not move
  a node or the camera. For this, the scene must keep `graph.modules` and
  `graph.modOf` from `setGraph`, and the ring color `BufferAttribute`
  that `drawGroups` now makes and discards. Nothing else on the page
  reads the palette, so the scene is the only thing to recolor.
- After the page sets `data-color-scheme`, it calls `scene.recolor()`.
- The color samples in the dialog come from each scheme's
  `--package-colors`. To read a scheme that is not active, read the
  variable with `getComputedStyle` on a hidden element that has the
  `data-color-scheme` attribute. The sample element for the default
  scheme has `data-color-scheme="default"`. Do not copy the colors into
  TypeScript.

### Procedure

1. Add the scheme to `colors.css` and its name to `COLOR_SCHEMES`.
2. In `GraphScene`, keep `graph.modules`, `graph.modOf`, and the ring
   color `BufferAttribute` from `setGraph` and `drawGroups`. Add
   `recolor()`: compute the colors with
   `packageColors(modules, readPalette())`, set `haloBase` and `coreBase`,
   write the ring colors into the attribute, and set `needsUpdate`.
3. Add a test to `web/src/settings/palette.test.ts`: each name in
   `COLOR_SCHEMES` other than `default` has a block in `colors.css`. Read
   the file as text.

## Validation

All steps must pass.

1. `go test ./...`, `npm --prefix web test`, and
   `npm --prefix web run typecheck` pass.
2. Move `~/.config/agent-work-visualizer/settings.json` away, if it
   exists. Start the server on this repository with the test session.
   Open the page. In the developer tools, remove `awv.speed` from
   `localStorage`, if it exists. Reload.
3. The page opens as before this plan: auto-rotate on, Follow edit on,
   speed 1×. The settings file does not exist.
4. Click SETTINGS. The dialog shows the defaults. Press Esc. The dialog
   closes. The settings file still does not exist.
5. Open the dialog. Turn off auto-rotate and Follow edit, and set the speed
   to 4×. Click Save. The page state does not change. The settings file
   exists and holds the new values.
6. Reload. Auto-rotate is off, Follow edit is off, and the speed is 4×.
7. Click the speed button to 8×. Reload. The speed is 8×, because the last
   speed overrides the setting.
8. Open the page in a second browser that has no `awv.speed`. It opens
   with auto-rotate off, Follow edit off, and the speed at 4×.
9. In the first browser, set the speed to 2× in the dialog and click Save.
   Reload. The speed is 2×. In the developer tools, `localStorage` has no
   `awv.speed` key until you click the speed button again.
10. Pick the test session. Turn off skip waits in the dialog and click Save.
    Play the session over a wait. The playhead moves through the wait at the
    playback speed. Turn skip waits on again. The playhead jumps over the
    wait.
11. Pick the second color scheme and click Save. The node and ring colors
    change at once. No node moves. The camera does not move. Reload. The
    second scheme is still active.
12. Edit the settings file by hand: set `"colorScheme": "nosuch"`. Reload.
    The page uses the default scheme.
13. Edit the settings file by hand: set `"speed": 3`. Reload. Open the
    dialog. The speed shows 1×, and the other settings keep their values.
14. Make the settings directory read-only. Change a setting and click Save.
    The dialog stays open and shows the error. Make the directory writable
    again.
15. Click Reset in the dialog. The controls show the defaults. Click
    Cancel. Open the dialog again. The saved values show.
16. Use only the keyboard: Tab to SETTINGS, Enter, change each control,
    Tab to Save, Enter. The focus goes back to SETTINGS.
17. Turn on reduced motion in the operating system. Set auto-rotate on in
    the settings. Reload. Auto-rotate is off. The dialog shows the note
    next to auto-rotate.
18. Make the window 400 px wide. Open the dialog. All controls show with
    no horizontal scroll.
19. Run
    `curl -s -X POST -H 'Origin: http://example.com' -d '{}' http://127.0.0.1:7777/api/settings`.
    The server returns `cross-origin request`.

## Other plans

- **Plan 07 (view host).** If plan 07 is merged first, the startup state
  of auto-rotate is in `web/src/view/graph3d.ts`. Follow edit and the speed
  stay in the shell. `recolor` becomes a method of `GraphView`.
- **Plan 10 (2D board).** If the 2D board exists, `Graph2D.recolor()` sets
  `--pc` on each package card again.
- **Plan 14 (isolate).** If plan 14 is merged first, `recolor()` must keep
  the dim rings of an isolation. Calculate the ring colors from the base
  colors and the isolation in one function, and call it from both
  `recolor()` and `setIsolation()`.
- **Plan 16 (slow speeds).** Plan 16 adds speeds below 1× and arrow keys
  on the speed button. Both plans change `SPEEDS` and the speed button
  handler. The plan that is merged second must also update the valid
  values of the `speed` setting in `Validate`, `parseSettings`, and the
  speed list of the dialog.
- **Plan 15.1 (Rust port).** The new package `internal/settings` and the
  two `/api/settings` routes are more surface for the Rust port to
  mirror.
