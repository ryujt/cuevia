# Cuevia screen layout design

- Reference date: 2026-10-31
- Status: Proposed design for a new macOS app. Measurements and layouts are implementation proposals.
- Scope: All 11 user-visible screens and major overlays.
- References: [core design](system-design-to-be.md), [navigation](navigation.md), [requirements](system-design-to-be.md#requirements), [public contracts](system-design-to-be.md#contracts), [delivery rules](system-design-to-be.md#delivery), and [verification plan](system-design-to-be.md#verification).

<a id="common"></a>
## 1. Common layout and input rules

Cuevia is a single local macOS service. The library, settings, and review use the main window. Recording uses floating toolbar, canvas, and note windows together. Each `layout` below shows region nesting and reading order. Code modules and the OS window hierarchy are defined separately. Horizontal numbers are relative weights. The DSL specifies neither exact widths nor heights.

| Common concern | Target behavior |
|---|---|
| Recording status | Use text and icons for Starting, Recording, Paused, Stopped, and Error. Use cues beyond red alone to indicate recording. |
| Input target | Identify activeScene, inspectedScene, the explicit note target, and post-recording revisions separately. The frontmost OS window does not determine the recording target. |
| Input commitment | Distinguish provisional strokes, text, and notes from saved input. On failure, retain the draft and show the failed item and retry action. |
| Keyboard and IME | Tab order follows layout order. Tool shortcuts and global commands leave Korean IME composition intact. Pause and Stop request composition completion and handle committed, cancelled, or blocked. If blocked, retain the draft and actual recording state, and retry after committing or canceling. Closing a window never forces commitment. |
| Shortcuts | Tool shortcuts work only when the canvas has focus. Text fields prioritize text and editing keys. Enable global commands after user configuration and conflict checks. Esc cancels selection or provisional input and leaves recording active. |
| Accessibility | Give every icon an action name, state, and disabled reason. VoiceOver reads capture name, Scene number, time, and target. Announce summaries of status warnings and avoid repeated announcements for audio meters and timer updates. |
| Keyboard alternatives | Provide keyboard access to scene and shape lists and focus coordinate or position controls. Users can express the same question or request in text. |
| Loading and duplicates | Show work in progress, cancellation availability, and results in the working area. Prevent duplicate start, capture, stop, compile, and export commands. Keep surrounding UI visible. |
| Empty states and errors | Provide a next action for empty lists. Distinguish denied permissions, lost devices, save failures, missing assets, and missing transcripts. Only actual results qualify for success. |
| Size and scale | Use internal scrolling and vertical stacking for large text and narrow displays. Keep primary actions within view without horizontal scrolling. Restore floating windows within display bounds. |
| Privacy | Show capture source and microphone status before and during recording. Automatic prompt generation is local. External AI delivery is manual. |

New captures, annotations, focus, and notes use the [SessionCoordinator contract](system-design-to-be.md#contracts). While Paused, all recording inputs are read-only until Resume. Save post-recording corrections in a separate revision and label them as later additions.

<a id="sessionlibrary"></a>
## 2. SessionLibrary

Start a new session or choose a locally saved one. This is the main window on first launch, after skipping recovery, or after leaving review. It uses the `SessionLibrary` identifier. [Navigation](navigation.md#start), [FR-08](system-design-to-be.md#requirements).

```layout
Screen V LibraryHeader, LibraryBody, LibraryFooter
LibraryHeader > AppName, NewSessionButton, SettingsButton
LibraryBody V SessionSearch, RecoveryNotice, SessionList, LibraryStatus
SessionList V SessionRows, EmptySessionAction
LibraryFooter > LocalStorageSummary, LibraryHelp
```

| Region | Display, input, and result |
|---|---|
| LibraryHeader | New Session opens SessionSetup. Settings opens Settings. |
| SessionSearch and SessionRows | Show name, date, input mode, recording and revision state, generation revision, and warning summary. Selecting a session opens SessionReview with recording stopped. |
| RecoveryNotice | Show the unfinished session count and open RecoveryPanel. Deferring recovery preserves the originals. |
| LibraryStatus and LocalStorageSummary | Show loading, read failures with retry, storage location, and space used. |

An empty library shows **Choose a screen and record your first question** with a New Session button. Read failure has a separate state. Stack search and settings controls in narrow windows and move secondary metadata to a second line within each row. Support keyboard navigation, opening, and clearing search. Include the row's overall state in its VoiceOver name.

<a id="sessionsetup"></a>
## 3. SessionSetup

Confirm the first Scene, intent mode, and microphone use. Enter through New Session in SessionLibrary. Recording is still stopped, and Start requires one prepared Scene. [Navigation](navigation.md#start), [FR-01, FR-02, FR-05, FR-09](system-design-to-be.md#requirements).

```layout
Screen V SetupHeader, SetupBody, SetupStatus, SetupActions
SetupHeader > SessionName, DraftBadge
SetupBody > CaptureSetup : 55, InputSetup : 45
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions > SaveDraftButton, CloseSetupButton, StartButton
```

In a narrow window, stack `InputSetup` below `CaptureSetup`.

```layout
Screen V SetupHeader, CaptureSetup, InputSetup, SetupStatus, SetupActions
SetupHeader V SessionName, DraftBadge
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions V StartButton, SaveDraftButton, CloseSetupButton
```

| Region | Display, input, and result |
|---|---|
| CaptureType and SelectCaptureButton | Choose a window, display, or region on one display. Open the OS CapturePicker or RegionOverlay. Capture metadata is fixed at capture time. |
| InitialScenePreview and CaptureMetadata | Show the immutable image, source name and type, actual capture time, dimensions, scale, and region. Mark **Captured Before Recording**. Preserve the draft if reselection fails. |
| IntentMode and SessionGoal | Explicit question, change request, or mixed mode and an optional overall goal. Intent can be entered as text without transcription. |
| MicrophoneChoice and MicrophoneStatus | Enable or disable the microphone, choose a device, and check permission and level. Show **Text Only** when disabled. On error, offer explicit choice to start without a microphone. |
| TranscriptionChoice | Show availability, configuration, permission, and local model status for optional transcription. Show microphone recording availability separately. |
| SetupStatus and StartButton | Show capture, configuration, and recording readiness and failure reasons. Disable Start without the first Scene and prevent duplicate starts during Starting. |

Canceling the first capture restores Setup and the existing draft. If capture permission is denied, offer Settings and Retry below the picker controls. Allow the OS picker after a preflight failure and base Start availability on an actual verified capture. If microphone permission is denied, show alternative input beneath the microphone controls. Commit any Korean IME composition in the name or goal before Start. Show the actual startup stage, such as **Preparing Microphone**. Recording state begins at successful start. Preserve draft and input on failure.

<a id="recordingtoolbar"></a>
## 4. RecordingToolbar

Check recording state, target, and important commands while viewing another app. The toolbar appears in Starting and stays through Recording and Paused. SessionReview takes over after stopping. Closing the window hides it. Stop explicitly ends recording. [Navigation](navigation.md#recording), [FR-02, FR-03, FR-09](system-design-to-be.md#requirements).

```layout
Screen V ToolbarMain, ToolbarStatus
ToolbarMain > RecordingState, SessionClock, ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, PauseResumeButton, ReviewButton, StopButton
ToolbarStatus > OperationMessage, SaveState, FocusSummary
```

If horizontal space is limited, wrap commands into two rows and keep Stop, status, and Pause/Resume visible.

```layout
Screen V EssentialControls, SecondaryControls, ToolbarStatus
EssentialControls > RecordingState, SessionClock, PauseResumeButton, StopButton
SecondaryControls > ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, ReviewButton
ToolbarStatus V OperationMessage, SaveState, FocusSummary
```

| Region | Display, input, and result |
|---|---|
| RecordingState and SessionClock | Show Starting, Recording, or Paused, session elapsed time, and actual audio status. Elapsed time includes pauses. Identify gaps in collection or recording separately. |
| ActiveSceneLabel | Show **Recording Target Sxx: Title**, independent of the frontmost OS window or viewed scene. Activating the label raises the corresponding SceneCanvas. |
| MicrophoneIndicator | Show Off, Recording, Paused, or Device Error. Distinguish a low input level from permission failure. |
| CaptureButton, CanvasButton, and NoteButton | Choose a new Scene, open the canvas, or open notes. During source-app interaction, CanvasButton shows **Resume Scene Explanation** and closes the InteractionInterval on activation. Prevent duplicate capture requests. Opening auxiliary windows preserves the target. |
| PauseResumeButton and ReviewButton | Pause/Resume updates the state. Review Recording opens read-only Review after a successful pause. |
| StopButton | **Stop and Generate Prompt**. Show accepted input save and finalization progress, and prevent duplicate commands. |
| SaveState and FocusSummary | Show Saving, Saved, or Save Failed, plus the active focus target and its release control. Warn when focus has been active a long time. |

While Paused, disable capture, new annotations, and notes with a reason. During Pause or Stop, block new gestures and captures while accepting the current editor's composition response. If blocked, show **Could not stop. Commit or cancel your input and try again**, and keep the draft and actual recording state. Device loss or sleep stops new samples, opens a gap, closes the target, and pauses pending explicit Resume. If composition blocks the pause transition, show **Audio Stopped** or **Audio Failed** separately from actual session state and clear the recording badge. Global shortcuts leave IME composition intact. Esc during capture selection cancels the selection only. Make the toolbar keyboard-accessible and provide a keyboard alternative to dragging it.

<a id="capturepicker"></a>
## 5. CapturePicker

This is the OS window and display picker, entered from Setup or when adding a capture during recording. The block illustrates the app contract around the OS picker. Appearance and cancellation can vary by OS version and permission state. [Navigation](navigation.md#start), [FR-01, FR-09](system-design-to-be.md#requirements).

```layout
Screen V OSSelectionSurface
OSSelectionSurface V OSManagedTargetChoices, OSManagedSelectionActions
```

`OSManagedTargetChoices` represents the windows and displays offered by the OS. `OSManagedSelectionActions` represents confirmation and cancellation. The OS controls fields, shortcuts, and sorting. Choose app-specific capture modes and region options in SessionSetup or RecordingToolbar.

Hide auxiliary windows before selection and restore them after completion or cancellation. Change the Scene only after capture and save succeed. Show permission denial, protected content, missing windows, and display changes in the calling screen while preserving the prior Scene. In region mode, open RegionOverlay after display selection. Verify OS picker behavior with VoiceOver, the keyboard, and multiple displays on supported OS versions.

<a id="regionoverlay"></a>
## 6. RegionOverlay

Confirm a static region on a single display. The overlay opens on that display after selection. Regions must fit within one display. [Navigation](navigation.md#recording), [FR-01](system-design-to-be.md#requirements).

```layout
Screen V RegionInstructions, DisplaySelectionSurface, RegionActions
RegionInstructions > SelectedDisplayName, SelectionHint
RegionActions > RegionDimensions, KeyboardAdjustHint, CancelRegionButton, ConfirmRegionButton
```

| Region | Display, input, and result |
|---|---|
| DisplaySelectionSurface | Overlay a selection frame on the dimmed display. Drag to create or adjust it and use handles to resize. Constrain it to the display and store its coordinates and size in metadata. |
| RegionDimensions and KeyboardAdjustHint | Show readable dimensions and scale. Offer keyboard movement and resizing, and numeric input when needed. |
| RegionActions | Enable confirmation only for valid regions. Enter and Esc confirm or cancel outside text composition. Exclude the selection overlay from the final capture. |

Initially show **Select a Region**. If the region is too small, the display disconnects, or its scale changes, block confirmation and offer reselection or cancellation. Canceling restores the previous activeScene and window layout. With large text or a small display, tool descriptions may collapse, while Confirm, Cancel, and dimensions stay visible. Verify precise keyboard selection and overlay exclusion during implementation.

<a id="scenecanvas"></a>
## 7. SceneCanvas

Inspect the original capture, time-aligned annotations and focus, and post-recording revisions. Open from the toolbar or a Scene selection in SessionReview. NotePanel can stay open alongside it. Annotations are displayed on a separate layer above the original image. [Navigation](navigation.md#recording), [FR-01, FR-03, FR-06](system-design-to-be.md#requirements).

```layout
Screen V CanvasHeader, CanvasBody, CanvasFooter
CanvasHeader > InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
CanvasBody > SceneRail : 18, CanvasWorkArea : 64, AnnotationInspector : 18
SceneRail V SceneThumbnails, SceneSelectionStatus
CanvasWorkArea V AnnotationTools, SceneViewport, CanvasMessage
AnnotationInspector V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

In narrow layouts, move SceneRail to a selection strip at the top and make the inspector expandable.

```layout
Screen V CanvasHeader, SceneStrip, AnnotationTools, SceneViewport, InspectorDisclosure, CanvasMessage, CanvasFooter
CanvasHeader V InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
SceneStrip > PreviousSceneButton, SceneSelector, NextSceneButton
InspectorDisclosure V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

| Region | Display, input, and result |
|---|---|
| InspectedSceneTitle and ActiveSceneBadge | Show inspected Sxx and recording target Syy together, with a warning when they differ. The explicit target switch is available while recording. Viewing earlier scenes leaves the target intact. |
| CanvasModeBadge | **Recording Input**, **Paused: Read-Only**, or **Post-Recording Revision**. Distinguish later revisions from original timeline input. |
| InteractSourceButton | **Interact with Source App** hides the canvas and starts InteractionInterval. **Resume Scene Explanation** in the toolbar closes it. Speech crossing the interval requires review before assignment to an existing target. Opening notes leaves the mode intact. |
| AnnotationTools | Selection, pen, arrow, rectangle, text, and pointing. Identify color, width, and current tool in text. Commit changes through CommitInput. |
| SceneViewport | Separate source coordinates and size from display size. Zoom and pan preserve stored coordinates. Show transparent annotation and focus layers over the original. |
| AnnotationInspector and ObjectList | Show annotation ID, type, linked time, content, and selection state. Offer keyboard selection, deletion, and correction, plus a text alternative. Use traceable corrections for original annotations. |
| FocusControls | Show current point, target, elapsed time, and explicit release. Warn about long-held focus. Close focus on target change, deletion of the selected annotation, Pause, or Stop. Resume starts with no focused target. |
| CanvasTime and SceneNoteButton | Show inspection time and recording time separately. Opening notes for this Scene sets only the NotePanel target. |

While capture is pending, show progress over the current image. On failure, keep the current Scene and input. For a missing image, show Scene metadata, a missing-asset message, a recheck action, and a path to Review. Starting requires a Scene, but damaged recovered sessions can open in this error state.

The text tool shows provisional text during composition and saves the event after commitment. Esc cancels provisional shapes or input and leaves recording running. Pause or Stop during drawing waits for accepted provisional input to be committed or canceled. Tool shortcuts require canvas focus and remain inactive while typing in NotePanel. VoiceOver provides the Scene name, capture metadata, annotation list, and focus location. Offer a text description when coordinates alone would be unclear.

<a id="notepanel"></a>
## 8. NotePanel

Enter meaning alongside or in place of speech and add information after recording. Open from the toolbar, canvas, or review. Raising this window leaves the recording target intact. [Navigation](navigation.md#recording), [FR-04, FR-06](system-design-to-be.md#requirements).

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
| NoteModeBadge | Identify recording input, paused read-only mode, or post-recording revision. |
| TargetScopeSelector and ExplicitSceneSelector | Choose the whole session or a specific Sxx under **Note Target**. Commit or retain drafts before changing the target. Existing notes keep their original links unless explicitly changed. |
| NoteIntent | Choose question, change request, or context. Show the session default and let each note specify its actual intent. |
| NoteTextInput | Support multiline Korean text. Enter inserts a newline and Save is separate. Placeholder examples stay out of saved data. |
| SavedNotes | Show time, target, intent, and recorded or postrecord state. Select a note to inspect or make allowed corrections. Recording target changes leave the note selection intact. |
| NoteStatus and SaveNoteButton | Show saving, completion, failure, empty-content validation, and shortcut guidance. Clear the draft after confirmed success. |

In text-only sessions, open this window as the first input surface so the user can record a question without a microphone. An empty list shows the target and an example. While Paused, disable the editor, save, and target controls while allowing users to read saved notes. Post-recording revisions save their badge, creation time, and target, and mark the prompt stale.

Save shortcuts leave Korean IME composition intact. Save after composition, and indicate unsaved drafts before hiding the window, changing target, or stopping. Show errors and retry in NoteStatus. Stack TargetScope and scene selection in narrow windows. Keep input target and save status visible when history is collapsed. VoiceOver announces save success once without repeating the input.

<a id="sessionreview"></a>
## 9. SessionReview

Review originals, timing, and unresolved references, and add post-recording revisions. Enter from paused review, automatic generation after Stop, opening a saved session, or completed recovery. [Navigation](navigation.md#pause-review), [FR-05, FR-06, FR-08](system-design-to-be.md#requirements).

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

In narrow windows, show evidence first and stack unresolved items and transcripts below.

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
| ReviewStateBanner | Show paused read-only state, revision availability, recovered data and gaps, compilation and save errors, or awaiting_input_review. Show artifact_status and intent_status separately so evidence generation and automatic question generation have distinct outcomes. |
| IntentModeSelector and RevisionState | Show question, request, or mixed mode and input and prompt revisions. Mode changes after recording count as revisions and require regeneration. |
| ReviewSceneRows and SelectedScenePreview | Show Scene ID, capture time, source metadata, and file availability. Selecting a row inspects the Scene and leaves activeScene intact. |
| AudioPlayback | Show waveform or intervals, playback, speed, gaps, and microphone-off state. Playback leaves recording stopped. Provide keyboard controls and textual times. |
| EventContext and TimelineTracks | Align Scene changes, annotations, focus, notes, and audio. Show SelectionInterval, InteractionInterval, their outcomes, and unresolved or candidate links for overlapping speech. Show pause boundaries and accepted order, with separate wall and session times. |
| UnresolvedItems | Show actual ambiguous references such as “here,” unlinked speech or scenes, failed transcription, and low-confidence intervals. Selecting an item shows its time, Scene, and evidence. |
| TranscriptEditor and CorrectionActions | Show absent, pending, failed, partial, or complete transcripts. Show the original and outcomes of manual transcription, correction, or leaving unconfirmed. Review masking of derived delivery images and remaining text quotes. Require evidence for speech content. |
| AddCorrectionButton | Open NotePanel or SceneCanvas in revision mode after recording. Preserve prior records and advance the revision. |
| ExternalInputReview | Show the actual provider, stage, and transmission scope. For external ASR, play selected audio and excluded intervals. For interpretation, show reviewed text and masked frames. Audio exclusion is separate from image masking. |
| ReviewedRevisionStatus and RunReviewedExternalButton | **Process Reviewed Materials Externally** uses the pinned ReviewInputRevision and scope hash. Changes after review require a new review. Provider configuration alone does not upload data. |
| GeneratePromptButton and OpenPromptButton | Show automatic generation progress after stopping and allow regeneration after revisions. Only the current revision is shown as latest. |

While Paused, only audio playback, timeline browsing, and Scene inspection are available. Disable revisions and mode changes, and offer **Revise After Recording** or Resume. After stopping, notes, scenes, and annotations can support generation without audio or a transcript. When intent evidence is insufficient, show **Insufficient Input: Automatic Question Generation Incomplete** and offer revision. Basic evidence output can be viewed as partial.

Let users correct, confirm, or leave items unresolved. Keep unconfirmed items visibly unconfirmed. If transcription finishes for the same snapshot and review revision, proceed to the next stage in that generation. Edits require generation on the new revision and make old results stale. If automatic generation fails, keep original materials available and offer errors, retry, and text additions. Screen readers can use an ordered event list to access the same evidence as the visual timeline.

After Stop, automatic generation runs local base generation and configured local engines. External stages await review. Show readable intent_status values for generated, user_authored, unavailable, and insufficient. Missing or failed engines yield unavailable or partial. Missing question evidence yields insufficient or partial. Log the actual scope and history of transmissions after review. Inform users that later revisions cannot recall prior transmissions.

<a id="promptpreview"></a>
## 10. PromptPreview

Inspect prompt text and actual attachments together. Enter from a ready or partial result for the current revision or by opening a saved generation. AI delivery happens outside this screen. [Navigation](navigation.md#delivery), [FR-05, FR-06, FR-07, FR-09](system-design-to-be.md#requirements).

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

In narrow windows, stack AttachmentPane below PromptTextPane. Wrap prompt text and support text selection without horizontal scrolling. Keep DeliveryNotice and copy and export status near the text even when the asset list moves down.

| Region | Display, input, and result |
|---|---|
| PromptRevision and PromptStateBanner | Show current input and generation revisions, current or regeneration-required status, warnings, and failures. Label old generations as stale. |
| PromptSections and PromptTextView | The selected DeliveryProjection supplies intent, questions or change requests, Scene references, timing evidence, and unconfirmed text. Apply the same masking and metadata exclusions as copy and export. Return to Review to edit. |
| DeliveryProfile | Choose the default **Delivery** or explicit **Include Originals**. Delivery includes composited frames, prompt, context, and evidence. It excludes original audio, source images, PID, window titles, and local absolute paths. Include Originals warns that it contains unmasked material. |
| ActualAssetRows | Show the selected profile's actual images, file IDs, relative references, and inclusion or exclusion reasons. Show target ID, effective time, and highlighting for point or rectangle focus on composite images. Identify original assets as local evidence and placeholders as placeholders. |
| UnresolvedReferenceRows | Show counts and targets of missing and unconfirmed references. Selecting a row returns to the evidence in Review. |
| DeliveryNotice | **Copy includes text only. Attach images and audio separately in your AI tool.** |
| CopyPromptButton | Copy only text allowed by the current DeliveryProjection shared with Preview and export. Announce **Prompt Text Copied**. |
| RevealAssetsButton and ExportBundleButton | Reveal actual assets in Finder and create a verified bundle. Use the delivery contract for relative paths and manifests. |
| ExportResult | Show progress, actual successful path, included files, and errors. Enable Reveal in Finder after validating the path. |

If edits, manual transcription, mode changes, or revisions invalidate a result, show **Regeneration Required** at the top and near the actions. Disable copying or exporting the prompt as current until generation succeeds for the current revision. For missing assets, write failure, or low space, identify affected files and offer retry or another location. Export success reports local files only. VoiceOver announces copy success or the bundle path once without reading the full prompt automatically.

<a id="settings"></a>
## 11. Settings

Check local input, permissions, transcription, storage, and shortcuts. Open from Library, Setup, or Toolbar and return there on close. [Navigation](navigation.md#recovery-settings), [FR-09](system-design-to-be.md#requirements).

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
| PermissionsSection | Show actual OS permissions for screen capture and microphone and link to System Settings. The OS grants permissions. |
| AudioSection | Show microphone device, defaults, and testing. Clearly indicate unavailable devices and describe text-only input. |
| TranscriptionSection | Optional local transcription settings, model, language, unavailable state, download requirement, and failures. Configure external transcription and interpretation providers separately, with possible transmission scope, key management, and policies. Actual uploads require input review and explicit action in Review. |
| StorageSection | Show local storage path, usage, default export destination, and read/write checks. Explain the contract and effect of moving existing sessions separately. |
| KeyboardSection | Show shortcuts, conflicts, and activation scope. Explain IME and text input protection and keyboard access. |
| CurrentSessionNotice and AvailabilityStatus | Show applied to current session or **Starting Next Session**. Disable settings that cannot change while recording and show the reason. |

In narrow windows, replace the left list with a selection menu above vertically arranged settings. Recheck actual permissions after returning from System Settings and report denied permissions accurately. Show recording and transcription availability separately. Screen capture plus notes remains available without microphone permission. Show save or device-list failures and retry actions in the affected section.

<a id="recoverypanel"></a>
## 12. RecoveryPanel

Recover verified local data from unfinished sessions and inspect missing intervals. Enter from the startup guard or the library notice. Successful recovery opens Review with recording stopped. [Navigation](navigation.md#recovery-settings), [FR-08](system-design-to-be.md#requirements).

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
| RecoverySessionRows | Show session name, last save time, abnormal exit flag, and recoverability. Support keyboard selection. |
| LastVerifiedPosition and AvailableAssets | Show the last verified position and actual availability of scenes, events, and audio. |
| MissingRanges and RecoveryConsequences | Show missing or incomplete intervals and what can be read or revised after recovery. Report loss based on verification. |
| RecoveryStatus | Show inspection and recovery progress, failure reason, and retry. A failure in one session leaves other sessions selectable. |
| RecoverSelectedButton and LaterButton | Recover the selected session or return to Library and preserve originals. Originals are retained after recovery failure. |

If recoverable items disappear, show an empty state and a path back to Library. Report inspection failures separately from empty results. Stack details below the session list in narrow windows. Announce progress and completion, and list missing items in order. Regenerate the prompt for the actual recovered revision and mark earlier generations stale.

## 13. Static review and remaining verification

Each `layout` is a single-root tree. Check for duplicate containers, multiple parents, cycles, and excessive horizontal weights within each block. Cross-check all 11 navigation screens against the 11 layout identifiers. This design specifies intended interactions, errors, empty states, permissions, keyboard access, and narrow-window behavior. Runtime responsive and accessibility verification is pending.

Prioritize first capture permission denial, a window disappearing during capture, text-only startup, microphone start failure and device loss, Stop during Korean IME composition, paused read-only review, viewing earlier scenes and explicitly switching targets, long-held focus, unavailable or failed transcription, stale compilation finishing during a revision, missing assets and export failure, and crash recovery. Use the acceptance criteria in the [verification plan](system-design-to-be.md#verification).

Static review on 2026-10-31 checked 15 layout blocks for one root, multiple parents, cycles, duplicate containers, and horizontal weights, and checked all 11 screen anchors. It found zero structural errors. Link and anchor checks across both UI documents reported FAIL 0 and WARN 0. Renderer conversion, actual window sizes, VoiceOver, and IME behavior were outside that check.
