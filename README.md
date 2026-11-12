# Cuevia

Cuevia is a local macOS tool for capturing a monitor or app, adding voice and text explanations, and copying a prompt. Press **Command+Control+S** to start. Cuevia waits in the menu bar and opens the capture editor in **Google Chrome**. It collects images and audio in a local materials folder. Paste the generated request, which includes absolute paths to those files, into your agent.

## Windows distribution

Inno Setup packages the installer for Windows 10 version 1809 or later on x64. The build PC needs Go 1.27 or later and Inno Setup 6.3 or later. The target PC needs neither.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/package-windows.ps1 -Verify
# Specify the version or compiler path
# scripts/package-windows.ps1 -AppVersion 0.2.2 -IsccPath 'C:\Program Files\Inno Setup 7\ISCC.exe'
```

The build produces `dist/releases/Cuevia-0.2.2-windows-x64-setup.exe` and a SHA-256 checksum file. It leaves existing versioned installers intact. Installation is per user under `%LOCALAPPDATA%\Programs\Cuevia`, with a Start menu shortcut and an optional desktop shortcut. Cuevia opens a browser and tray icon without a PowerShell window. Choose **Exit** from the tray to quit. Launching again reopens the existing instance. If port 8742 is busy, Cuevia tries subsequent ports before falling back to a system-assigned port. The GUI launcher manages the child server through a Windows Job Object so the server also stops if the launcher is terminated. Finish recording and saving, then exit before updating or uninstalling. Uninstallation preserves settings and materials in `%APPDATA%\Cuevia`. The distribution excludes private `.env` files, API keys, and records. The current Windows installer is unsigned.

On Windows, press **Ctrl+Alt+S**, choose **Start Capture** in the tray, or double-click the tray icon. Drag to select an area and press Esc to cancel. The captured image goes through the existing PNG validation and storage path, then opens in the browser for annotation, description, and prompt generation. The web capture button also opens the tray picker. Windows uses region selection, while monitor and application selection are macOS features. Windows supports text logging, browser microphone recording, and archives. Enter the transcription API key in the web settings.

`-Verify` uses a separate AppId and a temporary user folder to test installation, port conflicts, repeat launches, server cleanup after forced termination, hotkey registration, real region selection and PNG import, cancellation, exit from the tray, and uninstallation. Existing user installations and records are preserved.

## Installation and launch

Building from source requires macOS 14 or later, Go 1.27 or later, Xcode Command Line Tools, and Google Chrome. Speech transcription uses the **OpenAI `whisper-1` API** over the internet. Enter your API key and choose a transcription model under **Settings…** in the menu bar. The default remains `whisper-1`.

```sh
# Copy the example only if the configuration is missing, then enter OPENAI_API_KEY
test -f .env || cp .env.example .env
# Build, install to the user's Applications folder, register launch at login, and start
./install.sh
```

`install.sh` finds the project from any working directory and builds for the current Mac. It installs to `~/Applications/Cuevia.app` and registers `~/Library/LaunchAgents/dev.cuevia.desktop.startup.plist` so Cuevia starts in the background when you log in after a reboot. Quitting from the menu keeps it closed until the next login. Run the same script to update.

Finish recording and logging before uninstalling with the commands below. Run them without `sudo`.

```sh
./uninstall.sh --dry-run  # Preview what will be removed
./uninstall.sh            # Remove the per-user app installed by install.sh
# If you installed from the DMG into Applications:
./uninstall.sh --app /Applications/Cuevia.app
```

The uninstaller quits the selected Cuevia app cleanly, unregisters its login item, and moves the app and plist to your Trash. It preserves API keys, settings, captures, recordings, archives, and the source tree's `dist` builds. It leaves login entries pointing to other installations intact. To restore, return the files from the Trash folder shown by the script to their original locations. To reinstall from source, run `./install.sh` again.

On the first source installation, the script copies the project's `.env` to `~/Library/Application Support/Cuevia/.env` with `0600` permissions. The installed app reads that file directly, independently of the source tree or working directory. Saving in Settings writes `CUEVIA_SETTINGS_VERSION=1`, and later runs of `./install.sh` preserve that file. If you have yet to save in Settings, update the project's `.env` and reinstall to refresh the installed settings. The key stays out of the app bundle and login plist. Existing sessions, captures, and audio continue to use their current local storage.

### Distribute as a DMG

Users can download the DMG from the [Cuevia download and user guide](https://tools.camp/cuevia) and drag the app into Applications. Go and Xcode are unnecessary. Enter the API key under **Settings…** in the menu bar after first launch.

On a Mac with the private key for a Developer ID Application certificate, run the command below. The default is a Universal app for Apple Silicon and Intel. The app and DMG are notarized separately with Apple and both receive stapled tickets.

```sh
CUEVIA_SIGN_IDENTITY='<certificate name or SHA-1>' \
CUEVIA_NOTARY_PROFILE='<notarytool Keychain profile>' \
sh scripts/package-macos.sh
```

As an alternative to the profile, set `CUEVIA_NOTARY_KEY` (path to an existing `.p8`), `CUEVIA_NOTARY_KEY_ID`, and `CUEVIA_NOTARY_ISSUER`. Set `CUEVIA_APP_VERSION` and `CUEVIA_BUILD_NUMBER` to identify the release. Existing versioned files are preserved. After verification, the script writes the DMG, SHA-256 checksum, and release metadata to `dist/releases/` and updates the site's `src/data/cuevia-release.json`. Upload the download before deploying the site. The installer excludes real keys, personal settings, and records. To build an unnotarized test file, set `CUEVIA_RELEASE_MODE=preview` and keep it out of the site's official releases.

During development, Cuevia first looks for `.env` in the current working directory, then in the project root containing the development bundle `dist/Cuevia.app`. Installed apps prefer the separately stored configuration. Existing process environment variables take precedence over file values. `OPENAI_TRANSCRIPTION_MODEL` defaults to `whisper-1` and `CUEVIA_ADDR` to `127.0.0.1:8742`. Set `CUEVIA_ENV_FILE` in the process environment to point to a different settings file. The key is used only on the server and stays out of UI responses and copied requests.

### OpenAI settings

Open **Settings…** from the menu bar or **Settings** in the web editor. Enter the **OpenAI API Key** in the password field. The UI shows only whether a key exists. Leave the field empty when saving to keep the existing key, including when changing only the model. The field clears after saving.

- `whisper-1` is the default. Speech segment timestamps connect explanations to captures.
- `gpt-4o-transcribe` and `gpt-4o-mini-transcribe` provide no segment timestamps. Cuevia preserves the whole recording as one segment and displays possible captures and targets for review. Use `whisper-1` to link captures and speech more precisely.

**Changes take effect on the next transcription request without restarting the app.** Requests already in flight finish with the previous key and model. You can save settings while recording. A recording whose transcription request has yet to be sent uses the new settings. The settings tab opened from the menu leaves the editor session, microphone, and edit permissions intact.

Cuevia atomically saves settings to `~/Library/Application Support/Cuevia/.env` with file permissions `0600` and directory permissions `0700`. It preserves other environment settings. The key and model saved in Settings take precedence on future runs. If only the file copied at first installation exists, existing process environment precedence still applies. `CUEVIA_ENV_FILE` and the development folder's `.env` supply initial transcription values when app settings have yet to be saved. Saving in Settings always updates the app settings file above. Other environment variables retain their existing precedence. The settings screen displays the actual save path.

Cuevia starts in the menu bar without opening a window or Chrome. Press **⌘ Command + ⌃ Control + S** or choose **Screen Capture…** to open the two-tab picker. In the default **Application Capture** tab, hover over a window to see its outline and PID, then click to capture it. In **Monitor Capture**, click a live preview. Cuevia captures the entire monitor or selected application window as a PNG. Canceling or a capture failure preserves the existing record. The scene records the app name, PID, and window ID or the monitor's display ID, resolution, and scale. Choose **Quit** in the menu bar to exit.

Screen capture requires macOS screen recording permission. For microphone recording, allow microphone access for the site in Chrome and for Chrome in macOS. The app bundle includes the identifier `dev.cuevia.desktop` and permission usage descriptions. The build automatically uses a single valid Apple Development or Developer ID Application certificate in Keychain. With several candidates, it first looks for the signer of the installed app, then the previous build. If neither matches, it uses the sole Apple Development certificate when available. If multiple candidates remain, set `CUEVIA_SIGN_IDENTITY` to a name or SHA-1 fingerprint. An explicit identity takes precedence, and DMG distribution continues to use Developer ID Application. Signing with a certificate enables the hardened runtime. When you first change the signing method, grant screen recording permission to the currently installed app again. Subsequent builds with the same certificate and identifier preserve the app's designated requirement. Apple Development signing is for local development. Use `CUEVIA_SIGN_IDENTITY=-` when you specifically need ad hoc signing. See [Apple's code signing requirements](https://developer.apple.com/documentation/technotes/tn3127-inside-code-signing-requirements).

The icon source is [`assets/Cuevia.png`](assets/Cuevia.png). The build script generates an `.icns` with standard and Retina sizes and bundles it as an app resource. The purple and teal icon represents capture and voice. The final generation prompt is in [`assets/icon-prompt.txt`](assets/icon-prompt.txt).

### Update only the web UI

The installed app reads the web files from `~/Library/Application Support/Cuevia/web` and falls back to the embedded UI if no external files are available. After an app upgrade, it also falls back to the new embedded UI if `frontend-version.json` is missing or its API version differs from the app's. For older apps without this feature, run `./install.sh` once. For later HTML, CSS, or JavaScript changes, run the command below and refresh Cuevia in Chrome. This requires neither restarting nor re-signing the app. Reinstall the app when changing the Go API or storage contract.

```bash
python3 scripts/update-web.py
```

The update script requires Python 3. It stages the new web files in a separate versioned folder and then swaps the symlink. It preserves `.env`, session materials, and the signed app bundle. To serve files directly from the source tree in development, run `CUEVIA_WEB_DIR="$PWD/web" go run . -web-only`.

### Capture list and deletion

The bottom list retains its horizontal scroll position when you select a capture or explanation. A capture's trash button excludes it and its associated explanations from the list, new prompts, and newly generated image materials. **Undo Delete** restores the most recently deleted item first. Deletion state persists after restart. You can still add captures or undo a deletion after deleting every capture.

Deletion is reversible. Originals and recordings follow the existing 7-day retention policy, and previously generated request materials are preserved. Finish drafting an explanation, recording, or saving audio before deleting a capture.

Capture list verification used temporary data to check persistence, restoration, and UI behavior. In Chrome with 12 temporary captures, the checks covered horizontal scroll position after selecting the last item, deletion, refresh, and undo. In the installed app, the web update preserved the process PID and signature, and the changed files were served immediately.

Initial installation checks covered the Mediawave team's Apple Development signature, `codesign --verify --strict`, the installed app's screen recording permission, and the capture picker.

Run the following commands for development. Press Ctrl+C in Terminal to stop `go run`. The app and development server use the same port, so run one at a time.

```sh
go run .
# Run the HTTP UI without the hotkey or menu bar
go run . -web-only
# Use a different port
go run . -addr 127.0.0.1:8743
# Build the development bundle without installing it
sh scripts/build-macos.sh
```

The default address is [http://127.0.0.1:8742/](http://127.0.0.1:8742/). Use the web update command above to refresh the installed app's web files.

## Use Cuevia and save your work

1. Click **Record** or **Log**. Record uses the microphone and speech transcription. Log saves capture selections and text explanations in chronological order without audio. You can start with a general note before adding a capture.
2. Select or add a capture and enter an explanation. Record and Log are mutually exclusive toggles. Click the active button again to stop.
3. Starting or switching modes saves the current draft and prompt, archives the current version, and opens a new version. Time, explanations, and audio reset. Only the selected original capture is copied to the new version.
4. Use **Number**, **Arrow**, or **Rectangle** to mark and describe a target. **Undo** reverses the last annotation or a clear-all action.
5. Use **Archive** to review past prompts and audio in read-only mode. Choose **Generate Prompt → Copy Prompt** to share the current version.

An empty draft version created before microphone permission failed is reused when you retry. The timer stops at the moment you stop or switch modes, excluding upload and transcription waits. Reopening the page shows interrupted work at its last saved time. Starting again creates a separate version. If a capture opens in a new tab, Cuevia sends it to the existing recording window so the microphone and timer stay active. A second window cannot start the same version concurrently.

Each version has its own session, audio, and generated prompt. The existing 7-day local retention policy also covers archives.

The freehand **Pen** tool was removed. Captures still support **Number**, **Arrow**, **Rectangle**, **Undo**, and **Clear All**. Undo restores a clear-all action in one step or reverses individual annotations from newest to oldest. Hidden annotations retain their numbers so remaining numbers stay stable. Legacy freehand marks remain in the original sessions and are omitted from the UI, new prompts, and images.

The prompt lists each image once as `S01`, `S02`, and so on, then uses these aliases throughout the log. It refers to annotations as `S01#1` and uses short pixel coordinates. Visible numbers, arrows, and rectangles are composited into the image with the same numbers. Captures without marks use the original image. Speech stays in its original segments without inferred classifications. The first recording starts at `00:00`. Capture selections, verbatim speech, and typed explanations follow in timestamp order. Entries made before recording and entries with unknown timing are distinct. Speech that spans a capture change and uncertainty about timing are marked briefly.

```text
## Captures
- S01: Job Flow Editor · 3008×3324 px · [Open image](...)

## Timeline
[00:00.000] Selected capture S01
[00:05.000] Speech 1 (S01): "Please reduce the vertical spacing."
[00:18.000] Note 2 (S01): "Use the same spacing in the right panel."
```

Cuevia records microphone input as **16 kHz mono PCM16 WAV**. The server sends it to the selected OpenAI model for Korean transcription. The default `whisper-1` returns `verbose_json` with segment timestamps, which Cuevia converts from seconds to milliseconds for alignment with captures. GPT transcription models return JSON text for the entire recording. Cuevia preserves it as one segment and uses `timingEstimated` to indicate missing per-sentence timestamps. The UI and prompt identify the full-recording timing and leave captures and targets unconfirmed. See the [OpenAI transcription API reference](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create).

If a speech segment returned by the API extends past the original audio, Cuevia clamps it to the end and retains the original timestamps and whether they were adjusted. Each recording is limited to 5 minutes. If transcription fails, Cuevia keeps the original audio and offers **Retry Transcription**. Retries are manual and use the same provider.

Sessions, original captures, draft explanations, original audio, and transcripts are stored under `Cuevia/sessions` in the macOS user's Application Support directory. Reopening the browser or app restores recent sessions within the retention period. The capture cache is under `Cuevia/prototype/captures` in the user's Caches directory.

Generated materials go to `Cuevia/requests/YYYY-MM-DD/HHmmss-<sessionID>-r<revision>-<token>/` in Application Support. The folder contains `request.md`, `session.json`, `images/`, and `audio/`. The clipboard receives the same request as in the displayed local folder. Files are saved locally without a browser download. Audio that could not be converted is preserved in its original format in the session and generated materials.

Local records expire **7 days after the last save**. Generated materials expire **7 days after creation**. The capture cache and app-managed temporary processing records also expire after 7 days. Cleanup runs at startup and hourly while the app is running. Items that expire while the app is closed are cleaned up at next startup. The editor periodically saves active sessions, and expired temporary browser records are skipped on restore.

Cuevia requires no local transcription model files or paths. Prompt generation puts capture selections and explanations in chronological order so you can paste the request into an agent.

## Development and verification

Annotation restoration was verified with 81 Node tests covering Number, Arrow, Rectangle, pen exclusion, coordinate conversion, undo, clear-all, number reservation, prompt references, and version isolation. Go tests verify that only captures with visible annotations need a composite PNG for the current revision. Captures with only hidden or legacy pen marks export the original.

Recording and logging version checks use `go test ./...`, `go vet ./...`, and Node tests for mutually exclusive toggles, time reset, draft preservation, microphone errors, save delays, read-only archives, and recovery from a failed switch. A temporary session in Chrome also verified starting and stopping a text log, opening a new version, reading a past prompt, and playing archive audio. Physical microphone input and audio bandwidth are verified separately.

Freehand removal verification on 2026-10-31 passed `go test ./...`, `go vet ./...`, and 39 browser model, app, and recording tests. An existing session was opened in Chrome through the signed installed app, and new request materials were generated. Checks confirmed byte-for-byte original PNGs, all 9 explanations, one image path occurrence, and legacy freehand fields preserved in the original session and omitted from new materials. The sample prompt, including paths, shrank from 21,961 to 1,918 characters.

Native hotkeys and source selection are in [platform_darwin.go](platform_darwin.go) and [capture_sources.go](capture_sources.go). Recording is in [web/recorder.js](web/recorder.js) and OpenAI transcription is in [openai_transcription.go](openai_transcription.go). [session_store.go](session_store.go) and [session_materials.go](session_materials.go) handle persistence and local materials. [retention.go](retention.go) handles 7-day cleanup. HTTP wiring is in [application_http.go](application_http.go).

```sh
go test ./...
go vet ./...
node --test tests/*.test.mjs
node --check web/app.js
sh scripts/test-native-picker.sh
sh scripts/build-macos.sh
```

The standard OpenAI tests use a local HTTP test server to cover the upload contract, timestamps, cancellation, API errors, key redaction, and redirect blocking without a paid API call. Running the following explicitly makes one real request. It reads the key from `.env` and uses a short Korean recording to check transcription, segment timing, storage, and restoration from a new server. `CUEVIA_TEST_REAL_EXPECT` is the phrase expected in the recording.

```sh
CUEVIA_TEST_OPENAI=1 \
CUEVIA_TEST_REAL_WAV=/path/to/spoken-korean.wav \
CUEVIA_TEST_REAL_EXPECT='저장됩니다' \
go test . -run '^TestOpenAIRealTranscription$' -v
```

Verification covered actual captures of an app window (1446×1880) and a monitor (6016×3384), linking captures and explanations in Chrome, and including PID and monitor details in the prompt. A short Korean sample passed the real `whisper-1` API call, HTTP transcription flow, segment timing and original timestamp preservation, storage, and restoration from a new server. Tests also cover local material generation and retry, the exact 7-day expiration boundary, preservation after a recent save, serialized deletion and writes, and protection of out-of-scope files and symlinks. **Chrome in the verification environment found no input devices. The end-to-end flow starting with a physical microphone and keyboard shortcut remains unverified.**

Earlier design documents are available in the [target system design](docs/design/20261031-093004/to-be/system-design-to-be.md), [navigation](docs/design/20261031-093004/to-be/navigation.md), and [screen layout](docs/design/20261031-093004/to-be/screen-layout.md). Their freehand annotation sections describe an earlier design.
