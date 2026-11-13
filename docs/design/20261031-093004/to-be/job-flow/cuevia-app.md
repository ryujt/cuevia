# Cuevia internal app job flow

This flow covers modules and detailed operations in a single app service. Parent coordinators connect worker results to subsequent calls. Workers do not call peers directly. The [core contracts](../system-design-to-be.md#contracts) define public contracts, errors, and fields. Return branches represent alternative paths. Asynchronous callbacks are independent entry points.

## 1. Prepare the first capture, start recording, and add scenes

### 1.1 Prepare before recording

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

- Close and verify the draft's original image file before Start. In voice mode, map the host anchor returned by Start to the common t0 and activate the first Scene at t=0. In microphone-free mode, skip AudioRecorder.Start and use SessionClock.BeginEpoch.
- AudioRecorder.Start registers the buffer callback and returns ready once preparation completes. Trim initial buffers before the public recording start at the start boundary. The UI shows starting until ready.
- If BeginSession fails, SessionCoordinator calls AudioRecorder.Stop and keeps the draft and closed file for recovery. Show recording in the UI only after the DB start succeeds.
- Mark the first capture pre_recording. Its capture time can precede t0.

### 1.2 Add captures during recording

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

- Start a SelectionInterval for each request token and preserve it through commit, cancel, or failure. On success, close the interval and activate the new Scene in one AppendSceneAndActivate transaction. On cancel or failure, save the ending status and keep the existing Scene and Focus.
- Speech crossing a SelectionInterval stays unresolved or linked to candidates. Explicit assignment requires user confirmation even if the explicit targets on either side appear identical.
- The token includes the request ID and state revision. Pause, Stop, another selection request, and session switches cancel the pending token. At completion, recheck the current state and token. Discard results as stale if recording has stopped or the request is outdated.
- Report DB commit failure separately from capture failure. Stop recording and offer recovery because persistence of the original is uncertain.

### 1.3 Continuous audio and interruptions

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

- Start connects OnAnchor, OnChunkClosed, and OnDiscontinuity, which recur during recording. The buffer callback copies only the required data into a bounded queue inside Recorder. A non-real-time writer closes short chunks. Keep the DB operations shown in the diagram off the real-time callback.
- The Pause/Finalize barrier blocks new gestures and captures, cancels the pending capture token and SelectionInterval, and asks the current editor to end IME composition. Accept the existing editor's committed or cancelled response, commit the note or stroke and close focus, then proceed to ready and drain audio.
- If the editor returns blocked, preserve the draft and actual recording or paused state and report that the transition did not complete. Retry after committing or canceling composition. A timeout or forced window close cannot save unconfirmed composition. Closing the image view alone does not pause or finalize recording.
- Stop disables the registered audio callback and waits for the current chunk to close and for results to drain. Seal may mark parts that are damaged or fail to finish within the limit as partial. Skip Recorder calls in microphone-free mode.
- A microphone interruption or sleep stops new samples. Open a gap from the last valid sample, close the target, and move to paused through the input barrier where possible. If the editor is blocked, preserve the draft and actual session state and show audio_status=stopped/failed. Require an explicit Resume.
- Resume accepts input after establishing a new epoch and anchor and committing to the DB. Register late close and anchor drain results for pre-pause buffers and chunks as media_revision. Accept no new samples or edits while paused. Discard stale captures and callbacks carrying another session's token.

KeepActualStateAndDraft is the shared fallback for an editor returning blocked during either Pause or Finalize. Each command uses that path independently, and retries require a new request.

## 2. Point, annotate, take notes, and undo

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

- ValidateInput checks session state, expected revision, scene and target, coordinates, and IME commitment. Define CommitInput as a typed command and distinguish committing an existing stroke from starting a new stroke.
- SetFocus and SceneActivated follow the same serialization, timestamp, and DB atomicity rules. Moving to another Scene with an annotation selected closes the previous Focus and activates the new Scene in one transaction. Deleting the selected target annotation closes its open Focus in the same transaction.
- Stage stroke points in short batches and commit at pointer-up. Pause or Stop explicitly commits or cancels only the points received. Show strokes left unfinished by an abnormal exit in the recovery summary.
- NoteRevision stores the anchor separately from edit time. A Revise after recording creates a separate post_recording revision and preserves the original event.
- Undo and redo append semantic operations. Reconstruct earlier images at a selected event sequence cutoff.

## 3. Automatic material generation, partial failure, and verification

### 3.1 From snapshot to transcript, evidence, and prompt

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

- Compile specifies an immutable snapshot and review revision. FrameRenderer and Interpreter use those inputs without reading live editor state or private DB data.
- The branches select the transcription and interpretation policies. Notes can supply questions or requests without audio. Mark unavailable required audio intervals as missing.
- Enabled means the stage can run locally or external input review is complete for the pinned ReviewInputRevision and scope hash. Immediately after Stop, run only local material generation and local engines. Store external stages as awaiting_input_review and request review of exact inputs and exclusions. Provider configuration alone cannot upload audio or captures.
- When the user requests external processing from SessionReview, send only the pinned inputs for that stage. External ASR uses selected audio and excluded intervals. External interpretation uses transcripts, notes, and masked frames. Later edits create a new revision and cannot recall inputs already sent.
- Store each stage's input hash, version, and result in GenerationStore. Retry from the failed stage and reuse successful external calls where applicable.
- Track artifact_status and intent_status independently. Verified automatic interpretation is generated and explicitly authored final items are user_authored. A missing or failed engine yields unavailable or partial. Missing intent input yields insufficient or partial. Base material output alone does not indicate that automatic question generation completed.
- Publish checks the latest revision. Keep an older job that finishes during user editing in history without replacing the current PromptPreview.
- Cancel invalidates the job token and keeps late responses out of the current generation. The external provider may continue processing.

The branches for successful, partial, failed, and skipped transcription converge at Bind, which processes each input once. ValidateGeneration joins skipped interpretation, successful validation, and the base prompt fallback. UseBasePrompt handles both failed interpretation and rejected validation. SaveInputReviewPending records pending review for the selected external stage.

### 3.2 Frame selection and ambiguous targets

```jobflow
orchestrator: EvidenceBinder
Object: EvidenceBinder, IntervalResolver, ReferenceResolver
EvidenceBinder.Bind --> IntervalResolver.SplitAtContextBoundaries
IntervalResolver.SplitAtContextBoundaries.result --> ReferenceResolver.ResolveCandidates
ReferenceResolver.ResolveCandidates.explicit --> EvidenceBinder.LinkExplicit
ReferenceResolver.ResolveCandidates.multiple --> EvidenceBinder.MarkNeedsReview
ReferenceResolver.ResolveCandidates.missing --> EvidenceBinder.KeepUnbound
```

- EvidenceBinder uses the public inputs and outputs shown in the parent diagram. This diagram expands an internal pure helper within the same service.
- Compare the entire speech interval with Scene and Focus boundaries. Without word timestamps, keep candidates without dividing the speech by inferred meaning.
- Speech crossing a capture SelectionInterval or an original-app InteractionInterval stays unresolved or linked to candidates, even on cancellation or failure. It cannot be assigned automatically to either Scene or target. Explicit canvas hiding and resuming explanation delimit original-app interaction. Opening the note window leaves that mode unchanged.
- FrameRenderer replays each link's `(scene_id, event_seq)` state so earlier speech is shown with the annotations that existed then. For point or rectangle Focus, include target ID, effective time, and a visual highlight in the exported frame.
- Keep unresolved links and interpreter guesses distinct from explicit user assignments. The user can review and correct them or include them as unconfirmed.

## 4. Crash recovery and export

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

- Check DB recovery on a copy. List open chunks, temporary and orphan candidates, and the extent of loss. Preserve the original data.
- Recovered sessions are interrupted. Continuing requires an explicit request to SessionCoordinator, which starts a new Session with parent_session_id.
- Compile only the parts with enough material to generate frames or questions. Items requiring missing evidence remain unresolved.

The verified path and the path that records missing assets converge at SealInterrupted to seal one recovery result.

### 4.2 Share the actual files

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

- Load verifies the current revision, profile, and review state. Sharing pins that generation so later edits cannot change the files being exported.
- Preview, copy, and export use the same DeliveryProjection from DeliveryExporter.BuildProjection. Profile metadata exclusions and text and image masking also apply when copying.
- The originals-included profile loads a pinned source snapshot and asset list referenced by GenerationStore. The exporter uses that contract for access to source data.
- Verify checks file existence, hashes, relative references, profile exclusions, and path traversal. Only published output is returned as a successful bundle.
- The UI distinguishes copy completion from export completion. Confirmation of attachment in the recipient AI is outside this public contract.
