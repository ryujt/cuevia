import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../web/recorder.js', import.meta.url), 'utf8');
const turn = () => new Promise(resolve => setImmediate(resolve));
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return {promise, resolve, reject};
};
const audio = (channels, sampleRate = 16000) => ({
  sampleRate, numberOfChannels: channels.length, length: channels[0].length,
  getChannelData(index) { return channels[index]; }
});
function setup(config = {}) {
  const tracks = [new class extends EventTarget {
    readyState = 'live';
    stopCalls = 0;
    stop() { this.stopCalls++; this.readyState = 'ended'; }
  }()];
  const stream = {getTracks: () => tracks, getAudioTracks: () => tracks};
  const contexts = [], media = [], frames = new Map();
  const env = {now: 100, stream, tracks, contexts, media, frames, audio: config.audio || audio([new Float32Array(16000)])};
  class Recorder extends EventTarget {
    static isTypeSupported() { return config.supported !== false; }
    state = 'inactive';
    mimeType = 'audio/webm;codecs=opus';
    stopCalls = 0;
    constructor(input, settings) {
      super();
      if (config.constructorError) throw config.constructorError;
      this.input = input; this.settings = settings; media.push(this);
    }
    start(timeslice) {
      if (config.startError) throw config.startError;
      this.timeslice = timeslice; this.state = 'recording';
      if (config.autoStart !== false) queueMicrotask(() => this.dispatchEvent(new Event('start')));
    }
    stop() {
      this.stopCalls++; this.state = 'inactive';
      queueMicrotask(() => {
        this.dispatchEvent(Object.assign(new Event('dataavailable'), {data: new Blob(['last-real-container-data'])}));
        this.dispatchEvent(new Event('stop'));
      });
    }
  }
  class Audio {
    state = 'running';
    closeCalls = 0;
    constructor(settings) { this.settings = settings; contexts.push(this); }
    async decodeAudioData(bytes) {
      this.decodedBytes = bytes.byteLength;
      if (config.decode) return config.decode(bytes);
      return env.audio;
    }
    async close() { this.closeCalls++; this.state = 'closed'; }
    async resume() { this.state = 'running'; }
    createMediaStreamSource() { return {connect() {}, disconnect() {}}; }
    createAnalyser() { return {fftSize: 256, getFloatTimeDomainData(data) { data.fill(.25); }, disconnect() {}}; }
  }
  const sandbox = {Blob, DataView, ArrayBuffer, Float32Array, Number, Math, Set, Promise,
    navigator: {mediaDevices: {getUserMedia: config.getUserMedia || (async () => stream)}},
    window: {MediaRecorder: Recorder, AudioContext: Audio,
      requestAnimationFrame(fn) { const id = frames.size + 1; frames.set(id, fn); return id; },
      cancelAnimationFrame(id) { frames.delete(id); }}
  };
  vm.runInNewContext(source, sandbox, {filename: 'recorder.js'});
  env.api = sandbox.window.CueviaRecorder;
  env.options = {getTimeMs: () => env.now, getContext: () => ({sceneId: 'scene-a'})};
  return env;
}
const values = async blob => {
  const data = new DataView(await blob.arrayBuffer());
  return Array.from({length: (blob.size - 44) / 2}, (_, i) => data.getInt16(44 + i * 2, true));
};

test('WAV stores a RIFF PCM16 mono header and clipped signed samples', async () => {
  const env = setup();
  const blob = env.api.encodeWav(audio([new Float32Array([-2, -1, 0, .5, 1, 2, NaN])]));
  const bytes = await blob.arrayBuffer(), view = new DataView(bytes);
  assert.equal(blob.type, 'audio/wav');
  assert.equal(new TextDecoder().decode(bytes.slice(0, 4)), 'RIFF');
  assert.equal(new TextDecoder().decode(bytes.slice(8, 12)), 'WAVE');
  assert.equal(view.getUint32(4, true), blob.size - 8);
  assert.equal(view.getUint16(20, true), 1);
  assert.equal(view.getUint16(22, true), 1);
  assert.equal(view.getUint32(24, true), 16000);
  assert.equal(view.getUint32(28, true), 32000);
  assert.equal(view.getUint16(34, true), 16);
  assert.equal(view.getUint32(40, true), 14);
  assert.deepEqual(await values(blob), [-32768, -32768, 0, 16384, 32767, 32767, 0]);
});

test('WAV mixes stereo and resamples fallback audio rather than lying in its header', async () => {
  const env = setup();
  const sourceAudio = audio([new Float32Array([1, 1, -1, -1]), new Float32Array([0, 0, 0, 0])], 32000);
  const blob = env.api.encodeWav(sourceAudio);
  assert.equal(blob.size, 48);
  assert.deepEqual(await values(blob), [16384, -16384]);
  assert.throws(() => env.api.encodeWav(audio([new Float32Array()])), {code: 'invalid_audio'});
});

test('UI reports recording only after MediaRecorder start; stop waits for final data and caches result', async () => {
  const env = setup({autoStart: false});
  const states = [], complete = [];
  const recorder = env.api.create({onState: state => states.push(state), onComplete: result => complete.push(result)});
  const starting = recorder.start(env.options);
  await turn();
  assert.equal(recorder.state, 'requesting');
  assert.equal(recorder.isRecording, false);
  assert.equal(env.media[0].timeslice, 1000);
  env.now = 250;
  env.media[0].dispatchEvent(new Event('start'));
  assert.equal((await starting).startMs, 250);
  assert.equal(recorder.isRecording, true);
  env.now = 1250;
  const stopping = recorder.stop();
  assert.equal(recorder.state, 'stopping');
  const result = await stopping;
  assert.equal(result.blob.type, 'audio/wav');
  assert.equal(result.blob.size, 32044);
  assert.equal(result.startMs, 250);
  assert.equal(result.endMs, 1250);
  assert.equal(result.durationMs, 1000);
  assert.equal(result.sampleRate, 16000);
  assert.equal(env.contexts[0].decodedBytes, result.originalBlob.size);
  assert.equal(env.contexts[0].state, 'closed');
  assert.equal(env.tracks[0].readyState, 'ended');
  assert.equal(complete.length, 1);
  assert.equal(await recorder.stop(), result);
  assert.equal(env.media[0].stopCalls, 1);
  assert.equal(recorder.isRecording, false);
  assert.deepEqual(states, ['requesting', 'recording', 'stopping', 'ready']);
});

test('screen and numbered-target changes preserve session timestamps and WAV offsets', async () => {
  const env = setup();
  env.options.getContext = () => ({sceneId: 'scene-a', targetId: 'legacy-start'});
  const recorder = env.api.create();
  await recorder.start(env.options);
  recorder.markContext({sceneId: 'scene-a', targetId: 'legacy-other', tMs: 200});
  env.now = 400;
  recorder.markContext({sceneId: 'scene-b', targetId: null, tMs: 350});
  recorder.markContext({sceneId: 'scene-b', targetId: null, tMs: 400});
  env.now = 200; // The consumer clock cannot move later events backwards.
  recorder.markContext({sceneId: 'scene-b', targetId: 'point-b'});
  recorder.markContext({sceneId: 'scene-c'});
  env.now = 1100;
  const result = await recorder.stop();
  assert.equal(result.contexts.length, 5);
  assert.deepEqual(Array.from(result.contexts,context=>context.tMs),[100,200,350,400,400]);
  assert.deepEqual(Array.from(result.contexts,context=>context.sampleOffset),[0,1600,4000,4800,4800]);
  assert.deepEqual(Array.from(result.contexts,context=>context.targetId),['legacy-start','legacy-other',null,'point-b',null]);
  assert.equal(result.contexts[4].sceneId,'scene-c');
  assert.equal(env.frames.size, 0);
});

test('screen contexts retain null and unbound transitions and returning to an earlier screen', async () => {
  const env = setup();
  const recorder = env.api.create();
  await recorder.start(env.options);
  recorder.markContext({sceneId: null, tMs: 200});
  recorder.markContext({tMs: 250, targetId: 'ignored'});
  recorder.markContext({sceneId: 'unbound', tMs: 300});
  recorder.markContext({sceneId: 'scene-a', tMs: 400});
  env.now = 1100;
  const result = await recorder.stop();
  assert.deepEqual(Array.from(result.contexts, context => context.sceneId), ['scene-a', null, 'unbound', 'scene-a']);
  assert.deepEqual(Array.from(result.contexts, context => context.tMs), [100, 200, 300, 400]);
  assert.deepEqual(Array.from(result.contexts, context => context.sampleOffset), [0, 1600, 3200, 4800]);
  assert.ok(result.contexts.every(context => Object.hasOwn(context, 'targetId')));
});

test('permission rejection never enters recording and allows a later genuine retry', async () => {
  let attempts = 0;
  const env = setup({getUserMedia: async () => {
    if (!attempts++) throw Object.assign(new Error('denied'), {name: 'NotAllowedError'});
    return env.stream;
  }});
  const states = [], errors = [];
  const recorder = env.api.create({onState: state => states.push(state), onError: error => errors.push(error)});
  await assert.rejects(recorder.start(env.options), {code: 'microphone_denied'});
  assert.equal(recorder.state, 'error');
  assert.equal(recorder.isRecording, false);
  assert.equal(env.media.length, 0);
  assert.equal(env.contexts.length, 0);
  assert.deepEqual(states, ['requesting', 'error']);
  assert.equal(errors.length, 1);
  await recorder.start(env.options);
  env.now = 1100;
  await recorder.stop();
  assert.equal(recorder.state, 'ready');
});

test('cancellation while permission is pending stops a late grant and cannot revive recording', async () => {
  const permission = deferred();
  const env = setup({getUserMedia: () => permission.promise});
  const states = [], completions = [];
  const recorder = env.api.create({onState: state => states.push(state), onComplete: item => completions.push(item)});
  const starting = recorder.start(env.options);
  const rejected = assert.rejects(starting, {code: 'recording_canceled'});
  assert.equal(await recorder.stop(), null);
  permission.resolve(env.stream);
  await rejected;
  assert.equal(env.tracks[0].readyState, 'ended');
  assert.equal(env.media.length, 0);
  assert.equal(recorder.state, 'idle');
  assert.deepEqual(states, ['requesting', 'idle']);
  assert.equal(completions.length, 0);
});

test('format and MediaRecorder startup failure release device and AudioContext', async () => {
  for (const config of [{supported: false}, {startError: new Error('failed start')}]) {
    const env = setup(config);
    const recorder = env.api.create();
    await assert.rejects(recorder.start(env.options));
    assert.equal(recorder.state, 'error');
    assert.equal(env.tracks[0].readyState, 'ended');
    assert.equal(env.contexts[0].state, 'closed');
    assert.equal(recorder.isRecording, false);
  }
});

test('track interruption closes capture, drains once, and records the session-clock discontinuity', async () => {
  for (const [event, reason] of [['ended', 'track-ended'], ['mute', 'track-muted']]) {
    const env = setup();
    const complete = [];
    const recorder = env.api.create({onComplete: result => complete.push(result)});
    await recorder.start(env.options);
    env.now = 1100;
    env.tracks[0].dispatchEvent(new Event(event));
    const result = await recorder.stop();
    assert.equal(result.discontinuities.length, 1);
    assert.equal(result.discontinuities[0].reason, reason);
    assert.equal(result.discontinuities[0].tMs, 1100);
    assert.equal(result.endMs, 1100);
    assert.equal(env.media[0].stopCalls, 1);
    assert.equal(complete.length, 1);
    assert.equal(env.contexts[0].state, 'closed');
  }
});

test('duration discrepancy is explicit and context offsets never exceed available PCM', async () => {
  const env = setup();
  const recorder = env.api.create();
  await recorder.start(env.options);
  env.now = 7100;
  recorder.markContext({sceneId: 'scene-after-gap'});
  env.now = 8100;
  const result = await recorder.stop();
  assert.equal(result.endMs, 8100);
  assert.equal(result.durationMs, 1000);
  assert.equal(result.contexts[1].sampleOffset, 16000);
  assert.equal(result.discontinuities[0].reason, 'audio-duration-mismatch');
  assert.equal(result.discontinuities[0].sessionDurationMs, 8000);
  assert.equal(result.timing, 'estimated');
});

test('decode failure preserves the actual original container and never completes with a false WAV', async () => {
  const env = setup({decode: async () => { throw new Error('corrupt container'); }});
  env.options.getContext = () => ({sceneId: 'scene-a', targetId: 'legacy-start'});
  const errors = [], complete = [];
  const recorder = env.api.create({onError: error => errors.push(error), onComplete: result => complete.push(result)});
  await recorder.start(env.options);
  env.now = 1100;
  await assert.rejects(recorder.stop(), error => {
    assert.equal(error.code, 'audio_decode_failed');
    assert.equal(error.originalBlob.type, 'audio/webm;codecs=opus');
    assert.equal(error.originalBlob.size, new Blob(['last-real-container-data']).size);
    assert.equal(error.startMs, 100);
    assert.equal(error.contexts[0].sceneId, 'scene-a');
    assert.equal(error.contexts[0].targetId,'legacy-start');
    return true;
  });
  assert.equal(recorder.state, 'error');
  assert.equal(env.contexts[0].state, 'closed');
  assert.equal(env.tracks[0].readyState, 'ended');
  assert.equal(complete.length, 0);
  assert.equal(errors.length, 1);
});

test('cancel during decoding drops completion and closes every acquired resource', async () => {
  const decoding = deferred();
  const env = setup({decode: () => decoding.promise});
  const complete = [];
  const recorder = env.api.create({onComplete: result => complete.push(result)});
  await recorder.start(env.options);
  env.now = 1100;
  const stopping = recorder.stop();
  await turn();
  await recorder.cancel();
  decoding.resolve(env.audio);
  await turn();
  assert.equal(await stopping, null);
  assert.equal(recorder.state, 'idle');
  assert.equal(env.contexts[0].state, 'closed');
  assert.equal(env.tracks[0].readyState, 'ended');
  assert.equal(complete.length, 0);
});

test('second start is rejected, audio levels are read only, and cleanup cancels their animation', async () => {
  const env = setup();
  const levels = [];
  const recorder = env.api.create({onLevel: level => levels.push(level)});
  await recorder.start(env.options);
  await assert.rejects(recorder.start(env.options), {code: 'recorder_busy'});
  assert.equal(env.media.length, 1);
  const levelFrame = [...env.frames.values()][0];
  env.frames.clear();
  levelFrame();
  assert.equal(levels[0], .25);
  env.now = 1100;
  await recorder.stop();
  assert.equal(env.frames.size, 0);
});

test('native DOMException numeric codes normalize to actionable microphone errors', async () => {
  for (const [cause, code] of [
    [new DOMException('Requested device not found', 'NotFoundError'), 'microphone_missing'],
    [Object.assign(new Error('No matching input'), {name: 'NotFoundError', code: 0}), 'microphone_missing'],
    [new DOMException('Permission denied', 'NotAllowedError'), 'microphone_denied'],
    [Object.assign(new Error('Permission denied'), {name: 'NotAllowedError', code: 18}), 'microphone_denied'],
    [new DOMException('Capture blocked', 'SecurityError'), 'microphone_denied'],
    [new DOMException('Device busy', 'NotReadableError'), 'microphone_unavailable']
  ]) {
    const env = setup({getUserMedia: async () => { throw cause; }});
    const states = [], errors = [];
    const recorder = env.api.create({onState: state => states.push(state), onError: error => errors.push(error)});
    await assert.rejects(recorder.start(env.options), error => {
      assert.equal(error.code, code);
      assert.equal(typeof error.code, 'string');
      assert.equal(error.cause, cause);
      return true;
    });
    assert.equal(errors.length, 1);
    assert.equal(errors[0].code, code);
    assert.equal(recorder.isRecording, false);
    assert.deepEqual(states, ['requesting', 'error']);
    assert.equal(env.media.length, 0);
  }
});

test('native numeric decoding errors retain source audio under the stable audio_decode_failed code', async () => {
  const cause = new DOMException('Detached encoded audio buffer', 'DataCloneError');
  assert.equal(typeof cause.code, 'number');
  assert.ok(cause.code > 0);
  const env = setup({decode: async () => { throw cause; }});
  const errors = [];
  const recorder = env.api.create({onError: error => errors.push(error)});
  await recorder.start(env.options);
  env.now = 1100;
  await assert.rejects(recorder.stop(), error => {
    assert.equal(error.code, 'audio_decode_failed');
    assert.equal(error.cause, cause);
    assert.ok(error.originalBlob.size > 0);
    return true;
  });
  assert.equal(errors.length, 1);
  assert.equal(errors[0].code, 'audio_decode_failed');
  assert.equal(env.tracks[0].readyState, 'ended');
  assert.equal(env.contexts[0].state, 'closed');
});
