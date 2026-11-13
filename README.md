# Cuevia

Show your screen, say what you mean, hand it to your AI agent.

Cuevia is a local desktop tool: **capture a screen → mark it up → explain by voice or text → copy a ready-made prompt**. The prompt references the captured images and audio by absolute path, so you can paste it straight into a coding agent.

- **macOS 14+**: press **⌘ Command + ⌃ Control + S** (or **Capture screen…** in the menu bar), pick an app window or monitor, and click to capture.
- **Windows 10 1809+ (x64)**: press **Ctrl+Alt+S** (or **Start capture** in the tray) and drag a region. Esc cancels.

The editor opens in Google Chrome. Everything is stored locally and deleted automatically after 7 days.

## Quick start

### Install a release (macOS)

Download the DMG from [tools.camp/cuevia](https://tools.camp/cuevia), move Cuevia into Applications, launch it, then open **Settings…** in the menu bar and enter your OpenAI API key.

### Build from source (macOS)

Requires macOS 14+, Go 1.27+, Xcode Command Line Tools, and Google Chrome.

```sh
test -f .env || cp .env.example .env   # then set OPENAI_API_KEY
./install.sh                           # build, install to ~/Applications, start at login
```

Run `./install.sh` again to update. To uninstall (settings and recordings are kept):

```sh
./uninstall.sh --dry-run                     # preview
./uninstall.sh                               # remove the app installed by install.sh
./uninstall.sh --app /Applications/Cuevia.app  # remove a DMG install
```

Grant **Screen Recording** to Cuevia, and allow the microphone for Chrome, when macOS asks.

### Run in development

```sh
go run .                       # full app (menu bar + hotkey)
go run . -web-only             # web UI only, no hotkey or menu bar
go run . -addr 127.0.0.1:8743  # different port
```

The UI is served at <http://127.0.0.1:8742/>. The app and `go run` share this port, so run only one.

## How to use

1. Click **Record** (microphone, transcribed by OpenAI) or **Text** (typed notes only). The two are mutually exclusive toggles.
2. Capture screens and select the one you are talking about.
3. Mark targets with **number, arrow, or box** annotations. Undo and clear-all are available.
4. Click **Create prompt → Copy prompt** and paste it into your agent.

Starting a new session archives the current one as a read-only version (prompt + audio), viewable under **Archive**.

The prompt lists each image once as `S01`, `S02`, …, and a timeline references them by alias:

```text
## Screens
- S01: Job Flow Editor · 3008×3324 px · [Open image](...)

## Timeline
[00:00.000] Select screen S01
[00:05.000] Speech 1 (S01): "Please reduce the vertical spacing."
[00:18.000] Note 2 (S01): "Please match the right panel to the same spacing."
```

## Transcription settings

Open **Settings** (menu bar or web UI) to set the OpenAI API key and model. Changes apply from the next transcription request, with no restart needed. The key never leaves the local server.

| Model | Timing |
| --- | --- |
| `whisper-1` (default) | Per-segment timestamps, so speech is linked to the screen shown at that moment |
| `gpt-4o-transcribe`, `gpt-4o-mini-transcribe` | Whole recording as one segment; screen links are marked for review |

Recordings are 16 kHz mono WAV, up to 5 minutes each, transcribed in English (`en`). If transcription fails, the original audio is kept and you can retry.

## Configuration

Settings are read from `.env` (see [`.env.example`](.env.example)). Process environment variables take precedence.

| Variable | Purpose |
| --- | --- |
| `OPENAI_API_KEY` | OpenAI key for transcription |
| `OPENAI_TRANSCRIPTION_MODEL` | Transcription model (default `whisper-1`) |
| `CUEVIA_ADDR` | Server address (default `127.0.0.1:8742`) |
| `CUEVIA_ENV_FILE` | Use a different settings file |
| `CUEVIA_WEB_DIR` | Serve web files from a folder (development) |

The installed app keeps its settings in `~/Library/Application Support/Cuevia/.env` (mode `0600`). Keys are never bundled into the app or the launch agent.

## Data and retention

All data stays on your machine:

| What | Where (macOS) | Deleted |
| --- | --- | --- |
| Sessions, captures, audio, transcripts | `~/Library/Application Support/Cuevia/sessions` | 7 days after last save |
| Generated prompt materials (`request.md`, `images/`, `audio/`) | `~/Library/Application Support/Cuevia/requests/` | 7 days after creation |
| Capture cache | `~/Library/Caches/Cuevia/prototype/captures` | after 7 days |

Expired data is cleaned up at startup and hourly while the app runs. On Windows, data lives in `%APPDATA%\Cuevia`.

## Project structure

| Area | Files |
| --- | --- |
| Entry point, hotkey, menu bar | [main.go](main.go), [platform_darwin.go](platform_darwin.go) |
| Screen capture and source picker | [capture_sources.go](capture_sources.go), `native_*_darwin.m`, `source_picker_*` |
| HTTP API | [application_http.go](application_http.go), [session_http.go](session_http.go) |
| Sessions and prompt materials | [session_store.go](session_store.go), [session_materials.go](session_materials.go) |
| Transcription | [openai_transcription.go](openai_transcription.go), [settings.go](settings.go) |
| Retention | [retention.go](retention.go) |
| Web UI | [web/app.js](web/app.js), [web/model.js](web/model.js) (prompt builder), [web/annotation.js](web/annotation.js), [web/recorder.js](web/recorder.js) |
| Windows packaging | [packaging/windows/](packaging/windows/) |

## Development and testing

```sh
go test ./...
go vet ./...
node --test tests/*.test.mjs
node --check web/app.js
sh scripts/test-native-picker.sh   # macOS source picker smoke test
```

The regular tests use a local HTTP stub, so they never call the paid OpenAI API. To test against the real API with one request:

```sh
CUEVIA_TEST_OPENAI=1 \
CUEVIA_TEST_REAL_WAV=/path/to/spoken-english.wav \
CUEVIA_TEST_REAL_EXPECT='will be saved' \
go test . -run '^TestOpenAIRealTranscription$' -v
```

To update only the web UI of an installed app (no restart or re-signing):

```sh
python3 scripts/update-web.py   # then reload the Cuevia tab in Chrome
```

## Packaging

**macOS DMG** (requires a Developer ID Application certificate; builds a Universal app, notarizes, and staples):

```sh
CUEVIA_SIGN_IDENTITY='<certificate name or SHA-1>' \
CUEVIA_NOTARY_PROFILE='<notarytool keychain profile>' \
sh scripts/package-macos.sh
```

Output goes to `dist/releases/`. Set `CUEVIA_APP_VERSION` and `CUEVIA_BUILD_NUMBER` for each release. Unnotarized builds need `CUEVIA_RELEASE_MODE=preview`.

**Windows installer** (requires Go 1.27+ and Inno Setup 6.3+ on the build PC):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/package-windows.ps1 -Verify
```

This produces `dist/releases/Cuevia-<version>-windows-x64-setup.exe` and installs per user into `%LOCALAPPDATA%\Programs\Cuevia`. `-Verify` runs an install, launch, and uninstall check in an isolated profile.

## Known limitations

- Windows supports region capture only. The app/monitor picker is macOS only.
- The Windows installer is not code-signed.
- No automatic retry or fallback to another transcription provider.
- The full path from a physical microphone and physical hotkey has not been verified end to end.

## Design documents

These are earlier design documents. The freehand pen they describe has since been removed.

- [TO-BE system design](docs/design/20261031-093004/to-be/system-design-to-be.md)
- [Navigation](docs/design/20261031-093004/to-be/navigation.md)
- [Screen layout](docs/design/20261031-093004/to-be/screen-layout.md)
