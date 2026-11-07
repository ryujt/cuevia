# 매크로 Job Flow

Cuevia 전체를 하나의 시스템으로 본다. 내부 모듈은 [앱 내부 흐름](cuevia-app.md), 계약은 [설계서](../system-design-to-be.md#contracts)에 있다. `message`는 논리적 요청·응답·알림이며 네트워크나 메시지 브로커를 뜻하지 않는다. 같은 요청의 성공/취소/실패 응답은 대체 결과다.

## 1. 화면을 고르고 설명을 기록한다

```jobflow
scope: 사용자와 Cuevia 및 macOS의 기록 협력
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

- PrepareRequest에서 대상 선택과 첫 캡처를 준비한다. 취소/오류 시 녹음은 시작하지 않는다. 마이크 없는 텍스트 모드는 StartRequest에서 직접 RecordingStarted로 응답하는 대체 경로다.
- StartMicrophoneRequest는 권한 확인과 장치 시작을 포함한다. `MicrophoneReady`는 시작 응답이고 이후 연속 버퍼 수신은 내부 흐름에 있다. 권한 실패를 녹음 성공으로 바꾸지 않는다.
- InputCommand는 가리키기·판서·메모·새 캡처·pause/resume 명령이다. 실제 저장 완료와 일시적 UI 표시를 구분한다.
- SourceSaved는 원본 봉인 결과다. 전사·프롬프트 완료 알림은 다음 시나리오의 별도 계약이다. 저장 오류는 SourceSaveFailed로 응답하고 확보 자료 복구로 이동한다.

## 2. 종료 후 자동 프롬프트 생성과 부분 실패

```jobflow
scope: 원본 저장 후 프롬프트 생성과 선택적 외부 처리
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

- source 저장 후 Cuevia가 자동으로 compile한다. 외부 처리 설정이 없으면 외부 요청은 없고 로컬 adapter 또는 입력만으로 생성한다. 외부 처리가 필요하면 awaiting_input_review로 대기하고 실제 전송 입력 검토를 요구한다. ExternalProcessingRequest는 사용자가 검토한 동일 review revision·scope hash에만 유효하며, ASR 원음과 의미 정리 입력은 단계별로 확인한다. ProcessingProvider는 구성된 ASR/의미 정리 제공자를 추상화한 외부 경계다.
- CompileAccepted는 작업 접수다. PromptReady/Partial/Failed는 job ID와 source/review revision이 있는 완료 알림이다. 완료·취소·timeout 뒤 늦은 응답으로 다른 작업을 완료하지 않는다.
- 전사·의미 정리 실패에도 기본 근거 프롬프트를 만들 수 있다. 필요한 질문/요청 입력이 없으면 PromptPartial로 알리고 본문에도 누락 사유를 남긴다.
- 사용자가 수정하면 이전 결과는 stale이다. 같은 입력 revision에만 결과를 publish한다. 원본을 다시 녹음하지 않고 후처리만 재시도한다.

## 3. 검토 결과를 실제 파일로 전달한다

```jobflow
scope: 사용자 검토와 전달 자료 생성
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

- ExportRequest는 최신 generation revision, 전달용/원본포함 프로필, 대상 경로를 포함한다. 파일 검증과 manifest 확정 후 BundleReady를 알린다.
- CopyPrompt는 텍스트만 복사한다. TextCopiedWithAttachmentList는 이미지가 전송·첨부됐다는 뜻이 아니다.
- 사용자가 자료를 선택한 AI 도구에 붙여 넣고 파일을 첨부한다. 직접 AI 전송은 첫 버전 범위가 아니므로 Cuevia→AI 자동 전송 화살표는 없다.
