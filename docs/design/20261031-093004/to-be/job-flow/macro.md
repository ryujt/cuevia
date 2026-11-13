# Macro Job Flow

This view treats Cuevia as a single system. Internal modules are in the [app internal flow](cuevia-app.md), and contracts are in the [design document](../system-design-to-be.md#contracts). A `message` is a logical request, response, or notification; it does not imply a network or message broker. Success, cancellation, and failure responses to the same request are alternative outcomes.

## 1. Select a screen and record an explanation

```jobflow
scope: Recording collaboration between the user, Cuevia, and macOS
Object: User, Cuevia, MacOS
User.OnPrepare --> Cuevia.message.PrepareRequest
Cuevia.message.PrepareRequest --> MacOS.message.SelectCaptureRequest
MacOS.message.SelectCaptureRequest --> Cuevia.message.CaptureReady
MacOS.message.SelectCaptureRequest --> Cuevia.message.CaptureCancelled
MacOS.message.SelectCaptureRequest --> Cuevia.message.CaptureFailed
Cuevia.message.CaptureReady --> User.message.DraftReady
Cuevia.message.CaptureCancelled --> User.message.SetupUnchanged
Cuevia.message.CaptureFailed --> User.message.CaptureError
User.OnStart --> Cuevia.message.StartRequest
Cuevia.message.StartRequest --> MacOS.message.StartMicrophoneRequest
MacOS.message.StartMicrophoneRequest --> Cuevia.message.MicrophoneReady
MacOS.message.StartMicrophoneRequest --> Cuevia.message.MicrophoneFailed
Cuevia.message.MicrophoneReady --> User.message.RecordingStarted
Cuevia.message.MicrophoneFailed --> User.message.AudioSetupRequired
User.OnDescribe --> Cuevia.message.InputCommand
Cuevia.message.InputCommand --> User.message.InputCommitted
User.OnStop --> Cuevia.message.StopRequest
Cuevia.message.StopRequest --> User.message.SourceSaved
```

- PrepareRequest handles target selection and the first capture. On cancellation or error, recording does not start. Text mode without a microphone is an alternative path in which StartRequest responds directly with RecordingStarted.
- StartMicrophoneRequest includes the permission check and device start. `MicrophoneReady` is the start response; continuous buffer delivery afterwards belongs to the internal flow. A permission failure is never turned into a recording success.
- InputCommand covers pointing, annotation, notes, new captures, and pause/resume commands. Actual persistence is distinguished from transient UI display.
- SourceSaved is the result of sealing the source. Transcription and prompt completion notifications are separate contracts in the next scenario. A save error responds with SourceSaveFailed and moves to recovery of the data already secured.

## 2. Automatic prompt generation after stop, and partial failure

```jobflow
scope: Prompt generation and optional external processing after the source is saved
Object: User, Cuevia, ProcessingProvider
Cuevia.OnSourceSealed --> Cuevia.message.CompileRequest
Cuevia.message.CompileRequest --> User.message.CompileAccepted
Cuevia.OnExternalProcessingNeeded --> User.message.ExternalInputReviewRequired
User.OnReviewedExternalInputApproved --> Cuevia.message.ExternalProcessingRequest
Cuevia.message.ExternalProcessingRequest --> ProcessingProvider.message.ProcessRequest
ProcessingProvider.message.ProcessRequest --> Cuevia.message.ProcessResult
ProcessingProvider.message.ProcessRequest --> Cuevia.message.ProcessFailed
Cuevia.OnGenerationReady --> User.message.PromptReady
Cuevia.OnGenerationPartial --> User.message.PromptPartial
Cuevia.OnGenerationFailed --> User.message.GenerationFailed
User.OnRevise --> Cuevia.message.ReviseRequest
Cuevia.message.ReviseRequest --> User.message.GenerationStale
User.OnRegenerate --> Cuevia.message.CompileRequest
```

- After the source is saved, Cuevia compiles automatically. If no external processing is configured, there is no external request and generation uses only local adapters or the inputs. If external processing is needed, it waits in awaiting_input_review and asks the user to review the inputs that will actually be sent. ExternalProcessingRequest is valid only for the exact review revision and scope hash the user reviewed, and the original audio for ASR and the inputs for semantic summarization are confirmed per stage. ProcessingProvider is the external boundary abstracting the configured ASR and semantic summarization providers.
- CompileAccepted means the job was accepted. PromptReady/Partial/Failed are completion notifications carrying a job ID and source/review revision. A late response after completion, cancellation, or timeout never completes a different job.
- Even when transcription or semantic summarization fails, a base evidence prompt can still be produced. If the required question/request input is missing, the user is notified with PromptPartial and the reason for the omission is also written into the body.
- When the user revises, previous results become stale. Results are published only for the same input revision. Only post-processing is retried; the source is never re-recorded.

## 3. Deliver the reviewed result as real files

```jobflow
scope: User review and creation of delivery materials
Object: User, Cuevia, FileSystem
User.OnExport --> Cuevia.message.ExportRequest
Cuevia.message.ExportRequest --> FileSystem.message.WriteBundleRequest
FileSystem.message.WriteBundleRequest --> Cuevia.message.BundleWritten
FileSystem.message.WriteBundleRequest --> Cuevia.message.BundleWriteFailed
Cuevia.message.BundleWritten --> User.message.BundleReady
Cuevia.message.BundleWriteFailed --> User.message.ExportFailed
User.OnCopyPrompt --> Cuevia.message.CopyPromptRequest
Cuevia.message.CopyPromptRequest --> User.message.TextCopiedWithAttachmentList
```

- ExportRequest includes the latest generation revision, the delivery or source-included profile, and the destination path. BundleReady is announced after file verification and manifest finalization.
- CopyPrompt copies text only. TextCopiedWithAttachmentList does not mean images were sent or attached.
- The user pastes the materials into the AI tool of their choice and attaches the files. Sending directly to an AI is out of scope for the first version, so there is no automatic Cuevia→AI arrow.
