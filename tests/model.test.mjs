import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../web/model.js', import.meta.url), 'utf8');
const sandbox = {window: {}};
vm.runInNewContext(source, sandbox, {filename: 'model.js'});
const M = sandbox.window.CueviaModel;
const makeScene = (id = 'scene-one', extra = {}) => ({
  id, title: '실제 캡처한 화면', app: '작업 프로그램', capturedAt: 42, width: 1000, height: 800, note: '', ...extra
});
const makeSession = options => Object.assign(M.createSession({title: '실제 화면 요청', mode: 'mixed'}), options);
const section = (prompt, name) => {
  const heading = `## ${name}\n`, start = prompt.indexOf(heading);
  assert.notEqual(start, -1, `missing section ${name}`);
  const rest = prompt.slice(start + heading.length), end = rest.indexOf('\n## ');
  return end < 0 ? rest : rest.slice(0, end);
};
const utterance = (id, text, extra = {}) => ({id, kind: 'speech', text, t: 1, sceneId: 'scene-one', ...extra});
const count = (text, fragment) => text.split(fragment).length - 1;

test('new sessions have no drawing schema, examples, or invented semantic requests', () => {
  const session = M.createSession({title: '  실제 요청\u0000  ', project: ' 작업 ', mode: 'invalid', mic: false});
  assert.match(session.id, /^session-/);
  assert.equal(session.title, '실제 요청');
  assert.equal(session.project, '작업');
  assert.equal(session.mode, 'mixed');
  assert.equal(session.mic, false);
  assert.equal(session.status, 'review');
  assert.equal(session.duration, 0);
  assert.equal(session.elapsedMs, 0);
  assert.equal(session.runMode, null);
  assert.equal(session.runState, 'ready');
  assert.equal(session.runVersion, 0);
  assert.equal(session.sceneSelections.length, 0);
  assert.equal(Object.hasOwn(session, 'previousRunId'), false);
  assert.equal(session.scenes.length, 0);
  assert.equal(session.transcript.length, 0);
  assert.equal(session.notes, '');
  assert.equal(session.prompt, '');
  assert.equal(Object.hasOwn(session, 'annotationHistoryVersion'), false);
  assert.equal(Object.hasOwn(session, 'annotationEvents'), false);
  assert.deepEqual(Object.keys(M).sort(), ['createSession', 'generatePrompt', 'markNumber', 'promptScenes', 'supportedAnnotations']);
  const prompt = M.generatePrompt(session);
  assert.match(prompt, /발화·메모 없음/);
  assert.match(section(prompt, '화면'), /캡처된 화면 없음/);
  assert.match(section(prompt, '타임라인'), /기록 없음/);
  assert.doesNotMatch(prompt, /## 질문|## 변경 요청|## 유지 조건|판서|annotation/);
});

test('prompt scope keeps scene evidence with stable active ordinals and ignores old freehand drawings', () => {
  const scenes = Array.from({length: 10}, (_, i) => makeScene('scene-' + (i + 1)));
  scenes[0].annotations = [{id: 'old-hidden', hidden: true, type: 'point', x: .1, y: .2}];
  scenes[2].annotations = [{id: 'old-visible', type: 'pen', points: [{x: .2, y: .3}]}];
  scenes[3].note = '장면 메모';
  scenes[6].deletedAt = 100;
  const session = makeSession({scenes, transcript: [
    utterance('u-used', '화면 설명', {sceneId: 'scene-6', candidates: [{sceneId: 'scene-9'}]}),
    utterance('u-deleted', '삭제한 설명', {sceneId: 'scene-7', candidates: [{sceneId: 'scene-2'}]})
  ], recordings: [{id: 'audio-id', transcriptionStatus: 'complete', contexts: [{sceneId: 'scene-10', tMs: 1000}]}]});
  const original = JSON.stringify(session);
  assert.deepEqual(Array.from(M.promptScenes(session), scene => scene.id), ['scene-4', 'scene-6', 'scene-9', 'scene-10']);
  const prompt = M.generatePrompt(session), images = section(prompt, '화면');
  for (const alias of ['S04', 'S06', 'S08', 'S09']) assert.equal(count(images, `images/${alias}.png`), 1);
  for (const alias of ['S01', 'S02', 'S03', 'S05', 'S07']) assert.ok(!images.includes(`images/${alias}.png`));
  assert.doesNotMatch(prompt, /삭제한 설명|old-hidden|old-visible/);
  assert.equal(JSON.stringify(session), original);
});

test('without scene evidence only the latest active capture is introduced', () => {
  const session = makeSession({scenes: [makeScene('first', {annotations: [{id: 'old-stroke', type: 'pen'}]}), makeScene('last'), makeScene('deleted', {deletedAt: 123})]});
  assert.deepEqual(Array.from(M.promptScenes(session), scene => scene.id), ['last']);
  const prompt = M.generatePrompt(session);
  assert.match(prompt, /S02: `images\/S02\.png`/);
  assert.doesNotMatch(prompt, /images\/S01\.png|images\/S03\.png|old-stroke/);
});

test('image introductions use a path once and readable source names without process metadata', () => {
  const scene = makeScene('scene-one', {source: {kind: 'program', name: '디자인 도구', pid: 31415, windowId: 27182, title: '설정 — 실제 작업'}});
  const prompt = M.generatePrompt(makeSession({scenes: [scene]}));
  assert.match(section(prompt, '화면'), /S01: `images\/S01\.png` · 실제 캡처한 화면 · 작업 프로그램 · 설정 — 실제 작업 · 1000×800 px/);
  assert.equal(count(prompt, 'images/S01.png'), 1);
  assert.equal(count(prompt, 'session.json'), 1);
  assert.doesNotMatch(prompt, /scene-one|31415|27182|PID|windowId|annotation-events/);
});

test('legacy drawing data is opaque and stays untouched even when its old history is malformed', () => {
  const session = makeSession({
    scenes: [makeScene('scene-one', {annotations: [{id: 'legacy-mark', type: 'text', text: '삭제된 기능의 텍스트', x: .2, y: .3, hidden: true}]})],
    annotationHistoryVersion: 42, annotationEvents: {invalid: 'old-history'},
    transcript: [utterance('u-original', '현재 화면 설명입니다.', {target: 'legacy-mark', targetDescription: '삭제된 기능의 대상'})]
  });
  const original = JSON.stringify(session), prompt = M.generatePrompt(session);
  assert.equal(JSON.stringify(session), original);
  assert.match(prompt, /현재 화면 설명입니다/);
  assert.doesNotMatch(prompt, /legacy-mark|old-history|삭제된 기능|좌표|판서|표시|숨김|annotation/);
});

test('raw speech stays intact once without keyword classification or inferred cancellation', () => {
  const texts = [
    '이 버튼이 두 번 작동하는 이유가 뭔가요?',
    '이 버튼을 오른쪽 위로 옮겨주세요. 기존 저장 동작은 유지해주세요.',
    '아니, 링크를 왼쪽으로 옮겨 주세요.',
    '설정정보 표시를 추가해 주세요.'
  ];
  const session = makeSession({scenes: [makeScene()], transcript: texts.map((text, i) => utterance('u-' + i, text, {t: i + 1}))});
  const original = JSON.stringify(session), prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  texts.forEach(text => assert.equal(count(timeline, text), 1));
  assert.doesNotMatch(prompt, /## 질문|## 변경 요청|## 유지 조건|## 반영한 정정|로 정정됨/);
  assert.equal(JSON.stringify(session), original);
});

test('explicit replacement links retain both raw records using short references', () => {
  const session = makeSession({scenes: [makeScene()], transcript: [
    utterance('u-original', '버튼을 오른쪽으로 옮겨 주세요.'),
    utterance('u-edited', '버튼을 중앙으로 옮겨 주세요.', {t: 2, replaces: 'u-original', unresolved: true})
  ]});
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.match(timeline, /말 1 \[말 2로 정정됨\].*버튼을 오른쪽으로 옮겨 주세요/);
  assert.match(timeline, /말 2 \[연결\? · 말 1 정정\].*버튼을 중앙으로 옮겨 주세요/);
  assert.doesNotMatch(prompt, /u-original|u-edited/);
});

test('notes retain complete wording once with unknown timing and ignore old annotation text', () => {
  const scene = makeScene('scene-one', {annotations: [{id: 'inline-note', type: 'text', text: '이전 판서 텍스트'}], note: '읽는 순서가 헷갈립니다.\n제목의 위치를 위로 옮겨 주세요.'});
  const session = makeSession({scenes: [scene], notes: '결제 화면에서 확인한 맥락입니다.'});
  const prompt = M.generatePrompt(session), notes = section(prompt, '메모');
  assert.match(notes, /시각 미상/);
  assert.ok(notes.includes(JSON.stringify(scene.note)));
  assert.ok(notes.includes(JSON.stringify(session.notes)));
  assert.equal(count(prompt, '제목의 위치를 위로 옮겨 주세요.'), 1);
  assert.doesNotMatch(prompt, /이전 판서 텍스트/);
  assert.doesNotMatch(section(prompt, '타임라인'), /메모/);
});

test('incomplete audio and timing limitations use grouped short aliases', () => {
  const session = makeSession({recordings: [
    {id: 'raw-estimated', transcriptionStatus: 'complete', metadata: {timing: 'estimated'}},
    {id: 'raw-failed', transcriptionStatus: 'failed', metadata: {timing: 'estimated', discontinuities: [{tMs: 4000, reason: 'track-muted'}]}},
    {id: 'raw-pending', transcriptionStatus: 'pending'}
  ], originalRecordings: [{id: 'raw-original'}]});
  const prompt = M.generatePrompt(session), caveats = section(prompt, '확인 필요');
  assert.match(caveats, /A1, A2: 음성 시각 추정/);
  assert.match(caveats, /A2: 입력 중단\/길이 차이로 화면 연결 미확정/);
  assert.match(caveats, /A2: 전사 실패, 원음 보존/);
  assert.match(caveats, /A3, A4: 전사 미완료, 원음 보존/);
  assert.match(caveats, /발화·메모 없음/);
  assert.doesNotMatch(prompt, /raw-estimated|raw-failed|raw-pending|raw-original|track-muted/);
});

function switchingSession() {
  return makeSession({scenes: [makeScene('scene-one', {capturedAt: 10}), makeScene('scene-two', {capturedAt: 15})], recordings: [{
    id: 'audio-private-id', startMs: 100000, endMs: 120000, transcriptionStatus: 'complete', contexts: [
      {tMs: 100000, sceneId: 'scene-one', targetId: 'old-target-a'},
      {tMs: 101000, sceneId: 'scene-one', targetId: 'old-target-b'},
      {tMs: 103000, sceneId: 'scene-two', targetId: null},
      {tMs: 108000, sceneId: 'scene-one', targetId: null}
    ]
  }], transcript: []});
}

test('audio origin and actual scene switches reconstruct segments that span two screens', () => {
  const session = switchingSession();
  session.transcript.push(utterance('u-span', '두 화면에 걸친 설명입니다.', {audioId: 'audio-private-id', startMs: 102000, endMs: 110000, t: 102, unresolved: false}));
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.match(prompt, /시간: 첫 녹음 시작 기준/);
  assert.match(timeline, /\[녹음 전 -01:30\.000\] 캡처 S01/);
  assert.match(timeline, /\[00:00\.000\] 화면 선택 S01/);
  assert.match(timeline, /\[00:02\.000–00:10\.000\] 말 1 \[연결\?\] \(S01; 후보 S01\/S02\)/);
  assert.match(timeline, /\[00:03\.000\] 화면 선택 S02/);
  assert.match(timeline, /\[00:08\.000\] 화면 선택 S01/);
  assert.ok(timeline.indexOf('말 1') < timeline.indexOf('[00:03.000]'));
  assert.equal(count(prompt, 'images/S02.png'), 1, 'context-only scene must be included');
  assert.doesNotMatch(prompt, /target|표시 해제|00:01\.000/);
});

test('same-scene legacy target changes do not revive removed drawing ambiguity', () => {
  const session = switchingSession();
  session.transcript.push(utterance('u-single-screen', '화면 설명입니다.', {audioId: 'audio-private-id', startMs: 100500, endMs: 103000, t: 100.5, unresolved: true, target: null, candidates: [
    {tMs: 100000, sceneId: 'scene-one', targetId: 'old-target-a'},
    {tMs: 101000, sceneId: 'scene-one', targetId: 'old-target-b'}
  ]}));
  const timeline = section(M.generatePrompt(session), '타임라인');
  assert.match(timeline, /\[00:00\.500–00:03\.000\] 말 1 \(S01\): "화면 설명입니다\."/);
  assert.equal(count(timeline, '화면 선택 S01'), 2);
  assert.doesNotMatch(timeline, /연결\?|후보|old-target/);
});

test('candidate-only scenes and genuine unknown timing remain explicit without raw IDs', () => {
  const session = makeSession({scenes: [makeScene(), makeScene('scene-two')], transcript: [
    utterance('private-utterance', '이 부분을 확인해 주세요.', {sceneId: 'missing-scene', unresolved: true, timingUncertain: true, timingAdjusted: true, t: undefined, candidates: [{sceneId: 'scene-two', tMs: 3000}]})
  ]});
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.match(prompt, /S02: `images\/S02\.png`/);
  assert.match(timeline, /\[시각 미상\] 말 1 \[연결\? · 시간\? · 시각 보정\] \(화면\?; 후보 S02\)/);
  assert.doesNotMatch(prompt, /missing-scene|private-utterance|images\/S01\.png/);
});

test('untimed records stay untimed and records without IDs still get distinct short references', () => {
  const session = makeSession({scenes: [makeScene('scene-one', {capturedAt: undefined})], transcript: [
    utterance(undefined, '첫 기록', {t: undefined}), utterance(undefined, '둘째 기록', {t: undefined})
  ]});
  const timeline = section(M.generatePrompt(session), '타임라인');
  assert.match(timeline, /\[시각 미상\] 캡처 S01/);
  assert.match(timeline, /\[시각 미상\] 말 1 \(S01\): "첫 기록"/);
  assert.match(timeline, /\[시각 미상\] 말 2 \(S01\): "둘째 기록"/);
  assert.doesNotMatch(timeline, /00:00/);
});

test('unconverted original metadata supplies the audio origin and older scene selections without mutation', () => {
  const session = makeSession({scenes: [makeScene('earlier'), makeScene('latest')], originalRecordings: [{
    id: 'original-private-id', transcriptionStatus: 'pending', metadata: {startMs: 50000, endMs: 55000, contexts: [{tMs: 50000, sceneId: 'earlier'}]}
  }]});
  const original = JSON.stringify(session);
  assert.deepEqual(Array.from(M.promptScenes(session), scene => scene.id), ['earlier']);
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.match(prompt, /시간: 첫 녹음 시작 기준/);
  assert.match(prompt, /images\/S01\.png/);
  assert.doesNotMatch(prompt, /images\/S02\.png|original-private-id/);
  assert.match(timeline, /\[00:00\.000\] 녹음 A1 시작/);
  assert.match(timeline, /\[00:00\.000\] 화면 선택 S01/);
  assert.match(timeline, /\[00:05\.000\] 녹음 A1 끝/);
  assert.equal(JSON.stringify(session), original);
});

test('screen selection clearing survives and missing audio flags inherit actual discontinuities', () => {
  const session = makeSession({scenes: [makeScene()], recordings: [{
    id: 'audio-gap', startMs: 10000, endMs: 20000, transcriptionStatus: 'complete', contexts: [
      {tMs: 10000, sceneId: null}, {tMs: 11000, sceneId: 'scene-one'}
    ], metadata: {discontinuities: [{tMs: 12000, reason: 'track-muted'}]}
  }], transcript: [
    utterance('speech-gap', '음성 설명', {audioId: 'audio-gap', startMs: 13000, endMs: 14000, t: 13}),
    utterance('typed', '입력한 메모', {kind: 'note', t: 15})
  ]});
  const timeline = section(M.generatePrompt(session), '타임라인');
  assert.match(timeline, /\[00:00\.000\] 화면 선택 해제/);
  assert.match(timeline, /말 1 \[연결\? · 시간\?\] \(S01\): "음성 설명"/);
  assert.match(timeline, /메모 2 \(S01\): "입력한 메모"/);
});

test('explicit new runs use supplied mode and version with an independent zeroed clock', () => {
  const previous = makeSession({scenes: [makeScene()], elapsedMs: 123456, duration: 123.456, transcript: [utterance('old', '이전 버전')]});
  const original = JSON.stringify(previous);
  const next = M.createSession({runMode: 'text', runVersion: 2, previousRunId: '  ' + previous.id + '  ', runState: 'stopped', elapsedMs: 123456, duration: 123.456});
  assert.equal(next.runMode, 'text');
  assert.equal(next.runVersion, 2);
  assert.equal(next.previousRunId, previous.id);
  assert.equal(next.runState, 'ready');
  assert.equal(next.elapsedMs, 0);
  assert.equal(next.duration, 0);
  assert.equal(next.mic, false);
  assert.equal(next.scenes.length, 0);
  assert.equal(next.transcript.length, 0);
  assert.equal(next.sceneSelections.length, 0);
  assert.notEqual(next.id, previous.id);
  assert.equal(JSON.stringify(previous), original);
  const audio = M.createSession({runMode: 'audio', runVersion: 3});
  assert.equal(audio.runMode, 'audio');
  assert.equal(audio.runVersion, 3);
  assert.equal(audio.mic, true);
  for (const runVersion of [undefined, 0, -1, 1.5, '2', Number.MAX_SAFE_INTEGER + 1]) {
    const draft = M.createSession({runMode: 'invalid', runVersion});
    assert.equal(draft.runMode, null);
    assert.equal(draft.runVersion, 0);
  }
  assert.equal(M.createSession().runVersion, 0, 'the model never advances a global run version');
});

test('text runs merge raw timed notes with scene selections and create no audio labels', () => {
  const session = makeSession({runMode: 'text', runState: 'stopped', runVersion: 2, elapsedMs: 7000, duration: 7,
    scenes: [makeScene('first', {capturedAt: 0}), makeScene('second', {capturedAt: 2}), makeScene('deleted', {deletedAt: 1}), makeScene('unused-audio')],
    sceneSelections: [
      {tMs: 0, sceneId: null}, {tMs: 500, sceneId: 'first'}, {tMs: 1000, sceneId: 'first'},
      {tMs: 1500, sceneId: null}, {tMs: 3000, sceneId: 'second'}, {tMs: 4000, sceneId: 'deleted'}, {tMs: 5000, sceneId: 'first'}
    ],
    transcript: [utterance('typed-one', '제목을 오른쪽으로 옮겨 주세요. 기존 동작은 유지해 주세요.', {kind: 'note', t: 3.25, sceneId: 'second'})],
    recordings: [{id: 'stale-audio', startMs: 9000, endMs: 10000, transcriptionStatus: 'failed', contexts: [{tMs: 9000, sceneId: 'unused-audio'}]}]
  });
  const original = JSON.stringify(session);
  assert.deepEqual(Array.from(M.promptScenes(session), scene => scene.id), ['first', 'second']);
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.equal(count(prompt, 'v2 · 기록'), 1);
  assert.match(prompt, /시간: 기록 시작 기준\. 메모는 원문입니다/);
  assert.match(timeline, /\[00:00\.000\] 화면 선택 해제/);
  assert.match(timeline, /\[00:00\.500\] 화면 선택 S01/);
  assert.match(timeline, /\[00:01\.500\] 화면 선택 해제/);
  assert.match(timeline, /\[00:03\.000\] 화면 선택 S02/);
  assert.match(timeline, /\[00:03\.250\] 메모 1 \(S02\): "제목을 오른쪽으로 옮겨 주세요\. 기존 동작은 유지해 주세요\."/);
  assert.match(timeline, /\[00:05\.000\] 화면 선택 S01/);
  assert.equal(count(timeline, '화면 선택 S01'), 2);
  assert.equal(count(prompt, session.transcript[0].text), 1);
  assert.equal(count(prompt, 'images/S01.png'), 1);
  assert.equal(count(prompt, 'images/S02.png'), 1);
  assert.doesNotMatch(prompt, /images\/S03\.png|images\/S04\.png|stale-audio|녹음|음성|마이크|원음|전사|\] 말 /);
  assert.equal(JSON.stringify(session), original);
});

test('text-only notes without captures keep their own timestamp and never invent audio', () => {
  const session = makeSession({runMode: 'text', runVersion: 1, runState: 'recording', scenes: [], sceneSelections: [],
    transcript: [{id: 'typed-no-kind', text: '화면 없이 남긴 기록', t: 1.75, sceneId: null}]});
  const prompt = M.generatePrompt(session);
  assert.match(prompt, /캡처된 화면 없음/);
  assert.match(prompt, /\[00:01\.750\] 메모 1 \(전체 메모\): "화면 없이 남긴 기록"/);
  assert.doesNotMatch(section(prompt, '타임라인'), /연결\?/);
  assert.doesNotMatch(prompt, /녹음|음성|마이크|원음|전사|\] 말 /);
});

test('legacy audio sessions remain compatible and do not acquire invented run versions', () => {
  const session = switchingSession();
  delete session.runMode;delete session.runState;delete session.runVersion;delete session.sceneSelections;
  session.transcript.push(utterance('legacy', '기존 음성 원문', {audioId: 'audio-private-id', startMs: 101500, endMs: 102500, t: 101.5}));
  const original = JSON.stringify(session), prompt = M.generatePrompt(session);
  assert.match(prompt, /시간: 첫 녹음 시작 기준/);
  assert.match(prompt, /\[00:01\.500–00:02\.500\] 말 1 \(S01\): "기존 음성 원문"/);
  assert.doesNotMatch(prompt, /^v\d+ · /m);
  assert.equal(JSON.stringify(session), original);
});

test('scene selections and audio contexts share one deduplicated screen timeline', () => {
  const session = switchingSession();
  session.runMode = 'audio';session.runVersion = 3;
  session.sceneSelections = [{tMs: 100000, sceneId: 'scene-one'}, {tMs: 103000, sceneId: 'scene-two'}, {tMs: 108000, sceneId: 'scene-one'}];
  const timeline = section(M.generatePrompt(session), '타임라인');
  assert.equal(count(timeline, '[00:00.000] 화면 선택 S01'), 1);
  assert.equal(count(timeline, '[00:03.000] 화면 선택 S02'), 1);
  assert.equal(count(timeline, '[00:08.000] 화면 선택 S01'), 1);
});

test('supported annotation numbering excludes pen and malformed marks while reserving hidden numbers', () => {
  const scene = makeScene('scene-one', {annotations: [
    null, [], 'bad', 4, {id: 'bad-number-type', type: 0}, {id: 'old-pen', type: 'pen'},
    {id: 'point-one', type: 'point'}, {id: 'hidden-arrow', type: 'arrow', hidden: true},
    {id: 'old-pen-two', type: 'pen'}, {id: 'rect-three', type: 'rect'}, {id: 'default-four'}, {id: 'empty-five', type: ''}, {id: 'unsupported', type: 'text'}
  ]});
  const original = JSON.stringify(scene);
  assert.deepEqual(Array.from(M.supportedAnnotations(scene), mark => mark.id), ['point-one', 'hidden-arrow', 'rect-three', 'default-four', 'empty-five']);
  assert.equal(M.markNumber(scene, 'point-one'), 1);
  assert.equal(M.markNumber(scene, 'hidden-arrow'), 2);
  assert.equal(M.markNumber(scene, 'rect-three'), 3);
  assert.equal(M.markNumber(scene, 'empty-five'), 5);
  for (const id of ['old-pen', 'unsupported', 'missing', undefined, null]) assert.equal(M.markNumber(scene, id), 0);
  assert.equal(M.supportedAnnotations(null).length, 0);
  assert.equal(M.supportedAnnotations({annotations: {}}).length, 0);
  assert.equal(JSON.stringify(scene), original);
});

test('visible structured marks close prompt scope and get concise geometry once with stable numbers', () => {
  const scene = makeScene('marked', {annotations: [
    {id: 'freehand', type: 'pen', points: [{x: 1, y: 1}]},
    {id: 'point-one', type: 'point', x: .2, y: .3, label: '설명할 위치'},
    {id: 'hidden-arrow', type: 'arrow', x: .1, y: .2, x2: .8, y2: .9, hidden: true},
    {id: 'rect-three', type: 'rect', x: .4, y: .5, w: .2, h: .1, label: '선택한 영역'},
    {id: 'default-four', x: .5, y: .5}, {id: 'empty-five', type: '', x: .7, y: .7}
  ]});
  const session = makeSession({runMode: 'text', runVersion: 2, scenes: [makeScene('unused'), scene, makeScene('last')],
    transcript: [{id: 'typed-target', kind: 'note', text: '이 항목을 옮겨 주세요.', t: 2, sceneId: 'marked', target: 'rect-three'}]});
  const original = JSON.stringify(session);
  assert.deepEqual(Array.from(M.promptScenes(session), scene => scene.id), ['marked']);
  const prompt = M.generatePrompt(session), images = section(prompt, '화면');
  assert.match(images, /S02#1 point \(200,240\)/);
  assert.match(images, /S02#3 rect \(400,400,200,80\)/);
  assert.match(images, /S02#4 point \(500,400\)/);
  assert.match(images, /S02#5 point \(700,560\)/);
  assert.equal(count(prompt, '좌표: px, 좌상단(0,0)'), 1);
  assert.equal(count(prompt, '(400,400,200,80)'), 1);
  assert.equal(count(prompt, 'images/S02.png'), 1);
  assert.match(section(prompt, '타임라인'), /메모 1 \(S02#3\): "이 항목을 옮겨 주세요\."/);
  assert.doesNotMatch(prompt, /S02#2|freehand|hidden-arrow|rect-three|설명할 위치|선택한 영역|pen|%|annotation-events/);
  assert.equal(JSON.stringify(session), original);
});

test('hidden structured targets retain raw notes with a scene fallback and no historical geometry', () => {
  const session = makeSession({runMode: 'text', scenes: [makeScene('scene-one', {annotations: [
    {id: 'hidden-point', type: 'point', x: .25, y: .75, hidden: true}
  ]})], transcript: [{id: 'typed-hidden', kind: 'note', t: 3, sceneId: 'scene-one', target: 'hidden-point', text: '기존 내용은 유지해 주세요.'}]});
  const prompt = M.generatePrompt(session);
  assert.match(prompt, /메모 1 \[표시 지워짐\] \(S01\): "기존 내용은 유지해 주세요\."/);
  assert.doesNotMatch(prompt, /S01#1|좌표:|\(250,600\)|hidden-point/);
  assert.equal(count(prompt, '기존 내용은 유지해 주세요.'), 1);
});

test('structured target selections deduplicate across context sources and do not create scene uncertainty', () => {
  const scene = makeScene('scene-one', {annotations: [
    {id: 'point-one', type: 'point', x: .1, y: .2},
    {id: 'legacy-pen', type: 'pen', points: [{x: .3, y: .4}]},
    {id: 'arrow-two', type: 'arrow', x: .2, y: .3, x2: .6, y2: .7}
  ]});
  const session = makeSession({runMode: 'audio', runVersion: 1, scenes: [scene], sceneSelections: [{tMs: 10000, sceneId: 'scene-one', targetId: 'point-one'}],
    recordings: [{id: 'audio-structured', startMs: 10000, endMs: 15000, transcriptionStatus: 'complete', contexts: [
      {tMs: 10000, sceneId: 'scene-one', targetId: 'point-one'}, {tMs: 11000, sceneId: 'scene-one', targetId: 'arrow-two'}
    ]}], transcript: [{id: 'speech-structured', kind: 'speech', startMs: 10000, endMs: 12000, sceneId: 'scene-one', target: null, unresolved: true, text: '화면의 두 표시를 확인해 주세요.'}]
  });
  const prompt = M.generatePrompt(session), timeline = section(prompt, '타임라인');
  assert.equal(count(timeline, '[00:00.000] 가리키기 S01#1'), 1);
  assert.match(timeline, /\[00:01\.000\] 가리키기 S01#2/);
  assert.match(timeline, /말 1 \(S01; 후보 S01#1\/S01#2\): "화면의 두 표시를 확인해 주세요\."/);
  assert.doesNotMatch(timeline, /연결\?|legacy-pen/);
  assert.match(section(prompt, '화면'), /S01#2 arrow \(200,240\)→\(600,560\)/);
});
