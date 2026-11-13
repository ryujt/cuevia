import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {randomUUID} from 'node:crypto';
import vm from 'node:vm';

const [modelSource, clientSource, appSource] = await Promise.all([
  'model.js', 'session-client.js', 'app.js'
].map(name => readFile(new URL('../web/' + name, import.meta.url), 'utf8')));
const turn = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => {
  let resolve;
  const promise = new Promise(yes => { resolve = yes; });
  return {promise, resolve};
};

function boot({storageMode = 'malformed', snapshot = null, cached, microphoneDenied = false, deferRefresh = null, saveFailure = null, stopDelay = null, loseStartResponse = false, reuseReady = false, withMarks = false, query = '', settingsData = null, settingsFailure = null} = {}) {
  const current = deferred(), calls = [], timers = [], writes = [], errors = [], timeouts = [], saves = [];
  const documentEvents = new Map(), windowEvents = new Map(), settingsSaves=[];
  const nodes = new Map(['#app', '#toast', '#modal-root', '#descriptions', '#voice-control', '#recording-list', '#save-status', '.context-count', '.scenes']
    .map(selector => [selector, {innerHTML: '', textContent: '', append(node) {node.parentNode = this;}}]));
  // Replacing the real root creates a new scroller with scrollLeft=0.
  let appHTML = '';
  nodes.get('.scenes').scrollLeft = 0;
  Object.defineProperty(nodes.get('#app'), 'innerHTML', {get: () => appHTML, set(value) {appHTML = value; nodes.get('.scenes').scrollLeft = 0;}});
  const annotationMounts=[],frameRequests=[];if(withMarks)nodes.set('#capture-body',{});
  let latest = snapshot;
  const materialRequests = [], archives = new Map(), starts = [];
  let microphoneStarts = 0,refreshDeferred=false;
  let clock = Date.now();const clockStart=clock;
  class ClockDate extends Date {
    constructor(...args) { super(...(args.length ? args : [clock])); }
    static now() { return clock; }
  }
  const stored = new Map([['cuevia.real.v1', cached === undefined ? '{invalid json' : JSON.stringify(cached)]]);
  const denied = () => { throw new DOMException('Storage is unavailable', 'SecurityError'); };
  const storage = storageMode === 'denied-methods' ? {getItem: denied, setItem: denied, removeItem: denied} : {
    getItem: key => stored.get(key) ?? null,
    setItem(key, value) { writes.push({key, value}); stored.set(key, value); },
    removeItem(key) { stored.delete(key); }
  };
  const jsonResponse = (body, status = 200) => ({ok: status >= 200 && status < 300, status, json: async () => structuredClone(body)});
  const sandbox = {
    URLSearchParams, structuredClone, Date: ClockDate, crypto: {randomUUID}, performance: {now: () => clock-clockStart},
    location: {search: query}, navigator: {},
    console: {error: (...args) => errors.push(args), log() {}},
    document: {
      querySelector: selector => nodes.get(selector) ?? null,
      createElement() {
        const player = {setAttribute() {}, pause() {}};
        const labels = [{}, {}], actions = {};
        return {innerHTML: '', querySelector: selector => selector === 'audio' ? player : actions, querySelectorAll: () => labels};
      },
      addEventListener: (name, callback) => documentEvents.set(name, callback)
    },
    window: {addEventListener: (name, callback) => windowEvents.set(name, callback),
      CueviaAnnotation: {mount(options){annotationMounts.push(options);return()=>{};}},
      CueviaFrame: {render:async scene=>new Uint8Array([1,2,3])},
      CueviaRecorder: {create(options) {
        let state='idle';return {get state(){return state;},markContext(){},cancel:async()=>{},
          async start(){microphoneStarts++;if(microphoneDenied)throw {code:'microphone_denied'};state='recording';options.onState('recording');return {startMs:0};},
          async stop(){state='stopping';if(stopDelay)await stopDelay.promise;state='ready';return null;}
        };
      }}
    },
    fetch: async (path, options = {}) => {
      calls.push(path);
      if (path === '/api/settings') {if(options.method==='POST'){settingsSaves.push(JSON.parse(options.body));if(settingsFailure)return jsonResponse({error:settingsFailure},400);return jsonResponse({...settingsData,apiKeyConfigured:!!settingsSaves.at(-1).apiKey||!!settingsData?.apiKeyConfigured});}return jsonResponse(settingsData);}
      if (path === '/api/sessions') return jsonResponse([...archives.values()].map(value=>({id:value.session.id,title:value.session.title,runMode:value.session.runMode,runVersion:value.session.runVersion,archivedAt:value.archivedAt,transcriptCount:value.session.transcript.length,audioCount:value.audio.length})));
      if (path.endsWith('/runs')) {
        const input=JSON.parse(options.body),prior=structuredClone(latest);starts.push(input);
        if(!reuseReady)archives.set(prior.session.id,{...prior,archivedAt:new ClockDate().toISOString()});
        const selected=prior.session.scenes.find(scene=>scene.id===input.sceneId);
        latest={revision:1,updatedAt:new ClockDate().toISOString(),audio:[],draft:{},session:{...sandbox.window.CueviaModel.createSession({runMode:input.mode,runVersion:(prior.session.runVersion||0)+1}),id:'run-'+starts.length,previousRunId:prior.session.id,scenes:selected?[{...selected,capturedAt:0,note:'',annotations:[]}]:[]}};
        if(reuseReady){latest.session.id=prior.session.id;delete latest.session.previousRunId;latest.revision=prior.revision+1;}
        if(loseStartResponse&&starts.length===1)throw new TypeError('response lost');
        return jsonResponse(latest);
      }
      if(options.method==='PUT'&&path.includes('/frame/')){frameRequests.push({path,body:options.body});return jsonResponse({ok:true});}
      if (options.method === 'PUT' && path.startsWith('/api/session/')) {
        const saved = JSON.parse(options.body);
        saves.push(saved);if(saveFailure?.(saved))return jsonResponse({error:'storage_failed'},500);
        latest = {...saved, revision: saved.revision + 1, audio: latest?.audio || [], updatedAt: new ClockDate().toISOString()};
        return jsonResponse(latest);
      }
      if (path === '/api/session/current') {
        await current.promise;
        return latest ? jsonResponse(latest) : jsonResponse({error: 'not_found'}, 404);
      }
      if (/^\/api\/session\/[^/]+$/.test(path)) {const value=archives.get(path.split('/').at(-1))||latest;if(deferRefresh&&!refreshDeferred){refreshDeferred=true;await deferRefresh.promise;}return jsonResponse(value);}
      if (path.endsWith('/original-audio')) return jsonResponse([]);
      if (path.includes('/materials?revision=')) {
        const value=archives.get(path.split('/')[3])||latest;
        materialRequests.push({path, session: structuredClone(value.session)});
        return jsonResponse({directory: '/synthetic/request', prompt: value.session.prompt, createdAt: new ClockDate().toISOString()});
      }
      if (path === '/api/capture/activity?since=0') return jsonResponse([]);
      if (path === '/api/transcription/status') return jsonResponse({ready: false});
      throw new Error('Unexpected bootstrap request: ' + path);
    },
    setInterval(callback, delay) { timers.push({callback, delay}); return timers.length; },
    setTimeout(callback, delay) { const timer = {callback, delay}; timeouts.push(timer); return timer; },
    clearTimeout(timer) { if (timer) timer.canceled = true; }
  };
  if (storageMode === 'denied-getter') Object.defineProperty(sandbox, 'localStorage', {get: denied});
  else sandbox.localStorage = storage;
  const context = vm.createContext(sandbox);
  vm.runInContext(modelSource, context, {filename: 'web/model.js'});
  vm.runInContext(clientSource, context, {filename: 'web/session-client.js'});
  assert.doesNotThrow(() => vm.runInContext(appSource, context, {filename: 'web/app.js'}), 'initial production app execution must render safely');
  return {
    nodes, calls, timers, writes, errors, stored, saves, settingsSaves, materialRequests, annotationMounts, frameRequests, archives, starts, microphoneStarts:()=>microphoneStarts,
    finishBootstrap: async () => { current.resolve(); await turn(); await turn(); },
    advanceTime: ms => { clock += ms; },
    beforeUnload: () => windowEvents.get('beforeunload')({preventDefault() {}}),
    click(action, data = {}) {
      const element = {dataset: {action, ...data}, disabled: false};
      documentEvents.get('click')({target: {closest: () => element, matches: () => false}});
    },
    submit(id='composer') {documentEvents.get('submit')({target:{id},preventDefault(){}});},
    input(value) { documentEvents.get('input')({target: {id: 'description-text', value}}); },
    backup: () => JSON.parse(stored.get('cuevia.real.v1')),
    prompt: value => sandbox.window.CueviaModel.generatePrompt(value),
    async flushSave() {
      const timer = timeouts.findLast(item => item.delay === 350 && !item.canceled);
      assert.ok(timer, 'a changed session must schedule its actual persistence path');
      timer.canceled = true;
      await timer.callback(); await turn();
      return saves.at(-1);
    }
  };
}

const captureButton = html => {
  const tag = html.match(/<button\b[^>]*data-action="capture"[^>]*>/)?.[0];
  assert.ok(tag, 'the initial standby screen must expose the real capture action');
  return tag;
};

test('production app renders and completes bootstrap when localStorage access is denied', async () => {
  for (const storageMode of ['denied-getter', 'denied-methods']) {
    const app = boot({storageMode});
    assert.match(app.nodes.get('#app').innerHTML, /standby-screen/);
    assert.match(captureButton(app.nodes.get('#app').innerHTML), /disabled/);
    assert.match(app.nodes.get('#app').innerHTML, /Loading your record/);
    await app.finishBootstrap();
    assert.doesNotMatch(captureButton(app.nodes.get('#app').innerHTML), /disabled/);
    assert.doesNotMatch(app.nodes.get('#app').innerHTML, /Loading your record/);
    assert.deepEqual(app.calls, ['/api/session/current', '/api/capture/activity?since=0', '/api/transcription/status']);
    assert.deepEqual(app.errors, []);
    assert.doesNotThrow(app.beforeUnload, 'backup failure must not crash beforeunload');
    app.advanceTime(7 * 24 * 60 * 60 * 1000 + 1);
    const retentionTimer = app.timers.find(timer => timer.delay === 60000);
    assert.ok(retentionTimer);
    assert.doesNotThrow(retentionTimer.callback, 'expiry must not crash if removing browser storage is denied');
    assert.match(app.nodes.get('#app').innerHTML, /standby-screen/);
    assert.doesNotMatch(captureButton(app.nodes.get('#app').innerHTML), /disabled/);
  }
});

test('malformed local JSON cannot prevent a fresh standby session and safe subsequent backup', async () => {
  const app = boot();
  assert.match(captureButton(app.nodes.get('#app').innerHTML), /disabled/);
  await app.finishBootstrap();
  assert.doesNotMatch(captureButton(app.nodes.get('#app').innerHTML), /disabled/);
  assert.doesNotMatch(app.nodes.get('#app').innerHTML, /data-action="resume-session"/);
  assert.doesNotThrow(app.beforeUnload);
  const backup = JSON.parse(app.stored.get('cuevia.real.v1'));
  assert.equal(backup.session.scenes.length, 0);
  assert.equal(backup.session.transcript.length, 0);
  assert.match(backup.session.id, /^session-/);
  assert.deepEqual(app.errors, []);
});

test('recent server records survive unavailable or corrupt local cache and remain reopenable', async () => {
  for (const storageMode of ['denied-getter', 'denied-methods', 'malformed']) {
    const snapshot = {
      revision: 4, updatedAt: new Date().toISOString(), audio: [],
      session: {
        id: 'session-real-recent', title: 'Actual record saved on the server', status: 'review', mode: 'mixed', elapsedMs: 17000, duration: 17,
        scenes: [{id: 'scene-real-recent', title: 'Actual screen', captureId: 'a'.repeat(32), imageUrl: '/captures/' + 'a'.repeat(32) + '.png', width: 1200, height: 800, annotations: [], capturedAt: 0}],
        transcript: [{id: 'text-recent', kind: 'note', text: 'Move the save button to the right.', t: 12, sceneId: 'scene-real-recent', target: null}], notes: '', prompt: ''
      },
      draft: {sceneId: 'scene-real-recent', noteDraft: '', draftContext: null, editingId: null}
    };
    const app = boot({storageMode, snapshot});
    await app.finishBootstrap();
    assert.match(app.nodes.get('#app').innerHTML, /data-action="resume-session"/);
    app.click('resume-session');
    await turn();
    assert.match(app.nodes.get('#app').innerHTML, /Actual record saved on the server/);
    assert.match(app.nodes.get('#app').innerHTML, /Actual screen/);
    assert.match(app.nodes.get('#descriptions').innerHTML, /text-recent/);
    assert.match(app.nodes.get('#descriptions').innerHTML, /Move the save button to the right/);
    assert.equal(app.nodes.get('.context-count').textContent, '1 entry');
    assert.deepEqual(app.errors, []);
    assert.doesNotThrow(app.beforeUnload);
    if (storageMode === 'malformed') {
      const backup = JSON.parse(app.stored.get('cuevia.real.v1'));
      assert.equal(backup.revision, 4);
      assert.equal(backup.session.id, 'session-real-recent');
      assert.equal(backup.session.transcript[0].id, 'text-recent');
    }
  }
});

function savedSnapshot() {
  return {
    revision: 8, updatedAt: new Date().toISOString(), audio: [],
    session: {
      id: 'session-annotations', title: 'Captures and descriptions', status: 'review', mode: 'mixed', elapsedMs: 17000, duration: 17,
      scenes: [
        {id: 'scene-one', title: 'First screen', captureId: 'a'.repeat(32), imageUrl: '/captures/' + 'a'.repeat(32) + '.png', width: 1200, height: 800, capturedAt: 0, note: 'First screen note', annotations: [
          {id: 'saved-pen', type: 'pen', points: [{x: .1, y: .2}, {x: .4, y: .5}], label: 'First drawing'},
          {id: 'saved-arrow', type: 'arrow', x: .2, y: .3, x2: .6, y2: .7, label: 'Button position'},
          {id: 'previous-hidden', type: 'point', x: .8, y: .8, label: 'Previously removed mark', hidden: true, deletedAtMs: 800}
        ]},
        {id: 'scene-two', title: 'Second screen', captureId: 'b'.repeat(32), imageUrl: '/captures/' + 'b'.repeat(32) + '.png', width: 1200, height: 800, capturedAt: 10, note: 'Other screen note', annotations: [
          {id: 'other-scene', type: 'rect', x: .3, y: .3, w: .2, h: .2, label: 'Other screen mark'}
        ]}
      ],
      transcript: [{id: 'speech-original', kind: 'speech', text: 'Move this button to the right.', t: 12, sceneId: 'scene-one', target: 'saved-arrow'}],
      notes: 'Keep the existing descriptions and notes.', prompt: '# Previous prompt'
    },
    draft: {sceneId: 'scene-one', targetId: 'saved-arrow', noteDraft: '', draftContext: null, editingId: null}
  };
}

function actionButton(app, action) {
  const tag = app.nodes.get('#app').innerHTML.match(new RegExp(`<button\\b[^>]*data-action="${action}"[^>]*>`))?.[0];
  assert.ok(tag, `production toolbar must expose ${action}`);
  return tag;
}

test('legacy pen data stays opaque while structured tools and screen anchors remain', async () => {
  const snapshot = savedSnapshot();
  snapshot.session.annotationEvents = [{id: 'legacy-event', type: 'create'}];
  const app = boot({snapshot});
  await app.finishBootstrap(); app.click('resume-session'); await turn();
  assert.doesNotMatch(app.nodes.get('#app').innerHTML, /data-tool="pen"|Freehand drawing|Drawing log/);
  assert.doesNotMatch(app.nodes.get('#descriptions').innerHTML, /annotation-history|Marked location|legacy-event/);
  app.input('Reduce the spacing in the right panel.');
  const saved = await app.flushSave();
  assert.deepEqual(saved.session.scenes[0].annotations, snapshot.session.scenes[0].annotations);
  assert.deepEqual(saved.session.annotationEvents, snapshot.session.annotationEvents);
  assert.equal(saved.draft.draftContext.sceneId, 'scene-one');
  assert.equal(saved.draft.draftContext.targetId, 'saved-arrow');
  assert.equal(saved.draft.targetId, 'saved-arrow');
  assert.doesNotMatch(app.prompt(saved.session), /saved-pen|saved-arrow|previous-hidden|annotation-events|Pen area|Drawing/);
  assert.deepEqual(app.errors, []);
});

test('selecting captures and descriptions retains the horizontal filmstrip position', async () => {
  const app = boot({snapshot: savedSnapshot()});
  await app.finishBootstrap();app.click('resume-session');await turn();
  app.nodes.get('.scenes').scrollLeft = 720;
  app.click('scene', {id: 'scene-two'});await turn();
  assert.equal(app.nodes.get('.scenes').scrollLeft, 720);
  app.beforeUnload();
  assert.equal(app.backup().draft.sceneId, 'scene-two');
  app.click('speech', {id: 'speech-original'});await turn();
  assert.equal(app.nodes.get('.scenes').scrollLeft, 720);
});

test('capture delete persists a reversible exclusion, moves selection, and preserves stored evidence', async () => {
  const app = boot({snapshot: savedSnapshot()});
  await app.finishBootstrap();app.click('resume-session');await turn();
  app.beforeUnload();
  const before = app.backup().session.annotationEvents;
  app.click('delete-scene', {id: 'scene-one'});await turn();
  const saved = await app.flushSave();
  assert.ok(saved.session.scenes[0].deletedAt > 0);
  assert.deepEqual(saved.session.annotationEvents, before);
  assert.equal(saved.draft.sceneId, 'scene-two');
  assert.doesNotMatch(app.nodes.get('#app').innerHTML, /data-action="scene" data-id="scene-one"/);
  assert.doesNotMatch(app.nodes.get('#descriptions').innerHTML, /speech-original/);
  assert.doesNotMatch(app.prompt(saved.session), /Move this button to the right/);
  const reopened = boot({snapshot: {...saved, revision: 9}});
  await reopened.finishBootstrap();reopened.click('resume-session');await turn();
  assert.doesNotMatch(reopened.nodes.get('#app').innerHTML, /data-action="scene" data-id="scene-one"/);
  reopened.click('restore-scene');await turn();
  const restored = await reopened.flushSave();
  assert.equal(restored.session.scenes[0].deletedAt, undefined);
  assert.match(reopened.nodes.get('#descriptions').innerHTML, /speech-original/);
  assert.deepEqual(restored.session.annotationEvents, before);
  assert.match(reopened.prompt(restored.session), /Move this button/);
});

test('capture deletion protects unsaved text and permits restoring after all captures were deleted', async () => {
  const app = boot({snapshot: savedSnapshot()});
  await app.finishBootstrap();app.click('resume-session');await turn();
  app.input('Description not saved yet');
  app.click('delete-scene', {id: 'scene-one'});await turn();
  assert.equal(app.backup().session.scenes[0].deletedAt, undefined);
  app.input('');
  app.click('delete-scene', {id: 'scene-one'});await turn();
  app.advanceTime(1);
  app.click('delete-scene', {id: 'scene-two'});await turn();
  assert.match(app.nodes.get('#app').innerHTML, /Choose a screen to describe/);
  assert.match(app.nodes.get('#app').innerHTML, /data-action="restore-scene"/);
  app.click('restore-scene');await turn();
  app.beforeUnload();
  assert.equal(app.backup().draft.sceneId, 'scene-two');
});


test('generate saves screen selection and materializes original captures without drawing or frame modules', async () => {
  const snapshot = savedSnapshot();
  snapshot.session.scenes[1].note = '';snapshot.session.scenes[1].annotations=[];snapshot.session.scenes[0].annotations=[];
  const unrelated = structuredClone(snapshot.session.scenes[1]);
  unrelated.id = 'scene-unused'; unrelated.title = 'Unrelated screen'; unrelated.captureId = 'c'.repeat(32); unrelated.note = '';
  unrelated.annotations = [{id: 'unused-mark', type: 'pen', points: [{x: .5, y: .5}]}];
  snapshot.session.scenes.push(unrelated);
  const app = boot({snapshot});
  await app.finishBootstrap(); app.click('resume-session'); await turn();
  app.click('generate');
  for (let i = 0; i < 8; i++) await turn();
  assert.equal(app.materialRequests.length, 1, 'actual app → save → materials flow must finish');
  assert.deepEqual(app.saves.at(-1).session.promptSceneIds, ['scene-one']);
  assert.match(app.materialRequests[0].path, /materials\?revision=9$/);
  assert.ok(app.calls.every(path => !path.includes('/frame/')));
  assert.match(app.nodes.get('#modal-root').innerHTML, /Prompt for your agent/);
  assert.doesNotMatch(app.saves.at(-1).session.prompt, /unused-mark|saved-arrow|Unrelated screen|Pen area|annotation-events/);
  assert.deepEqual(app.errors, []);
});


test('legacy audio target changes resolve one screen while actual screen switches stay uncertain', async () => {
  const snapshot = savedSnapshot();
  snapshot.session.transcript = [];
  snapshot.audio = [{id: 'audio-legacy', url: '/audio.wav', startMs: 0, endMs: 10000, transcriptionStatus: 'complete', contexts: [
    {tMs: 0, sceneId: 'scene-one', targetId: 'old-pen'},
    {tMs: 1000, sceneId: 'scene-one', targetId: 'old-arrow'},
    {tMs: 3000, sceneId: 'scene-two', targetId: null}
  ], transcription: [
    {startMs: 0, endMs: 2000, text: 'This describes the same screen.'},
    {startMs: 2000, endMs: 5000, text: 'This describes it while switching screens.'}
  ]}];
  const app = boot({snapshot});
  await app.finishBootstrap(); app.click('resume-session'); await turn(); app.click('generate');
  for (let i = 0; i < 8; i++) await turn();
  const items = app.saves.at(-1).session.transcript;
  assert.equal(items[0].sceneId, 'scene-one');
  assert.equal(items[0].unresolved, false);
  assert.equal(items[0].target, null);
  assert.equal(items[0].candidates.length, 1);
  assert.equal(items[1].unresolved, true);
  assert.equal(items[1].candidates.length, 2);
  assert.deepEqual(app.errors, []);
});


const settle = async () => {for(let i=0;i<20;i++)await turn();};

test('text toggle starts an independent version without microphone and timestamps notes until stop', async () => {
  const app=boot({snapshot:savedSnapshot()});await app.finishBootstrap();app.click('resume-session');await turn();
  app.click('toggle-text');await settle();
  assert.equal(app.starts.length,1);assert.equal(app.microphoneStarts(),0);
  assert.match(app.nodes.get('#voice-control').innerHTML,/data-action="toggle-text" aria-pressed="true"/);
  app.advanceTime(5000);app.input('Text description for the new version');app.submit();await turn();
  let saved=await app.flushSave();
  assert.equal(saved.session.runMode,'text');assert.equal(saved.session.transcript.length,1);assert.equal(saved.session.transcript[0].t,5);
  assert.deepEqual(saved.session.recordings,[]);assert.equal(app.archives.size,1);
  const old=structuredClone([...app.archives.values()][0]);
  app.click('toggle-text');await settle();app.beforeUnload();
  assert.equal(app.backup().session.runState,'stopped');const elapsed=app.backup().session.elapsedMs;
  app.advanceTime(20000);app.beforeUnload();assert.equal(app.backup().session.elapsedMs,elapsed);
  assert.deepEqual([...app.archives.values()][0],old);
  assert.match(app.materialRequests.at(-1).session.prompt,/relative to text start/);
  assert.doesNotMatch(app.materialRequests.at(-1).session.prompt,/Move this button to the right/);
});

test('switching toggles commits draft to old version, resets time and audio, and keeps history read-only', async () => {
  const app=boot({snapshot:savedSnapshot()});await app.finishBootstrap();app.click('toggle-text');await settle();
  app.advanceTime(2000);app.input('Description typed right before stopping');
  app.click('toggle-audio');await settle();
  assert.equal(app.starts.length,2);assert.equal(app.microphoneStarts(),1);
  assert.equal(app.archives.get('run-1').session.transcript[0].text,'Description typed right before stopping');
  assert.equal(app.archives.get('run-1').session.transcript[0].t,2);
  app.beforeUnload();assert.equal(app.backup().session.transcript.length,0);assert.equal(app.backup().session.elapsedMs,0);
  assert.match(app.nodes.get('#voice-control').innerHTML,/data-action="toggle-audio" aria-pressed="true"/);
  assert.match(app.nodes.get('#voice-control').innerHTML,/data-action="toggle-text" aria-pressed="false"/);
  const activeID=app.backup().session.id;app.click('history');await settle();
  assert.match(app.nodes.get('#modal-root').innerHTML,/Archive/);
  app.click('view-archive',{id:'run-1'});await settle();app.beforeUnload();
  assert.match(app.nodes.get('#modal-root').innerHTML,/read-only/);
  assert.match(app.nodes.get('#modal-root').innerHTML,/Description typed right before stopping/);
  assert.equal(app.backup().session.id,activeID);
});

test('microphone denial leaves a zero-time ready run and text mode never requests microphone', async () => {
  const app=boot({snapshot:savedSnapshot(),microphoneDenied:true});await app.finishBootstrap();app.click('toggle-audio');await settle();
  app.advanceTime(10000);app.beforeUnload();
  assert.equal(app.backup().session.runState,'ready');assert.equal(app.backup().session.elapsedMs,0);
  assert.match(app.nodes.get('#voice-control').innerHTML,/Mic access wasn/);
  app.click('toggle-text');await settle();
  assert.equal(app.microphoneStarts(),1);assert.equal(app.backup().session.runMode,'text');
});

test('reload interrupts an active run at saved elapsed without adding closed-window time', async () => {
  const snapshot=savedSnapshot();snapshot.session.runMode='text';snapshot.session.runState='recording';snapshot.session.elapsedMs=3000;snapshot.session.duration=3;
  const cached={...snapshot,clockRunning:true,updatedAt:Date.now()-60000};
  const app=boot({snapshot,cached});await app.finishBootstrap();app.click('resume-session');app.advanceTime(5000);app.beforeUnload();
  assert.equal(app.backup().session.runState,'interrupted');assert.equal(app.backup().session.elapsedMs,3000);
});


test('empty zero-capture text run can stop and start again without a materialization loop', async () => {
  const app=boot();await app.finishBootstrap();app.click('toggle-text');await settle();
  assert.equal(app.starts.length,1);app.click('toggle-text');await settle();
  assert.equal(app.materialRequests.length,0);app.click('toggle-text');await settle();
  assert.equal(app.starts.length,2);app.beforeUnload();assert.equal(app.backup().session.runState,'recording');
});

test('stop freezes elapsed before delayed microphone stop completes', async () => {
  const delay=deferred(),app=boot({snapshot:savedSnapshot(),stopDelay:delay});await app.finishBootstrap();app.click('toggle-audio');await settle();
  app.advanceTime(3000);app.click('toggle-audio');await turn();app.advanceTime(20000);delay.resolve();await settle();app.beforeUnload();
  assert.equal(app.backup().session.runState,'stopped');assert.equal(app.backup().session.elapsedMs,3000);
});

test('refresh started against an older version cannot merge its audio into a new run', async () => {
  const delay=deferred(),app=boot({snapshot:savedSnapshot(),deferRefresh:delay});await app.finishBootstrap();app.click('resume-session');await turn();
  const refresh=app.timers.find(timer=>timer.delay===3000).callback();await turn();
  app.click('toggle-text');await settle();assert.equal(app.starts.length,1);
  delay.resolve();await refresh;await settle();app.beforeUnload();
  assert.equal(app.backup().session.id,'run-1');assert.deepEqual(app.backup().session.transcript,[]);assert.deepEqual(app.backup().session.recordings,[]);
});

test('save failure after acquiring microphone preserves the real active state and can be stopped safely', async () => {
  let failed=false;
  const app=boot({snapshot:savedSnapshot(),saveFailure:saved=>{if(!failed&&saved.session.runMode==='audio'&&saved.session.runState==='recording'){failed=true;return true;}return false;}});
  await app.finishBootstrap();app.click('toggle-audio');await settle();app.beforeUnload();
  assert.equal(failed,true);assert.equal(app.backup().session.runState,'recording');
  assert.match(app.nodes.get('#voice-control').innerHTML,/Recording from mic · Save failed/);
  app.advanceTime(2000);app.click('toggle-audio');await settle();app.beforeUnload();assert.equal(app.backup().session.runState,'stopped');
});

test('a second tab cannot archive a version with a live editor lease', async () => {
  const app=boot({snapshot:savedSnapshot()});await app.finishBootstrap();
  app.stored.set('cuevia.editor.lease.v1',JSON.stringify({id:'other-live-editor',sessionId:'session-annotations',at:Date.now()}));
  app.click('toggle-text');await settle();
  assert.equal(app.starts.length,0);assert.equal(app.saves.length,0);
  assert.match(app.nodes.get('#toast').innerHTML,/Another window is recording this version/);
});


test('lost StartRun responses reconcile both new successors and an in-place first version', async () => {
  for(const reuseReady of [false,true]){
    const snapshot=savedSnapshot();if(reuseReady){snapshot.session.transcript=[];snapshot.session.notes='';snapshot.session.prompt='';snapshot.session.scenes=[];}
    const app=boot({snapshot,loseStartResponse:true,reuseReady});await app.finishBootstrap();app.click('toggle-text');await settle();app.beforeUnload();
    assert.equal(app.starts.length,1);assert.equal(app.backup().session.runState,'recording');assert.equal(app.backup().session.runMode,'text');
    assert.equal(app.backup().session.id,reuseReady?snapshot.session.id:'run-1');
  }
});


test('numbered point, arrow and rectangle restore undo and clear-all without freehand pen', async () => {
  const app=boot({snapshot:savedSnapshot(),withMarks:true});await app.finishBootstrap();app.click('toggle-text');await settle();
  const commit=mark=>app.annotationMounts.at(-1).onCommit(mark);
  commit({id:'point-1',type:'point',x:.2,y:.3});
  commit({id:'arrow-2',type:'arrow',x:.3,y:.4,x2:.6,y2:.7});
  commit({id:'rect-3',type:'rect',x:.4,y:.5,w:.2,h:.1});
  commit({id:'unsupported-pen',type:'pen',points:[{x:.1,y:.1}]});
  let scene=app.backup().session.scenes[0];assert.equal(scene.annotations.length,3);
  assert.match(app.nodes.get('#app').innerHTML,/data-action="undo-mark"/);
  assert.match(app.nodes.get('#app').innerHTML,/data-action="clear-marks"/);
  assert.doesNotMatch(app.nodes.get('#app').innerHTML,/data-tool="pen"/);
  app.input('This describes the area marked with a box.');
  app.click('clear-marks');await turn();
  scene=app.backup().session.scenes[0];assert.ok(scene.annotations.every(mark=>mark.hidden));
  assert.equal(app.backup().draft.draftContext.targetId,'rect-3');
  app.click('undo-mark');await turn();
  scene=app.backup().session.scenes[0];assert.ok(scene.annotations.every(mark=>!mark.hidden));
  assert.match(app.nodes.get('#app').innerHTML,/Screen 01 · #3/);
  app.click('undo-mark');await turn();
  scene=app.backup().session.scenes[0];assert.equal(scene.annotations[2].hidden,true);assert.equal(scene.annotations[1].hidden,false);
  app.input('');app.click('generate');await settle();
  const currentFrames=app.frameRequests.filter(request=>request.path.includes('/run-1/'));
  assert.equal(currentFrames.length,1,'visible structured marks require an annotated PNG');
  const frameRevision=currentFrames[0].path.split('revision=')[1],materialRevision=app.materialRequests.at(-1).path.split('revision=')[1];
  assert.equal(frameRevision,materialRevision);
  const prompt=app.materialRequests.at(-1).session.prompt;
  assert.match(prompt,/S01#1 point/);assert.match(prompt,/S01#2 arrow/);assert.doesNotMatch(prompt,/S01#3 rect|unsupported-pen/);
});

test('text notes keep numbered target anchors while screen-only legacy ambiguity remains cleared', async () => {
  const app=boot({snapshot:savedSnapshot(),withMarks:true});await app.finishBootstrap();app.click('toggle-text');await settle();
  app.annotationMounts.at(-1).onCommit({id:'point-current',type:'point',x:.2,y:.3});
  app.advanceTime(2000);app.input('Please change this position.');app.submit();await turn();
  const saved=await app.flushSave();assert.equal(saved.session.transcript[0].target,'point-current');
  assert.equal(saved.session.transcript[0].t,2);assert.match(app.prompt(saved.session),/S01#1/);
  app.click('clear-marks');await turn();
  const cleared=await app.flushSave();assert.equal(cleared.session.transcript[0].text,'Please change this position.');
  assert.doesNotMatch(app.prompt(cleared.session),/S01#1 point/);
});


test('menu settings tab preserves the active editor lease and never restores a recording session', async () => {
  const settingsData={apiKeyConfigured:true,model:'whisper-1',models:['whisper-1','gpt-4o-transcribe','gpt-4o-mini-transcribe'],storagePath:'/synthetic/Cuevia/.env'};
  const app=boot({query:'?settings',settingsData});
  app.stored.set('cuevia.editor.v1','active-recording-editor');
  app.stored.set('cuevia.editor.lease.v1',JSON.stringify({id:'active-recording-editor',sessionId:'active-recording',at:Date.now()}));
  const lease=app.stored.get('cuevia.editor.lease.v1');
  const field={value:'',placeholder:''},select={value:'whisper-1',addEventListener(){}},save={disabled:false},message={textContent:''},state={textContent:''};
  for(const [id,node] of Object.entries({'#openai-key':field,'#transcription-model':select,'#settings-save':save,'#settings-message':message,'#settings-key-state':state}))app.nodes.set(id,node);
  await turn();await turn();
  assert.deepEqual(app.calls,['/api/settings']);
  assert.equal(app.microphoneStarts(),0);
  assert.equal(app.timers.length,0,'standalone settings must not create session timers');
  assert.equal(app.writes.length,0,'standalone settings must not write session backups or ownership');
  assert.equal(app.stored.get('cuevia.editor.v1'),'active-recording-editor');
  assert.equal(app.stored.get('cuevia.editor.lease.v1'),lease);
  assert.match(app.nodes.get('#modal-root').innerHTML,/type="password"/);
  assert.match(app.nodes.get('#modal-root').innerHTML,/whisper-1 · default · segment times/);
  assert.match(app.nodes.get('#modal-root').innerHTML,/gpt-4o-mini-transcribe · no segment times/);
  assert.match(app.nodes.get('#modal-root').innerHTML,/Leave blank to keep it/);
  field.value='fake-new-settings-key';select.value='gpt-4o-transcribe';app.submit('settings-form');
  assert.equal(field.value,'','the key must be removed from the DOM immediately on submission');
  await turn();await turn();
  assert.deepEqual(app.settingsSaves,[{apiKey:'fake-new-settings-key',model:'gpt-4o-transcribe'}]);
  assert.equal(field.value,'');assert.match(message.textContent,/Saved\./);
  assert.doesNotMatch(app.nodes.get('#modal-root').innerHTML,/fake-new-settings-key/);
  assert.equal(app.stored.get('cuevia.editor.v1'),'active-recording-editor');
  assert.equal(app.stored.get('cuevia.editor.lease.v1'),lease);
  assert.equal(app.writes.length,0);
  app.submit('settings-form');await turn();await turn();
  assert.equal(app.settingsSaves[1].apiKey,'','model-only save must preserve the server-side key');
});

test('settings error responses never echo a provider or key value into the UI', async () => {
  const app=boot({query:'?settings',settingsData:{model:'whisper-1',apiKeyConfigured:false,models:['whisper-1'],storagePath:'/synthetic/.env'},settingsFailure:'fake-secret-do-not-render'});
  const field={value:'fake-secret-do-not-render'},message={textContent:''};
  app.nodes.set('#openai-key',field);app.nodes.set('#transcription-model',{value:'whisper-1',addEventListener(){}});app.nodes.set('#settings-save',{disabled:false});app.nodes.set('#settings-message',message);
  await turn();await turn();app.submit('settings-form');await turn();await turn();
  assert.equal(field.value,'');assert.match(message.textContent,/Couldn't save settings/);assert.doesNotMatch(message.textContent,/fake-secret/);
  assert.equal(app.writes.length,0);
});


test('GPT recording-wide transcripts keep every screen as a candidate without assigning a target', async () => {
  const snapshot=savedSnapshot();snapshot.session.transcript=[];
  snapshot.audio=[{id:'audio-gpt',url:'/audio.wav',startMs:2000,endMs:12000,transcriptionStatus:'complete',contexts:[
    {tMs:2000,sceneId:'scene-one',targetId:'saved-arrow'},
    {tMs:6000,sceneId:'scene-two',targetId:null}
  ],transcription:[{startMs:0,endMs:10000,text:'Keeps every sentence of the full recording as is.',timingEstimated:true}]}];
  const app=boot({snapshot});await app.finishBootstrap();app.click('resume-session');await turn();app.click('generate');await settle();
  const item=app.saves.at(-1).session.transcript[0],prompt=app.saves.at(-1).session.prompt;
  assert.equal(item.sceneId,null);assert.equal(item.target,null);assert.equal(item.unresolved,true);assert.equal(item.timingUncertain,true);assert.equal(item.timingEstimated,true);
  assert.deepEqual(item.candidates.map(value=>value.sceneId),['scene-one','scene-two']);
  assert.match(app.nodes.get('#descriptions').innerHTML,/no per-sentence times · whole recording span/i);
  assert.match(app.nodes.get('#descriptions').innerHTML,/Recording 00:02–00:12/);
  assert.match(prompt,/no per-sentence times · whole recording span/i);assert.match(prompt,/link\?/);assert.match(prompt,/candidates S01/);assert.match(prompt,/S02/);
});

test('a GPT recording with one screen still requires confirmation and does not infer a target', async () => {
  const snapshot=savedSnapshot();snapshot.session.transcript=[];
  snapshot.audio=[{id:'audio-gpt-single',url:'/audio.wav',startMs:0,endMs:5000,transcriptionStatus:'complete',contexts:[{tMs:0,sceneId:'scene-one',targetId:'saved-arrow'}],transcription:[{startMs:0,endMs:5000,text:'Even a single screen needs its link confirmed.',timingEstimated:true}]}];
  const app=boot({snapshot});await app.finishBootstrap();app.click('resume-session');await turn();app.click('generate');await settle();
  const item=app.saves.at(-1).session.transcript[0];assert.equal(item.sceneId,null);assert.equal(item.target,null);assert.equal(item.unresolved,true);assert.equal(item.candidates.length,1);
  assert.match(app.saves.at(-1).session.prompt,/no per-sentence times · whole recording span/i);
});
