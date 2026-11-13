# Cuevia App Internal Job Flow

This is the module-level, detailed flow of the single app service. A higher-level orchestrator connects Worker results to the next call, and a Worker never calls a sibling Worker directly. The [core contracts](../system-design-to-be.md#contracts) are the source of truth for public contracts, errors, and data fields. Return-value branches in a block are alternative paths, and receipt of an asynchronous callback is an independent entry point.

## 1. First capture preparation, recording start, and new scenes

### 1.1 Preparation before recording

```jobflow
orchestrator: SessionCoordinator
Object: SessionCoordinator, CaptureGateway, SessionStore, AudioRecorder, SessionClock
SessionCoordinator.PrepareDraft --> CaptureGateway.SelectAndCapture
CaptureGateway.SelectAndCapture.ready --> SessionStore.SaveDraftAsset
CaptureGateway.SelectAndCapture.cancelled --> SessionCoordinator.KeepSetup
CaptureGateway.SelectAndCapture.failed --> SessionCoordinator.ShowCaptureError
SessionStore.SaveDraftAsset.result --> SessionCoordinator.DraftReady
SessionCoordinator.Start --> AudioRecorder.Start
AudioRecorder.Start.ready --> SessionClock.BeginEpoch
AudioRecorder.Start.failed --> SessionCoordinator.ShowAudioSetupRequired
SessionClock.BeginEpoch.result --> SessionStore.BeginSession
SessionStore.BeginSession.result --> SessionCoordinator.RecordingReady
```

- Before Start, the draft source image file is closed and verified. In voice mode, the host anchor of a successful Start is mapped to the common t0, and the first Scene is activated from t=0. Mode without a microphone skips AudioRecorder.Start and proceeds to SessionClock.BeginEpoch.
- AudioRecorder.Start registers the buffer callback connection and returns ready once start preparation is complete. Initial buffers from before the published recording start are trimmed at the start boundary, and the UI shows starting until ready.
- If BeginSession fails, SessionCoordinator calls AudioRecorder.Stop and keeps the draft and closed files as recovery targets. The UI is not switched to recording before the DB start succeeds.
- The first capture is pre_recording material; it does not claim the image was taken exactly at t0.

### 1.2 Additional captures during recording

```jobflow
orchestrator: SessionCoordinator
Object: SessionCoordinator, CaptureGateway, SessionClock, SessionStore
SessionCoordinator.Capture --> SessionCoordinator.BeginSelectionAttempt
SessionCoordinator.BeginSelectionAttempt --> SessionStore.AppendSelectionStarted
SessionStore.AppendSelectionStarted.result --> CaptureGateway.SelectAndCapture
CaptureGateway.SelectAndCapture.ready --> SessionCoordinator.ValidateCaptureToken
CaptureGateway.SelectAndCapture.cancelled --> SessionStore.CloseSelectionCancelled
SessionStore.CloseSelectionCancelled.result --> SessionCoordinator.KeepCurrentScene
CaptureGateway.SelectAndCapture.failed --> SessionStore.CloseSelectionFailed
SessionStore.CloseSelectionFailed.result --> SessionCoordinator.ShowCaptureError
SessionCoordinator.ValidateCaptureToken.valid --> SessionClock.Stamp
SessionCoordinator.ValidateCaptureToken.stale --> CaptureGateway.DiscardTemporary
SessionClock.Stamp.result --> SessionStore.AppendSceneAndActivate
SessionStore.AppendSceneAndActivate.committed --> SessionCoordinator.PublishActiveScene
SessionStore.AppendSceneAndActivate.failed --> SessionCoordinator.InterruptRecording
```

- Each selection attempt starts a SelectionInterval for the request token and keeps it through commit/cancel/fail. On success, the end of the selection interval and activation of the new Scene are finalized in a single AppendSceneAndActivate transaction. Cancellation or failure stores only the corresponding terminal state and keeps the existing Scene and Focus.
- Utterances that overlap a SelectionInterval are not automatically linked as explicit to either the previous or new Scene; they remain unresolved/candidates. Even if the explicit target before and after the selection appears identical, it is not finalized without user confirmation.
- The token includes the request ID and state revision. Pause, Stop, the next selection request, and session switching cancel any in-flight token. On completion, the current state and token are rechecked, and if the session is no longer recording or the request is outdated, the result is discarded as stale.
- A DB commit failure is distinguished from a failure of the capture itself. Because source persistence is no longer guaranteed, recording is interrupted and the user is guided to recovery.

### 1.3 Continuous audio input and interruption

```jobflow
orchestrator: SessionCoordinator
Object: SessionCoordinator, AudioRecorder, SessionClock, SessionStore
AudioRecorder.OnAnchor --> SessionClock.MapAudioHostTime
SessionClock.MapAudioHostTime.result --> SessionStore.AppendMediaAnchor
AudioRecorder.OnChunkClosed --> SessionStore.RegisterAudioChunk
AudioRecorder.OnDiscontinuity --> SessionCoordinator.HandleAudioGap
SessionCoordinator.HandleAudioGap --> SessionStore.AppendGapAndClearFocus
SessionStore.AppendGapAndClearFocus.result --> SessionCoordinator.PausedAfterAudioGap
SessionCoordinator.Pause --> SessionCoordinator.PreparePauseBarrier
SessionCoordinator.PreparePauseBarrier.ready --> AudioRecorder.Pause
SessionCoordinator.PreparePauseBarrier.blocked --> SessionCoordinator.KeepActualStateAndDraft
AudioRecorder.Pause.result --> SessionStore.CommitPauseBoundary
SessionStore.CommitPauseBoundary.result --> SessionCoordinator.Paused
SessionCoordinator.Resume --> AudioRecorder.Resume
AudioRecorder.Resume.ready --> SessionClock.BeginEpoch
AudioRecorder.Resume.failed --> SessionCoordinator.ShowResumeError
SessionClock.BeginEpoch.result --> SessionStore.CommitResumeBoundary
SessionCoordinator.Finalize --> SessionCoordinator.PrepareFinalizeBarrier
SessionCoordinator.PrepareFinalizeBarrier.ready --> AudioRecorder.Stop
SessionCoordinator.PrepareFinalizeBarrier.blocked --> SessionCoordinator.KeepActualStateAndDraft
AudioRecorder.Stop.result --> SessionStore.Seal
SessionStore.Seal.sealed --> SessionCoordinator.SourceSaved
SessionStore.Seal.partial --> SessionCoordinator.SourceInterrupted
```

- OnAnchor/OnChunkClosed/OnDiscontinuity are connected at Start and fire repeatedly during recording. The buffer callback copies only the necessary data into a bounded queue inside the Recorder, and a non-real-time writer closes short chunks. The DB operations shown in the diagram are never performed inside the real-time callback.
- The Pause/Finalize barrier blocks new gestures and capture intake, cancels pending capture tokens and SelectionIntervals, and then asks the current editor to end IME composition. The existing editor's committed/cancelled response is not blocked; it is committed together with the note/stroke and focus termination, after which the flow proceeds to ready and performs the audio drain.
- If the editor responds blocked, the draft and the actual recording/paused state are kept and the user is told the transition did not complete. The user retries after committing or cancelling; an uncommitted composition is never saved through a timeout or forced window close. Merely closing the image view does not complete a pause or stop.
- Stop halts the registered audio callback and waits for the chunk being written to close and its result to drain. Seal may record parts that could not finish within the time limit, or that are corrupted, as partial. Mode without a microphone skips the Recorder calls.
- A microphone interruption or sleep stops receiving new samples, opens a gap from the last valid sample, closes the target, and transitions to paused through whatever input barrier is possible. If the editor is blocked, the draft and actual session state are preserved but audio_status=stopped/failed is shown. Recording does not resume automatically; an explicit Resume is required.
- Resume re-enables input after a new epoch/anchor is established and the DB commit succeeds. Late close and anchor drain results for buffers/chunks collected before Pause are registered as a media_revision, and no new samples after Pause are added. New edits are rejected while paused, and callbacks from stale captures or tokens of other sessions are discarded.

KeepActualStateAndDraft is the shared alternative path chosen when the editor is blocked during Pause or Finalize. It does not mean the two terminating commands run consecutively or that blocked input is retried automatically.

## 2. Pointing, annotation, notes, and undo

```jobflow
orchestrator: SessionCoordinator
Object: SessionCoordinator, SessionClock, SessionStore
SessionCoordinator.CommitInput --> SessionCoordinator.ValidateInput
SessionCoordinator.ValidateInput.valid --> SessionClock.Stamp
SessionCoordinator.ValidateInput.invalid --> SessionCoordinator.RejectInput
SessionClock.Stamp.result --> SessionStore.AppendInputAndProjection
SessionStore.AppendInputAndProjection.committed --> SessionCoordinator.InputCommitted
SessionStore.AppendInputAndProjection.failed --> SessionCoordinator.InterruptRecording
SessionCoordinator.Revise --> SessionStore.AppendReviewRevision
SessionStore.AppendReviewRevision.result --> SessionCoordinator.InvalidateGeneration
```

- ValidateInput checks the session state, expected revision, scene/target, coordinates, and IME commit state. CommitInput is defined as a typed command and distinguishes committing a stroke already in progress from starting a new stroke.
- SetFocus and SceneActivated follow the same serialization, timing, and DB atomicity rules. Moving to another Scene while an annotation is selected finalizes the end of the previous Focus and activation of the new Scene in one transaction. Deleting the selected target annotation also closes the open Focus in the same transaction.
- Stroke points are recorded provisionally in short batches and committed on pointer-up. Pause/Stop explicitly commits or cancels only the points that have arrived. Incomplete strokes left by an abnormal termination are shown in the recovery summary.
- NoteRevision separates the anchor from the edited time. After recording ends, Revise does not modify source events; it is stored as a separate post_recording revision.
- Undo/redo does not delete events; it appends a new semantic operation. Past images are reproduced from a cut at an event seq.

## 3. Automatic material generation, partial failure, and history verification

### 3.1 snapshot→transcript→evidence→prompt

```jobflow
orchestrator: CompileCoordinator
Object: CompileCoordinator, SessionStore, TranscriptionGateway, EvidenceBinder, FrameRenderer, PromptAssembler, IntentInterpreter, GenerationStore
CompileCoordinator.Compile --> SessionStore.ReadSnapshot
SessionStore.ReadSnapshot.result --> GenerationStore.BeginJob
GenerationStore.BeginJob.result --> CompileCoordinator.SelectTranscriptionPolicy
CompileCoordinator.SelectTranscriptionPolicy.enabled --> TranscriptionGateway.Transcribe
CompileCoordinator.SelectTranscriptionPolicy.disabled --> CompileCoordinator.UseTextOnlyEvidence
CompileCoordinator.UseTextOnlyEvidence --> EvidenceBinder.Bind
TranscriptionGateway.Transcribe.succeeded --> CompileCoordinator.MapTranscript
CompileCoordinator.MapTranscript --> EvidenceBinder.Bind
TranscriptionGateway.Transcribe.partial --> CompileCoordinator.MapAvailableTranscript
CompileCoordinator.MapAvailableTranscript --> EvidenceBinder.Bind
TranscriptionGateway.Transcribe.failed --> CompileCoordinator.RecordMissingSpeech
CompileCoordinator.RecordMissingSpeech --> EvidenceBinder.Bind
CompileCoordinator.SelectTranscriptionPolicy.awaiting_input_review --> GenerationStore.SaveInputReviewPending
EvidenceBinder.Bind.result --> FrameRenderer.Render
FrameRenderer.Render.result --> PromptAssembler.Assemble
PromptAssembler.Assemble.result --> CompileCoordinator.SelectInterpretationPolicy
CompileCoordinator.SelectInterpretationPolicy.enabled --> IntentInterpreter.Interpret
CompileCoordinator.SelectInterpretationPolicy.disabled --> CompileCoordinator.ValidateGeneration
CompileCoordinator.SelectInterpretationPolicy.awaiting_input_review --> GenerationStore.SaveInputReviewPending
IntentInterpreter.Interpret.succeeded --> CompileCoordinator.ValidateInterpretation
IntentInterpreter.Interpret.failed --> CompileCoordinator.UseBasePrompt
CompileCoordinator.ValidateInterpretation.valid --> CompileCoordinator.ValidateGeneration
CompileCoordinator.ValidateInterpretation.invalid --> CompileCoordinator.UseBasePrompt
CompileCoordinator.UseBasePrompt --> CompileCoordinator.ValidateGeneration
CompileCoordinator.ValidateGeneration.valid --> GenerationStore.Publish
CompileCoordinator.ValidateGeneration.invalid --> GenerationStore.SaveFailure
GenerationStore.Publish.result --> CompileCoordinator.NotifyResult
GenerationStore.SaveInputReviewPending.result --> CompileCoordinator.NotifyInputReviewRequired
```

- Compile pins an immutable snapshot/review revision. FrameRenderer/Interpreter never read in-progress editing state or DB internals directly.
- The branches select the corresponding transcription and interpretation policies. Questions and requests can be built from notes even without voice. When required speech could not be obtained, the missing interval is recorded.
- enabled means either local execution is available or external input review has been completed for a pinned ReviewInputRevision and scope hash. Right after Stop, only the local base materials and local engines run; external stages are saved as awaiting_input_review, and the user is asked to review the inputs that would actually be sent and the excluded ranges. Configuration alone never uploads original audio or screens.
- When the user requests external processing using materials reviewed in SessionReview, only the pinned inputs are passed to that stage. The selected original audio and excluded intervals for external ASR are kept distinct from the transcripts, notes, and masked frames for external semantic summarization. Later amendments form a new revision and do not retract inputs already sent.
- Each stage stores its input hash/version and result in GenerationStore. Retries start from the failed stage, and external calls that already succeeded are not repeated unconditionally.
- artifact_status and intent_status are distinct. A verified automatic summary is generated, explicitly authored final items are user_authored, a missing or failed engine is unavailable/partial, and insufficient purpose input is insufficient/partial. Outputting base materials alone is not shown as automatic question authoring being complete.
- Publish is checked against the latest revision. An old job that finishes while the user is revising remains only in history and does not replace the current PromptPreview.
- Cancel does not guarantee that the external provider stops processing. It invalidates the job token so late responses are not applied to the current generation.

The lines converging on Bind from transcription success, partial success, failure, and non-use are a merge of alternative branches that process the same input once. ValidateGeneration is the merge of interpretation not used, verification passed, and base prompt fallback, and UseBasePrompt is the shared alternative path for interpretation failure and verification rejection. SaveInputReviewPending is the merge that stores the review-pending state for the currently selected external stage.

### 3.2 Frame selection and target ambiguity

```jobflow
orchestrator: EvidenceBinder
Object: EvidenceBinder, IntervalResolver, ReferenceResolver
EvidenceBinder.Bind --> IntervalResolver.SplitAtContextBoundaries
IntervalResolver.SplitAtContextBoundaries.result --> ReferenceResolver.ResolveCandidates
ReferenceResolver.ResolveCandidates.explicit --> EvidenceBinder.LinkExplicit
ReferenceResolver.ResolveCandidates.multiple --> EvidenceBinder.MarkNeedsReview
ReferenceResolver.ResolveCandidates.missing --> EvidenceBinder.KeepUnbound
```

- EvidenceBinder's public inputs and outputs are the same as in the higher-level diagram. This details internal pure helpers and is not a separate service.
- The full utterance interval is compared against Scene/Focus change points. Without word-level timestamps, utterances are not arbitrarily split by meaning; candidates are kept instead.
- Utterances that overlap the SelectionInterval of a capture selection or the InteractionInterval of source-app interaction are excluded from automatic explicit links to the previous or new Scene/target regardless of cancellation or failure, and remain candidates or unresolved. Source-app interaction is identified by the explicit hide-canvas/resume-explanation action; opening the note window does not count.
- FrameRenderer replays the state at each link's `(scene_id, event_seq)`. Past utterances are never explained using only the final annotation image. A point/rect Focus includes its target ID, effective time, and visual highlight in the export frame so the recipient can see what was pointed at.
- Unresolved links and interpreter guesses are never confused with the user's explicit designations. The user can correct them after checking, or include them as unconfirmed.

## 4. Recovery from abnormal termination and export

### 4.1 Recovery

```jobflow
orchestrator: SessionStore
Object: SessionStore, DatabaseRecovery, AssetVerifier
SessionStore.Recover --> DatabaseRecovery.ReadCommittedTail
DatabaseRecovery.ReadCommittedTail.readable --> AssetVerifier.VerifyReferencedAssets
DatabaseRecovery.ReadCommittedTail.corrupt --> SessionStore.ReportRecoveryBlocked
AssetVerifier.VerifyReferencedAssets.complete --> SessionStore.SealInterrupted
AssetVerifier.VerifyReferencedAssets.partial --> SessionStore.RecordMissingAssets
SessionStore.RecordMissingAssets --> SessionStore.SealInterrupted
SessionStore.SealInterrupted.result --> SessionStore.Recover.result
```

- DB recovery inspects a copy. Chunks/temp/orphan candidates that were open, and the extent of loss, are listed, and existing source files are never deleted arbitrarily.
- The recovery result is interrupted, and the existing recording is not resumed automatically on the same timeline. SessionCoordinator, on explicit request, starts a new Session with a parent_session_id.
- Only the portions from which frames and questions can be generated may be compiled. Items that require missing evidence remain unresolved.

SealInterrupted after the verification-complete path and after recording missing assets is the merge of alternative branches that seal a single recovery result.

### 4.2 Sharing actual files

```jobflow
orchestrator: DeliveryExporter
Object: DeliveryExporter, GenerationStore, BundleWriter, BundleVerifier
DeliveryExporter.Export --> GenerationStore.Load
GenerationStore.Load.current --> DeliveryExporter.BuildProjection
DeliveryExporter.BuildProjection.result --> BundleWriter.WriteTemporary
GenerationStore.Load.stale --> DeliveryExporter.RejectStale
BundleWriter.WriteTemporary.written --> BundleVerifier.Verify
BundleWriter.WriteTemporary.failed --> DeliveryExporter.ReportWriteFailure
BundleVerifier.Verify.valid --> BundleWriter.Publish
BundleVerifier.Verify.invalid --> DeliveryExporter.ReportInvalidBundle
BundleWriter.Publish.result --> DeliveryExporter.Export.result
```

- Load verifies the current revision/profile and review state. What is shared is the generation pinned at that moment, so later edits do not change files being exported.
- Preview, copy, and export use the same DeliveryProjection from DeliveryExporter.BuildProjection. This ensures per-profile metadata exclusion and text/image masking are not skipped on the copy path.
- A source-included profile obtains the pinned source snapshot and asset list referenced by GenerationStore. The exporter never reads private DB tables directly.
- Verify checks attachment existence, hashes, relative references, per-profile excluded items, and path escapes. Output that failed before publish is never returned as a success folder.
- UI completion indicators for copy and export are distinct. Completion of attachments on the receiving AI's side is outside this public contract.
