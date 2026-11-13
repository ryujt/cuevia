# Cuevia Screen Layout Design

- Reference date: 2026-10-31
- Status: TO-BE proposal for a new macOS app. Values and arrangements are implementation proposals, not measured UI.
- Scope: per-screen sections combined. Includes all 11 user-perceived screens and the main overlays.
- Source links: [Core design](system-design-to-be.md), [Navigation](navigation.md), [Requirements](system-design-to-be.md#requirements), [Public contracts](system-design-to-be.md#contracts), [Delivery rules](system-design-to-be.md#delivery), [Verification plan](system-design-to-be.md#verification).

<a id="common"></a>
## 1. Common layout and input rules

Cuevia is a single local macOS service. The list, settings, and review use the main window, while recording uses floating toolbar, canvas, and note windows together. The `layout` blocks below express region containment and reading order and do not determine code modules or the OS window hierarchy. Horizontal numbers are relative distribution weights and do not guarantee pixels or exact screen ratios. Vertical sizes are not specified in the DSL.

| Common item | Target behavior |
|---|---|
| Recording state | Show Starting, recording, paused, finished, and error with text and icons. Never distinguish recording by red alone |
| Input target | Show separately the activeScene during recording, the inspectedScene while browsing, the note's explicit target, and post-recording supplementation. Never confuse the OS frontmost window with the recording target |
| Input commit | Distinguish temporary strokes, text, and note input from saved state. On save failure, keep the draft and show the failed item and retry |
| Keyboard / IME | Tab reading order matches layout order. Tool shortcuts and global commands never intercept characters during IME (e.g. Korean) composition. Pause and Stop request end of composition and then handle committed/cancelled/blocked responses. Blocked keeps the draft and actual recording state; retry after commit/cancel. No forced commit by closing a window |
| Shortcuts | Tool shortcuts work only when the canvas has focus. In input fields, character and editing keys take priority. Global commands are enabled after user settings and conflict checks. Esc cancels selection/temporary work and does not stop recording |
| Accessibility | Every icon provides an action name, state, and reason when disabled. VoiceOver reads the capture name, Scene number, time, and target. State warnings announce only a summary and do not continuously read out the audio meter or every time update |
| Non-mouse paths | Provide keyboard alternatives for the Scene list, shape list, and focus coordinate/position adjustment. Keep it possible to express the same question or request via text input |
| Loading / duplicates | Show running state, cancelability, and result in the work area. Block duplicate start, capture, stop, compile, and export commands. Never blank the whole UI without reason |
| Empty / errors | Empty lists offer a next action. Distinguish permission denied, device lost, save failure, missing assets, and absent transcription. Never attach a success badge to an empty result |
| Size / scale | With large text and narrow displays, the main window uses internal scrolling and vertical stacking. Primary action buttons are never hidden behind horizontal scrolling. Floating windows are restored within screen bounds |
| Privacy | Capture target and whether recording is on can be checked before starting and during recording. Automatic prompt generation is local processing, and there is no UI for automatic sending to an external AI |

New captures, annotations and focus, and notes during recording use the [SessionCoordinator contract](system-design-to-be.md#contracts). All recording input while Paused is read-only and becomes available after Resume. Corrections after finishing are saved as a separate supplementation revision. Post-recording supplementation is never presented as if it were original material collected at recording time.

<a id="sessionlibrary"></a>
## 2. SessionLibrary

Purpose: start a new session and select a locally saved session. This is the main window entered on first app launch, after skipping recovery, and after finishing review. The `SessionLibrary` identifier is used instead of an address. [Navigation](navigation.md#start), [FR-08](system-design-to-be.md#requirements).

```layout
Screen V LibraryHeader, LibraryBody, LibraryFooter
LibraryHeader > AppName, NewSessionButton, SettingsButton
LibraryBody V SessionSearch, RecoveryNotice, SessionList, LibraryStatus
SessionList V SessionRows, EmptySessionAction
LibraryFooter > LocalStorageSummary, LibraryHelp
```

| Region | Display, input, and result |
|---|---|
| LibraryHeader | New session opens SessionSetup; Settings opens Settings |
| SessionSearch·SessionRows | Name, date, input mode, recording/supplementation state, generated revision, and warning summary. Selecting opens SessionReview without automatically resuming recording |
| RecoveryNotice | Number of incomplete recordings and a way to open RecoveryPanel. Originals are kept even if handled later |
| LibraryStatus·LocalStorageSummary | List loading, read failure and retry, storage location and usage summary |

An empty session list shows **Select a screen and record your first question** and the New session button. Read failure and an empty list are never shown as the same state. In a narrow window, the search and settings buttons are stacked vertically, and secondary table metadata moves to a second line within the row. The list can be navigated, opened, and the search cleared with the keyboard, and each row's full state is included in its VoiceOver name.

<a id="sessionsetup"></a>
## 3. SessionSetup

Purpose: confirm the first Scene, intent mode, and microphone use. Entered via New session in SessionLibrary. Nothing is recorded yet, and Start is possible only after one Scene is prepared. [Navigation](navigation.md#start), [FR-01·FR-02·FR-05·FR-09](system-design-to-be.md#requirements).

```layout
Screen V SetupHeader, SetupBody, SetupStatus, SetupActions
SetupHeader > SessionName, DraftBadge
SetupBody > CaptureSetup : 55, InputSetup : 45
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions > SaveDraftButton, CloseSetupButton, StartButton
```

In a narrow window, `InputSetup` is stacked after `CaptureSetup`.

```layout
Screen V SetupHeader, CaptureSetup, InputSetup, SetupStatus, SetupActions
SetupHeader V SessionName, DraftBadge
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions V StartButton, SaveDraftButton, CloseSetupButton
```

| Region | Display, input, and result |
|---|---|
| CaptureType·SelectCaptureButton | Window, display, or region within a single display. Opens the OS CapturePicker or RegionOverlay and never tracks window titles live on its own |
| InitialScenePreview·CaptureMetadata | Immutable image, target name and type, actual capture time, size, scale, and region. Shows **Captured before recording started**. Keeps the existing draft if reselection fails |
| IntentMode·SessionGoal | Explicit choice of question, change request, or mixed, and an optional overall goal. Intent can be entered as text even without transcription |
| MicrophoneChoice·MicrophoneStatus | Use / don't use, device selection, permission, and level check. Not using means **Text only**. On error, starting without a microphone is chosen explicitly |
| TranscriptionChoice | Availability, not-configured, permission, and local model state for optional transcription. Distinguishes that transcription being unavailable does not mean microphone recording is unavailable |
| SetupStatus·StartButton | Readiness and failure reasons for capture, settings, and recording start. Start without a first Scene is disabled. Prevents duplicate starts during Starting |

Canceling the first capture restores Setup and the existing draft. Capture permission denial places go-to-settings and retry below the selection area. The preflight result alone never blocks the OS picker; whether Start is possible is decided by an actually captured and validated asset. Microphone permission denial places alternative input below the microphone. Any IME composition remaining in the name or goal is committed before Start. Start delays show the actual step, such as **Preparing microphone**, and the recording state turns on from the moment of success. A start failure keeps the draft and input on this screen.

<a id="recordingtoolbar"></a>
## 4. RecordingToolbar

Purpose: check recording state, target, and key commands while looking at other apps. Shown from Starting and kept during recording and pause; after finishing, SessionReview takes over its role. Closing the window hides it; stopping recording uses an explicit Stop. [Navigation](navigation.md#recording), [FR-02·FR-03·FR-09](system-design-to-be.md#requirements).

```layout
Screen V ToolbarMain, ToolbarStatus
ToolbarMain > RecordingState, SessionClock, ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, PauseResumeButton, ReviewButton, StopButton
ToolbarStatus > OperationMessage, SaveState, FocusSummary
```

When horizontal space is insufficient, commands wrap onto two lines, and Stop, state, and Pause/Resume are always exposed.

```layout
Screen V EssentialControls, SecondaryControls, ToolbarStatus
EssentialControls > RecordingState, SessionClock, PauseResumeButton, StopButton
SecondaryControls > ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, ReviewButton
ToolbarStatus V OperationMessage, SaveState, FocusSummary
```

| Region | Display, input, and result |
|---|---|
| RecordingState·SessionClock | Starting, recording, paused; session elapsed time and actual recording state. Pause intervals remain in session elapsed time, and collection/recording interruptions are indicated separately |
| ActiveSceneLabel | **Recording target Sxx: title**. Independent of the OS frontmost window and the viewed scene. Clicking brings that SceneCanvas to the front |
| MicrophoneIndicator | Not used, recording, paused, device error. Low level is never confused with permission failure |
| CaptureButton·CanvasButton·NoteButton | Select new Scene, open canvas, open notes. While operating the source app, CanvasButton shows **Resume scene explanation** and ends the InteractionInterval when explicitly run. Prevents duplicate commands during capture. Recording target is unchanged even when auxiliary windows are open |
| PauseResumeButton·ReviewButton | Pause/Resume updates the state. Review recording opens read-only Review after Pause succeeds |
| StopButton | **Stop and generate prompt**. Shows progress of saving accepted input and stopping, and blocks duplicate runs |
| SaveState·FocusSummary | Saving, saved, save failed; the persisting focus target and release. Warns of elapsed time when focus is held long |

While Paused, capture, new annotations, and notes are shown disabled with the reason exposed. During Pause/Stop processing, new gestures and captures are blocked, but the existing editor's composition-end responses are accepted. If blocked, it shows **Interruption incomplete: commit/cancel input and try again** and preserves the draft and actual recording state. On microphone device loss or sleep, new samples stop, a gap is recorded, the target ends, and then paused is shown, requiring an explicit Resume. If a composition block prevents completing the transition to paused, **Audio interrupted/failed** is shown separately from the actual session state, and the **Recording** badge is not kept. Global shortcuts never intercept IME composition, and Esc during capture selection cancels only the selection. The toolbar must be findable and activatable by keyboard, and the drag handle needs an alternative for moving the window.

<a id="capturepicker"></a>
## 5. CapturePicker

Purpose: OS-provided window and display selection. Entered from the first selection in Setup or a new capture during recording. The block below is not a custom picker layout to be implemented by the product but a **diagram** describing the contract before and after the app. The actual appearance and cancel method may vary by OS version and permission state. [Navigation](navigation.md#start), [FR-01·FR-09](system-design-to-be.md#requirements).

```layout
Screen V OSSelectionSurface
OSSelectionSurface V OSManagedTargetChoices, OSManagedSelectionActions
```

`OSManagedTargetChoices` are the selectable windows and displays exposed by the OS, and `OSManagedSelectionActions` are confirm and cancel selection. The app makes no promises about the detailed fields, shortcuts, or ordering of the OS-owned UI. Capture modes and region options added by the app are chosen in SessionSetup or RecordingToolbar.

Before selection, the app hides its auxiliary windows and restores them after completion or cancellation. Selection success alone does not change the Scene; it is applied after actual capture and save succeed. Permission denial, protected content, vanished windows, and display changes are shown in the calling screen's error area, and the previous Scene is kept. In region mode, RegionOverlay opens after display selection succeeds. The OS picker's VoiceOver, keyboard, and multi-display behavior are empirically verified on supported OS versions.

<a id="regionoverlay"></a>
## 6. RegionOverlay

Purpose: confirm a static capture region within one display. Opens only over that screen after choosing a display. Regions spanning multiple displays are not supported. [Navigation](navigation.md#recording), [FR-01](system-design-to-be.md#requirements).

```layout
Screen V RegionInstructions, DisplaySelectionSurface, RegionActions
RegionInstructions > SelectedDisplayName, SelectionHint
RegionActions > RegionDimensions, KeyboardAdjustHint, CancelRegionButton, ConfirmRegionButton
```

| Region | Display, input, and result |
|---|---|
| DisplaySelectionSurface | A selection frame overlaid on a single dimmed screen. Drag to start and adjust; resize with handles. Constrained to the display bounds; coordinates and size are saved in the result metadata |
| RegionDimensions·KeyboardAdjustHint | Shows selection size and scale readably. Provides keyboard move/resize and, if needed, a numeric input alternative |
| RegionActions | Only a valid region can be confirmed. Enter to confirm and Esc to cancel never intercept text composition. Ensures the selection indicator itself does not appear in the final capture |

The initial empty region shows **Specify a region**. Too-small regions, display disconnection during selection, and scale changes block confirmation and offer reselection or cancel. Cancel restores the previous activeScene and window arrangement. With large text or small displays, tool descriptions may collapse, but confirm, cancel, and selection size remain exposed. Precise keyboard selection and whether the overlay is excluded are implementation verification items.

<a id="scenecanvas"></a>
## 7. SceneCanvas

Purpose: review the original capture together with time-synchronized annotations, pointing, and post-recording supplementation. Entered by selecting a Scene from the toolbar or SessionReview, and can be open together with NotePanel. The captured image is not modified; annotations are shown on a separate layer. [Navigation](navigation.md#recording), [FR-01·FR-03·FR-06](system-design-to-be.md#requirements).

```layout
Screen V CanvasHeader, CanvasBody, CanvasFooter
CanvasHeader > InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
CanvasBody > SceneRail : 18, CanvasWorkArea : 64, AnnotationInspector : 18
SceneRail V SceneThumbnails, SceneSelectionStatus
CanvasWorkArea V AnnotationTools, SceneViewport, CanvasMessage
AnnotationInspector V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

When narrow, SceneRail moves to a top selection strip and the inspector to a disclosure area.

```layout
Screen V CanvasHeader, SceneStrip, AnnotationTools, SceneViewport, InspectorDisclosure, CanvasMessage, CanvasFooter
CanvasHeader V InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
SceneStrip > PreviousSceneButton, SceneSelector, NextSceneButton
InspectorDisclosure V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

| Region | Display, input, and result |
|---|---|
| InspectedSceneTitle·ActiveSceneBadge | Shows viewed Sxx and recording target Syy together, with an explicit warning when they differ. The target switch button is used only during recording, and viewing a past Scene does not itself switch the target |
| CanvasModeBadge | **Recording input**, **Paused: read-only**, **Post-recording supplementation**. Post-recording supplementation uses a marker distinct from records at the original time |
| InteractSourceButton | **Operate source app** hides the canvas and starts an InteractionInterval. **Resume scene explanation** in the toolbar ends the interval. Speech crossing the interval is excluded from automatic linking to the existing target. Distinct from opening the note window |
| AnnotationTools | Select, pen, arrow, rectangle, text, pointing. Color, thickness, and the current tool can be checked as text. Changes are committed through CommitInput |
| SceneViewport | Separates original size/coordinates from on-screen display size. Zoom and pan do not change saved coordinates. Shows the transparent annotation layer and focus over the original |
| AnnotationInspector·ObjectList | Annotation ID, type, linked time, content, and selection state. Keyboard selection, delete/correct, and text alternatives. Original annotations use a traceable correction policy |
| FocusControls | Current point position, target, held duration, and explicit release. Warns of stale focus. Target switch, deleting the selected annotation, Pause, and Stop close focus; Resume starts with target released |
| CanvasTime·SceneNoteButton | Shows the difference between viewing time and recording time. Opening this Scene's note only sets the NotePanel target; activeScene stays the same |

While waiting for a new capture, only the processing state is shown over the existing image. A capture failure keeps the existing Scene and input. A missing image asset shows Scene metadata, a missing message, re-check, and go-to-review instead of a blank black canvas. Recording cannot start without a Scene, but a damaged recovered copy can be opened in this error state.

The text tool shows the temporary string during composition and saves the event after composition completes and is committed. Esc cancels temporary shapes and input and does not stop recording. A Pause or Stop request while drawing waits for the commit/cancel result of the accepted temporary input. Tool shortcuts are not executed when the Canvas is not focused or when typing in NotePanel. VoiceOver accesses the image through the Scene name, capture metadata, annotation list, and focus position description. When coordinates alone cannot convey meaning, a descriptive text for the target can be entered alongside.

<a id="notepanel"></a>
## 8. NotePanel

Purpose: enter meaning instead of or alongside speech, and supplement information after finishing. Opened from the toolbar, canvas, or review. The recording target does not change even when this window comes to the OS front. [Navigation](navigation.md#recording), [FR-04·FR-06](system-design-to-be.md#requirements).

```layout
Screen V NoteHeader, NoteTarget, NoteEditor, NoteHistory, NoteStatus, NoteActions
NoteHeader > NoteModeBadge, SessionTitle, HideNoteButton
NoteTarget > TargetScopeSelector, ExplicitSceneSelector, ViewTargetButton
NoteEditor V NoteIntent, NoteTextInput
NoteHistory V SavedNotes, SelectedNoteDetails
NoteActions > SaveNoteButton, ClearDraftButton
```

| Region | Display, input, and result |
|---|---|
| NoteModeBadge | Clearly distinguishes recording input, paused read-only, and post-recording supplementation |
| TargetScopeSelector·ExplicitSceneSelector | Whole session or a specific Sxx, labeled **Note target**. Commits/preserves the in-progress draft before changing target and never silently changes existing notes' links |
| NoteIntent | Choose question, change request, or context. Shows the session's default mode while allowing each note's actual intent to be stated |
| NoteTextInput | Multi-line text input with IME support. Enter inserts a line break; save is a separate command. The placeholder is an example and is never saved as input data |
| SavedNotes | Time, target, intent, and recorded/postrecord distinction. Select to view content and make permitted corrections. Never switches automatically to another Scene's notes during recording |
| NoteStatus·SaveNoteButton | Saving, done, failed; empty-content validation and shortcut hints. The draft is not cleared before success is confirmed |

In a text-only session, this window opens together as the first recording window so questions can be left even without a microphone. An empty note list provides a target and examples. While Paused, the editor, save, and target-change inputs are disabled, and existing notes can be read. Post-recording supplementation saves a supplementation badge, authoring time, and linked target, and marks the prompt as stale.

The save shortcut never intercepts commit during IME composition. Save happens after composition ends, and hiding the window, changing target, or Stop indicate that an unsaved draft exists. Errors provide cause and retry in NoteStatus. When narrow, TargetScope and Scene selection are stacked vertically, and even with history collapsed, the input target and save state remain. VoiceOver announces save success once and does not repeatedly read the input content.

<a id="sessionreview"></a>
## 9. SessionReview

Purpose: review originals, the timeline, and unresolved references, and supplement after finishing. Entered via Paused review, automatic generation after Stop, opening a saved session, or completed recovery. [Navigation](navigation.md#pause-review), [FR-05·FR-06·FR-08](system-design-to-be.md#requirements).

```layout
Screen V ReviewHeader, ReviewStateBanner, ReviewBody, ReviewTimeline, ReviewActions
ReviewHeader > SessionName, IntentModeSelector, RevisionState
ReviewBody > ReviewScenes : 20, EvidenceViewer : 50, ReviewInspector : 30
ReviewScenes V SceneFilter, ReviewSceneRows
EvidenceViewer V SelectedScenePreview, AudioPlayback, EventContext
ReviewInspector V UnresolvedSummary, UnresolvedItems, TranscriptEditor, CorrectionActions, ExternalInputReview
ExternalInputReview V ExternalStageScope, ReviewedTextAndFrames, AudioSelectionAndExclusions, ReviewedRevisionStatus, RunReviewedExternalButton
ReviewTimeline V TimelineTracks, TimelinePosition
ReviewActions > ReturnToLibraryButton, ResumeRecordingButton, AddCorrectionButton, GeneratePromptButton, OpenPromptButton
```

A narrow window shows the evidence first and stacks the unresolved list and transcription below.

```layout
Screen V ReviewHeader, ReviewStateBanner, ReviewSceneSelector, EvidenceViewer, ReviewTimeline, ReviewInspector, ReviewActions
ReviewHeader V SessionName, IntentModeSelector, RevisionState
EvidenceViewer V SelectedScenePreview, AudioPlayback, EventContext
ReviewTimeline V TimelineTracks, TimelinePosition
ReviewInspector V UnresolvedSummary, UnresolvedItems, TranscriptEditor, CorrectionActions, ExternalInputReview
ExternalInputReview V ExternalStageScope, ReviewedTextAndFrames, AudioSelectionAndExclusions, ReviewedRevisionStatus, RunReviewedExternalButton
ReviewActions V GeneratePromptButton, OpenPromptButton, AddCorrectionButton, ResumeRecordingButton, ReturnToLibraryButton
```

| Region | Display, input, and result |
|---|---|
| ReviewStateBanner | Paused read-only, post-recording supplementation available, recovered copy and missing ranges, compile/save errors, awaiting_input_review. artifact_status and intent_status are separated to distinguish base material success from automatic question generation success |
| IntentModeSelector·RevisionState | Question, request, or mixed mode, plus input revision and prompt revision. Mode changes are applied as post-recording supplementation and show regeneration required |
| ReviewSceneRows·SelectedScenePreview | Scene ID, capture time, target metadata, and actual file existence. Selecting is viewing and does not change activeScene |
| AudioPlayback | Audio waveform/segments, playback, speed, missing audio, microphone unused. Playback does not mean resuming recording. Provides keyboard control and time text |
| EventContext·TimelineTracks | Check Scene changes, annotations, focus, notes, and audio at the same point in time. Shows capture SelectionIntervals, source-app InteractionIntervals, and candidates/unresolved status for speech crossing their end. Shows Pause boundaries and accepted order, and distinguishes wall time from session time |
| UnresolvedItems | Actually judged items such as an ambiguous "here", unlinked speech or Scenes, and transcription failures or low-confidence segments. Selecting shows the related time, Scene, and evidence |
| TranscriptEditor·CorrectionActions | Distinguishes no transcription / pending / failed / partial / complete. Shows the original text and the results of manual transcription, correction, or keeping as unconfirmed. Also checks masking of derived delivery images and remaining text quotes. Never auto-guesses speech content |
| AddCorrectionButton | Opens the supplementation mode of NotePanel or SceneCanvas after finishing. Preserves existing records and changes the revision |
| ExternalInputReview | Shows the actual external provider, stage, and sending scope. For external ASR, play the selected original audio and excluded ranges; for semantic organization, check the reviewed text and masked frames. Never claims that image masking also masked audio |
| ReviewedRevisionStatus·RunReviewedExternalButton | Runs **Process externally with reviewed materials** for a fixed ReviewInputRevision and scope hash. Requires re-review if changed after review. Never uploads automatically just because a provider is configured |
| GeneratePromptButton·OpenPromptButton | Automatic generation progress right after finishing; regenerate after supplementation. Only the current revision's result is shown as latest |

While Paused, only audio playback, the timeline, and Scene viewing are available. Supplementation input and mode changes are disabled, and **Supplement after stopping recording** or Resume is offered. After finishing, generation is possible from notes, Scenes, and annotations even without original audio or transcription. However, if there is no evidence for questions/requests, **Insufficient input: automatic question generation incomplete** is shown with supplementation actions. The base evidence output can be viewed as partial, but an empty prompt is never shown as a completed result.

Unresolved items support correct, confirm, and keep-unconfirmed, but unconfirmed content is never turned into definite statements. If transcription completes under the same snapshot and review revision, the same generation proceeds to its next step. If user supplementation changes the revision, the previous result is not shown as the latest success and is regenerated with the new revision. Automatic generation failure does not block reviewing originals and offers error, retry, and text supplementation. Screen readers must be able to access the same evidence through a sorted event list instead of the timeline's visual bars.

Automatic generation right after Stop processes only up to local base materials and ready local engines. External stages are in the awaiting-review state, neither ready nor failed. intent_status values generated, user_authored, unavailable, and insufficient are shown readably. No engine or engine failure is unavailable/partial, and insufficient question evidence is insufficient/partial. After review, the sending history and actual scope are recorded, and the UI indicates that later supplementation does not retract previous sends.

<a id="promptpreview"></a>
## 10. PromptPreview

Purpose: check the prompt text to deliver together with the actual attached assets. Entered from a ready/partial result for the current revision or by opening a saved generated copy. There is no button for sending to an external AI. [Navigation](navigation.md#delivery), [FR-05·FR-06·FR-07·FR-09](system-design-to-be.md#requirements).

```layout
Screen V PromptHeader, PromptStateBanner, DeliveryProfile, PromptBody, DeliveryNotice, PromptActions, ExportResult
PromptHeader > SessionTitle, PromptMode, PromptRevision
DeliveryProfile V ProfileSelector, IncludedSourceWarning, MaskingReviewStatus
PromptBody > PromptTextPane : 65, AttachmentPane : 35
PromptTextPane V PromptSections, PromptTextView
AttachmentPane V AssetSummary, ActualAssetRows, UnresolvedReferenceRows
PromptActions > BackToReviewButton, RegenerateButton, CopyPromptButton, RevealAssetsButton, ExportBundleButton
ExportResult V ExportProgress, ExportLocation, ExportFileList, ExportError
```

In a narrow window, AttachmentPane is stacked after PromptTextPane. The prompt body supports line wrapping and text selection and does not require horizontal scrolling. Even if the asset list moves down, DeliveryNotice and copy/export status stay close to the body.

| Region | Display, input, and result |
|---|---|
| PromptRevision·PromptStateBanner | Current input and generated revision; latest, regeneration required, warning, failed. Opening a stale generated copy never shows it as latest |
| PromptSections·PromptTextView | Goal, questions/change requests, Scene references, time evidence, and unconfirmed text built from the selected profile's DeliveryProjection. Applies the same redaction and metadata exclusion as copy/export. Original edits are done by returning to Review |
| DeliveryProfile | Default **Delivery** and explicit **Include originals** options. Delivery includes composited frames and prompt/context/evidence and excludes original audio, original images, PID, window titles, and local absolute paths. Include originals indicates that pre-masking materials are included |
| ActualAssetRows | Actual composited images, file identifiers, relative references, and inclusion/exclusion reasons for the selected profile. Target ID, effective time, and highlight of point/rect focus can be verified in composited images. Original assets are distinguished as local evidence views. Placeholders are never shown as actual attachments |
| UnresolvedReferenceRows | Count and targets of missing or unconfirmed references. Selecting returns to the evidence in Review |
| DeliveryNotice | **Copy copies text only. Attach images and audio separately to the AI you use.** |
| CopyPromptButton | Copies only the currently permitted text from the same DeliveryProjection as Preview and export. The success notice is also limited to **Prompt text copied** |
| RevealAssetsButton·ExportBundleButton | Show actual assets in Finder; create a verified bundle. Relative paths and manifest rules use the delivery contract |
| ExportResult | Progress, actual successful path, included files, errors. The open-in-Finder action is enabled after path verification |

When input changes, manual transcription, mode edits, or new supplementation invalidate the result, **Regeneration required** is shown at the top and in the action area. Copying the latest version and exporting with the prompt are disabled until a successful result for the current revision is ready. Missing assets, write failures, and insufficient storage show the affected file with retry and another location. Export success is never described as meaning the AI read the files or processed the question. VoiceOver announces copy success and the bundle path once and does not automatically read the entire long prompt.

<a id="settings"></a>
## 11. Settings

Purpose: check local input, permissions, transcription, storage, and shortcut settings. Opened from Library, Setup, or Toolbar; closing returns to the calling screen. [Navigation](navigation.md#recovery-settings), [FR-09](system-design-to-be.md#requirements).

```layout
Screen V SettingsHeader, SettingsBody, SettingsStatus, SettingsActions
SettingsHeader > SettingsTitle, CurrentSessionNotice
SettingsBody > SettingsSections : 25, SelectedSettings : 75
SettingsSections V PermissionsSection, AudioSection, TranscriptionSection, StorageSection, KeyboardSection
SelectedSettings V SectionExplanation, SettingControls, AvailabilityStatus, SettingHelp
SettingsActions > RestoreDefaultsButton, CloseSettingsButton
```

| Region | Display, input, and result |
|---|---|
| PermissionsSection | OS permission state for screen capture and microphone, and opening System Settings. Never changes permission values as if granted within the app |
| AudioSection | Microphone device, default usage, and check. When no device exists, a clear "none" and a text-only explanation |
| TranscriptionSection | Optional local transcription settings, model/language, unavailable/download-required/failed states. External providers and semantic organization providers are configured separately, showing possible sending scope, key management, and policy. Actual session upload is possible only after input review and explicit run in Review |
| StorageSection | Local storage location, usage, default export location, read/write check results. Moving existing sessions is shown separately from the applied contract |
| KeyboardSection | Shortcuts, conflicts, and active scope. Guidance on IME/text input protection and keyboard access paths |
| CurrentSessionNotice·AvailabilityStatus | Applied to the current session, or **From the next session**. Settings that cannot change during recording are disabled with the reason |

In a narrow window, the left list becomes a top selection menu and the settings body is shown vertically. Returning from System Settings re-checks the actual permissions and never shows success without permission. Voice recording and transcription availability are separated so that an unconfigured model or transcription failure does not look like recording is impossible app-wide. The screen + note flow remains usable even without microphone permission. Save failures and device list loading errors place error and retry only in the relevant section.

<a id="recoverypanel"></a>
## 12. RecoveryPanel

Purpose: recover verified local materials from sessions that did not finish, and check missing ranges. Entered from the app launch guard or the recovery notice in Library. Recovery success opens Review without automatically resuming recording. [Navigation](navigation.md#recovery-settings), [FR-08](system-design-to-be.md#requirements).

```layout
Screen V RecoveryHeader, RecoveryBody, RecoveryStatus, RecoveryActions
RecoveryHeader V RecoveryTitle, RecoveryExplanation
RecoveryBody > RecoverableSessions : 35, RecoveryDetails : 65
RecoverableSessions V RecoverySessionRows, RecoveryEmptyState
RecoveryDetails V LastVerifiedPosition, AvailableAssets, MissingRanges, RecoveryConsequences
RecoveryActions > RecoverSelectedButton, InspectFailureButton, LaterButton
```

| Region | Display, input, and result |
|---|---|
| RecoverySessionRows | Session name, last saved time, abnormal termination indicator, recoverable state. Selectable by keyboard |
| LastVerifiedPosition·AvailableAssets | Last verified point and the actual existing range of Scenes, events, and audio |
| MissingRanges·RecoveryConsequences | Missing or incomplete intervals, and the readable/supplementable scope after recovery. Never assumes there is no loss |
| RecoveryStatus | Checking or recovering, failure cause and retry. One session's failure does not block selecting other sessions |
| RecoverSelectedButton·LaterButton | Recover the selected copy, or go to Library while keeping originals. Originals of failed recoveries are never deleted automatically |

If the recovery target has disappeared, an empty state and Back to Library are shown. A recovery check failure is never presented as an empty-list success. When narrow, details are stacked after the session list. Recovery progress and completion are read out as status announcements, and missing items are provided as an ordered list. After recovery, the prompt must be regenerated with the actual recovered revision, and past successful copies are not treated as latest.

## 13. Static review and unverified scope

Each `layout` is written as a tree with a single root, reviewed so that within the same block there are no duplicate container definitions, multiple parents, cycles, or excess horizontal weights. The 11 screens in Navigation are cross-checked against the 11 identifiers in this document. This document defines target interactions, errors, empty values, permissions, keyboard, and narrow-window states, and is not a record of completed responsive or accessibility verification of the actual app.

Paths to verify first during implementation: first-capture permission denial, window disappearing during capture, text-only start, microphone start failure and device loss, Stop during IME composition, Pause review and supplementation prohibition, past Scene viewing and explicit target switching, long-held focus, no/failed transcription, previous compile completing during supplementation, missing assets and export failure, and abnormal termination recovery. Verification acceptance criteria follow the [Core verification plan](system-design-to-be.md#verification).

2026-10-31 static check: the 15 layout blocks were checked for single root, duplicate parents, cycles, duplicate containers, and horizontal weights, along with the 11 screen anchors, with 0 structural errors. The link and anchor check of the two UI files was FAIL 0, WARN 0. Renderer conversion and verification of actual window sizes, VoiceOver, and IME behavior were not part of this check.
