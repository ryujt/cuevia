# Cuevia Navigation Design

- Reference date: 2026-10-31
- Status: TO-BE proposal for a new local macOS app. Not yet implemented or verified by running the product.
- Scope: initial screen selection, synchronized recording, review and supplementation, prompt generation and delivery, recovery, and settings. Per-scenario sections are combined into one file.
- Source links: [Core design](system-design-to-be.md), [Requirements](system-design-to-be.md#requirements), [Public contracts](system-design-to-be.md#contracts), [Delivery rules](system-design-to-be.md#delivery), [Verification plan](system-design-to-be.md#verification).
- Screen regions and accessibility: [Screen Layout](screen-layout.md). The core design is the source of truth for internal state, storage, and compile contracts; the process names below only summarize navigation decisions.

## 1. Meaning of screens and navigation

This app has no web URLs or HTTP endpoints. App launch starts at `SessionLibrary`. Re-entry from the Dock or a menu command brings the already open window to the front, and if there is an incomplete session to recover, `RecoveryPanel` is shown first. Opening a saved session does not automatically resume recording.

`RecordingToolbar`, `SceneCanvas`, and `NotePanel` are windows that can float together. Arrows between them mean opening or bringing a window to the front and do not destroy the originating window. During capture selection, the app's auxiliary windows are temporarily hidden, and on cancel the previous window arrangement and input target return. `CapturePicker` is an identifier representing the OS-owned selection UI. `RegionOverlay` is an app overlay for selecting a region within a single display.

| Screen identifier | Entry and lifetime | Layout |
|---|---|---|
| SessionLibrary | The app's main window and entry point for saved sessions | [List](screen-layout.md#sessionlibrary) |
| SessionSetup | New session draft; settings before recording starts | [Setup](screen-layout.md#sessionsetup) |
| RecordingToolbar | Persists during the Starting, Recording, and Paused states | [Toolbar](screen-layout.md#recordingtoolbar) |
| CapturePicker | Exists only during OS screen/window selection | [OS picker](screen-layout.md#capturepicker) |
| RegionOverlay | Exists only while specifying a region on the selected single display | [Region selection](screen-layout.md#regionoverlay) |
| SceneCanvas | Shows the immutable capture with annotations and pointing | [Canvas](screen-layout.md#scenecanvas) |
| NotePanel | Text input explicitly linked to a scene or the whole session | [Notes](screen-layout.md#notepanel) |
| SessionReview | Read-only review while Paused; supplementation and error review after finishing | [Review](screen-layout.md#sessionreview) |
| PromptPreview | Check the generated result and the actual delivery assets | [Preview](screen-layout.md#promptpreview) |
| Settings | Local permissions, audio, transcription, and storage settings | [Preferences](screen-layout.md#settings) |
| RecoveryPanel | Recovery entry point for abnormal termination, corruption, or incomplete recordings | [Recovery](screen-layout.md#recoverypanel) |

Screens and points of view are distinct. `activeScene` is the explicit target of subsequent recording, and `inspectedScene` is the scene currently being viewed. `activeScene` does not change when another app comes to the front or when NotePanel is clicked. Viewing a past scene does not change the target either. Only pressing **Record with this scene as target** on the canvas changes the target of subsequent speech, annotations, and pointing. Existing input stays with its original target. [FR-01·FR-03·FR-04](system-design-to-be.md#requirements)

### Navigation decisions and local contracts

Parenthesized nodes are internal app processes, not server calls. The aliases below point to the corresponding public entry points without duplicating the contracts. On failure, the existing screen's error area shows the cause, retry, and alternative input.

| Process alias | Public entry point / decision | Screen result |
|---|---|---|
| prepare_capture | SessionCoordinator.PrepareDraft | First Scene prepared, or Setup kept |
| start_session | SessionCoordinator.Start | Starting then Recording, or start failure shown in Setup |
| capture_scene | SessionCoordinator.Capture | New Scene and activeScene replaced only on success |
| pause_for_review | SessionCoordinator.Pause including the input barrier | Read-only review opens after Paused is confirmed |
| resume_session | SessionCoordinator.Resume | Return to toolbar and canvas accepting new recording |
| finalize_session | SessionCoordinator.Finalize | Recording ends and Review opens |
| compile_prompt | CompileCoordinator.Compile | Latest result in Preview, or supplementation state in Review |
| recover_session | SessionStore.Recover | Recovery result in Review, or failure state kept |
| export_delivery | DeliveryExporter.Export | Show only results whose actual files and paths are confirmed |

An input save failure is not shown as a successful navigation. The input field's draft and retry action are kept, and follow-up commands such as Finalize or close wait until the save succeeds or the user chooses to discard the draft. A failed capture does not replace the existing Scene. Contract details and error types follow the [Public contracts](system-design-to-be.md#contracts).

<a id="start"></a>
## 2. New session and first capture

Entry is **New session** in `SessionLibrary`. The user sets the question, change request, or mixed mode and whether to use the microphone, and selects the capture target. In this document, text-only means **screen + text input, microphone unused**. Sessions with only voice or session-wide notes and no capture are excluded from the MVP. One first Scene must be prepared before starting. [FR-01·FR-02·FR-05·FR-09](system-design-to-be.md#requirements)

```navigation
SessionLibrary --> SessionSetup : New session
SessionSetup --> CapturePicker : Select window or display
CapturePicker --> (prepare_capture) : Capture target confirmed
CapturePicker --> RegionOverlay : Single display for region mode confirmed
CapturePicker --> SessionSetup : First selection canceled
RegionOverlay --> (prepare_capture) : Region confirmed
RegionOverlay --> SessionSetup : First region selection canceled
(prepare_capture) --> SessionSetup : First Scene prepared
(prepare_capture) --> SessionSetup : Permission denied or capture failed
SessionSetup --> (start_session) : Start with first Scene and input settings
(start_session) --> RecordingToolbar : Recording started
(start_session) --> SessionSetup : Start failed
RecordingToolbar --> SceneCanvas : Show prepared first scene
RecordingToolbar --> NotePanel : Open text input
SessionSetup --> SessionLibrary : Close draft
```

1. Before selection, show the capture permission state and whether recording is enabled. OS permission dialogs are not imitated in app screens. After denial, Setup guides applicable choices such as going to settings, retrying, or starting without a microphone. A capture-permission preflight failure alone does not block the OS picker path. Start is allowed based on the existence of a successfully captured and validated first Scene asset.
2. The first capture is an immutable asset of the session draft. After success, Setup shows a preview along with the target name, type, and capture time. Reselecting prepares a new draft, and the existing draft is kept until it succeeds. Cancel returns to the existing draft.
3. Start goes through the Starting state and fixes the actual recording start time. The effective session time of the pre-prepared first Scene is 0, and the capture time is shown with **Captured before recording started**. Start wait time is not displayed as if it were recorded time.
4. If the selected microphone cannot be opened, show the cause. If the user chooses **Start without microphone**, the session starts text-only and the toolbar keeps showing this. It never silently succeeds as if recording.
5. Closing Setup does not stop recording. If a prepared draft exists, an inline confirmation on the same screen offers save and close, or discard.

<a id="recording"></a>
## 3. Adding scenes, past scenes, and notes during recording

Entry is RecordingToolbar or an already open SceneCanvas. A new capture adds a static Scene and does not follow live screen changes. Support for excluding app windows from captures and the details of the OS picker are implementation verification items. [FR-01·FR-03·FR-04](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> CapturePicker : Select window or display for new scene
SceneCanvas --> CapturePicker : New capture
CapturePicker --> RegionOverlay : Single display for new region confirmed
CapturePicker --> (capture_scene) : New capture target confirmed
RegionOverlay --> (capture_scene) : New region confirmed
(capture_scene) --> SceneCanvas : New Scene succeeded and target switched
(capture_scene) --> SceneCanvas : Capture failed and existing scene kept
CapturePicker --> SceneCanvas : Selection canceled during recording and previous windows restored
RegionOverlay --> SceneCanvas : Region canceled during recording and previous windows restored
RecordingToolbar --> SceneCanvas : View scene or explicitly resume explanation
RecordingToolbar --> NotePanel : Open notes
SceneCanvas --> NotePanel : Open notes for this scene
NotePanel --> SceneCanvas : View note target scene
SceneCanvas --> RecordingToolbar : Start operating source app and hide canvas
NotePanel --> RecordingToolbar : Hide notes
```

- State is restored during capture selection so recording and microphone status are not lost. Opening the picker itself is not a Pause. Each request records a SelectionInterval, and speech crossing that interval is not automatically assigned to the previous or new Scene/target regardless of cancellation or failure; it is marked as a candidate or unresolved. Other input follows the actual activation boundary of the new Scene. Duplicate new-capture commands while waiting are blocked.
- Selecting a past Scene is browsing within the canvas. The **Current recording target** badge and the **Scene being viewed** are shown side by side. **Record with this scene as target** records an explicit target switch and lets the user re-confirm the selected scene and subsequent pointing positions.
- Point focus persists until explicitly released, the target switches, Pause, or Stop. A long-held focus shows elapsed time and **Release**, and does not disappear after an arbitrary TTL. Arbitrary pointer positions outside the canvas are not collected as references.
- Deleting a selected annotation also closes the open focus on that target. Pointed points/rects can be verified in delivery frames by target ID, effective time, and highlight.
- NotePanel shows **Scene: Sxx** or **Whole session** fixed before input. Choosing and saving a note's target is not an activeScene switch. Opening SceneCanvas does not change the target or text of a note being written.
- Hiding the canvas with **Operate source app** starts an InteractionInterval and shows the state in the toolbar. Explicitly running **Resume scene explanation** closes the interval. Deictic expressions in speech crossing that interval are not automatically assigned to the existing target. Opening the note window is not this mode switch.
- Pen, arrow, rectangle, text, and focus changes, and note saves, are input within the same screen, so they are not separate nodes in the navigation diagram. Temporary strokes/text are distinguished from the saved indicator. Specific input and IME rules follow [Canvas](screen-layout.md#scenecanvas) and [Notes](screen-layout.md#notepanel).

<a id="pause-review"></a>
## 4. Pause, read-only review, and resume

The recording Pause button only changes state within the toolbar, so it is omitted from the navigation diagram. **Review recording** opens Review after Pause's input barrier and state confirmation succeed. On capture/input confirmation failure or blocked, the originating window, draft, and actual recording state are kept. [FR-02·FR-06](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> (pause_for_review) : Review recording
(pause_for_review) --> SessionReview : Pause succeeded
(pause_for_review) --> RecordingToolbar : Input blocked or pause failed
SessionReview --> SceneCanvas : View selected time and scene
SessionReview --> NotePanel : View saved notes
SessionReview --> (resume_session) : Resume recording
(resume_session) --> RecordingToolbar : Resume succeeded
(resume_session) --> SessionReview : Resume failed
RecordingToolbar --> SceneCanvas : Show resumed recording target
```

While Paused, Scenes, events, recordings, and notes can be read and browsed. New captures and creating or editing annotations, focus, or recording notes are disabled, with the explanation **Available after resuming**. Changing the playback or viewing position does not change the subsequent recording target. Pause closes the open focus and any in-progress capture token. A late capture completion re-checks the current state, is discarded, and does not create a new Scene. When Resume succeeds, the previous activeScene is kept and the target starts released, so the user must explicitly point again to re-explain a specific spot. How session time represents Pause intervals follows the [Time contract](system-design-to-be.md#contracts).

Read-only review while Paused and supplementation after Finalize are different states. No workaround is provided that alters the original recording as if supplementing during Pause. To finish, use **Stop recording and generate prompt** on the same screen.

The Pause/Stop input barrier blocks new gestures and captures, cancels pending capture tokens, and then asks the editor to end composition. The editor's committed/cancelled result is confirmed via CommitInput, and focus termination and media drain must finish before Paused/finished is shown. If blocked, it shows **Commit or cancel your input, then try again**, keeps the draft, and cancels the transition. Composition is never committed by force-closing a window or by timeout. During the barrier, the editor's existing composition-end responses are accepted.

<a id="finalize-compile"></a>
## 5. Stopping recording, automatic generation, and supplementation

Stop can be used during recording or while Paused. Once the command is accepted, new recording input is closed, accepted input is confirmed and saved, and then Finalize runs. SessionReview opens first to show generation progress and the originals, and PromptPreview opens when the local deterministic compile finishes. Generation completion does not mean it was sent to an external AI. [FR-05·FR-06](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> (finalize_session) : Stop recording and generate prompt
SessionReview --> (finalize_session) : Stop paused recording and generate prompt
(finalize_session) --> SessionReview : Recording saved and stopped
(finalize_session) --> RecordingToolbar : Stop failed and Recording state kept
(finalize_session) --> SessionReview : Paused stop failed or interrupted partial preserved
SessionReview --> (compile_prompt) : Automatic generation right after stop
(compile_prompt) --> PromptPreview : ready or partial result for current input
(compile_prompt) --> SessionReview : Awaiting review of external processing input
(compile_prompt) --> SessionReview : Output generation failed
PromptPreview --> SessionReview : Review evidence and unresolved items
SessionReview --> SceneCanvas : Open post-recording scene supplementation
SessionReview --> NotePanel : Open post-recording supplementary note
SceneCanvas --> SessionReview : Save supplementation and return to review
NotePanel --> SessionReview : Save supplementation and return to review
SessionReview --> (compile_prompt) : Regenerate with supplementation
```

| Result / event | What the user sees and next action |
|---|---|
| Finalize in progress | Input blocked, save progress, duplicate Stop prevented. Closing the app never falsely shows that stopping completed |
| Finalize failed | Shows the actual recording state and unconfirmed items. If called from Paused, the failure stays in the Paused Review and is retried |
| Transcription not configured / unsupported | Shows **No transcription** and the voice segments. Text supplementation and original audio playback are available. Never produces sentences implying the speech was understood |
| Transcription pending / failed / partial | Shows the state and affected segments. Provides original audio and manual transcription/summary input; an empty transcription is not shown as a completed result |
| External processing required | Shows awaiting_input_review and the actual input to be sent and excluded ranges. Nothing is uploaded just because a provider is configured; waits for an explicit reviewed run |
| Insufficient input | Message that there is not enough evidence to form a meaningful question or request, plus supplementary input. The base evidence output can be viewed as partial but is not shown as automatic question generation complete or an empty success |
| Unresolved reference / uncertain transcription | Go to the relevant time and Scene to correct, explicitly confirm, or keep as unconfirmed. Never turned into a definite interpretation |
| Generation succeeded for current revision | Preview including mode, questions/requests, Scene references, and unconfirmed items. Unresolved items keep showing their count and evidence |
| Only evidence materials generated | artifact_status and intent_status are shown separately. generated/user_authored is distinguished from unavailable/insufficient, and base material success is not shown as automatic question authoring complete |
| Supplementation / mode / transcription changed | Existing Preview shows **Regeneration required**. Copy/export of the latest result is locked and linked to regeneration |

Post-recording scene, note, and transcription corrections are marked as **post-recording supplementation** that does not overwrite records from the original time. They are linked to the relevant Scene or the whole session, and saving changes the reviewRevision, making the existing prompt stale. The original recording and original captures are not changed by supplementation. If transcription finishes under the same snapshot and review revision, the same generation proceeds to the next compile step. If the user has added supplementation, the previous generation is not published as latest; a new revision is explicitly regenerated. Follows the [Public contracts](system-design-to-be.md#contracts).

Automatic Compile right after Stop runs only up to local base materials and available local engines. External ASR (original audio and selected segments) and external semantic organization (transcriptions, notes, masked frames) are each previewed separately in SessionReview. Running **Process externally with reviewed materials** processes only input under a fixed ReviewInputRevision and scope hash. It never claims that image masking also masked the audio; audio exclusion ranges are selected separately. Later supplementation cannot retract already-sent materials and requires a new review revision. Automatic delivery of the final prompt to another AI is not provided.

<a id="delivery"></a>
## 6. Copy, actual assets, and bundle export

Entry is a PromptPreview generated for the current revision. **Copy prompt** puts only text on the clipboard. **Images and audio must be attached separately** is always shown next to the button. Copying is never described as attaching files or sending to an AI. [FR-07·FR-09](system-design-to-be.md#requirements)

```navigation
PromptPreview --> (export_delivery) : Create delivery bundle
(export_delivery) --> PromptPreview : Actual files and paths confirmed
(export_delivery) --> PromptPreview : Export failed
PromptPreview --> SessionReview : Check missing assets or unresolved evidence
PromptPreview --> SessionLibrary : Back to session list
SessionLibrary --> SessionReview : Open saved session
SessionReview --> PromptPreview : View current generated result
```

Copy, show assets in Finder, and choose save location are actions within Preview or delegated to OS windows, so no fake app screen nodes are added. Preview, copy, and export use the DeliveryProjection of the same generation/profile so redaction and metadata exclusion do not differ by path. The default **Delivery** profile delivers prompt, context, evidence, and composited frames, and excludes original audio, original images, PID, window titles, and project-local absolute paths. **Include originals** is explicitly selected after seeing the included scope and a notice that pre-masking originals are included. Bundle success is shown only after confirming the actually created path and list of included files. Failure never shows uncreated files as downloadable and offers retry and another location. Partially missing assets are identified individually so they are not mistaken for the latest complete bundle. Path, manifest, and relative asset reference rules follow the [Delivery contract](system-design-to-be.md#delivery).

A prompt with unresolved information keeps warnings and unconfirmed content. Whether delivery is allowed with critical missing assets, a stale revision, generation failure, or insufficient input is decided by the contract, and the reason is shown in the UI. Exporting a session for original preservation and successfully delivering the latest prompt are never merged into the same success message.

<a id="recovery-settings"></a>
## 7. Recovery, preferences, and closing

If there is a recovery target at app launch, an entry guard opens RecoveryPanel. Since this guard determines the actual user screen, it starts as a process node. App launch and OS window reactivation themselves are not drawn as nodes. [FR-08·FR-09](system-design-to-be.md#requirements)

```navigation
(check_recovery) --> RecoveryPanel : Incomplete session to recover exists
(check_recovery) --> SessionLibrary : No recovery target
SessionLibrary --> RecoveryPanel : Incomplete session recovery notice
RecoveryPanel --> (recover_session) : Recover selected session
(recover_session) --> SessionReview : Open recoverable materials
(recover_session) --> RecoveryPanel : Recovery failed or selection needed
RecoveryPanel --> SessionLibrary : Keep originals and handle later
SessionReview --> SessionSetup : Continue recording in a new session linked to the recovered copy
SessionLibrary --> Settings : Preferences
SessionSetup --> Settings : Check permissions or input settings
RecordingToolbar --> Settings : Check recording settings
Settings --> SessionLibrary : Close preferences opened from list
Settings --> SessionSetup : Return to start settings
Settings --> RecordingToolbar : Return to recording screen
```

- Recover shows the last verified record and missing ranges. It never automatically turns on the microphone or resumes the existing session. **Continue recording** opens Setup for a new session linked via parent_session_id and prepares the first capture. After completing recovery review, the post-recording supplementation, regeneration, and export paths are used.
- Closing Settings returns to the calling screen. Settings that change the current session's microphone, timeline, or transcription policy indicate whether they can change immediately; if they cannot apply, they show **From the next session**. If recording is interrupted by a permission change, the toolbar keeps the actual state and alternative input.
- The close button on windows during recording hides auxiliary windows. Ending a session and quitting the app are not the same. When quitting the app is requested, the recording state is checked and an inline choice offers **Stop and save**, **Keep recording**, or the necessary recovery preservation. Quitting is never treated as successful while saving has failed.
- Opening the last saved copy is a direct entry into SessionReview. When opening a stale result, only the supplementation-needed and regeneration states are shown, without automatic recording or automatic sending.

## 8. Review scope

The proposed scenarios and the full screen list, the mapping between local processes and public contracts, and success, failure, cancel, resume, and direct-entry-from-saved-copy paths are reviewed statically. Time acceptance of in-screen input, IME, accessibility, multi-window restoration, the actual layout of the OS picker, and permission changes, microphone failure, save failure, and recovery behavior are checked in the [Verification plan](system-design-to-be.md#verification) and the [Layout common rules](screen-layout.md#common). This document is not evidence that the product was run or that the DSL was rendered.

2026-10-31 static check: node and label syntax of the 6 navigation blocks and Layout correspondence for the 11 screens were checked. The link and anchor check of the two UI files resulted in FAIL 0, WARN 0. Verification with the actual OS UI and by running the product has not been performed.
