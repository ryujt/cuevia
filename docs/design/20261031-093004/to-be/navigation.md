# Cuevia Navigation 설계

- 기준일: 2026-10-31
- 상태: 신규 macOS 로컬 앱의 TO-BE 제안. 구현·제품 실행 검증 전이다.
- 범위: 첫 화면 선택, 동기화 기록, 검토·보완, prompt 생성·전달, 복구, 설정. 시나리오별 절을 한 파일에 통합한다.
- 원본 연결: [핵심 설계](system-design-to-be.md), [요구사항](system-design-to-be.md#requirements), [공개 계약](system-design-to-be.md#contracts), [전달 규칙](system-design-to-be.md#delivery), [검증 계획](system-design-to-be.md#verification).
- 화면 영역과 접근성: [Screen Layout](screen-layout.md). 내부 상태·저장·컴파일 계약은 핵심 설계가 원본이며, 아래 처리 이름은 화면 이동의 판단만 요약한다.

## 1. 화면과 이동의 의미

이 앱에는 웹 URL이나 HTTP endpoint가 없다. 앱 실행은 `SessionLibrary`에서 시작한다. Dock 재진입·메뉴 명령은 이미 열린 창을 앞으로 가져오며, 복구할 미완료 세션이 있으면 `RecoveryPanel`을 먼저 표시한다. 저장된 세션을 여는 동작은 기록을 자동 재개하지 않는다.

`RecordingToolbar`, `SceneCanvas`, `NotePanel`은 함께 떠 있을 수 있는 창이다. 이들 사이의 화살표는 창을 열거나 앞으로 가져오는 동작이며 출발 창을 파괴하지 않는다. 캡처 선택 동안 앱 보조 창을 잠시 숨기며, 취소하면 이전 창 배치·입력 대상이 돌아온다. `CapturePicker`는 OS 소유 선택 UI를 대표하는 식별자다. `RegionOverlay`는 단일 디스플레이 안의 영역 선택용 앱 오버레이다.

| 화면 식별자 | 진입·수명 | Layout |
|---|---|---|
| SessionLibrary | 앱의 기본 창, 저장된 세션 진입점 | [목록](screen-layout.md#sessionlibrary) |
| SessionSetup | 새 세션 초안, 기록 시작 전 설정 | [설정](screen-layout.md#sessionsetup) |
| RecordingToolbar | Starting·Recording·Paused 상태 동안 유지 | [도구막대](screen-layout.md#recordingtoolbar) |
| CapturePicker | OS 화면·창 선택 중에만 존재 | [OS 선택](screen-layout.md#capturepicker) |
| RegionOverlay | 선택한 단일 디스플레이의 영역 지정 중에만 존재 | [영역 선택](screen-layout.md#regionoverlay) |
| SceneCanvas | 불변 캡처와 판서·가리키기를 표시 | [캔버스](screen-layout.md#scenecanvas) |
| NotePanel | 장면 또는 전체 세션에 명시적으로 연결한 텍스트 입력 | [노트](screen-layout.md#notepanel) |
| SessionReview | Paused의 읽기 전용 검토, 종료 후 보완·오류 검토 | [검토](screen-layout.md#sessionreview) |
| PromptPreview | 생성 결과와 실제 전달 자산 확인 | [미리보기](screen-layout.md#promptpreview) |
| Settings | 로컬 권한·오디오·전사·저장 설정 | [환경설정](screen-layout.md#settings) |
| RecoveryPanel | 비정상 종료·손상·미완료 기록의 복구 진입점 | [복구](screen-layout.md#recoverypanel) |

화면과 시점은 구분한다. `activeScene`은 이후 기록의 명시적 대상이고, `inspectedScene`은 지금 보고 있는 장면이다. 다른 앱이 맨 앞으로 오거나 NotePanel을 클릭해도 `activeScene`이 바뀌지 않는다. 과거 장면을 보는 행위도 대상을 바꾸지 않는다. 캔버스의 **이 장면을 대상으로 기록**을 눌러야 이후 발화·판서·가리키기의 대상이 바뀐다. 기존 입력은 원래 대상에 남는다. [FR-01·FR-03·FR-04](system-design-to-be.md#requirements)

### 이동 판단과 로컬 계약

괄호 노드는 서버 호출이 아닌 앱 내부 처리다. 아래 별칭은 계약을 복제하지 않고 해당 공개 진입점을 가리킨다. 실패 시 기존 화면의 오류 영역에 원인·재시도·대체 입력을 표시한다.

| 처리 별칭 | 공개 진입점 / 판단 | 화면 결과 |
|---|---|---|
| prepare_capture | SessionCoordinator.PrepareDraft | 첫 Scene 준비 또는 Setup 유지 |
| start_session | SessionCoordinator.Start | Starting 후 Recording, 또는 Setup에 시작 실패 표시 |
| capture_scene | SessionCoordinator.Capture | 성공 때만 새 Scene과 activeScene 교체 |
| pause_for_review | SessionCoordinator.Pause의 입력 장벽 포함 | Paused가 확인된 뒤 읽기 전용 검토 열기 |
| resume_session | SessionCoordinator.Resume | 새 기록을 수용하는 toolbar·canvas 복귀 |
| finalize_session | SessionCoordinator.Finalize | 기록 종료 및 Review 열기 |
| compile_prompt | CompileCoordinator.Compile | 최신 결과 Preview 또는 Review의 보완 상태 |
| recover_session | SessionStore.Recover | 복구 결과 Review 또는 실패 상태 유지 |
| export_delivery | DeliveryExporter.Export | 실제 파일·경로가 확정된 결과만 표시 |

입력 저장 실패는 이동 성공으로 표시하지 않는다. 입력창의 초안과 재시도 동작을 유지하며, Finalize·닫기 같은 뒤따르는 명령은 저장 성공 또는 사용자가 선택한 초안 버리기까지 대기한다. 실패한 캡처는 기존 Scene을 교체하지 않는다. 계약 세부와 오류 종류는 [공개 계약](system-design-to-be.md#contracts)을 따른다.

<a id="start"></a>
## 2. 새 세션과 첫 캡처

진입은 `SessionLibrary`의 **새 세션**이다. 질문·수정 요청·혼합 모드와 마이크 사용 여부를 설정하고 캡처 대상을 선택한다. 이 문서의 텍스트 전용은 **화면 + 텍스트 입력, 마이크 미사용**이다. 캡처 없는 음성·전체 노트만의 세션은 MVP에서 제외한다. 첫 Scene 하나가 준비되어야 시작할 수 있다. [FR-01·FR-02·FR-05·FR-09](system-design-to-be.md#requirements)

```navigation
SessionLibrary --> SessionSetup : 새 세션
SessionSetup --> CapturePicker : 창 또는 디스플레이 선택
CapturePicker --> (prepare_capture) : 캡처 대상 확정
CapturePicker --> RegionOverlay : 영역 방식의 단일 디스플레이 확정
CapturePicker --> SessionSetup : 첫 선택 취소
RegionOverlay --> (prepare_capture) : 영역 확정
RegionOverlay --> SessionSetup : 첫 영역 선택 취소
(prepare_capture) --> SessionSetup : 첫 Scene 준비 성공
(prepare_capture) --> SessionSetup : 권한 거부 또는 캡처 실패
SessionSetup --> (start_session) : 첫 Scene과 입력 설정으로 시작
(start_session) --> RecordingToolbar : 기록 시작 성공
(start_session) --> SessionSetup : 시작 실패
RecordingToolbar --> SceneCanvas : 준비한 첫 장면 표시
RecordingToolbar --> NotePanel : 텍스트 입력 열기
SessionSetup --> SessionLibrary : 초안 닫기
```

1. 선택 전에 캡처 권한 상태와 녹음 여부를 보여 준다. OS 권한 대화상자는 앱 화면을 흉내 내지 않는다. 거부 후 Setup에서 설정 이동·재시도·마이크 없이 시작 같은 적용 가능한 선택을 안내한다. 캡처 권한 preflight 실패만으로 OS picker 경로를 막지 않는다. Start는 성공적으로 캡처·검증한 첫 Scene 자산의 존재를 기준으로 허용한다.
2. 첫 캡처는 세션 초안의 불변 자산이다. 성공 후 Setup에 미리보기와 대상 이름·종류·캡처 시각을 표시한다. 재선택은 새 초안을 준비하며 성공 전까지 기존 초안을 유지한다. 취소는 기존 초안으로 돌아온다.
3. Start는 Starting 상태를 거쳐 실제 기록 시작 시각을 확정한다. 사전에 준비된 첫 Scene의 유효 세션 시간은 0이며, 캡처 시각에는 **기록 시작 전 캡처**를 함께 표시한다. 시작 대기 시간을 녹음된 시간처럼 표시하지 않는다.
4. 선택한 마이크가 열리지 않으면 원인을 보인다. **마이크 없이 시작**을 사용자가 선택하면 텍스트 전용으로 시작하며 toolbar에 이를 계속 표시한다. 자동으로 녹음 중인 것처럼 성공시키지 않는다.
5. Setup 닫기는 기록 중단이 아니다. 준비한 초안이 있으면 같은 화면의 인라인 확인에서 저장 후 닫기 또는 버리기를 선택한다.

<a id="recording"></a>
## 3. 기록 중 장면 추가·과거 장면·노트

진입은 RecordingToolbar 또는 이미 열린 SceneCanvas다. 새로운 캡처는 정적인 Scene을 추가하며 실시간 화면 변경을 따라가지 않는다. 앱 창을 캡처에서 제외하는 지원 범위와 OS 선택기의 세부 동작은 구현 검증 항목이다. [FR-01·FR-03·FR-04](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> CapturePicker : 새 장면의 창 또는 디스플레이 선택
SceneCanvas --> CapturePicker : 새 캡처
CapturePicker --> RegionOverlay : 새 영역의 단일 디스플레이 확정
CapturePicker --> (capture_scene) : 새 캡처 대상 확정
RegionOverlay --> (capture_scene) : 새 영역 확정
(capture_scene) --> SceneCanvas : 새 Scene 성공과 대상 전환
(capture_scene) --> SceneCanvas : 캡처 실패와 기존 장면 유지
CapturePicker --> SceneCanvas : 기록 중 선택 취소와 이전 창 복원
RegionOverlay --> SceneCanvas : 기록 중 영역 취소와 이전 창 복원
RecordingToolbar --> SceneCanvas : 장면 보기 또는 명시적 설명 재개
RecordingToolbar --> NotePanel : 노트 열기
SceneCanvas --> NotePanel : 이 장면의 노트 열기
NotePanel --> SceneCanvas : 노트 대상 장면 보기
SceneCanvas --> RecordingToolbar : 원본 앱 조작 시작과 캔버스 숨기기
NotePanel --> RecordingToolbar : 노트 숨기기
```

- 캡처 선택 중에도 기록 중·마이크 상태를 놓치지 않도록 상태를 복원한다. 선택을 여는 동작 자체는 Pause가 아니다. 요청마다 SelectionInterval을 남기며, 그 구간을 교차한 발화는 취소·실패 여부와 무관하게 이전/새 Scene·target으로 자동 확정하지 않고 후보·미해결로 표시한다. 그 밖의 입력은 실제 새 Scene 활성 경계를 따른다. 대기 중 새 캡처 중복 명령은 막는다.
- 과거 Scene 선택은 캔버스 내부 열람이다. **현재 기록 대상** 배지와 **열람 중인 장면**을 나란히 표시한다. **이 장면을 대상으로 기록**은 명시적 대상 전환을 기록하고, 선택한 장면과 이후 가리키기 위치를 사용자가 다시 확인할 수 있게 한다.
- point focus는 명시 해제·대상 전환·Pause·Stop까지 유지한다. 오래 유지된 focus에는 경과 표시와 **해제**를 제공하며, 임의 TTL로 사라지지 않는다. 캔버스 밖 임의 포인터 위치는 참조로 수집하지 않는다.
- 선택된 주석을 삭제하면 해당 target의 열린 focus도 닫는다. 가리킨 point/rect는 전달 frame에서 target ID·유효 시각·강조로 확인할 수 있다.
- NotePanel은 **장면: Sxx** 또는 **전체 세션**을 입력 전에 고정 표시한다. 노트의 대상 선택과 저장은 activeScene 전환이 아니다. SceneCanvas를 열어도 작성 중인 노트의 대상·텍스트를 바꾸지 않는다.
- **원본 앱 조작**으로 캔버스를 숨기면 InteractionInterval을 시작하고 toolbar에 상태를 표시한다. **장면 설명 재개**를 명시적으로 실행하면 구간을 닫는다. 해당 구간을 교차한 발화의 지시어는 기존 target으로 자동 확정하지 않는다. 노트 창 열기는 이 모드 전환이 아니다.
- 펜·화살표·사각형·텍스트·focus 변경과 노트 저장은 같은 화면 안의 입력이므로 이동도에 별도 노드로 만들지 않는다. 임시 획·텍스트와 저장 완료 표시를 구분한다. 구체적인 입력 및 IME 규칙은 [캔버스](screen-layout.md#scenecanvas)·[노트](screen-layout.md#notepanel)를 따른다.

<a id="pause-review"></a>
## 4. 일시정지·읽기 전용 검토·재개

기록의 Pause 버튼은 toolbar 안의 상태만 바꾸므로 이동도에서 생략한다. **기록 검토**는 Pause의 입력 장벽과 상태 확정 성공 뒤 Review를 연다. 캡처/입력 확정 실패·blocked 시 출발 창과 초안·실제 기록 상태를 유지한다. [FR-02·FR-06](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> (pause_for_review) : 기록 검토
(pause_for_review) --> SessionReview : 일시정지 성공
(pause_for_review) --> RecordingToolbar : 입력 blocked 또는 일시정지 실패
SessionReview --> SceneCanvas : 선택한 시점과 장면 열람
SessionReview --> NotePanel : 저장된 노트 열람
SessionReview --> (resume_session) : 기록 재개
(resume_session) --> RecordingToolbar : 재개 성공
(resume_session) --> SessionReview : 재개 실패
RecordingToolbar --> SceneCanvas : 재개한 기록 대상 표시
```

Paused에서는 Scene·이벤트·녹음·노트를 읽고 탐색할 수 있다. 신규 캡처, 판서·focus·기록용 노트의 생성·수정은 비활성이고 **재개 후 기록 가능**을 설명한다. 재생·열람 위치가 바뀌어도 이후 기록 대상은 바뀌지 않는다. Pause는 열린 focus와 진행 중 캡처 token을 닫는다. 늦은 캡처 완료는 현재 state를 재검사하여 폐기하고 새 Scene을 만들지 않는다. Resume이 성공하면 이전 activeScene을 유지하고 target은 해제된 상태로 시작하므로, 특정 지점을 다시 설명하려면 명시적으로 가리킨다. 세션 시간이 Pause 구간을 어떻게 표현하는지는 [시간 계약](system-design-to-be.md#contracts)을 따른다.

Paused의 읽기 전용 검토와 Finalize 뒤의 보완은 서로 다른 상태다. Pause 중에 보완한 것처럼 원래 기록을 바꾸는 우회 동작은 제공하지 않는다. 종료를 원하면 같은 화면의 **기록 종료 및 prompt 생성**을 사용한다.

Pause/Stop 입력 장벽은 신규 제스처·캡처를 막고 pending capture token을 취소한 뒤 편집기에 조합 종료를 요청한다. 편집기의 committed/cancelled 결과를 CommitInput으로 확정하고 focus 종료·media drain을 마쳐야 Paused/종료를 표시한다. blocked이면 **입력을 확정하거나 취소한 뒤 다시 시도하세요**와 초안을 유지하고 전환을 취소한다. 창 강제 닫기·timeout으로 조합을 확정하지 않는다. 장벽 동안 편집기의 기존 조합 종료 응답은 수용한다.

<a id="finalize-compile"></a>
## 5. 기록 종료·자동 생성·보완

Stop은 기록 중 또는 Paused에서 사용할 수 있다. 명령이 수용되면 새 기록 입력을 닫고, 수용한 입력을 확정·저장한 후 Finalize한다. SessionReview가 먼저 열려 생성 경과와 원본을 보여 주고, 로컬 결정적 컴파일이 끝나면 PromptPreview를 연다. 생성 완료는 외부 AI에 전송했다는 뜻이 아니다. [FR-05·FR-06](system-design-to-be.md#requirements)

```navigation
RecordingToolbar --> (finalize_session) : 기록 종료 및 prompt 생성
SessionReview --> (finalize_session) : 일시정지 기록 종료 및 prompt 생성
(finalize_session) --> SessionReview : 기록 저장과 종료 완료
(finalize_session) --> RecordingToolbar : 종료 실패와 Recording 상태 유지
(finalize_session) --> SessionReview : 일시정지 종료 실패 또는 interrupted 부분 보존
SessionReview --> (compile_prompt) : 종료 직후 자동 생성
(compile_prompt) --> PromptPreview : 현재 입력의 ready 또는 partial 결과
(compile_prompt) --> SessionReview : 외부 처리 입력 검토 대기
(compile_prompt) --> SessionReview : 출력 생성 실패
PromptPreview --> SessionReview : 근거와 미해결 항목 검토
SessionReview --> SceneCanvas : 종료 후 장면 보완 열기
SessionReview --> NotePanel : 종료 후 보완 노트 열기
SceneCanvas --> SessionReview : 보완 저장 후 검토로 돌아가기
NotePanel --> SessionReview : 보완 저장 후 검토로 돌아가기
SessionReview --> (compile_prompt) : 보완 반영하여 다시 생성
```

| 결과·이벤트 | 사용자가 보는 결과와 다음 동작 |
|---|---|
| Finalize 처리 중 | 입력 차단과 저장 경과, 중복 Stop 방지. 앱을 닫아도 종료가 완료되었다고 거짓 표시하지 않는다 |
| Finalize 실패 | 실제 기록 상태와 미확정 항목을 표시. Paused에서 호출했다면 Paused Review에 실패를 유지하며 재시도한다 |
| 전사 미설정·미지원 | **전사 없음**과 음성 구간을 표시. 텍스트 보완·원음 확인 가능. 음성 내용이 이해되었다는 문장을 만들지 않는다 |
| 전사 대기·실패·일부 성공 | 상태와 해당 구간을 표시. 원음과 수동 전사·요약 입력을 제공하며, 빈 전사를 완료 결과로 표시하지 않는다 |
| 외부 처리 필요 | awaiting_input_review와 실제 전송 입력·제외 범위 표시. 제공자 설정만으로 업로드하지 않으며 명시적인 검토 실행을 기다린다 |
| 입력 부족 | 의미 있는 질문·요청을 만들 근거가 부족하다는 메시지와 보완 입력. 기본 근거 출력은 partial로 확인 가능하지만 질문 자동 생성 완료나 빈 성공으로 표시하지 않는다 |
| 참조 미해결·전사 불확실 | 해당 시점·Scene으로 이동하여 정정, 명시적 확인, 또는 미확인으로 유지. 확실한 해석으로 바꾸지 않는다 |
| 현재 revision 생성 성공 | 모드·질문/요청·Scene 참조·미확인 항목을 포함한 Preview. 미해결 항목은 수와 근거를 계속 표시한다 |
| 근거 자료만 생성됨 | artifact_status와 intent_status를 따로 표시. generated/user_authored와 unavailable/insufficient를 구분하며 기본 자료 성공을 자동 질문 작성 완료로 표시하지 않는다 |
| 보완·모드·전사 변경 | 기존 Preview에 **다시 생성 필요**. 최신 결과의 copy/export를 잠그고 다시 생성으로 연결한다 |

종료 후 장면·노트·전사 정정은 원래 시간의 기록을 덮어쓰지 않는 **종료 후 보완**으로 표시한다. 해당 Scene 또는 전체 세션에 연결하며, 저장 후 reviewRevision이 바뀌어 기존 prompt를 오래된 결과로 만든다. 원래 녹음과 원본 캡처는 보완 때문에 바뀌지 않는다. 같은 snapshot·review revision에서 전사가 끝나면 같은 generation의 다음 컴파일 단계로 진행한다. 사용자 보완이 생겼다면 이전 generation을 최신본으로 publish하지 않고 새 revision으로 명시적으로 다시 생성한다. [공개 계약](system-design-to-be.md#contracts)을 따른다.

Stop 직후 자동 Compile은 로컬 기본 자료와 사용 가능한 로컬 엔진까지만 실행한다. 외부 ASR은 원음·선택 구간을, 외부 의미 정리는 전사·메모·마스킹 프레임을 SessionReview에서 별도로 미리 확인한다. **검토한 자료로 외부 처리**를 실행하면 고정 ReviewInputRevision·scope hash의 입력만 처리한다. 이미지 마스킹이 음성을 가렸다고 안내하지 않으며 오디오 제외 구간은 별도로 선택한다. 이후 보완은 이미 전송한 자료를 되돌릴 수 없고 새 검토 revision이 필요하다. 최종 prompt를 다른 AI에 자동 전달하는 동작은 제공하지 않는다.

<a id="delivery"></a>
## 6. 복사·실제 자산·번들 내보내기

진입은 현재 revision으로 생성된 PromptPreview다. **prompt 복사**는 텍스트만 클립보드에 넣는다. 버튼 옆에 **이미지·오디오는 별도 첨부 필요**를 항상 표시한다. 복사가 파일을 첨부하거나 AI에 전송했다는 표현을 사용하지 않는다. [FR-07·FR-09](system-design-to-be.md#requirements)

```navigation
PromptPreview --> (export_delivery) : 전달 번들 만들기
(export_delivery) --> PromptPreview : 실제 파일과 경로 확인 완료
(export_delivery) --> PromptPreview : 내보내기 실패
PromptPreview --> SessionReview : 자산 누락 또는 미해결 근거 확인
PromptPreview --> SessionLibrary : 세션 목록으로 돌아가기
SessionLibrary --> SessionReview : 저장된 세션 열기
SessionReview --> PromptPreview : 현재 생성 결과 보기
```

복사·Finder에서 자산 보기·저장 위치 선택은 Preview 안의 동작 또는 OS 창으로 위임하므로 앱의 가짜 화면 노드를 추가하지 않는다. Preview·복사·내보내기는 같은 generation/profile의 DeliveryProjection을 사용하여 가림·메타데이터 제외가 경로마다 달라지지 않게 한다. 기본 **전달용** 프로필은 prompt·context·evidence·합성 frames를 전달하며 원음·원본 이미지·PID·창 제목·프로젝트 로컬 절대 경로는 제외한다. **원본 포함**은 포함 범위와 마스킹 전 원본이 들어간다는 안내를 보고 명시적으로 선택한다. 번들 성공은 실제 생성된 경로와 포함 파일 목록을 확인한 뒤 표시한다. 실패는 생성되지 않은 파일을 다운로드 가능하다고 표시하지 않고, 재시도와 다른 위치를 제공한다. 일부 자산 누락은 해당 자산을 지목하고 최신 완전 번들로 오인하지 못하게 한다. 경로·manifest·상대 자산 참조 규칙은 [전달 계약](system-design-to-be.md#delivery)을 따른다.

미해결 정보가 있는 prompt는 경고와 미확인 내용을 유지한다. 치명적 자산 누락, 오래된 revision, 생성 실패·입력 부족의 전달 가능 여부는 계약이 정하며 그 이유를 UI에 표시한다. 원본 보존용 세션 내보내기와 최신 prompt 전달 성공을 같은 성공 메시지로 합치지 않는다.

<a id="recovery-settings"></a>
## 7. 복구·환경설정·닫기

앱 실행 시 복구 대상이 있으면 진입 가드가 RecoveryPanel을 연다. 이 가드는 실제 사용자 화면을 결정하므로 처리 노드로 시작한다. 앱 실행·OS의 창 재활성화 자체는 노드로 그리지 않는다. [FR-08·FR-09](system-design-to-be.md#requirements)

```navigation
(check_recovery) --> RecoveryPanel : 복구할 미완료 세션 있음
(check_recovery) --> SessionLibrary : 복구 대상 없음
SessionLibrary --> RecoveryPanel : 미완료 세션 복구 알림
RecoveryPanel --> (recover_session) : 선택한 세션 복구
(recover_session) --> SessionReview : 복구 가능한 자료 열기
(recover_session) --> RecoveryPanel : 복구 실패 또는 선택 필요
RecoveryPanel --> SessionLibrary : 원본 유지하고 나중에 처리
SessionReview --> SessionSetup : 복구본에 연결한 새 세션으로 이어 기록
SessionLibrary --> Settings : 환경설정
SessionSetup --> Settings : 권한 또는 입력 설정 확인
RecordingToolbar --> Settings : 기록 설정 확인
Settings --> SessionLibrary : 목록에서 연 환경설정 닫기
Settings --> SessionSetup : 시작 설정으로 돌아가기
Settings --> RecordingToolbar : 기록 화면으로 돌아가기
```

- Recover는 마지막으로 검증된 기록과 누락 범위를 보여 준다. 자동으로 마이크를 켜거나 기존 세션을 재개하지 않는다. **이어 기록**은 parent_session_id로 연결한 새 세션의 Setup을 열고 첫 캡처를 준비하는 동작이다. 복구 검토를 완료하면 종료 후 보완·재생성·내보내기 경로를 사용한다.
- Settings 닫기는 호출한 화면으로 돌아간다. 현재 세션의 마이크·시간축·전사 정책을 바꾸는 설정은 즉시 변경 가능 여부를 표시하며, 적용할 수 없으면 **다음 세션부터**로 표시한다. 권한 변경으로 기록이 끊긴 경우 toolbar의 실제 상태·대체 입력을 유지한다.
- 기록 중 창의 닫기 버튼은 보조 창을 숨긴다. 세션 종료와 앱 종료는 같은 뜻이 아니다. 앱 종료 요청 시 기록 상태를 검사하고 인라인 선택으로 **종료 및 저장**, **계속 기록**, 또는 필요한 복구 보존을 제공한다. 저장 실패 상태에서 종료 성공으로 처리하지 않는다.
- 마지막 저장본 열기는 SessionReview의 직접 진입이다. 오래된 결과를 열 때는 자동 녹음·자동 전송 없이 보완 필요·재생성 상태만 보여 준다.

## 8. 검토 범위

제안 시나리오와 화면 전수 목록, local process와 공개 계약의 대응, 성공·실패·취소·재개·저장본 직접 진입 경로를 정적으로 검토한다. 화면 내부 입력의 시간 수용, IME, 접근성, 다중 창 복원, OS 선택기의 실제 레이아웃, 권한 변경·마이크 실패·저장 실패·복구 동작은 [검증 계획](system-design-to-be.md#verification)과 [Layout의 공통 규칙](screen-layout.md#common)에서 확인한다. 현재 문서는 제품 실행 또는 DSL 렌더링을 수행했다는 증거가 아니다.

2026-10-31 정적 확인: navigation 6개 블록의 노드·라벨 문법과 11개 화면의 Layout 대응을 검사했다. UI 두 파일의 링크·앵커 검사 결과는 FAIL 0, WARN 0이다. 실제 OS UI와 제품 실행 검증은 미실행이다.
