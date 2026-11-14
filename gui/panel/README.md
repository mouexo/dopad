# DOpad Panel

A small local control panel for DOpad. It runs on your computer, opens in your
default browser, and talks only to `127.0.0.1` — it never exposes a server to
the network.

The panel can:

- switch between **Wi‑Fi**, **Bluetooth**, and **Self** modes;
- start and stop the DOpad server, show its log, and display the live QR code;
- read buttons, icons, element types, and properties from `web/controls.js` at
  runtime (no duplicated catalog);
- edit a layout visually (drag, resize, add/remove elements and pad parts);
- load and save layouts in `layouts/<id>.json`;
- export the current layout as a `.json` download and import one back.

## Requirements

- Go 1.23 or newer.
- A built `DOpad` executable (`make build` in the project root), or point the
  panel at one explicitly.
- The panel builds on Linux, Windows, and macOS. DOpad itself targets Linux
  and Windows; Self mode is Linux-only.

## Build

From the project root, `make build` now builds both programs side by side:

```sh
make build
# ./DOpad
# ./dopad-panel
```

To build only the panel:

```sh
make build-panel
```

You can still build it directly from its directory:

```sh
cd gui/panel
go build -o ../../dopad-panel .
```

To embed a version string:

```sh
make build-panel PANEL_VERSION=0.9.4
```

`make dist` also places `dopad-panel` (or `dopad-panel.exe`) in every Linux
and Windows package.

Windows (cross-compile from Linux or build on Windows):

```sh
cd gui/panel
GOOS=windows GOARCH=amd64 go build -o ../../dopad-panel.exe .
```

The web UI is embedded with `go:embed`, so the result is one static binary.

## Run

```sh
./dopad-panel
```

The panel binds to a random free port on `127.0.0.1`, prints the address, and
opens the default browser. Options:

| Flag | Meaning |
| --- | --- |
| `-port 8765` | Use a fixed panel port (default `0` = random free port). |
| `-no-open` | Do not open the browser automatically. |
| `-root /path/to/dopad` | Explicit project root. |
| `-version` | Print the panel version and exit. |

The project root is the directory that contains `web/controls.js`. The panel
checks `-root`, `DOPAD_ROOT`, the executable location, and the working
directory. The DOpad binary is taken from `DOPAD_BIN` or found automatically in
the project root, `server/`, or the newest non-`+web` build under `dist/`.

The **Server** tab shows the live QR code: the panel asks DOpad's own `/qr`
endpoint for the PNG and displays it. In Bluetooth mode the code appears after
the first phone connects; before that the card explains why it is empty.

## Modes

| Mode | Command | Notes |
| --- | --- | --- |
| Wi‑Fi | `./DOpad` | Phone and computer on the same local network. |
| Bluetooth | `./DOpad -b` | Direct Bluetooth PAN connection. |
| Self | `sudo ./DOpad -s` | Linux only; the computer becomes the Wi‑Fi hotspot. |

Self mode needs root. Three options:

1. Run the panel itself with `sudo ./dopad-panel` for direct control.
2. Enter the sudo password in the panel. It is kept in memory only and cleared
   when the server stops; it is never written to disk.
3. Let `pkexec` ask for graphical authorization. Stopping the server may ask
   again because the panel does not keep the password.

## Layouts and the editor

The editor reads the catalog from `web/controls.js` every time it loads or when
you press **Refresh**. Adding an element uses defaults derived from the same
file, so new controls stay consistent with the server UI.

- **Save** writes `layouts/<id>.json`.
- **Export JSON** downloads `<id>.json` from the browser.
- **Import JSON** loads a `.json` file into the editor; save it to keep it.
- Layout IDs may contain English letters, digits, `.`, `_`, and `-`.

The saved file is the same format the server expects:

```json
{
  "name": "My layout",
  "elements": [
    { "type": "button", "key": "A", "anchor": "cc", "x": 0, "y": 0 }
  ]
}
```

## Security

- Listens on `127.0.0.1` only; there is no online service.
- Every API call requires a random per-run token injected into the page.
- API requests must come from a loopback `Host` and `Origin`.
- The sudo password is not persisted.
- Settings are stored simply as `layouts/panel/settings.json`, inside the
  layouts folder. The `panel/` subfolder keeps the file out of DOpad's layout
  list, and it is ignored by Git.

The panel does not modify `server/` or any packaged build. It only starts the
binary you point it at and writes layout JSON files.

## Troubleshooting

- **“Could not find the DOpad executable”** — build it with `make build` in the
  project root, or type its path in the panel.
- **Port already in use** — DOpad selects a nearby free port automatically; the
  actual port is parsed from its log.
- **Self mode does not start** — make sure you are on Linux and either run the
  panel as root, enter the sudo password, or have `pkexec` available.
- **Stopping Self mode asks for a password again** — that is the `pkexec` path;
  run the panel as root or use the password field to avoid it.
- **A layout will not save** — use only English letters, digits, `.`, `_`, and
  `-` in the file name.

See `README.ar.md` for the Arabic version.
