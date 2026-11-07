# Cuevia Screen Layout 설계

- 기준일: 2026-10-31
- 상태: 신규 macOS 앱의 TO-BE 제안. 수치와 배치는 구현 제안이며 실측 UI가 아니다.
- 범위: 화면별 절 통합. 사용자에게 인지되는 11개 화면·주요 오버레이를 모두 수록한다.
- 원본 연결: [핵심 설계](system-design-to-be.md), [화면 이동](navigation.md), [요구사항](system-design-to-be.md#requirements), [공개 계약](system-design-to-be.md#contracts), [전달 규칙](system-design-to-be.md#delivery), [검증 계획](system-design-to-be.md#verification).

<a id="common"></a>
## 1. 공통 배치·입력 규칙

Cuevia는 하나의 macOS 로컬 서비스다. 목록·설정·검토는 기본 창, 기록은 떠 있는 toolbar·canvas·note 창을 함께 사용한다. 아래 `layout`은 영역의 포함·읽기 순서를 나타내며 코드 모듈·OS 창 계층을 결정하지 않는다. 가로 숫자는 상대 배분 가중치이며 픽셀·정확한 화면 비율을 보장하지 않는다. 세로 크기는 DSL에 지정하지 않는다.

| 공통 항목 | 목표 동작 |
|---|---|
| 기록 상태 | Starting·기록 중·일시정지·종료·오류를 텍스트와 아이콘으로 표시. 빨간색만으로 녹음을 구분하지 않음 |
| 입력 대상 | 기록 중 activeScene, 열람 중 inspectedScene, 노트의 명시적 대상, 종료 후 보완을 각각 표시. OS 맨 앞 창과 기록 대상을 혼동하지 않음 |
| 입력 확정 | 획·텍스트·노트의 임시 입력과 저장 완료를 구분. 저장 실패는 초안을 유지하고 실패 항목과 재시도 표시 |
| 키보드·IME | Tab 읽기 순서가 배치 순서와 일치. 한글 조합 중 도구 단축키·전역 명령이 문자를 가로채지 않음. Pause·Stop은 조합 종료 요청 뒤 committed/cancelled/blocked 응답 처리. blocked는 초안·실제 기록 상태 유지, 확정/취소 후 재시도. 창 닫기로 강제확정 금지 |
| 단축키 | 도구 단축키는 canvas 포커스에서만 동작. 입력창에서는 문자·편집 키를 우선. 전역 명령은 사용자 설정·충돌 검증 후 활성화. Esc는 선택/임시 작업 취소이며 기록 종료가 아님 |
| 접근성 | 모든 아이콘에 동작명·상태·비활성 이유 제공. VoiceOver에 캡처 이름·Scene 번호·시간·대상을 읽어 줌. 상태 경고는 요약만 알리고 오디오 미터·매 시간 갱신을 연속 낭독하지 않음 |
| 비마우스 경로 | Scene 목록·도형 목록·focus 좌표/위치 조정에 키보드 대안 제공. 텍스트 입력으로 같은 질문·요청을 표현할 수 있게 유지 |
| 로딩·중복 | 작업 영역에 실행 중·취소 가능 여부·결과를 표시. 시작·캡처·종료·컴파일·내보내기 중복 명령 차단. 전체 UI를 이유 없이 빈 화면으로 만들지 않음 |
| 빈값·오류 | 빈 목록은 다음 동작을 제공. 권한 거부·장치 소실·저장 실패·자산 누락·전사 부재를 구분. 빈 결과에 성공 배지를 붙이지 않음 |
| 크기·배율 | 기본 창은 큰 글자와 좁은 디스플레이에서 내부 스크롤·세로 전환. 주요 실행 버튼은 가로 스크롤 밖에 숨기지 않음. 부동 창은 화면 경계 안으로 복원 |
| 개인정보 | 캡처 대상·녹음 여부를 시작 전과 기록 중 확인 가능. prompt 자동 생성은 로컬 처리이며 외부 AI 자동 전송 UI를 두지 않음 |

기록 중 새 캡처, 판서·focus, 노트는 [SessionCoordinator 계약](system-design-to-be.md#contracts)을 사용한다. Paused의 모든 기록 입력은 읽기 전용이며 Resume 후 가능하다. 종료 후의 정정은 별도 보완 revision으로 저장한다. 종료 후 보완을 기록 시간에 수집된 원본처럼 보이지 않게 한다.

<a id="sessionlibrary"></a>
## 2. SessionLibrary

목적은 새 세션 시작과 로컬 저장본 선택이다. 앱 최초 실행·복구 건너뛰기·검토 종료 후 진입하는 기본 창이다. 주소 대신 `SessionLibrary` 식별자를 사용한다. [이동](navigation.md#start), [FR-08](system-design-to-be.md#requirements).

```layout
Screen V LibraryHeader, LibraryBody, LibraryFooter
LibraryHeader > AppName, NewSessionButton, SettingsButton
LibraryBody V SessionSearch, RecoveryNotice, SessionList, LibraryStatus
SessionList V SessionRows, EmptySessionAction
LibraryFooter > LocalStorageSummary, LibraryHelp
```

| 영역 | 표시·입력·결과 |
|---|---|
| LibraryHeader | 새 세션은 SessionSetup, 설정은 Settings를 연다 |
| SessionSearch·SessionRows | 이름·날짜·입력 모드·기록/보완 상태·생성 revision·경고 요약. 선택 후 SessionReview를 열며 자동 기록 재개 없음 |
| RecoveryNotice | 미완료 기록 수와 RecoveryPanel 열기. 나중에 처리해도 원본 유지 |
| LibraryStatus·LocalStorageSummary | 목록 로딩, 읽기 실패와 재시도, 저장 위치·용량 요약 |

빈 세션 목록은 **화면을 선택하고 첫 질문을 기록하세요**와 새 세션 버튼을 보여 준다. 읽기 실패와 빈 목록을 같은 상태로 표시하지 않는다. 좁은 창에서는 검색·설정 버튼을 세로로 배치하고 표의 보조 메타데이터를 행 안 두 번째 줄로 옮긴다. 키보드로 목록 이동·열기·검색 지우기가 가능하고 행 전체의 상태를 VoiceOver 이름에 포함한다.

<a id="sessionsetup"></a>
## 3. SessionSetup

목적은 첫 Scene, 의도 모드, 마이크 사용 여부를 확정하는 것이다. SessionLibrary의 새 세션으로 진입한다. 아직 녹음하지 않으며 Scene 하나가 준비되어야 Start가 가능하다. [이동](navigation.md#start), [FR-01·FR-02·FR-05·FR-09](system-design-to-be.md#requirements).

```layout
Screen V SetupHeader, SetupBody, SetupStatus, SetupActions
SetupHeader > SessionName, DraftBadge
SetupBody > CaptureSetup : 55, InputSetup : 45
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions > SaveDraftButton, CloseSetupButton, StartButton
```

좁은 창에서는 `CaptureSetup` 뒤 `InputSetup`을 적층한다.

```layout
Screen V SetupHeader, CaptureSetup, InputSetup, SetupStatus, SetupActions
SetupHeader V SessionName, DraftBadge
CaptureSetup V CaptureType, SelectCaptureButton, InitialScenePreview, CaptureMetadata, CapturePermission
InputSetup V IntentMode, MicrophoneChoice, MicrophoneStatus, TranscriptionChoice, SessionGoal
SetupActions V StartButton, SaveDraftButton, CloseSetupButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| CaptureType·SelectCaptureButton | 창·디스플레이·단일 디스플레이 영역. OS CapturePicker 또는 RegionOverlay를 열며 창 제목을 임의로 실시간 추적하지 않음 |
| InitialScenePreview·CaptureMetadata | 불변 이미지, 대상 이름·종류, 실제 캡처 시각, 크기·배율·영역. **기록 시작 전 캡처** 표시. 재선택 실패 때 기존 초안 유지 |
| IntentMode·SessionGoal | 질문·수정 요청·혼합의 명시적 선택과 선택적 전체 목적. 전사 없이도 텍스트로 의도를 입력할 수 있음 |
| MicrophoneChoice·MicrophoneStatus | 사용/미사용, 장치 선택·권한·레벨 점검. 미사용은 **텍스트 전용**. 오류 시 마이크 없이 시작을 명시적으로 선택 |
| TranscriptionChoice | 선택적 전사의 사용 가능·미설정·권한·로컬 모델 상태. 전사 불가가 마이크 녹음 가능과 같은 뜻이 아님을 구분 |
| SetupStatus·StartButton | 캡처/설정/기록 시작 준비 상태와 실패 이유. 첫 Scene 없는 Start는 비활성. Starting 중 중복 시작 방지 |

첫 캡처를 취소하면 Setup과 기존 초안을 복원한다. 캡처 권한 거부는 선택 영역 아래에 설정 이동·재시도를 둔다. preflight 결과만으로 OS picker를 막지 않고 실제 캡처 성공·검증 자산으로 Start 가능 여부를 결정한다. 마이크 권한 거부는 마이크 아래에 대체 입력을 둔다. 이름·목적의 한글 조합이 남아 있으면 Start 전에 확정한다. 시작 지연은 **마이크 준비 중** 등 실제 단계를 표시하며, 성공한 시각부터 기록 상태를 켠다. 시작 실패는 이 화면에 초안과 입력을 유지한다.

<a id="recordingtoolbar"></a>
## 4. RecordingToolbar

목적은 다른 앱을 보면서 기록 상태·대상·중요 명령을 확인하는 것이다. Starting에서 표시되어 기록·일시정지 동안 유지되고, 종료 뒤에는 SessionReview가 역할을 넘겨받는다. 창 닫기는 숨기기이며 기록 종료는 명시적인 Stop을 사용한다. [이동](navigation.md#recording), [FR-02·FR-03·FR-09](system-design-to-be.md#requirements).

```layout
Screen V ToolbarMain, ToolbarStatus
ToolbarMain > RecordingState, SessionClock, ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, PauseResumeButton, ReviewButton, StopButton
ToolbarStatus > OperationMessage, SaveState, FocusSummary
```

가로 공간이 부족하면 명령을 두 줄로 나누고 Stop·상태·Pause/Resume을 항상 노출한다.

```layout
Screen V EssentialControls, SecondaryControls, ToolbarStatus
EssentialControls > RecordingState, SessionClock, PauseResumeButton, StopButton
SecondaryControls > ActiveSceneLabel, MicrophoneIndicator, CaptureButton, CanvasButton, NoteButton, ReviewButton
ToolbarStatus V OperationMessage, SaveState, FocusSummary
```

| 영역 | 표시·입력·결과 |
|---|---|
| RecordingState·SessionClock | Starting·기록 중·일시정지, 세션 경과와 실제 녹음 상태. 세션 경과에는 Pause 구간도 남고, 수집·녹음 중단은 별도 명시 |
| ActiveSceneLabel | **기록 대상 Sxx: 제목**. OS 맨 앞 창·열람 장면과 별개. 누르면 해당 SceneCanvas를 앞으로 가져옴 |
| MicrophoneIndicator | 사용 안 함·녹음 중·일시정지·장치 오류. 낮은 레벨을 권한 실패와 혼동하지 않음 |
| CaptureButton·CanvasButton·NoteButton | 새 Scene 선택, 캔버스 열기, 노트 열기. 원본 앱 조작 중에는 CanvasButton을 **장면 설명 재개**로 표시하고 명시 실행 시 InteractionInterval 종료. 캡처 중 명령 중복 방지. 보조 창이 열려 있어도 기록 대상 불변 |
| PauseResumeButton·ReviewButton | Pause/Resume은 상태를 갱신. 기록 검토는 Pause 성공 후 읽기 전용 Review 열기 |
| StopButton | **종료 및 prompt 생성**. 수용한 입력 저장·종료 경과를 표시하고 중복 실행 차단 |
| SaveState·FocusSummary | 저장 중·저장됨·저장 실패, 지속 중인 focus 대상과 해제. focus가 오래 유지되면 경과 경고 |

Paused에서는 캡처·새 판서·노트를 비활성으로 표시하고 이유를 노출한다. Pause/Stop 처리 중에는 신규 제스처·캡처를 막지만 기존 편집기의 조합 종료 응답은 수용한다. blocked이면 **중단 미완료: 입력을 확정/취소 후 다시 시도**를 표시하고 초안·실제 기록 상태를 보존한다. 마이크 장치 소실·절전은 신규 sample 중지·gap·target 종료 뒤 paused를 표시하고 명시 Resume을 요구한다. 조합 blocked로 paused 전환을 끝내지 못하면 실제 세션 상태와 별도로 **오디오 중단/실패**를 표시하며 **녹음 중** 배지를 유지하지 않는다. 전역 단축키는 IME 조합을 가로채지 않고, 캡처 선택 도중 Esc는 선택만 취소한다. toolbar를 키보드로 찾아 활성화할 수 있어야 하며 drag handle에는 창 이동의 대안이 필요하다.

<a id="capturepicker"></a>
## 5. CapturePicker

목적은 OS가 제공하는 창·디스플레이 선택이다. Setup의 첫 선택 또는 기록 중 새 캡처에서 진입한다. 아래 블록은 제품이 구현할 custom picker 배치가 아니라 앱 앞뒤의 계약을 설명하는 **도식**이다. OS 버전·권한 상태에 따라 실제 모양과 취소 방식이 달라질 수 있다. [이동](navigation.md#start), [FR-01·FR-09](system-design-to-be.md#requirements).

```layout
Screen V OSSelectionSurface
OSSelectionSurface V OSManagedTargetChoices, OSManagedSelectionActions
```

`OSManagedTargetChoices`는 OS가 노출하는 선택 가능한 창·디스플레이이고, `OSManagedSelectionActions`는 선택 확정·취소다. 앱은 OS-owned UI의 세부 필드·단축키·정렬을 약속하지 않는다. 앱이 추가하는 캡처 방식·영역 옵션은 SessionSetup 또는 RecordingToolbar에서 고른다.

선택 전 앱은 보조 창을 숨기고 완료·취소 후 복원한다. 선택 성공만으로 Scene을 바꾸지 않고 실제 캡처·저장 성공 후 반영한다. 권한 거부·보호된 콘텐츠·사라진 창·디스플레이 변경은 호출 화면의 오류에 표시하고 이전 Scene을 유지한다. 영역 방식에서는 디스플레이 선택 성공 후 RegionOverlay를 연다. OS picker의 VoiceOver·키보드·다중 디스플레이 동작은 지원 OS에서 실증 검증한다.

<a id="regionoverlay"></a>
## 6. RegionOverlay

목적은 한 디스플레이 안의 정적인 캡처 영역을 확정하는 것이다. 디스플레이를 고른 뒤 해당 화면 위에만 열린다. 여러 디스플레이를 가로지르는 영역은 지원하지 않는다. [이동](navigation.md#recording), [FR-01](system-design-to-be.md#requirements).

```layout
Screen V RegionInstructions, DisplaySelectionSurface, RegionActions
RegionInstructions > SelectedDisplayName, SelectionHint
RegionActions > RegionDimensions, KeyboardAdjustHint, CancelRegionButton, ConfirmRegionButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| DisplaySelectionSurface | 한 장의 어두워진 화면 위에 선택 프레임을 겹쳐 표시. 드래그로 시작·조정, 핸들로 크기 조정. 디스플레이 범위 안으로 제한하고 좌표·크기는 결과 메타데이터에 저장 |
| RegionDimensions·KeyboardAdjustHint | 선택 크기·배율을 읽을 수 있게 표시. 키보드 이동·크기 조정과 필요 시 수치 입력 대안 제공 |
| RegionActions | 유효 영역만 확인 가능. Enter 확정·Esc 취소는 텍스트 조합 중 가로채지 않음. 선택 표시 자체가 최종 캡처에 들어가지 않게 처리 |

초기 빈 영역에는 **영역을 지정하세요**가 표시된다. 너무 작은 영역·선택 중 디스플레이 분리·배율 변경은 확정을 막고 재선택 또는 취소를 제공한다. 취소는 이전 activeScene과 창 배치를 복원한다. 큰 글자·작은 디스플레이에서는 도구 설명을 접어도 확정·취소·선택 크기는 노출한다. 정밀 키보드 선택과 overlay 제외 여부는 구현 검증 항목이다.

<a id="scenecanvas"></a>
## 7. SceneCanvas

목적은 캡처 원본과 시간 동기화된 판서·가리키기·종료 후 보완을 확인하는 것이다. toolbar·SessionReview의 Scene 선택으로 진입하며 NotePanel과 함께 열 수 있다. 캡처 이미지는 수정하지 않고 주석을 별도 층으로 표시한다. [이동](navigation.md#recording), [FR-01·FR-03·FR-06](system-design-to-be.md#requirements).

```layout
Screen V CanvasHeader, CanvasBody, CanvasFooter
CanvasHeader > InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
CanvasBody > SceneRail : 18, CanvasWorkArea : 64, AnnotationInspector : 18
SceneRail V SceneThumbnails, SceneSelectionStatus
CanvasWorkArea V AnnotationTools, SceneViewport, CanvasMessage
AnnotationInspector V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

폭이 좁으면 SceneRail을 상단 선택 스트립으로, inspector를 펼침 영역으로 옮긴다.

```layout
Screen V CanvasHeader, SceneStrip, AnnotationTools, SceneViewport, InspectorDisclosure, CanvasMessage, CanvasFooter
CanvasHeader V InspectedSceneTitle, ActiveSceneBadge, CanvasModeBadge, TargetThisSceneButton, NewCaptureButton, InteractSourceButton
SceneStrip > PreviousSceneButton, SceneSelector, NextSceneButton
InspectorDisclosure V SelectedObjectDetails, ObjectList, FocusControls
CanvasFooter > ZoomControls, CanvasTime, SaveState, SceneNoteButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| InspectedSceneTitle·ActiveSceneBadge | 열람 Sxx와 기록 대상 Syy를 함께 표시. 둘이 다르면 명시 경고. 대상 전환 버튼은 기록 중에만 사용하며 과거 Scene 열람 자체로 전환하지 않음 |
| CanvasModeBadge | **기록 입력**, **일시정지: 읽기 전용**, **종료 후 보완**. 종료 후 보완은 원래 시각의 기록과 다른 표식 사용 |
| InteractSourceButton | **원본 앱 조작**으로 캔버스를 숨기고 InteractionInterval 시작. toolbar의 **장면 설명 재개**로 구간 종료. 구간 교차 발화는 기존 target 자동 연결에서 제외. 노트 창 열기와 구분 |
| AnnotationTools | 선택·펜·화살표·사각형·텍스트·가리키기. 색·두께와 현재 도구를 텍스트로 확인. 변경은 CommitInput을 통해 확정 |
| SceneViewport | 원본 크기·좌표와 화면 표시 크기를 분리. 확대·축소·이동이 저장 좌표를 바꾸지 않음. 투명 주석 층과 focus를 원본 위에 표시 |
| AnnotationInspector·ObjectList | 주석 ID·종류·연결 시각·내용·선택 상태. 키보드 선택·삭제/정정과 텍스트 대안. 원본 주석은 추적 가능한 정정 정책 사용 |
| FocusControls | 현재 point 위치·대상·유지 경과·명시 해제. 오래된 focus 경고. 대상 전환·선택 주석 삭제·Pause·Stop 시 focus를 닫고 Resume은 target 해제로 시작 |
| CanvasTime·SceneNoteButton | 열람 시간과 기록 중 시간의 차이를 표시. 이 Scene 노트 열기는 NotePanel 대상만 설정하며 activeScene은 그대로 |

새 캡처 대기에는 기존 이미지 위에 처리 상태만 표시한다. 캡처 실패는 기존 Scene과 입력을 유지한다. 이미지 자산 누락은 검은 빈 캔버스 대신 Scene 메타데이터·누락 메시지·다시 확인·검토 이동을 표시한다. Scene이 없는 상태로 기록을 시작할 수 없지만 손상된 복구본은 이러한 오류 상태로 열 수 있다.

텍스트 도구는 조합 중 임시 문자열을 보이고 조합 완료·확정 후 이벤트를 저장한다. Esc는 임시 도형·입력을 취소하며 기록 종료가 아니다. 그리기 중 Pause·Stop 요청은 수용된 임시 입력의 확정/취소 결과를 기다린다. Canvas가 포커스되지 않았거나 NotePanel에서 타이핑할 때 도구 단축키를 실행하지 않는다. VoiceOver는 이미지의 Scene 이름·캡처 메타데이터, 주석 목록, focus 위치 설명을 통해 접근한다. 좌표만으로 의미를 전달하기 어려우면 대상 설명 텍스트를 함께 입력할 수 있다.

<a id="notepanel"></a>
## 8. NotePanel

목적은 발화 대신 또는 발화와 함께 의미를 입력하고, 종료 후 정보를 보완하는 것이다. toolbar·canvas·review에서 열린다. 이 창이 OS 맨 앞에 와도 기록 대상은 바뀌지 않는다. [이동](navigation.md#recording), [FR-04·FR-06](system-design-to-be.md#requirements).

```layout
Screen V NoteHeader, NoteTarget, NoteEditor, NoteHistory, NoteStatus, NoteActions
NoteHeader > NoteModeBadge, SessionTitle, HideNoteButton
NoteTarget > TargetScopeSelector, ExplicitSceneSelector, ViewTargetButton
NoteEditor V NoteIntent, NoteTextInput
NoteHistory V SavedNotes, SelectedNoteDetails
NoteActions > SaveNoteButton, ClearDraftButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| NoteModeBadge | 기록 입력·일시정지 읽기 전용·종료 후 보완을 명확히 구분 |
| TargetScopeSelector·ExplicitSceneSelector | 전체 세션 또는 특정 Sxx. **노트 대상**이라고 표시. 대상 변경 전 작성 중 초안을 확정/보존하며 기존 노트의 연결을 조용히 바꾸지 않음 |
| NoteIntent | 질문·수정 요청·맥락 선택. 세션의 기본 모드를 보여 주되 개별 메모의 실제 의도를 명시 가능 |
| NoteTextInput | 다중 행 한글 입력. Enter는 줄바꿈, 저장 명령은 별도. placeholder는 예시이며 입력 데이터로 저장하지 않음 |
| SavedNotes | 시간·대상·의도·recorded/postrecord 구분. 선택하여 내용 확인·허용된 정정. 기록 중 자동으로 다른 Scene 노트로 바뀌지 않음 |
| NoteStatus·SaveNoteButton | 저장 중·완료·실패, 빈 내용 검증, 단축키 안내. 성공 확인 전 초안을 지우지 않음 |

텍스트 전용 세션에서는 이 창을 첫 기록 창으로 함께 열어 마이크가 없어도 질문을 남길 수 있게 한다. 빈 노트 목록에는 대상과 예시를 제공한다. Paused에서는 에디터·저장·대상 변경 입력을 비활성으로 하고 기존 노트를 읽을 수 있다. 종료 후 보완은 보완 배지·작성 시각·연결 대상을 저장하고 prompt를 오래된 결과로 표시한다.

한글 조합 중 저장 단축키가 확정을 가로채지 않는다. 조합이 끝난 뒤 저장하고, 창 숨김·대상 변경·Stop은 미저장 초안 존재를 표시한다. 오류는 NoteStatus에 원인과 재시도를 제공한다. 폭이 좁으면 TargetScope와 Scene 선택을 세로로 배치하고 히스토리를 접어도 입력 대상·저장 상태는 유지한다. VoiceOver가 저장 성공을 한 번 알리고 입력 내용을 반복 낭독하지 않게 한다.

<a id="sessionreview"></a>
## 9. SessionReview

목적은 원본·시간축·미해결 참조를 검토하고 종료 후 보완하는 것이다. Paused 검토, Stop 후 자동 생성, 저장된 세션 열기, 복구 완료로 진입한다. [이동](navigation.md#pause-review), [FR-05·FR-06·FR-08](system-design-to-be.md#requirements).

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

좁은 창은 근거를 먼저 보여 주고 미해결 목록·전사를 아래에 적층한다.

```layout
Screen V ReviewHeader, ReviewStateBanner, ReviewSceneSelector, EvidenceViewer, ReviewTimeline, ReviewInspector, ReviewActions
ReviewHeader V SessionName, IntentModeSelector, RevisionState
EvidenceViewer V SelectedScenePreview, AudioPlayback, EventContext
ReviewTimeline V TimelineTracks, TimelinePosition
ReviewInspector V UnresolvedSummary, UnresolvedItems, TranscriptEditor, CorrectionActions, ExternalInputReview
ExternalInputReview V ExternalStageScope, ReviewedTextAndFrames, AudioSelectionAndExclusions, ReviewedRevisionStatus, RunReviewedExternalButton
ReviewActions V GeneratePromptButton, OpenPromptButton, AddCorrectionButton, ResumeRecordingButton, ReturnToLibraryButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| ReviewStateBanner | Paused 읽기 전용, 종료 후 보완 가능, 복구본·누락 범위, 컴파일·저장 오류, awaiting_input_review. artifact_status와 intent_status를 분리하여 기본 자료 성공과 자동 질문 생성 성공 구분 |
| IntentModeSelector·RevisionState | 질문·요청·혼합 모드와 입력 revision·prompt revision. 모드 변경은 기록 종료 후 보완으로 반영하고 재생성 필요 표시 |
| ReviewSceneRows·SelectedScenePreview | Scene ID·캡처 시각·대상 메타데이터·실제 파일 존재. 선택은 열람이며 activeScene을 바꾸지 않음 |
| AudioPlayback | 음성 파형/구간·재생·속도·누락·마이크 미사용. 재생이 기록 재개를 의미하지 않음. 키보드 조작과 시간 텍스트 제공 |
| EventContext·TimelineTracks | Scene 전환·판서·focus·노트·오디오를 같은 시점에서 확인. 캡처 SelectionInterval·원본 앱 조작 InteractionInterval·종료 결과와 교차 발화의 후보·미해결 표시. Pause 경계·수용된 순서를 표시하며 wall time과 세션 시간을 구분 |
| UnresolvedItems | 모호한 '여기', 연결되지 않은 발화·Scene, 전사 실패·저신뢰 구간 등 실제 판정 항목. 선택 시 관련 시간·Scene·근거가 보임 |
| TranscriptEditor·CorrectionActions | 전사 없음/대기/실패/일부/완료 구분. 원문과 수동 전사·정정·미확인 유지의 결과를 보여 줌. 파생 전달 이미지의 마스킹과 남은 텍스트 인용도 함께 확인. 음성 내용 자동 추정 금지 |
| AddCorrectionButton | 종료 뒤 NotePanel 또는 SceneCanvas의 보완 모드 열기. 기존 기록은 보존하고 revision 변경 |
| ExternalInputReview | 실제 외부 제공자·단계·전송 범위 표시. 외부 ASR은 선택 원음과 제외 구간 재생, 의미 정리는 검토한 텍스트·마스킹 프레임 확인. 이미지 마스킹으로 음성도 가려졌다고 안내하지 않음 |
| ReviewedRevisionStatus·RunReviewedExternalButton | 고정 ReviewInputRevision·scope hash에 대해 **검토한 자료로 외부 처리** 실행. 검토 뒤 변경 시 재검토 요구. 제공자 설정만으로 자동 업로드하지 않음 |
| GeneratePromptButton·OpenPromptButton | 종료 직후 자동 생성 경과, 보완 후 다시 생성. 현재 revision 결과만 최신으로 표시 |

Paused에서는 오디오 재생·타임라인·Scene 열람만 가능하다. 보완 입력과 모드 변경은 비활성이며 **기록 종료 후 보완** 또는 Resume을 제공한다. 종료 후 원음이 없거나 전사가 없더라도 메모·Scene·주석으로 생성할 수 있다. 다만 질문/요청 근거가 없으면 **입력 부족: 자동 질문 생성 미완료**와 보완 동작을 보여 준다. 기본 근거 출력은 partial로 확인할 수 있으나 빈 prompt를 완료 결과처럼 표시하지 않는다.

미해결 항목은 정정·확인·미확인 유지를 지원하되 확인하지 않은 내용을 확정된 문장으로 바꾸지 않는다. 같은 snapshot·review revision에서 전사가 완료되면 같은 generation의 다음 단계로 진행한다. 사용자 보완으로 revision이 바뀌면 이전 결과는 최신 성공으로 표시하지 않고 새 revision으로 다시 생성한다. 자동 생성 실패는 원본 검토를 막지 않고 오류·재시도·텍스트 보완을 제공한다. 스크린리더는 시간축의 시각 막대 대신 정렬된 이벤트 목록으로 같은 근거에 접근할 수 있어야 한다.

Stop 직후 자동 생성은 로컬 기본 자료와 준비된 로컬 엔진까지만 처리한다. 외부 단계는 검토 대기 상태이며 ready나 실패가 아니다. intent_status의 generated·user_authored·unavailable·insufficient를 읽을 수 있게 표시한다. 엔진 없음·실패는 unavailable/partial이고, 질문 근거가 부족하면 insufficient/partial이다. 검토 후 전송 이력과 실제 범위를 남기고, 이후 보완이 이전 전송을 취소하지는 않는다는 사실을 표시한다.

<a id="promptpreview"></a>
## 10. PromptPreview

목적은 전달할 prompt 텍스트와 실제 첨부 자산을 함께 확인하는 것이다. 현재 revision의 ready/partial 결과 또는 저장된 생성본 열기로 진입한다. 외부 AI 전송 버튼은 없다. [이동](navigation.md#delivery), [FR-05·FR-06·FR-07·FR-09](system-design-to-be.md#requirements).

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

좁은 창에서는 PromptTextPane 뒤 AttachmentPane을 적층한다. prompt 본문은 줄바꿈·텍스트 선택을 지원하고 가로 화면 이동을 요구하지 않는다. 자산 목록이 아래로 내려가도 DeliveryNotice와 복사·내보내기 상태는 본문 가까이에 유지한다.

| 영역 | 표시·입력·결과 |
|---|---|
| PromptRevision·PromptStateBanner | 현재 입력과 생성본 revision, 최신·다시 생성 필요·경고·실패. 오래된 생성본을 열어도 최신처럼 표시하지 않음 |
| PromptSections·PromptTextView | 선택 profile의 DeliveryProjection으로 만든 목적·질문/수정 요청·Scene 참조·시간 근거·미확인 텍스트. copy/export와 같은 가림·metadata 제외 적용. 원본 수정은 Review로 돌아가 수행 |
| DeliveryProfile | 기본 **전달용**과 명시적인 **원본 포함** 선택. 전달용은 합성 frames와 prompt/context/evidence를 포함하고 원음·원본 이미지·PID·창 제목·로컬 절대 경로는 제외. 원본 포함에는 마스킹 전 자료가 들어감을 표시 |
| ActualAssetRows | 선택 프로필의 실제 합성 이미지·파일 식별자·상대 참조·포함/제외 이유. point/rect focus의 target ID·유효 시각·강조를 합성 이미지에서 확인. 원본 자산은 로컬 근거 보기로 구분. 자리표시자를 실제 첨부처럼 표시하지 않음 |
| UnresolvedReferenceRows | 누락·미확인 참조의 수와 대상. 선택하면 Review의 근거로 복귀 |
| DeliveryNotice | **복사는 텍스트만 복사합니다. 이미지·오디오는 사용할 AI에 별도로 첨부하세요.** |
| CopyPromptButton | Preview·export와 같은 DeliveryProjection의 현재 허용 텍스트만 복사. 성공 알림도 **prompt 텍스트 복사됨**으로 한정 |
| RevealAssetsButton·ExportBundleButton | 실제 자산을 Finder로 보기, 검증된 번들 생성. 상대 경로와 manifest 규칙은 전달 계약 사용 |
| ExportResult | 진행·성공한 실제 경로·포함 파일·오류. Finder 열기 동작은 경로 검증 후 활성화 |

입력 변경·수동 전사·모드 수정·새 보완으로 결과가 무효화되면 **다시 생성 필요**를 상단과 행동 영역에 표시한다. 최신본 복사·prompt 포함 내보내기는 현재 revision의 성공 결과가 준비될 때까지 비활성이다. 자산 누락·쓰기 실패·저장 공간 부족은 해당 파일과 재시도·다른 위치를 보여 준다. 내보내기 성공을 AI가 파일을 읽었거나 질문을 처리했다는 뜻으로 표현하지 않는다. VoiceOver는 복사 성공·번들 경로를 한 번 알리며 긴 prompt 전체를 자동 낭독하지 않는다.

<a id="settings"></a>
## 11. Settings

목적은 로컬 입력·권한·전사·저장·단축키 환경을 확인하는 것이다. Library·Setup·Toolbar에서 열고 닫으면 호출 화면으로 돌아간다. [이동](navigation.md#recovery-settings), [FR-09](system-design-to-be.md#requirements).

```layout
Screen V SettingsHeader, SettingsBody, SettingsStatus, SettingsActions
SettingsHeader > SettingsTitle, CurrentSessionNotice
SettingsBody > SettingsSections : 25, SelectedSettings : 75
SettingsSections V PermissionsSection, AudioSection, TranscriptionSection, StorageSection, KeyboardSection
SelectedSettings V SectionExplanation, SettingControls, AvailabilityStatus, SettingHelp
SettingsActions > RestoreDefaultsButton, CloseSettingsButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| PermissionsSection | 화면 캡처·마이크의 OS 권한 상태와 시스템 설정 열기. 앱에서 승인한 것처럼 권한 값을 바꾸지 않음 |
| AudioSection | 마이크 장치·사용 기본값·점검. 장치가 없을 때 명확한 없음과 텍스트 전용 설명 |
| TranscriptionSection | 선택적 로컬 전사 설정·모델/언어·사용 불가/다운로드 필요·실패 상태. 외부 제공자·의미 정리 제공자를 별도로 설정하고 가능한 전송 범위·키 관리·정책 표시. 실제 세션 업로드는 Review의 입력 검토와 명시 실행 후 가능 |
| StorageSection | 로컬 저장 위치·사용량·내보내기 기본 위치, 읽기/쓰기 점검 결과. 기존 세션 이동은 적용 계약과 별도 표시 |
| KeyboardSection | 단축키·충돌·활성 범위. IME·텍스트 입력 보호와 키보드 접근 경로 안내 |
| CurrentSessionNotice·AvailabilityStatus | 현재 세션에 적용됨 또는 **다음 세션부터**. 기록 중 변경할 수 없는 설정은 이유와 함께 비활성 |

좁은 창에서는 왼쪽 목록을 상단 선택 메뉴로 바꾸고 설정 본문을 세로로 표시한다. 시스템 설정에서 돌아오면 실제 권한을 재확인하며, 권한이 없는데 성공 표시하지 않는다. 모델 미설정·전사 실패가 앱 전체의 기록 불가능으로 보이지 않게 음성 녹음과 전사 가용성을 분리한다. 마이크를 허용하지 않아도 화면+노트 흐름을 계속 사용할 수 있다. 저장 실패·장치 목록 로딩 오류는 해당 섹션에만 오류·재시도를 배치한다.

<a id="recoverypanel"></a>
## 12. RecoveryPanel

목적은 종료되지 않은 세션에서 검증된 로컬 자료를 복구하고 누락 범위를 확인하는 것이다. 앱 기동 가드 또는 Library의 복구 알림에서 진입한다. 복구 성공은 녹음을 자동 재개하지 않고 Review를 연다. [이동](navigation.md#recovery-settings), [FR-08](system-design-to-be.md#requirements).

```layout
Screen V RecoveryHeader, RecoveryBody, RecoveryStatus, RecoveryActions
RecoveryHeader V RecoveryTitle, RecoveryExplanation
RecoveryBody > RecoverableSessions : 35, RecoveryDetails : 65
RecoverableSessions V RecoverySessionRows, RecoveryEmptyState
RecoveryDetails V LastVerifiedPosition, AvailableAssets, MissingRanges, RecoveryConsequences
RecoveryActions > RecoverSelectedButton, InspectFailureButton, LaterButton
```

| 영역 | 표시·입력·결과 |
|---|---|
| RecoverySessionRows | 세션 이름·최종 저장 시각·비정상 종료 표시·복구 가능 상태. 키보드로 선택 가능 |
| LastVerifiedPosition·AvailableAssets | 마지막 검증 시점, Scene·이벤트·오디오의 실제 존재 범위 |
| MissingRanges·RecoveryConsequences | 누락·불완전 구간, 복구 뒤 읽기/보완 가능 범위. 손실이 없다고 추정하지 않음 |
| RecoveryStatus | 검사·복구 중, 실패 원인·재시도. 한 세션 실패가 다른 세션의 선택을 막지 않음 |
| RecoverSelectedButton·LaterButton | 선택본 Recover, 원본을 유지한 채 Library로 이동. 복구 실패 원본을 자동 삭제하지 않음 |

복구 대상이 사라졌으면 빈 상태와 Library로 돌아가기를 표시한다. 복구 검사 실패는 빈 목록 성공으로 표현하지 않는다. 폭이 좁으면 세션 목록 뒤 상세를 적층한다. 복구 경과와 완료는 상태 알림으로 읽어 주고, 누락 항목을 순서 있는 목록으로 제공한다. 복구 뒤 prompt는 실제 복구 revision으로 다시 생성해야 하며 과거 성공본을 최신으로 취급하지 않는다.

## 13. 정적 검토와 미확인 범위

각 `layout`은 한 루트의 트리로 작성하고, 동일 블록에서 컨테이너 중복 정의·다중 부모·순환·가로 가중치 초과가 없도록 검토한다. Navigation의 11개 화면과 본문의 11개 식별자를 대조한다. 본문은 목표 상호작용·오류·빈값·권한·키보드·좁은 창 상태를 정의하며 실제 앱 반응형 또는 접근성 검증의 완료 기록이 아니다.

구현 시 우선 검증할 경로는 첫 캡처 권한 거부, 캡처 중 창 소실, 텍스트 전용 시작, 마이크 시작 실패·장치 소실, 한글 조합 중 Stop, Pause 검토와 보완 금지, 과거 Scene 열람과 명시적 대상 전환, focus 장기 유지, 전사 없음·실패, 보완 중 이전 컴파일 완료, 자산 누락·내보내기 실패, 비정상 종료 복구다. 검증 수용 조건은 [핵심 검증 계획](system-design-to-be.md#verification)을 따른다.

2026-10-31 정적 확인: 15개 layout 블록의 단일 루트·부모 중복·순환·컨테이너 중복·가로 가중치와 11개 화면 앵커를 검사하여 구조 오류 0건이었다. UI 두 파일의 링크·앵커 검사는 FAIL 0, WARN 0이다. 렌더러 변환과 실제 창 크기·VoiceOver·IME 동작 검증은 이 검사에 포함하지 않았다.
