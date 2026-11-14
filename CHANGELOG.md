# Changelog

All notable changes to DOpad are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.4] - 2026-11-14

### Added

- **Local control panel (`dopad-panel`).** A loopback-only browser UI that
  switches Wi‑Fi/Bluetooth/Self modes, starts and stops the server, shows its
  log, displays the live QR code, and edits layouts visually.
- **Visual layout editor.** Drag, resize, add or remove elements and pad parts,
  import/export JSON, and read buttons and properties from `web/controls.js`
  at runtime.
- **Panel packaging.** `make dist` now includes `dopad-panel` on Linux and
  `dopad-panel.exe` on Windows in all four packages. Embedded variants include
  `web/controls.js` so the panel editor still has the catalog.
- **Panel settings.** Stored locally in `layouts/panel/settings.json`; the
  subfolder keeps the file out of DOpad's layout list.
- **Live QR card.** The server page shows the PNG from DOpad's `/qr` endpoint
  and explains the Bluetooth state before the first phone connects.
- **CI checks** for the panel (gofmt and Linux/Windows vet).

### Changed

- `make build` builds `DOpad` and `dopad-panel` side by side;
  `make build-panel` builds only the panel.
- Panel settings moved from the OS config directory to
  `layouts/panel/settings.json`.
- The server page combines status, facts, run settings, and QR in one card.
- Redundant mode and port facts were removed from the status card.
- The panel uses the DOpad gamepad icon as its logo and favicon.
- README (English and Arabic) documents the panel and the new package layout.

### Removed

- `make run`, `make run-self`, `make run-bt`, and `make clean`; build the
  binaries and run them directly instead.

## [0.9.3] - 2026-11-12

### Added

- **Bluetooth mode (`-b`).** The phone provides a Bluetooth PAN and the
  computer joins as a PANU client, so no Wi-Fi is needed. Works on Linux and
  Windows, reconnects in the background, and prints the phone URL and QR code
  as soon as a link comes up.
- **Dynamic QR link.** In Bluetooth mode, `/qr` shows the latest connected
  phone instead of a fixed address.
- **`make dist`.** Builds four ready-to-run folders under `dist/`: embedded-web
  and external-web variants for Linux and Windows. The version comes from the
  latest Git tag and can be overridden with `make dist V=x.y.z`.
- **`make run-bt`** helper.
- **CI packaging.** A `package` job uploads the four distribution folders, and
  a `release` job attaches them to the GitHub Release created from a `v*` tag.
- **Optional `webembed` build tag.** Embeds `web/` inside the binary. An
  external `web/` folder still takes priority, and `layouts/` stays external.
- **Static Linux builds** (`CGO_ENABLED=0`) with `-trimpath`.

### Changed

- Windows builds embed `ViGEmClient.dll` through the `vigemdll` tag when the
  DLL is present.
- Makefile tag composition combines `webembed` and `vigemdll` without duplicate
  `-tags` flags.
- README (English and Arabic): real startup output, Bluetooth requirements and
  troubleshooting, security notes, tested configurations, and updated build
  instructions.
- Port-fallback message now says "ports" instead of "outlets".

### Fixed

- Embedded web extraction now uses a unique temporary directory per run, so two
  instances no longer delete each other's files.
- Corrected `make setup` guidance in uinput permission errors.
- Corrected the Arabic word `تثقيق` to `تثبيت`.
- Removed a duplicate `all` target in the Makefile.

## [0.9.2] - 2026-11-05

Initial public release: the phone acts as a gamepad over WebSocket, with a
virtual controller through uinput (Linux) or ViGEmBus (Windows), Wi-Fi hotspot
mode (`-s`), layouts, and the web UI.
