# Changelog

All notable changes to DOpad are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
