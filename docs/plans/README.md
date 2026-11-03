# Plans

Plans for future work, one Markdown file per piece of work. Delete a plan
once its work is merged; git history keeps it.

## The 2D view (plans 03 to 13)

Plans 03 to 13 add a second view of the graph: a 2D board of nested cards.
A card for each package holds a card for each file, and a file card holds a
chip for each callable. The 3D view stays. A toggle in the title bar selects
the view.

Each plan is one pull request. Each pull request leaves `main` in a working
state. The 2D view stays behind a hidden setting (`?view=2d`) until plan 13
shows the toggle.

### Decisions

These decisions apply to all of the plans. Change them here first if the
design changes.

- **Shared parts.** The two views use the same title bar, timeline,
  transport buttons, code pane (tabs, file view, typed diff, status line),
  agent stream, panel dividers, session state (`fold`), colors, and node
  selection. The views differ only in the area that shows the graph.
- **3D only.** The blast radius list, the camera tools (auto-rotate and
  reset), the lock label, and the run chip stay in the 3D view.
- **Follow edit.** The Follow edit button moves from the 3D camera tools to
  the transport row of the timeline, because it controls the code pane in
  both views.
- **2D only.** The 2D view adds a "Now" panel. The Now panel is a shared
  component, but only the 2D view shows it. A later plan can add it to the
  3D view.
- **2D layout.** The left column holds the Now panel, the agent stream, and
  the code pane, from top to bottom. The board fills the rest of the width.
  The column width and the line between the stream and the code pane are
  resizable. On a screen narrower than 980 px, the parts stack.
- **Clicks in the 2D view.** A click on a callable selects it, shows its
  calls, and opens its file in the code pane at that callable. A click on a
  file card highlights the file and opens its tab. A click on a package card
  highlights the package. The same click again, Esc, or FOLLOW AGENT
  returns the highlight to the agent.
- **Packages.** A package is a workspace member (for example a Rust crate)
  when the tree has two or more manifests of one kind below the root.
  Otherwise a package is a directory. Plan 08 has the rule and its tests.
- **No network.** The page must work with no network access. Do not load
  web fonts or other remote files.

### Order

| Plan | Title | Depends on |
| --- | --- | --- |
| 03 | Shared timeline and transport | — |
| 04 | Shared code pane | — |
| 05 | Shared stream, status, and format helpers | 04 |
| 06 | Shared panel dividers | — |
| 07 | View host | 03, 04, 05, 06 |
| 08 | Package of each callable | — |
| 09 | Session state and 2D model | 08 |
| 10 | 2D board | 09 |
| 11 | 2D layout and Now panel | 10 |
| 12 | 2D activity | 11 |
| 13 | 2D interaction and view toggle | 12 |

Plans 03, 04, 06, and 08 are independent. Do them in any order, or at the
same time. Plan 09 can go at the same time as plan 07.

### A test session

Many validation steps need a session with reads, edits, and commands. To
make one:

1. Build the server. Start it on this repository.
2. In this repository, start Claude Code.
3. Ask Claude Code to read two Go files, edit one function, and run
   `go test ./internal/session/`.
4. Wait until the stream shows the result of the test.
5. Stop and start the server again. The session picker now lists the
   session, so you can replay it.

Some steps need a large working tree. Use any working tree for which the
title bar shows more than 800 NODES.
