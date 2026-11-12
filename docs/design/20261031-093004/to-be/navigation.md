# Cuevia navigation design

- Reference date: 2026-10-31
- Status: Proposed design for a new local macOS app. Implementation and runtime verification are pending.
- Scope: Initial capture selection, synchronized recording, review and revisions, prompt generation and delivery, recovery, and settings. This document covers all scenarios.
- References: [core design](system-design-to-be.md), [requirements](system-design-to-be.md#requirements), [public contracts](system-design-to-be.md#contracts), [delivery rules](system-design-to-be.md#delivery), and [verification plan](system-design-to-be.md#verification).
- Screen regions and accessibility are described in the [screen layout](screen-layout.md). The core design defines state, storage, and compilation contracts. Process names below summarize navigation decisions.

## 1. Screens and transitions

The app has no web URLs or HTTP endpoints. It launches into `SessionLibrary`. Reopening from the Dock or choosing a menu command brings the existing window forward. If an unfinished session can be recovered, `RecoveryPanel` opens first. Opening a saved session leaves recording stopped.

`RecordingToolbar`, `SceneCanvas`, and `NotePanel` can be open at the same time. Arrows between them open or raise a window and leave the source window intact. Auxiliary windows hide during capture selection. Canceling restores the previous window layout and input target. `CapturePicker` identifies the OS-owned picker. `RegionOverlay` is the app overlay for selecting a region within one display.

| Screen ID | Entry and lifetime | Layout |
|---|---|---|
| SessionLibrary | Main window and entry point for saved sessions | [Library](screen-layout.md#sessionlibrary) |
| SessionSetup | New session draft and settings before recording | [Setup](screen-layout.md#sessionsetup) |
| RecordingToolbar | Remains visible in Starting, Recording, and Paused | [Toolbar](screen-layout.md#recordingtoolbar) |
| CapturePicker | Exists only while selecting a display or window in the OS picker | [OS picker](screen-layout.md#capturepicker) |
| RegionOverlay | Exists only while selecting a region on one display | [Region selection](screen-layout.md#regionoverlay) |
| SceneCanvas | Displays the immutable capture, annotations, and pointing | [Canvas](screen-layout.md#scenecanvas) |
| NotePanel | Text input explicitly linked to a scene or the whole session | [Notes](screen-layout.md#notepanel) |
| SessionReview | Read-only review while paused, plus revision and error review after recording | [Review](screen-layout.md#sessionreview) |
| PromptPreview | Generated result and actual delivery assets | [Preview](screen-layout.md#promptpreview) |
| Settings | Local permissions, audio, transcription, and storage | [Settings](screen-layout.md#settings) |
| RecoveryPanel | Entry point for crashed, damaged, or unfinished sessions | [Recovery](screen-layout.md#recoverypanel) |

`activeScene` is the explicit target for subsequent recording. `inspectedScene` is the scene on screen. Switching to another app, clicking NotePanel, or viewing an earlier scene leaves `activeScene` intact. Choose **Record Against This Scene** on the canvas to change the target for subsequent speech, annotations, and pointing. Existing input keeps its original target. [FR-01, FR-03, FR-04](system-design-to-be.md#requirements)

### Navigation decisions and local contracts

Parenthesized nodes represent app-local processes. Each alias refers to a public entry point. On failure, the current screen displays the cause, a retry, and alternative input options in its error region.

| Process alias | Public entry point or decision | Screen result |
|---|---|---|
| prepare_capture | SessionCoordinator.PrepareDraft | Prepare first Scene or stay in Setup |
| start_session | SessionCoordinator.Start | Go from Starting to Recording or show start failure in Setup |
| capture_scene | SessionCoordinator.Capture | Replace the Scene and activeScene only on success |
| pause_for_review | SessionCoordinator.Pause, including the input barrier | Open read-only review after Paused is confirmed |
| resume_session | SessionCoordinator.Resume | Return to the toolbar and canvas ready to record new input |
| finalize_session | SessionCoordinator.Finalize | End recording and open Review |
| compile_prompt | CompileCoordinator.Compile | Preview the latest result or show required revisions in Review |
| recover_session | SessionStore.Recover | Open recovery results in Review or stay in the failed state |
| export_delivery | DeliveryExporter.Export | Show the result only once files and paths are confirmed |

When input fails to save, keep the draft and retry action and report the transition failure. Later actions such as Finalize or Close wait for a successful save or an explicit discard. Capture failure keeps the existing Scene. See the [public contracts](system-design-to-be.md#contracts) for details and error types.

<a id="start"></a>
## 2. New session and first capture

Choose **New Session** in `SessionLibrary`. Select question, change request, or mixed mode, decide whether to use a microphone, and choose a capture source. Text-only means **capture plus text input without a microphone**. Voice-only and general-note-only sessions without a capture are outside the MVP. Starting requires one prepared Scene. [FR-01, FR-02, FR-05, FR-09](system-design-to-be.md#requirements)

```navigation
SessionLibrary --> SessionSetup : New session
SessionSetup --> CapturePicker : Select a window or display
CapturePicker --> (prepare_capture) : Confirm capture source
CapturePicker --> RegionOverlay : Confirm one display for region capture
CapturePicker --> SessionSetup : Cancel initial selection
RegionOverlay --> (prepare_capture) : Confirm region
RegionOverlay --> SessionSetup : Cancel initial region selection
(prepare_capture) --> SessionSetup : First Scene ready
(prepare_capture) --> SessionSetup : Permission denied or capture failed
SessionSetup --> (start_session) : Start with first Scene and input settings
(start_session) --> RecordingToolbar : Recording started
(start_session) --> SessionSetup : Start failed
RecordingToolbar --> SceneCanvas : Show the prepared first scene
RecordingToolbar --> NotePanel : Open text input
SessionSetup --> SessionLibrary : Close draft
```

1. Show capture permission and microphone status before selection. Use the actual OS permission dialog. If permission is denied, Setup offers applicable actions such as opening Settings, retrying, or starting without a microphone. A capture permission preflight failure alone still permits the OS picker path. Enable Start once the first Scene asset has been captured and verified.
2. The first capture is an immutable draft asset. On success, Setup shows a preview, source name and type, and capture time. Reselecting prepares a replacement draft. Keep the existing draft until success and restore it on cancellation.
3. Start passes through Starting before confirming the actual recording start time. The prepared first Scene has an effective session time of 0. Label its capture time **Captured Before Recording**. The wait before recording is excluded from recorded time.
4. If the selected microphone cannot be opened, show the cause. If the user chooses **Start Without a Microphone**, begin text-only recording and keep that status visible in the toolbar.
5. Closing Setup exits the draft screen. If a draft exists, an inline confirmation offers to save and close or discard it.

<a id="recording"></a>
## 3. Add scenes, inspect earlier scenes, and take notes while recording

Enter from RecordingToolbar or an open SceneCanvas. Each new capture adds a static Scene. Verify supported exclusions for app windows and OS picker behavior during implementation. [FR-01, FR-03, FR-04](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> CapturePicker : Choose a window or display for a new scene
SceneCanvas --> CapturePicker : New capture
CapturePicker --> RegionOverlay : Confirm one display for the new region
CapturePicker --> (capture_scene) : Confirm new capture source
RegionOverlay --> (capture_scene) : Confirm new region
(capture_scene) --> SceneCanvas : New Scene ready and target switched
(capture_scene) --> SceneCanvas : Capture failed and existing scene kept
CapturePicker --> SceneCanvas : Cancel selection and restore prior windows
RegionOverlay --> SceneCanvas : Cancel region selection and restore prior windows
RecordingToolbar --> SceneCanvas : View a scene or explicitly resume explaining
RecordingToolbar --> NotePanel : Open notes
SceneCanvas --> NotePanel : Open notes for this scene
NotePanel --> SceneCanvas : View the note's target scene
SceneCanvas --> RecordingToolbar : Interact with source app and hide canvas
NotePanel --> RecordingToolbar : Hide notes
```

- Restore visible recording and microphone status after capture selection. Opening the picker leaves recording running. Record a SelectionInterval for each request. Speech crossing that interval remains unresolved or linked to candidates regardless of cancellation or failure. Other inputs follow the actual activation boundary of the new Scene. Block duplicate capture requests while one is pending.
- Choosing an earlier Scene inspects it within the canvas. Show **Current Recording Target** alongside **Scene Being Viewed**. **Record Against This Scene** records an explicit target change and lets the user check the selected scene and subsequent pointing location.
- Point focus lasts until explicit release, a target change, Pause, or Stop. Show elapsed time and a **Clear Focus** action for long-held focus. Keep it until one of those events. Only explicitly indicated locations on the canvas are collected.
- Deleting the selected annotation closes open focus on its target. Delivery frames identify a pointed-to point or rectangle through its target ID, effective time, and highlight.
- Before entry, NotePanel fixes the target label to **Scene Sxx** or **Whole Session**. Selecting or saving a note leaves activeScene intact. Opening SceneCanvas preserves the draft note's target and text.
- Choosing **Interact with Source App** hides the canvas, starts an InteractionInterval, and updates the toolbar. Explicitly choosing **Resume Scene Explanation** closes the interval. References in speech crossing that interval require review before assignment to an existing target. Opening notes does not switch this mode.
- Pen, arrow, rectangle, text, focus changes, and note saves take place within the current screen. Show provisional strokes and text separately from saved input. See the [canvas](screen-layout.md#scenecanvas) and [note](screen-layout.md#notepanel) layouts for input and IME rules.

<a id="pause-review"></a>
## 4. Pause, review in read-only mode, and resume

Pause changes toolbar state in place. **Review Recording** opens Review after the Pause input barrier succeeds and Paused is confirmed. If capture or input commitment fails or is blocked, retain the source window, draft, and actual recording state. [FR-02, FR-06](system-design-to-be.md#requirements)

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

While Paused, users can browse scenes, events, recordings, and notes. Disable new captures and changes to annotations, focus, and recording notes, and explain that input requires resuming. Playback and inspection do not affect the recording target. Pause closes open focus and cancels pending capture tokens. Recheck state when a late capture arrives and discard it without creating a Scene. Resume keeps the prior activeScene and starts with no focused target. Point explicitly to explain a specific location again. See the [timing contract](system-design-to-be.md#contracts) for how pause appears on the session timeline.

Paused review is read-only. Revisions are available after Finalize. To finish, choose **Stop Recording and Generate Prompt** in Review.

The Pause/Stop input barrier blocks new gestures and captures, cancels pending capture tokens, and asks the editor to end composition. Accept the existing editor's committed or cancelled response through CommitInput, close focus, and drain media before showing Paused or stopped. If blocked, retain the draft, cancel the transition, and show **Commit or cancel your input, then try again**. Forced window closes and timeouts leave composition uncommitted.

<a id="finalize-compile"></a>
## 5. Stop recording, generate automatically, and revise

Stop is available while recording or paused. Once accepted, it closes input, commits and saves accepted input, then calls Finalize. SessionReview opens first with progress and original materials. PromptPreview opens after deterministic local compilation. Any external AI delivery is a separate user action. [FR-05, FR-06](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> (finalize_session) : Stop recording and generate prompt
SessionReview --> (finalize_session) : End paused recording and generate prompt
(finalize_session) --> SessionReview : Recording saved and stopped
(finalize_session) --> RecordingToolbar : Finalize failed and recording remains active
(finalize_session) --> SessionReview : Finalize failed while paused or interrupted data preserved
SessionReview --> (compile_prompt) : Generate automatically after stopping
(compile_prompt) --> PromptPreview : Ready or partial result for current input
(compile_prompt) --> SessionReview : Await review of external processing input
(compile_prompt) --> SessionReview : Output generation failed
PromptPreview --> SessionReview : Review evidence and unresolved items
SessionReview --> SceneCanvas : Open a post-recording scene revision
SessionReview --> NotePanel : Open a post-recording note revision
SceneCanvas --> SessionReview : Save revision and return to review
NotePanel --> SessionReview : Save revision and return to review
SessionReview --> (compile_prompt) : Regenerate with revisions
```

| Result or event | Visible result and next action |
|---|---|
| Finalize in progress | Block input and duplicate Stop commands, and show save progress. Closing the app does not indicate completion. |
| Finalize failed | Show actual recording state and uncommitted items. When invoked from Paused, show the failure in Paused Review and allow retry. |
| Transcription unconfigured or unsupported | Show **No Transcript** and audio intervals. Allow text additions and playback. Do not imply that the audio was interpreted. |
| Transcription pending, failed, or partial | Show status and affected intervals. Offer original audio and manual transcription or summary input. Empty transcription cannot be marked complete. |
| External processing required | Show awaiting_input_review, exact transmission inputs, and exclusions. Wait for explicit approval of the reviewed input before uploading. |
| Insufficient input | Explain the missing evidence and offer additional input. Basic evidence output is available as partial. Automatic question generation remains incomplete. |
| Unresolved reference or uncertain transcript | Go to the associated time and Scene to correct, explicitly confirm, or leave unconfirmed. |
| Current revision generated | Preview the mode, questions or requests, Scene references, and unconfirmed items. Continue to show unresolved counts and evidence. |
| Evidence materials only | Show artifact_status and intent_status separately. Distinguish generated/user_authored from unavailable/insufficient. Basic material generation alone leaves question generation incomplete. |
| Revision, mode, or transcript changed | Mark the existing Preview **Regeneration Required**. Disable copying and exporting it as the latest result until regenerated. |

Label scene edits, note changes, and transcript corrections after recording as **Post-Recording Revisions**. Link them to the scene or whole session and preserve the original recording, capture, and timeline entries. Saving changes reviewRevision and makes the previous prompt stale. If transcription completes for the same snapshot and review revision, continue compilation in that generation. After edits, require explicit regeneration for the new revision and keep the old generation out of the current preview. See the [public contracts](system-design-to-be.md#contracts).

Automatic Compile after Stop runs local base generation and available local engines. In SessionReview, separately preview the original audio and selected intervals for external ASR, and transcripts, notes, and masked frames for external interpretation. **Process Reviewed Materials Externally** sends only the inputs pinned by ReviewInputRevision and scope hash. Select audio exclusions separately from image masking. Later edits require a new review revision and cannot recall transmitted data. Final prompt delivery to another AI is manual.

<a id="delivery"></a>
## 6. Copy, inspect assets, and export a bundle

Enter from PromptPreview for the current revision. **Copy Prompt** puts text on the clipboard. Always show **Attach images and audio separately** next to the button. File attachment and AI delivery remain manual. [FR-07, FR-09](system-design-to-be.md#requirements)

```navigation
PromptPreview --> (export_delivery) : Create delivery bundle
(export_delivery) --> PromptPreview : Files and paths verified
(export_delivery) --> PromptPreview : Export failed
PromptPreview --> SessionReview : Check missing assets or unresolved evidence
PromptPreview --> SessionLibrary : Return to session library
SessionLibrary --> SessionReview : Open saved session
SessionReview --> PromptPreview : View current generation
```

Copying, showing assets in Finder, and selecting a destination happen in Preview or in OS dialogs. Preview, copy, and export use the same DeliveryProjection for a generation and profile, so masking and metadata exclusions agree. The default **Delivery** profile includes prompt, context, evidence, and composited frames. It excludes original audio, source images, PID, window titles, and local absolute project paths. **Include Originals** requires explicit selection after showing the scope and warning that it includes unmasked originals. Show success after checking the actual path and file list. On failure, offer retry or another location. Identify any missing assets and mark incomplete bundles. See the [delivery contract](system-design-to-be.md#delivery) for paths, the manifest, and relative asset references.

Keep warnings and unconfirmed items in prompts with unresolved information. The contract determines whether delivery is allowed when critical assets are missing, a revision is stale, generation fails, or input is insufficient. Show the reason in the UI. Use distinct success messages for exporting a source-preservation session and delivering the latest prompt.

<a id="recovery-settings"></a>
## 7. Recovery, settings, and closing windows

On launch, the recovery guard opens RecoveryPanel if there are recoverable sessions. The diagram begins with this process node because it determines the first visible screen. OS window reactivation uses the existing window. [FR-08, FR-09](system-design-to-be.md#requirements)

```navigation
(check_recovery) --> RecoveryPanel : Unfinished session available for recovery
(check_recovery) --> SessionLibrary : No sessions to recover
SessionLibrary --> RecoveryPanel : Unfinished session recovery notice
RecoveryPanel --> (recover_session) : Recover selected session
(recover_session) --> SessionReview : Open recoverable materials
(recover_session) --> RecoveryPanel : Recovery failed or selection required
RecoveryPanel --> SessionLibrary : Keep originals and handle later
SessionReview --> SessionSetup : Continue in a new session linked to the recovered one
SessionLibrary --> Settings : Open settings
SessionSetup --> Settings : Check permissions or input settings
RecordingToolbar --> Settings : Check recording settings
Settings --> SessionLibrary : Close settings opened from the library
Settings --> SessionSetup : Return to setup
Settings --> RecordingToolbar : Return to recording
```

- Recover shows the last verified record and missing ranges. The microphone remains off and the old session stays closed. **Continue Recording** opens Setup for a new session linked by parent_session_id and prepares its first capture. Once recovery review is complete, use post-recording revisions, regeneration, and export.
- Closing Settings returns to the calling screen. Indicate whether changes to the current session's microphone, timeline, or transcription policy can apply immediately. Mark deferred changes **Starting Next Session**. If a permission change interrupts recording, the toolbar shows the actual state and alternative input.
- Closing a window while recording hides that auxiliary window. A quit request checks recording state and offers inline choices to **Stop and Save**, **Keep Recording**, or preserve data for recovery as applicable. Report any save failure before quitting.
- Opening the last saved session goes directly to SessionReview. Stale results show the need for revisions or regeneration. Recording and delivery require user action.

## 8. Review scope

Static review covers proposed scenarios, the full screen inventory, mappings from local processes to public contracts, and success, failure, cancellation, resume, and direct entry paths for saved sessions. Check input timing, IME, accessibility, multi-window restoration, actual OS picker layout, permission changes, microphone and save failures, and recovery using the [verification plan](system-design-to-be.md#verification) and [common layout rules](screen-layout.md#common). This design documents planned behavior. Runtime verification and DSL rendering are separate checks.

Static review on 2026-10-31 checked node and label syntax in six navigation blocks and layout mappings for 11 screens. Link and anchor checks across the two UI documents reported FAIL 0 and WARN 0. Actual OS UI and runtime verification remained pending.
