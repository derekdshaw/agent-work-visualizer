# Plan 15: Settings

This plan adds a settings dialog to the page. The server keeps the settings
in a file, so each browser gets the same settings. The settings are:

- Skip the waits during playback.
- The color scheme of the graph, and custom color schemes (Task 6).
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
- **The server owns the rules.** The defaults, the valid speeds, and the
  color schemes, with their colors, are in Go. The page gets them from
  `GET /api/settings` and has no copy of them. So the page cannot open
  without the server, which is also true for the graph. A new speed or a
  new scheme is one Go edit, and plan 15.1 ports one list.
- **A color scheme is a name, a label, and RGB colors.** Each color is a
  `#rrggbb` string, because the screen and CSS use RGB. The page sets
  `--package-colors` on `<html>` from the scheme. `colors.css` keeps only
  the colors that are not part of a scheme.
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
  | `colorScheme` | string | `"default"` | the `Name` of a scheme in `Schemes` |
  | `autoRotate` | bool | `true` | `true`, `false` |
  | `followEdit` | bool | `true` | `true`, `false` |
  | `speed` | number | `1` | a value in `Speeds` |

- `Speeds`: `[1, 2, 4, 8, 16]`.
- `Scheme`, with the JSON fields `name` (`a-z`, `0-9`, and `-`), `label`,
  and `colors` (a list of `#rrggbb` strings). `Schemes` lists the schemes.
  The first is `default`, with the six colors that `colors.css` had. Each
  scheme must pass the checks in the comment at the top of `colors.css`:
  - Each color is visible on the `#04060b` background.
  - Adjacent colors are different for a viewer with a color vision
    deficiency (ΔE of 8 or more).
  - No color looks like the coral of an edit, or like `--edge-calls` or
    `--edge-called-by` in `colors.css`.
  Write the measured values in a comment above each scheme.
- `Options`, with the JSON fields `defaults` (`Defaults()`), `speeds`
  (`Speeds`), and `colorSchemes` (`Schemes`), and `AllOptions()` that
  returns it. The page gets its rules from this.
- `Defaults()`: the defaults above.
- `DefaultPath()`: the path of the decision "One file on the server".
- `Load(path)`: returns a `Loaded` with the settings in the file, `Found`
  (false if the file does not exist), and `Replaced` (one error for each
  value that was not valid and got its default). The server logs each of
  these states.
  - If the file does not exist, it returns `Defaults()` and no error.
  - If the file is not valid JSON, if a value has the wrong type, or if
    `version` is not `1`, it returns `Defaults()` and an error. The server
    logs the error and continues with the defaults. A Save then replaces
    the file.
  - It ignores fields that it does not know.
  - A field that is missing or not valid gets its default. The other fields
    keep their values. A value that is not valid is in `Replaced`.
- `Validate(s)`: returns an error that names the first field that is not
  valid.
- `Save(path, s)`: writes the file. It makes the directory if necessary
  (mode `0755`). It writes a temporary file in the same directory, then
  renames it, so a failed write does not leave a damaged file. The file
  mode is `0644`. The JSON is indented, so a person can edit the file.

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
   `"nosuch"`, and `version` 2.
5. Test `DefaultPath` with and without `XDG_CONFIG_HOME`.
6. Test `Schemes`: the first is `default`, each name has the valid form
   and is unique, and each color is a `#rrggbb` string.
7. Test the JSON field names of `Options` and of a `Scheme`, because
   `web/src/settings/settings.ts` reads them.

## Task 2: Serve the settings

### Result

- `Config` in `internal/server/server.go` has a new field `SettingsPath`.
  `main.go` sets it from `settings.DefaultPath()`.
- `Run` loads the settings once and logs one of these with `log.Printf`:
  the error, if `Load` returns one, and the defaults apply; "No settings
  file at <path>; using the defaults" if the file does not exist; else one
  line for each value in `Replaced`, then "Loaded settings from <path>".
  `liveServer` gets three fields:
  `settingsPath string`, `settings settings.Settings`, and
  `settingsMu sync.Mutex`. The existing `mu` guards only `view`, so the
  settings get their own mutex. The tests make a `liveServer` literal
  with a `settingsPath` in `t.TempDir()`.
- `GET /api/settings` returns `{"settings": ..., "options": ...}`: the
  current settings and `settings.AllOptions()`. One request gives the page
  all that it needs before it sets the startup state.
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
   defaults in `settings`, and `speeds` and `colorSchemes` in `options`.
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

- `interface Settings` with the fields of Task 1, `interface Scheme`, and
  `interface Options` with `defaults`, `speeds`, and `colorSchemes`. The
  page has no copy of the defaults, the speeds, or the schemes.
- `parseOptions(value: unknown): Result<Options>`. It uses type guards,
  not `as` casts. It fails if `speeds` or `colorSchemes` is empty, or if
  the defaults do not pass `parseSettings` with their own rules.
- `parseSettings(value: unknown, options: Options): Settings`. A field
  that is missing or not valid gets its default from `options`.
- `loadSettings(): Promise<Result<{ settings, options }>>`. It calls
  `getJSON('/api/settings')` from `web/src/net/live.ts`. If the request
  fails, `load()` shows the error as it does for a failed graph fetch,
  and retries.
- `saveSettings(s, options): Promise<Result<Settings>>`. It calls
  `postJSON`.

In `web/src/main.ts`:

- Code at the top level of the module sets the startup state
  (`autoRotate`, `followCamera`, the read of `awv.speed`, and the
  `aria-pressed` of the rotate button), and a fetch is asynchronous. So
  the settings cannot load before that code runs. Instead, `load()`
  calls `loadSettings()` one time, after the first `fetchGraph()` that
  succeeds and before `startView()`, and then calls a new
  `applyStartup(settings)`. A later `load()`, after a reset or a failed
  fetch, does not apply the settings again, so a server that was not
  reachable at first does not leave the page on the defaults. Nothing
  that the settings control is visible before the graph arrives, so this
  does not delay the page.
- `applyStartup` sets `autoRotate` to
  `settings.autoRotate && !reducedMotion`, sets `followCamera` (the
  Follow edit button) to `settings.followEdit`, and sets `aria-pressed`
  on both buttons from code. Remove `aria-pressed="true"` from
  `btnFollow` in `web/index.html`; `btnRot` already gets it from code.
- The valid speeds are a module-level `speeds` list that `applyStartup`
  sets from `options.speeds`. The speed button cycles over this list.
  `applyStartup` reads `awv.speed`, and sets `speed` to that value if it
  is in `speeds`, else to `settings.speed`. Then it calls `setSpeedUI()`.
  Keep `SPEED_KEY` and the write of `awv.speed` in the speed button
  handler, as now.
- `applyStartup` also sets `playback.skipWaiting` and calls
  `applyColorScheme`, as the next bullets describe.
- After a save that changes `speed`, remove `awv.speed` from
  `localStorage`. Read and write `localStorage` in try/catch, as now.
- `playback.skipWaiting` (`web/src/settings/playback.ts`) is set from
  `settings.skipWaiting`. Change the comment at the top of that file: the
  settings now set the value.
- `applyColorScheme(name)` finds the scheme in `options.colorSchemes`,
  or the first scheme if the name is not known, and sets
  `--package-colors` on `<html>` to its colors, as a comma-separated
  list. `readPalette` in `palette.ts` reads that variable, as now.
- `colors.css` loses `--package-colors` and the comment about scheme
  blocks. The comment at the top says that the schemes are in
  `internal/settings`. The checks in the comment stay, because the Go
  schemes refer to them.

### Tests

1. Make `web/src/settings/settings.test.ts`. Test `parseOptions` with a
   full object, with empty `speeds`, and with `null`. Test
   `parseSettings` with a full object, with `speed` 3, with a missing
   field, and with `null`.
2. Run `npm --prefix web test` and `npm --prefix web run typecheck`.

## Task 4: The settings dialog

### Result

- The title bar has a SETTINGS button after PAUSE AGENT, with the look of
  the other title bar buttons.
- The button opens a modal `<dialog>`. Esc and Cancel close it with no
  change.
- The dialog has two groups:

  **Playback** (applies when you save)

  | Control | Setting |
  | --- | --- |
  | Check box: "Skip the time the agent waits for you" | `skipWaiting` |

  **Theme** (applies when you save)

  A short description says that each package gets one color from the
  theme, in name order, and that the colors repeat lighter and darker when
  there are more packages than colors.

  | Control | Setting |
  | --- | --- |
  | Radio buttons: one per color scheme, each with a row of color samples | `colorScheme` |

  **When the page opens**

  | Control | Setting |
  | --- | --- |
  | Check box: "Auto-rotate the graph" | `autoRotate` |
  | Check box: "Follow edit" | `followEdit` |
  | List: one item per speed in `options.speeds` | `speed` |

  An `<option>` cannot hold the color samples, so the color scheme uses
  radio inputs, one `<label>` each. The dialog builds the radio inputs
  from `options.colorSchemes`, and the color samples from each scheme's
  `colors`. The speed list is a `<select>` built from `options.speeds`.

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
- **Reset** sets the controls to `options.defaults`. It does not save.
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
   `open(s: Settings, options: Options)`.
3. Add the CSS to `web/src/style.css`.

### Tests

1. Run `npm --prefix web test` and `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

## Task 5: Color schemes

### Problem

`settings.Schemes` has only the default scheme. The graph reads the
package colors only in `GraphScene.setGraph`
(`web/src/view/scene.ts:261`). A change of scheme does not change the
graph until it is drawn again.

### Result

- `settings.Schemes` has at least one more scheme, with the checks and
  the comment that Task 1 describes.
- `GraphScene.recolor()` reads the palette again. It sets the halo and core
  colors of each node and the ring color of each module. It does not move
  a node or the camera. For this, the scene must keep `graph.modules` and
  `graph.modOf` from `setGraph`, and the ring color `BufferAttribute`
  that `drawGroups` now makes and discards. Nothing else on the page
  reads the palette, so the scene is the only thing to recolor.
- After the page sets `--package-colors`, it calls `scene.recolor()`.

### Procedure

1. Add the scheme to `settings.Schemes`.
2. In `GraphScene`, keep `graph.modules`, `graph.modOf`, and the ring
   color `BufferAttribute` from `setGraph` and `drawGroups`. Add
   `recolor()`: compute the colors with
   `packageColors(modules, readPalette())`, set `haloBase` and `coreBase`,
   write the ring colors into the attribute, and set `needsUpdate`.
3. The `Schemes` test of Task 1 covers the new scheme.

## Task 6: Custom themes

### Problem

The viewer can select a theme, but cannot make one. The themes are in Go,
and a new theme is a code change.

### Decisions

- **A theme is an ordered list of colors.** `packageColors` in
  `web/src/settings/palette.ts` gives the colors to the packages in sorted
  name order, and repeats them lighter and darker when the list runs out.
  So the order of the colors is what decides which package gets which
  color, and one color is a valid theme: each package then gets a
  variation of it.
- **A theme does not name packages.** The settings are the same for each
  working tree, and package names belong to one tree. A map from a
  package to a color is a per-tree setting, and is not part of this plan.
- **Default is read-only.** The themes in `settings.Schemes` stay in Go.
  Custom themes are in the settings file.
- **No ΔE checks on custom themes.** The viewer selects the colors.
- **One Save.** The editor changes the settings in the page. Save in the
  settings dialog sends them to the server in one POST. Cancel discards
  the changes of the editor too.
- **The picker is the browser's.** Each color is an `<input type="color">`.
  The browser's picker has a spectrum or a wheel and a hex field, and the
  page needs no new dependency and no network.

### Result

**Server** (`internal/settings`)

- `Settings` gets a field `themes`: a list of `Scheme`. The default is an
  empty list. `Validate` checks each theme: the `name` has the valid form,
  is not the name of a scheme in `Schemes`, and is unique in the list;
  the `label` is 1 to 40 characters; `colors` has 1 to 12 items, each a
  `#rrggbb` string in lower case. `colorScheme` is valid if it names a
  scheme in `Schemes` or a theme in `themes`.
- `Load` removes a theme that is not valid, and puts it in `Replaced`.
  If `colorScheme` names a removed theme, it gets its default.
- `Save` writes the themes in the file, as part of `Settings`.
- `GET /api/settings` and `POST /api/settings` need no change, because
  the themes are part of `Settings`.

**Page**

- `Settings` in `web/src/settings/settings.ts` gets `themes: Scheme[]`.
  `parseSettings` reads it with `parseScheme`, and drops a theme that is
  not valid. `applyColorScheme` finds the scheme in `options.colorSchemes`
  and then in `settings.themes`.
- The Theme section of the dialog lists the schemes of the server, then
  the custom themes. Each row has a radio button, the label, and the
  swatch row. Each swatch has hover text that names the packages of the
  open graph that get that color, for example "pkg/analyzer, pkg/session".
  The dialog gets the package list from `graph.modules` when it opens.
- Buttons in the Theme section:
  - **+** makes a new theme. Its colors are a copy of the selected theme.
    Its label is "Theme N", where N is the smallest number that is not in
    use. Its name is the label in lower case with `-` for spaces, made
    unique with a number if necessary. The new theme is selected, and
    the editor opens.
  - **Edit**, a pencil icon with the hover text "Edit this theme", opens
    the editor for the selected theme. It is disabled when a scheme of
    the server is selected.
  - **Delete**, a trash can icon with the hover text "Delete this theme",
    removes the selected theme and selects the default scheme. It is
    disabled when a scheme of the server is selected.
  - The icon buttons have an `aria-label` with the same text as the hover
    text, and the look of the other icon buttons of the page.
- **The editor** is a second view in the same `<dialog>`. It has:
  - A text field for the label, with the name under it.
  - One row per color: a drag handle, an `<input type="color">`, the hex
    value as text, and a trash can button with the hover text "Remove
    this color". The remove button is disabled when there is one color.
  - **Reorder by drag.** The viewer drags a row by its handle to a new
    position. Use the pointer events, not the HTML drag and drop API,
    because the HTML API does not work with touch on most browsers. While
    a row moves, a line shows where it will drop. The handle can take the
    focus, and the Up and Down arrow keys move the row, so a keyboard
    user can reorder too.
  - An **Add color** button, disabled at 12 colors. The new color is a
    copy of the last color.
  - A preview: a list of the packages of the open graph, each with the
    color that it gets from this theme, with the lightness variation. A
    note says that the order of the colors decides which package gets
    which color. The list has a scroll bar when it is long.
  - **Done** returns to the settings view with the changes. **Cancel**
    returns with the theme as it was before the editor opened.
- **Live preview.** While the editor is open, each change to a color
  calls `applyColorScheme` with the colors of the editor, and then
  `scene.recolor()`. Done keeps the preview. Cancel in the editor, and
  Cancel in the settings dialog, put the saved scheme back.
- The dialog still fits in a window that is 400 px wide. The editor
  rows wrap or shrink; they do not need a horizontal scroll bar.

### Procedure

1. Add `Themes` to `Settings` and the checks to `Validate` and `Load`.
   Add the tests: a valid theme, a theme that uses the name `default`,
   a theme with a color that is not `#rrggbb`, a theme with 13 colors,
   a `colorScheme` that names a theme, and `Load` with a theme that is
   not valid.
2. Add `themes` to `settings.ts` and `parseSettings`, with tests.
3. Make the editor view in `web/index.html` and
   `web/src/ui/settingsDialog.ts`. `open` takes the package list.
   `applyColorScheme` in `main.ts` takes the colors to apply, so the
   editor can preview a theme that is not saved.
4. Add the CSS.

### Tests

1. Run `go test ./...`, `npm --prefix web test`, and
   `npm --prefix web run typecheck`.
2. Run `npm --prefix web run build`.

### Validation

1. Open the dialog. Click **+**. The editor opens with the colors of the
   default scheme and the label "Theme 1".
2. Change the first color. The graph recolors at once. The preview shows
   the new color on the first package by name.
3. Drag the first color below the second. The preview shows the packages
   with the colors in the new order. The graph follows. Focus the handle
   of a row and press Up. The row moves up.
4. Remove colors until one remains. The remove button is disabled. Each
   package in the preview has a variation of that color.
5. Click Done, then Save. Reload. The theme is selected and the graph
   has its colors. The settings file holds the theme.
6. Open the dialog, select Default, click Edit. The button is disabled.
   Select the custom theme, click Delete. Default is selected. Click
   Cancel. Open the dialog. The custom theme is still there, because
   Cancel discards the deletion.
7. Edit the settings file by hand: give the theme the color `"red"`.
   Reload. The server logs that it replaced the theme, and the dialog
   does not show it.
8. Edit the settings file by hand: give the theme the name `default`.
   Reload. The server logs that it replaced the theme.
9. Open the editor and change a color. Click Cancel in the editor. The
   graph shows the saved colors again.
10. Make the window 400 px wide. Open the editor with 12 colors and 20
    packages. The dialog shows with no horizontal scroll bar.

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
    The page uses the default scheme, and the dialog shows `Default`.
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
  on the speed button. After this plan, the valid speeds are
  `settings.Speeds` in Go, and the page gets them from the server. Plan
  16 adds its speeds there, and must change the top-level `SPEEDS` in
  `main.ts` only if this plan is not merged first.
- **Plan 15.1 (Rust port).** The new package `internal/settings`, with
  the defaults, the speeds, the color schemes, and the checks of the
  custom themes, and the two `/api/settings` routes are more surface for
  the Rust port to mirror.
- **Per-package colors.** A map from a package to a color is a per-tree
  setting. It is not part of this plan. A later plan can add it, with the
  map in the page's `localStorage` or in a file next to the session logs.
