(function (global) {
  'use strict';

  var MODES = ['mixed', 'question', 'change'];

  function uid(prefix) {
    return prefix + '-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 8);
  }

  function clean(value) {
    return typeof value === 'string' ? value.replace(/\u0000/g, '').trim() : '';
  }

  function time(value) {
    var seconds = Math.max(0, Math.floor(Number(value) || 0));
    return String(Math.floor(seconds / 60)).padStart(2, '0') + ':' + String(seconds % 60).padStart(2, '0');
  }

  function createSession(options) {
    options = options || {};
    var runMode = options.runMode === 'audio' || options.runMode === 'text' ? options.runMode : null;
    var session = {
      id: uid('session'),
      title: clean(options.title) || '제목 없는 세션',
      project: clean(options.project) || '개인 프로젝트',
      mode: MODES.indexOf(options.mode) >= 0 ? options.mode : 'mixed',
      runMode: runMode,
      runState: 'ready',
      runVersion: Number.isSafeInteger(options.runVersion) && options.runVersion > 0 ? options.runVersion : 0,
      status: 'review',
      date: new Date().toISOString(),
      elapsedMs: 0,
      duration: 0,
      mic: runMode === 'audio' && options.mic !== false,
      scenes: [],
      sceneSelections: [],
      transcript: [],
      notes: '',
      prompt: ''
    };
    if (clean(options.previousRunId)) session.previousRunId = clean(options.previousRunId);
    return session;
  }

  function audioRecords(session) {
    if (session.runMode === 'text') return [];
    return (Array.isArray(session.recordings) ? session.recordings : []).concat(Array.isArray(session.originalRecordings) ? session.originalRecordings : []).map(function (audio) {
      var metadata = audio.metadata && typeof audio.metadata === 'object' ? audio.metadata : {};
      // Unconverted originals keep the browser clock and contexts in metadata.
      return Object.assign({}, audio, {startMs: audio.startMs ?? metadata.startMs, endMs: audio.endMs ?? metadata.endMs,
        sampleRate: audio.sampleRate ?? metadata.sampleRate, contexts: Array.isArray(audio.contexts) ? audio.contexts : metadata.contexts});
    });
  }

  function supportedAnnotations(scene) {
    return (scene && Array.isArray(scene.annotations) ? scene.annotations : []).filter(function (mark) {
      if (!mark || typeof mark !== 'object' || Array.isArray(mark)) return false;
      var type = mark.type === undefined || mark.type === null || mark.type === '' ? 'point' : mark.type;
      return ['point', 'arrow', 'rect'].includes(type);
    });
  }

  function markNumber(scene, id) {
    return id ? supportedAnnotations(scene).findIndex(function (mark) { return mark.id === id; }) + 1 : 0;
  }

  function promptScenes(session) {
    session = session || {};
    var allScenes = Array.isArray(session.scenes) ? session.scenes : [];
    var deleted = new Set(allScenes.filter(function (scene) { return scene.deletedAt; }).map(function (scene) { return scene.id; }));
    var scenes = allScenes.filter(function (scene) { return !scene.deletedAt; }), used = new Set();
    function use(context) { if (context && context.sceneId) used.add(context.sceneId); }
    (Array.isArray(session.transcript) ? session.transcript : []).forEach(function (item) {
      if (deleted.has(item.sceneId)) return;
      use(item);
      (Array.isArray(item.candidates) ? item.candidates : []).forEach(use);
    });
    (Array.isArray(session.sceneSelections) ? session.sceneSelections : []).forEach(use);
    audioRecords(session).forEach(function (audio) { (Array.isArray(audio.contexts) ? audio.contexts : []).forEach(use); });
    var selected = scenes.filter(function (scene) { return used.has(scene.id) || clean(scene.note) || supportedAnnotations(scene).some(function (mark) { return !mark.hidden; }); });
    return selected.length ? selected : scenes.slice(-1);
  }

  function generatePrompt(session) {
    session = session || {};
    var allScenes = Array.isArray(session.scenes) ? session.scenes : [];
    var deleted = new Set(allScenes.filter(function (scene) { return scene.deletedAt; }).map(function (scene) { return scene.id; }));
    var activeScenes = allScenes.filter(function (scene) { return !scene.deletedAt; });
    var scenes = promptScenes(session), selected = new Set(scenes.map(function (scene) { return scene.id; }));
    var transcript = (Array.isArray(session.transcript) ? session.transcript : []).filter(function (item) { return !deleted.has(item.sceneId) && clean(item.text); });
    var textMode = session.runMode === 'text', recordings = audioRecords(session), sceneMap = new Map(), sceneData = new Map(), timeline = [], order = 0, notes = [], caveats = [];
    activeScenes.forEach(function (scene, index) { sceneMap.set(scene.id, 'S' + String(index + 1).padStart(2, '0'));sceneData.set(scene.id, scene); });

    function milliseconds(value) { return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? Math.round(value) : null; }
    function inline(value) { return clean(value).replace(/\s+/g, ' '); }
    function quote(value) { return JSON.stringify(clean(value)); }
    var starts = recordings.map(function (audio) { return milliseconds(audio.startMs); }).filter(function (value) { return value !== null; });
    var origin = starts.length ? Math.min.apply(null, starts) : 0;
    function clock(value) {
      if (value === null) return '시각 미상';
      var relative = value - origin, absolute = Math.abs(relative);
      return (relative < 0 ? '녹음 전 -' : '') + time(absolute / 1000) + '.' + String(absolute % 1000).padStart(3, '0');
    }
    function add(at, text, rank, end) { timeline.push({at: at, end: end === undefined ? null : end, text: text, rank: rank || 0, order: order++}); }
    function sceneRef(id) { return selected.has(id) && sceneMap.has(id) ? sceneMap.get(id) : '화면?'; }
    function targetID(context) { return context.targetId === undefined ? context.target : context.targetId; }
    function linkedMark(sceneId, id) { return id ? supportedAnnotations(sceneData.get(sceneId)).find(function (mark) { return mark.id === id; }) : null; }
    function targetRef(sceneId, id) {
      var mark = linkedMark(sceneId, id);
      return sceneRef(sceneId) + (mark && !mark.hidden ? '#' + markNumber(sceneData.get(sceneId), id) : '');
    }
    function geometry(scene, mark) {
      var width = Number(scene.width), height = Number(scene.height), type = mark.type || 'point';
      function pixels(value, size) { return Number.isFinite(value) && size > 0 ? Math.round(Math.max(0, Math.min(1, value)) * size) : null; }
      function point(x, y) {
        var px = pixels(x, width), py = pixels(y, height);
        return px !== null && py !== null ? '(' + px + ',' + py + ')' : '';
      }
      var start = point(mark.x, mark.y), detail = start;
      if (type === 'arrow') {
        var end = point(mark.x2, mark.y2);
        detail = start && end ? start + '→' + end : '';
      } else if (type === 'rect') {
        var w = pixels(mark.w, width), h = pixels(mark.h, height);
        detail = start && w !== null && h !== null ? start.slice(0, -1) + ',' + w + ',' + h + ')' : '';
      }
      return type + (detail ? ' ' + detail : '');
    }
    function contextTime(context, audio) {
      var at = milliseconds(context.tMs);
      if (at !== null) return at;
      var start = milliseconds(audio && audio.startMs);
      return start !== null && Number.isFinite(context.sampleOffset) && context.sampleOffset >= 0 && audio.sampleRate > 0 ? Math.round(start + context.sampleOffset * 1000 / audio.sampleRate) : null;
    }
    function speechTime(item) { return milliseconds(item.startMs) ?? (typeof item.t === 'number' ? milliseconds(item.t * 1000) : null); }
    function candidatesFor(item, start, end) {
      var candidates = (Array.isArray(item.candidates) ? item.candidates : []).slice();
      if (item.kind !== 'note' && start !== null) recordings.forEach(function (audio) {
        if (item.audioId && item.audioId !== audio.id) return;
        var audioStart = milliseconds(audio.startMs), audioEnd = milliseconds(audio.endMs);
        if (audioStart === null || (audioEnd !== null && audioEnd <= start) || audioStart > (end ?? start)) return;
        var contexts = (Array.isArray(audio.contexts) ? audio.contexts : []).map(function (context) { return {context: context, at: contextTime(context, audio)}; }).filter(function (entry) { return entry.at !== null; }).sort(function (a, b) { return a.at - b.at; });
        contexts.forEach(function (entry, index) {
          var until = contexts[index + 1] ? contexts[index + 1].at : (audioEnd ?? Infinity);
          var overlaps = end !== null && end > start ? entry.at < end && until > start : entry.at <= start && until > start;
          if (overlaps) candidates.push(entry.context);
        });
      });
      var unique = new Map();
      candidates.forEach(function (context) {
        if (context && !deleted.has(context.sceneId)) unique.set(context.sceneId + '/' + targetRef(context.sceneId, targetID(context)), context);
      });
      return Array.from(unique.values());
    }
    function timingUncertain(item, start, end) {
      return item.timingUncertain || item.timingEstimated || (item.kind !== 'note' && recordings.some(function (audio) {
        if (!audio.metadata || !Array.isArray(audio.metadata.discontinuities) || !audio.metadata.discontinuities.length) return false;
        if (item.audioId) return item.audioId === audio.id;
        return start !== null && milliseconds(audio.startMs) !== null && audio.startMs <= (end ?? start) && (milliseconds(audio.endMs) === null || audio.endMs > start);
      }));
    }

    var lines = ['# ' + (inline(session.title) || '제목 없는 세션')];
    if (Number.isSafeInteger(session.runVersion) && session.runVersion > 0) lines.push('v' + session.runVersion + ' · ' + (textMode ? '기록' : '녹음'));
    lines.push('', '화면과 기록을 함께 읽고 사용자의 질문·요청을 처리해 주세요. 연결? 항목은 추측하지 마세요.',
      '시간: ' + (textMode ? '기록 시작' : starts.length ? '첫 녹음 시작' : '세션 시작') + ' 기준. ' + (textMode ? '메모는' : '말·메모는') + ' 원문입니다.', '', '## 화면');
    if (scenes.some(function (scene) { return supportedAnnotations(scene).some(function (mark) { return !mark.hidden; }); })) lines.push('좌표: px, 좌상단(0,0). Snn#n은 이미지의 표시 번호입니다.');
    if (!scenes.length) lines.push('캡처된 화면 없음.');
    scenes.forEach(function (scene) {
      var label = sceneRef(scene.id), names = [inline(scene.title), inline(scene.app), inline(scene.source && scene.source.title)].filter(Boolean);
      var title = Array.from(new Set(names)).join(' · '), dimensions = scene.width > 0 && scene.height > 0 ? scene.width + '×' + scene.height + ' px' : '크기 미상';
      lines.push('- ' + label + ': `images/' + label + '.png` · ' + title + (title ? ' · ' : '') + dimensions);
      supportedAnnotations(scene).forEach(function (mark, index) { if (!mark.hidden) lines.push('  ' + label + '#' + (index + 1) + ' ' + geometry(scene, mark)); });
      add(typeof scene.capturedAt === 'number' ? milliseconds(scene.capturedAt * 1000) : null, '캡처 ' + label, 0);
      if (clean(scene.note)) notes.push('메모 (' + label + '): ' + quote(scene.note));
    });
    if (clean(session.notes)) notes.push('전체 메모: ' + quote(session.notes));

    var contexts = [], seenContexts = new Set();
    function collectContext(context, audio) {
      if (!context || deleted.has(context.sceneId)) return;
      var at = contextTime(context, audio), reference = targetRef(context.sceneId, targetID(context)), key = at + '/' + context.sceneId + '/' + reference;
      if (seenContexts.has(key)) return;
      seenContexts.add(key);
      contexts.push({at: at, sceneId: context.sceneId, reference: reference, order: contexts.length});
    }
    recordings.forEach(function (audio, index) {
      var label = 'A' + (index + 1), start = milliseconds(audio.startMs), end = milliseconds(audio.endMs);
      if (start !== null) add(start, '녹음 ' + label + ' 시작', 0);
      if (end !== null) add(end, '녹음 ' + label + ' 끝', 4);
      (Array.isArray(audio.contexts) ? audio.contexts : []).forEach(function (context) { collectContext(context, audio); });
    });
    (Array.isArray(session.sceneSelections) ? session.sceneSelections : []).forEach(function (context) { collectContext(context, {}); });
    transcript.forEach(function (item) { (Array.isArray(item.candidates) ? item.candidates : []).forEach(function (context) { collectContext(context, {}); }); });
    contexts.sort(function (a, b) { return a.at === null ? (b.at === null ? a.order - b.order : 1) : b.at === null ? -1 : a.at - b.at || a.order - b.order; });
    var previousSelection, hasPreviousSelection = false;
    contexts.forEach(function (context) {
      var selection = context.sceneId + '/' + context.reference;
      if (hasPreviousSelection && selection === previousSelection) return;
      previousSelection = selection;hasPreviousSelection = true;
      add(context.at, !context.sceneId || context.sceneId === 'unbound' ? '화면 선택 해제' : (context.reference.includes('#') ? '가리키기 ' : '화면 선택 ') + context.reference, 2);
    });

    transcript = transcript.slice().sort(function (a, b) {
      var at = speechTime(a), bt = speechTime(b);
      return at === null ? (bt === null ? 0 : 1) : bt === null ? -1 : at - bt;
    });
    var recordLabels = new Map(), itemLabels = new Map(), replacements = new Map();
    transcript.forEach(function (item, index) {
      var label = (textMode || item.kind === 'note' ? '메모 ' : '말 ') + (index + 1);
      itemLabels.set(item, label);
      if (clean(item.id)) recordLabels.set(item.id, label);
    });
    transcript.forEach(function (item) { if (clean(item.replaces) && recordLabels.has(item.replaces)) replacements.set(item.replaces, itemLabels.get(item)); });
    transcript.forEach(function (item) {
      var start = speechTime(item), end = milliseconds(item.endMs), candidates = candidatesFor(item, start, end), reference = targetRef(item.sceneId, item.target), flags = [];
      var candidateScenes = Array.from(new Set(candidates.map(function (context) { return context.sceneId; })));
      var candidateLabels = Array.from(new Set(candidates.map(function (context) { return targetRef(context.sceneId, targetID(context)); })));
      var mismatch = candidateScenes.length === 1 && candidateScenes[0] !== item.sceneId, uncertain = timingUncertain(item, start, end);
      var generalNote = (textMode || item.kind === 'note') && !item.sceneId && !candidates.length;
      if (generalNote) reference = '전체 메모';
      // Old unresolved flags may describe removed drawing targets. Scene-only
      // candidates establish the screen without reviving that obsolete ambiguity.
      if (!generalNote && (sceneRef(item.sceneId) === '화면?' || candidateScenes.length > 1 || mismatch || uncertain || (item.unresolved && !candidates.length))) flags.push('연결?');
      if (uncertain) flags.push('시간?');
      if (item.timingEstimated) flags.push('문장별 시각 없음 · 녹음 전체 구간');
      if (item.timingAdjusted) flags.push('시각 보정');
      var mark = linkedMark(item.sceneId, item.target);
      if (mark && mark.hidden) flags.push('표시 지워짐');
      if (candidateLabels.length > 1 || mismatch || (candidateLabels.length === 1 && candidateLabels[0] !== reference)) reference += '; 후보 ' + candidateLabels.join('/');
      if (clean(item.replaces)) flags.push(recordLabels.has(item.replaces) ? recordLabels.get(item.replaces) + ' 정정' : '이전 기록 정정');
      if (replacements.has(item.id)) flags.push(replacements.get(item.id) + '로 정정됨');
      add(start, itemLabels.get(item) + (flags.length ? ' [' + flags.join(' · ') + ']' : '') + ' (' + reference + '): ' + quote(item.text), 3, end !== null && start !== null && end >= start ? end : null);
    });
    lines.push('', '## 타임라인');
    timeline.sort(function (a, b) { return a.at === null ? (b.at === null ? a.order - b.order : 1) : b.at === null ? -1 : a.at - b.at || a.rank - b.rank || a.order - b.order; });
    if (!timeline.length) lines.push('기록 없음.');
    timeline.forEach(function (entry) { lines.push('[' + clock(entry.at) + (entry.end !== null && entry.end !== entry.at ? '–' + clock(entry.end) : '') + '] ' + entry.text); });
    if (notes.length) lines.push('', '## 메모', '시각 미상.', notes.join('\n'));

    var estimated = [], interrupted = [], failed = [], pending = [];
    recordings.forEach(function (audio, index) {
      var label = 'A' + (index + 1);
      if (audio.metadata && audio.metadata.timing === 'estimated') estimated.push(label);
      if (audio.metadata && audio.metadata.discontinuities && audio.metadata.discontinuities.length) interrupted.push(label);
      if (audio.transcriptionStatus === 'failed') failed.push(label);
      else if (audio.transcriptionStatus !== 'complete') pending.push(label);
    });
    if (estimated.length) caveats.push(estimated.join(', ') + ': 음성 시각 추정.');
    if (interrupted.length) caveats.push(interrupted.join(', ') + ': 입력 중단/길이 차이로 화면 연결 미확정.');
    if (failed.length) caveats.push(failed.join(', ') + ': 전사 실패, 원음 보존.');
    if (pending.length) caveats.push(pending.join(', ') + ': 전사 미완료, 원음 보존.');
    if (!transcript.length && !notes.length) caveats.push((textMode ? '메모' : '발화·메모') + ' 없음. 화면만으로 요청을 만들지 마세요.');
    if (caveats.length) lines.push('', '## 확인 필요', caveats.map(function (text) { return '- ' + text; }).join('\n'));
    lines.push('', '세부 기록: `session.json`.');
    return lines.join('\n');
  }

  global.CueviaModel = Object.freeze({createSession: createSession, generatePrompt: generatePrompt, promptScenes: promptScenes, supportedAnnotations: supportedAnnotations, markNumber: markNumber});
})(typeof window !== 'undefined' ? window : globalThis);
