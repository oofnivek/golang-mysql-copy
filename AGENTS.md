# mysql-copy — Agent & Developer Guide

## Menu Structure

The top-level menu is built dynamically in `main()` (main.go ~line 917).
Items only appear when the data they require actually exists.

```
What would you like to do?
├── Run a preset       (shown only when ≥1 preset exists)
├── View presets       (shown only when ≥1 preset exists)
├── Run a group        (shown only when ≥1 group exists)
├── Manage groups      (shown only when ≥1 preset exists — groups need presets)
├── Start new copy     (always shown)
└── Exit               (always shown)
```

The menu loops — after any action completes, the user is returned to this menu.
Ctrl+C also exits at any point.

Each item maps to a `case` in the `switch action` block directly below the menu.

| Menu label      | Handler / case in switch          | Key function called     |
|-----------------|-----------------------------------|-------------------------|
| Run a preset    | `case "Run a preset"`             | `runPreset(p)`          |
| View presets    | `case "View presets"`             | `viewPresets(presets)`  |
| Run a group     | `case "Run a group"`              | `runGroup(g)`           |
| Manage groups   | `case "Manage groups"`            | `manageGroups()`        |
| Start new copy  | `case "Start new copy"`           | inline in main()        |

### Manage Groups submenu

`manageGroups()` runs its own loop with a secondary menu:

```
Manage groups:
├── Create new group   → promptGroupForm(nil)  → saveGroup()
├── <Group Name> …     → submenu: Edit | Delete | ← Back
└── ← Back
```

The group submenu (`Edit` / `Delete`) appears when an existing group is selected.
Edit calls `promptGroupForm(&selectedGroup)`, Delete calls `deleteGroup(name)`.

### Run a Preset — truncate prompt

After selecting a preset, the user is shown a confirm prompt:
"Truncate destination before copying?" → `runPreset()` passes the flag to `copyTable()`.

### Start new copy — inline flow

1. `setupSide("Source")` — pick/create connection, pick database, pick table
2. `setupSide("Destination")` — same
3. Confirm truncate
4. Optionally save as preset → `savePreset()`
5. `copyTable()`

## Data Files

Saved in `~/.mysql-copy/`:

| File              | Contents                          |
|-------------------|-----------------------------------|
| `connections.json`| Saved DB connections (`savedConn`)|
| `presets.json`    | Saved copy presets (`preset`)     |
| `groups.json`     | Preset groups (`group`)           |

## Key Types

- `dbConfig` — live connection details (host, port, user, password, database)
- `savedConn` — persisted connection (adds `Name` field)
- `preset` — a named src→dst table copy job
- `group` — named collection of presets with a concurrency setting

## Adding a New Top-Level Menu Item

1. Add the label string to the `options` slice in `main()`, with any guard condition.
2. Add a matching `case "Label":` in the `switch action` block.
3. Implement or call the handler function.
