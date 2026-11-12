# Macro Job Flow

This flow treats Cuevia as a single system. See the [internal app flow](cuevia-app.md) for modules and the [design](../system-design-to-be.md#contracts) for contracts. A `message` represents a logical request, response, or notification. It implies neither a network nor a message broker. Success, cancellation, and failure are alternative outcomes of the same request.

## 1. Select a capture source and record an explanation

```jobflow
scope: Recording interactions among the user, Cuevia, and macOS
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

- PrepareRequest selects the source and prepares the first capture. Cancellation or failure leaves recording stopped. In text mode, StartRequest can return RecordingStarted directly without a microphone.
- StartMicrophoneRequest checks permission and starts the device. `MicrophoneReady` is the start response. The internal flow covers subsequent buffers. Permission failure prevents voice recording from starting.
- InputCommand covers pointing, annotation, notes, new captures, pause, and resume. The UI distinguishes transient input from committed data.
- SourceSaved reports sealed originals. Transcription and prompt completion belong to the next scenario. A save error returns SourceSaveFailed and moves to recovery of available material.

## 2. Generate a prompt automatically after stopping

```jobflow
scope: Prompt generation after source save and optional external processing
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

- Cuevia compiles automatically after saving the source. With no external processing configured, it uses a local adapter or the source inputs alone. If an external stage is needed, it waits in awaiting_input_review for review of the exact input to send. ExternalProcessingRequest is valid only for the reviewed revision and scope hash. ASR audio and interpretation inputs are reviewed separately. ProcessingProvider represents the configured external ASR or interpretation provider.
- CompileAccepted acknowledges the job. PromptReady/Partial/Failed report completion with a job ID and source and review revisions. Responses arriving after completion, cancellation, or timeout cannot complete another job.
- Transcription or interpretation failures can still yield a basic evidence prompt. Missing inputs for a question or request produce PromptPartial, with the reason in the output.
- User edits make prior results stale. Publish only to the same input revision. Retry post-processing without recording the source again.

## 3. Deliver the reviewed result as files

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

- ExportRequest includes the latest generation revision, the delivery or originals-included profile, and a destination. Send BundleReady after verifying files and committing the manifest.
- CopyPrompt copies text only. TextCopiedWithAttachmentList reports copied text and an attachment list. Image transfer and attachment remain user actions.
- The user pastes the materials into their chosen AI tool and attaches files. Automatic transfer from Cuevia to AI is outside the first release.
