# Cuevia 앱 내부 Job Flow

단일 앱 서비스의 모듈·상세 수준 흐름이다. 상위 조율자가 Worker 결과와 다음 호출을 연결하며, Worker는 형제 Worker를 직접 호출하지 않는다. 공개 계약·오류·데이터 필드는 [핵심 계약](../system-design-to-be.md#contracts)이 원본이다. 블록의 반환값 분기는 대체 경로이며, 비동기 callback 수신은 독립 진입점이다.

## 1. 첫 캡처 준비·녹음 시작·새 장면

### 1.1 기록 전 준비

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

- Start 전에 draft 원본 이미지 파일을 닫고 검증한다. 음성 모드에서는 Start 성공의 host anchor를 공통 t0에 대응시키고, 첫 Scene을 t=0부터 활성화한다. 마이크 미사용 모드는 AudioRecorder.Start를 생략하고 SessionClock.BeginEpoch로 진행한다.
- AudioRecorder.Start는 buffer callback 연결을 등록하고 시작 준비가 끝난 뒤 ready를 반환한다. 공개된 recording 시작 이전의 초기 buffer는 시작 경계에서 잘라 내며, ready 전까지 UI는 starting을 표시한다.
- BeginSession이 실패하면 SessionCoordinator가 AudioRecorder.Stop을 호출하고, draft와 닫힌 파일을 복구 대상으로 유지한다. DB 시작 성공 전에 UI를 녹음 중으로 바꾸지 않는다.
- 첫 캡처는 pre_recording 자료이며 이미지가 정확히 t0에 촬영되었다고 주장하지 않는다.

### 1.2 기록 중 추가 캡처

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

- 선택 시도마다 request token의 SelectionInterval을 시작하고 commit/cancel/fail까지 보존한다. 성공 시 선택 구간 종료와 새 Scene 활성화를 AppendSceneAndActivate의 한 transaction으로 확정한다. 취소·실패는 해당 종료 상태만 저장하며 기존 Scene과 Focus를 유지한다.
- SelectionInterval과 교차한 발화는 이전 Scene이나 새 Scene에 자동 explicit 연결하지 않고 unresolved/candidates로 남긴다. 선택 전후의 기존 명시 대상이 같아 보여도 사용자의 확인 없이 확정하지 않는다.
- token은 request ID와 state revision을 포함한다. Pause·Stop·다음 선택 요청·세션 전환은 진행 중 token을 취소한다. 완료 시 현재 상태와 token을 다시 검사하여 recording이 아니거나 오래된 요청이면 stale로 폐기한다.
- DB commit 실패는 캡처 자체의 실패와 구분한다. 원본 저장이 보장되지 않으므로 기록을 중단하고 복구를 안내한다.

### 1.3 연속 오디오 입력과 중단

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

- OnAnchor/OnChunkClosed/OnDiscontinuity는 Start 시 연결되고 녹음 중 반복해서 발생한다. buffer callback은 Recorder 내부의 bounded queue로 필요한 데이터만 복사하고, 비실시간 writer가 짧은 chunk를 닫는다. 그림의 DB 작업을 실시간 callback 안에서 수행하지 않는다.
- Pause/Finalize 장벽은 새 제스처·캡처 접수를 차단하고 pending capture token과 SelectionInterval을 취소한 뒤, 현재 편집기에 IME 조합 종료를 요청한다. 기존 편집기의 committed/cancelled 응답은 차단하지 않고 note/stroke·focus 종료와 함께 commit한 뒤 ready로 진행하여 audio drain을 수행한다.
- 편집기 응답이 blocked이면 초안과 실제 recording/paused 상태를 유지하고 전환 미완료를 알린다. 확정/취소 후 재시도하며 timeout·창 강제 닫기로 미확정 조합을 저장하지 않는다. 이미지 뷰를 닫는 것만으로 일시정지·종료가 완료되지 않는다.
- Stop은 등록한 audio callback을 중지하고 쓰는 중인 chunk의 close와 결과 drain을 기다린다. Seal은 제한 시간 내 마치지 못하거나 손상된 부분을 partial로 기록할 수 있다. 마이크 미사용 모드는 Recorder 호출을 생략한다.
- 마이크 중단·절전은 신규 sample 수신을 멈추고 마지막 유효 sample부터 gap을 열며 target을 닫은 뒤 가능한 입력 장벽을 거쳐 paused로 전환한다. 편집기 blocked이면 초안·실제 세션 상태를 보존하되 audio_status=stopped/failed로 표시한다. 자동 녹음 재개 없이 명시 Resume을 요구한다.
- Resume은 새 epoch/anchor 수립과 DB 확정 성공 뒤 입력을 재개한다. Pause 전 수집된 buffer/chunk의 늦은 close·anchor drain 결과는 media_revision으로 등록하고, Pause 뒤 신규 sample은 추가하지 않는다. paused 중 새 편집은 거부하며 stale capture·다른 세션 token의 callback은 폐기한다.

KeepActualStateAndDraft는 Pause와 Finalize 각각에서 편집기 blocked일 때 선택하는 공통 대체 경로다. 두 종료 명령을 연속 실행하거나 blocked 입력을 자동 재시도한다는 의미가 아니다.

## 2. 가리키기·판서·메모·undo

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

- ValidateInput은 session state, expected revision, scene/target, 좌표, IME 확정 상태를 검사한다. CommitInput은 typed command로 정의하고 시작한 stroke의 commit과 신규 stroke를 구분한다.
- SetFocus와 SceneActivated도 같은 직렬화·시각·DB 원자성 규칙을 사용한다. 주석을 선택한 채 다른 Scene으로 이동하면 이전 Focus 종료와 새 Scene 활성화를 한 transaction으로 확정한다. 선택된 target 주석 삭제도 같은 transaction에서 열린 Focus를 닫는다.
- stroke 점들은 짧은 batch마다 임시 기록하고 pointer-up에서 commit한다. Pause/Stop은 도착한 점만 명시적으로 확정하거나 취소한다. 비정상 종료로 남은 미완료 stroke는 recovery summary에 표시한다.
- NoteRevision은 anchor와 edited time을 분리한다. 기록 종료 후 Revise는 원본 이벤트를 수정하지 않고 post_recording의 별도 revision으로 저장한다.
- undo/redo는 이벤트를 지우지 않고 새로운 의미적 조작을 추가한다. 과거 이미지는 event seq의 cut으로 재현한다.

## 3. 자동 자료 생성·부분 실패·내역 검증

### 3.1 snapshot→transcript→근거→prompt

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

- Compile은 불변 snapshot/review revision을 지정한다. FrameRenderer/Interpreter는 녹음 중 편집 상태나 DB 내부를 직접 읽지 않는다.
- 분기는 해당 전사·해석 정책을 선택한다. 음성을 사용하지 않아도 메모에서 질문·요청을 구성할 수 있다. 필요한 음성을 얻지 못한 경우에는 missing 구간을 남긴다.
- enabled는 로컬 실행 가능 또는 고정 ReviewInputRevision·scope hash에 대한 외부 입력 검토 완료를 뜻한다. Stop 직후에는 로컬 기본 자료와 로컬 엔진까지만 실행하고, 외부 단계는 awaiting_input_review로 저장하여 실제 전송 입력·제외 범위의 검토를 요청한다. 설정만으로 원음·화면을 업로드하지 않는다.
- 사용자가 SessionReview에서 검토한 자료로 외부 처리를 요청하면 고정된 입력만 해당 stage에 전달한다. 외부 ASR의 선택 원음·제외 구간과 외부 의미 정리의 전사·메모·마스킹 프레임을 구분한다. 이후 보완은 새 revision이며 이미 전송한 입력을 되돌리지 않는다.
- 각 stage는 input hash/version과 결과를 GenerationStore에 저장한다. 실패한 stage부터 재시도하며 이미 성공한 외부 호출을 무조건 반복하지 않는다.
- artifact_status와 intent_status를 구분한다. 검증된 자동 정리는 generated, 명시적으로 작성한 최종 항목은 user_authored이며, 엔진 없음·실패는 unavailable/partial, 목적 입력 부족은 insufficient/partial이다. 기본 자료 출력만으로 자동 질문 작성 완료를 표시하지 않는다.
- Publish는 최신 revision과 대조한다. 사용자 보완 중 끝난 오래된 job은 이력에만 남기고 현재 PromptPreview를 대체하지 않는다.
- Cancel은 외부 provider의 처리 중단을 보장하지 않는다. job token을 무효화하고 늦은 응답을 현재 generation에 적용하지 않는다.

전사 성공·일부 성공·실패·미사용에서 Bind로 모이는 선은 같은 입력을 한 번 처리하는 대체 분기의 합류다. ValidateGeneration은 해석 미사용·검증 성공·기본 prompt 대체의 합류이며, UseBasePrompt는 해석 실패·검증 거부의 공통 대체 경로다. SaveInputReviewPending은 현재 선택된 외부 단계의 검토 대기를 저장하는 합류다.

### 3.2 프레임 선택과 대상의 모호함

```jobflow
orchestrator: EvidenceBinder
Object: EvidenceBinder, IntervalResolver, ReferenceResolver
EvidenceBinder.Bind --> IntervalResolver.SplitAtContextBoundaries
IntervalResolver.SplitAtContextBoundaries.result --> ReferenceResolver.ResolveCandidates
ReferenceResolver.ResolveCandidates.explicit --> EvidenceBinder.LinkExplicit
ReferenceResolver.ResolveCandidates.multiple --> EvidenceBinder.MarkNeedsReview
ReferenceResolver.ResolveCandidates.missing --> EvidenceBinder.KeepUnbound
```

- EvidenceBinder의 공개 입력·출력은 상위 그림과 같다. 내부 pure helper를 상세화한 것이며 별도 서비스가 아니다.
- 발화 전체 구간을 Scene/Focus 변경점과 비교한다. 단어 시각이 없으면 발화를 임의로 의미 분할하지 않고 후보를 남긴다.
- 캡처 선택의 SelectionInterval 또는 원본 앱 조작의 InteractionInterval과 교차한 발화는 취소·실패 여부와 무관하게 이전/새 Scene·target의 자동 explicit 연결에서 제외하고 후보 또는 미해결로 남긴다. 원본 앱 조작은 명시적인 캔버스 숨기기/설명 재개로 구분하며 노트 창 열기는 해당하지 않는다.
- FrameRenderer는 각 link의 `(scene_id, event_seq)` 상태를 재생한다. 최종 판서 이미지만으로 과거 발화를 설명하지 않는다. point/rect Focus는 해당 target ID·유효 시각과 시각적 강조를 export frame에 포함하여 수신자가 가리킨 대상을 확인할 수 있게 한다.
- 미해결 link와 interpreter 추정을 사용자의 명시 지정과 혼동하지 않는다. 사용자는 확인 후 정정하거나 미확인 상태로 포함할 수 있다.

## 4. 비정상 종료 복구와 내보내기

### 4.1 복구

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

- DB 복구는 복사본에서 검사한다. 열려 있던 chunk/temp/orphan 후보와 손실 범위를 목록으로 보여 주고 기존 원본을 임의로 삭제하지 않는다.
- 복구 결과는 interrupted이며 기존 녹음을 같은 시간축에서 자동 재개하지 않는다. SessionCoordinator가 명시적 요청을 받아 parent_session_id를 가진 새 Session을 시작한다.
- 프레임·질문을 생성할 수 있는 부분만 compile할 수 있다. 누락된 근거가 필요한 item은 unresolved로 남긴다.

검증 완료 경로와 누락 자산 기록 뒤의 SealInterrupted는 한 복구 결과를 봉인하는 대체 분기의 합류다.

### 4.2 실제 파일 공유

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

- Load는 current revision/profile과 review 상태를 검증한다. 공유 대상은 그 시점의 고정 generation이므로 이후 편집이 내보내는 중인 파일을 바꾸지 않는다.
- Preview·copy·export는 DeliveryExporter.BuildProjection의 같은 DeliveryProjection을 사용한다. profile별 metadata 제외와 텍스트·이미지 가림이 복사 경로에서 빠지지 않게 한다.
- 원본 포함 profile은 GenerationStore가 참조하는 고정 source snapshot과 asset list를 얻는다. exporter가 DB의 private table을 직접 읽지 않는다.
- Verify는 첨부 파일 존재·해시·상대 참조, profile별 제외 항목, 경로 탈출을 검증한다. publish 전 실패한 출력을 성공 폴더로 반환하지 않는다.
- copy와 export의 UI 완료 표시는 구분한다. 수신 AI의 첨부 완료는 이 공개 계약 범위 밖이다.
