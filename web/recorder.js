(() => {
  'use strict';

  const SAMPLE_RATE = 16000;
  const activeStates = new Set(['requesting', 'recording', 'stopping']);

  function recorderError(code, cause) {
    const error = new Error(code, cause ? {cause} : undefined);
    error.code = code;
    return error;
  }

  // decodeAudioData resamples to the AudioContext rate. The fallback also handles
  // contexts that cannot be opened at 16 kHz; every persisted WAV has one format.
  function encodeWav(audio, outputRate = SAMPLE_RATE) {
    if (!audio || !Number.isInteger(audio.length) || audio.length < 1 ||
        !Number.isFinite(audio.sampleRate) || audio.sampleRate <= 0 ||
        !Number.isInteger(audio.numberOfChannels) || audio.numberOfChannels < 1 ||
        !Number.isInteger(outputRate) || outputRate < 1) {
      throw recorderError('invalid_audio');
    }
    const channels = Array.from({length: audio.numberOfChannels}, (_, i) => audio.getChannelData(i));
    const count = Math.max(1, Math.round(audio.length * outputRate / audio.sampleRate));
    const bytes = new ArrayBuffer(44 + count * 2);
    const view = new DataView(bytes);
    const writeText = (offset, text) => {
      for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i));
    };
    writeText(0, 'RIFF'); view.setUint32(4, 36 + count * 2, true);
    writeText(8, 'WAVE'); writeText(12, 'fmt '); view.setUint32(16, 16, true);
    view.setUint16(20, 1, true); view.setUint16(22, 1, true);
    view.setUint32(24, outputRate, true); view.setUint32(28, outputRate * 2, true);
    view.setUint16(32, 2, true); view.setUint16(34, 16, true);
    writeText(36, 'data'); view.setUint32(40, count * 2, true);
    const mono = index => {
      let value = 0;
      for (const channel of channels) value += Number.isFinite(channel[index]) ? channel[index] : 0;
      return value / channels.length;
    };
    const ratio = audio.sampleRate / outputRate;
    for (let i = 0; i < count; i++) {
      let sample;
      if (ratio >= 1) {
        // Integrate each source interval instead of dropping samples when reducing
        // the rate. Chrome's normal 16 kHz decode path has ratio === 1.
        const from = i * ratio, to = Math.min(audio.length, (i + 1) * ratio);
        let sum = 0;
        for (let n = Math.floor(from); n < Math.ceil(to); n++) {
          sum += mono(Math.min(n, audio.length - 1)) * (Math.min(to, n + 1) - Math.max(from, n));
        }
        sample = sum / Math.max(Number.EPSILON, to - from);
      } else {
        const at = i * ratio, left = Math.min(Math.floor(at), audio.length - 1);
        const right = Math.min(left + 1, audio.length - 1);
        sample = mono(left) * (1 - (at - left)) + mono(right) * (at - left);
      }
      sample = Math.max(-1, Math.min(1, sample));
      view.setInt16(44 + i * 2, Math.round(sample * (sample < 0 ? 32768 : 32767)), true);
    }
    return new Blob([bytes], {type: 'audio/wav'});
  }

  function create(options = {}) {
    let state = 'idle', run = null, sequence = 0;
    const call = (name, ...args) => {
      // Consumer UI work must not interrupt MediaRecorder's data-drain sequence.
      try {
        const value = options[name]?.(...args);
        if (value && typeof value.catch === 'function') value.catch(() => {});
      } catch {}
    };
    const setState = (next, details = {}) => { state = next; call('onState', next, details); };
    const now = current => {
      const value = Number(current.getTimeMs());
      if (!Number.isFinite(value)) throw recorderError('invalid_clock');
      current.lastMs = Math.max(current.lastMs, Math.max(0, value));
      return current.lastMs;
    };
    const deferred = () => {
      let resolve, reject;
      const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
      // Auto-stop failures may precede the consumer's subsequent stop() call.
      promise.catch(() => {});
      return {promise, resolve, reject};
    };
    const stopInput = current => {
      for (const remove of current.trackCleanup.splice(0)) remove();
      for (const track of current.stream?.getTracks() || []) track.stop();
      if (current.frame != null) window.cancelAnimationFrame?.(current.frame);
      current.frame = null;
      try { current.source?.disconnect(); current.analyser?.disconnect(); } catch {}
      current.source = null; current.analyser = null;
    };
    const closeContext = async current => {
      try { if (current.audio?.state !== 'closed') await current.audio?.close(); } catch {}
    };
    const fail = async (current, error) => {
      if (current.failed || current.canceled) return;
      current.failed = true;
      try { if (current.media?.state && current.media.state !== 'inactive') current.media.stop(); } catch {}
      stopInput(current);
      await closeContext(current);
      if (run === current) setState('error', {error, startMs: current.startMs, endMs: current.endMs});
      current.started.reject(error);
      current.completed.reject(error);
      call('onError', error);
    };
    const markContext = context => {
      const current = run;
      if (!current || state !== 'recording' || current.canceled) return;
      const tMs = context.tMs == null ? now(current) : Math.max(current.startMs, current.contexts.at(-1)?.tMs || 0, Number(context.tMs));
      if (!Number.isFinite(tMs)) return;
      current.lastMs = Math.max(current.lastMs, tMs);
      const value = {tMs, sceneId: context.sceneId || null, targetId: context.sceneId&&context.sceneId!=='unbound'?context.targetId||null:null};
      const previous = current.contexts.at(-1);
      if (previous?.sceneId === value.sceneId && previous?.targetId === value.targetId) return;
      current.contexts.push(value);
    };
    const finish = async current => {
      if (current.finalizing || current.canceled || current.failed) return;
      current.finalizing = true;
      if (current.endMs == null) current.endMs = now(current);
      stopInput(current);
      if (run === current && state !== 'stopping') setState('stopping', {startMs: current.startMs, endMs: current.endMs});
      const originalBlob = new Blob(current.chunks, {type: current.media.mimeType || 'audio/webm'});
      try {
        if (!originalBlob.size) throw recorderError('empty_audio');
        const audio = await current.audio.decodeAudioData(await originalBlob.arrayBuffer());
        if (current.canceled) return;
        const blob = encodeWav(audio);
        const sampleCount = (blob.size - 44) / 2;
        const durationMs = sampleCount / SAMPLE_RATE * 1000;
        if (Math.abs(durationMs - (current.endMs - current.startMs)) > 250) {
          current.discontinuities.push({tMs: current.endMs, reason: 'audio-duration-mismatch',
            sessionDurationMs: current.endMs - current.startMs, audioDurationMs: durationMs});
        }
        const result = {blob, originalBlob, startMs: current.startMs, endMs: current.endMs,
          durationMs, sampleRate: SAMPLE_RATE, channels: 1, sampleCount, timing: 'estimated',
          startRequestMs: current.startRequestMs,
          startUncertaintyMs: Math.max(0, current.startMs - current.startRequestMs),
          contexts: current.contexts.map(context => ({...context,
            sampleOffset: Math.max(0, Math.min(sampleCount, Math.round((context.tMs - current.startMs) * SAMPLE_RATE / 1000)))})),
          discontinuities: current.discontinuities.slice()};
        await closeContext(current);
        if (current.canceled) return;
        current.result = result;
        if (run === current) setState('ready', {startMs: result.startMs, endMs: result.endMs});
        current.completed.resolve(result);
        call('onComplete', result);
      } catch (cause) {
        const error = recorderError(typeof cause.code === 'string' && cause.code ? cause.code : 'audio_decode_failed', cause);
        error.originalBlob = originalBlob;
        error.startMs = current.startMs; error.endMs = current.endMs;
        error.contexts = current.contexts.slice(); error.discontinuities = current.discontinuities.slice();
        await fail(current, error);
      }
    };
    const requestStop = current => {
      if (current.canceled || current.failed || current.finalizing || current.stopRequested) return;
      current.stopRequested = true;
      current.endMs = now(current);
      if (run === current) setState('stopping', {startMs: current.startMs, endMs: current.endMs});
      try {
        if (current.media?.state !== 'inactive') current.media.stop();
        else finish(current);
      } catch (error) { fail(current, error); }
      stopInput(current);
    };
    const cancel = async () => {
      const current = run;
      if (!current) { setState('idle'); return; }
      current.canceled = true;
      // Invalidate before stopping so stale permission grants cannot revive capture.
      run = null;
      current.started.reject(recorderError('recording_canceled'));
      current.completed.resolve(null);
      try { if (current.media?.state && current.media.state !== 'inactive') current.media.stop(); } catch {}
      stopInput(current);
      await closeContext(current);
      current.chunks.length = 0;
      if (!run) setState('idle');
    };
    const start = async ({getTimeMs, getContext = () => ({})} = {}) => {
      if (activeStates.has(state)) throw recorderError('recorder_busy');
      if (typeof getTimeMs !== 'function') throw recorderError('clock_required');
      const current = {id: ++sequence, getTimeMs, getContext, lastMs: 0, chunks: [], contexts: [],
        discontinuities: [], trackCleanup: [], started: deferred(), completed: deferred(), startMs: null,
        endMs: null, frame: null, canceled: false};
      run = current;
      setState('requesting');
      try {
        if (!navigator.mediaDevices?.getUserMedia || !window.MediaRecorder || !window.AudioContext) {
          throw recorderError('recording_unsupported');
        }
        current.stream = await navigator.mediaDevices.getUserMedia({audio: {
          channelCount: {ideal: 1}, echoCancellation: true, noiseSuppression: true
        }, video: false});
        if (current.canceled || run !== current) {
          stopInput(current); throw recorderError('recording_canceled');
        }
        if (!current.stream.getAudioTracks().length) throw recorderError('microphone_missing');
        try { current.audio = new window.AudioContext({sampleRate: SAMPLE_RATE}); }
        catch { current.audio = new window.AudioContext(); }
        const mimeType = ['audio/webm;codecs=opus', 'audio/webm'].find(type => window.MediaRecorder.isTypeSupported(type));
        if (!mimeType) throw recorderError('recording_format_unsupported');
        current.media = new window.MediaRecorder(current.stream, {mimeType, audioBitsPerSecond: 64000});
        current.media.addEventListener('start', () => {
          if (current.canceled || current.failed || current.stopRequested || run !== current) return;
          try {
            current.startMs = now(current);
            const context = getContext() || {};
            current.contexts.push({tMs: current.startMs, sceneId: context.sceneId || null, targetId: context.sceneId&&context.sceneId!=='unbound'?context.targetId||null:null});
            setState('recording', {startMs: current.startMs});
            current.started.resolve({startMs: current.startMs});
          } catch (error) {
            try { current.media.stop(); } catch {}
            fail(current, error);
          }
        });
        current.media.addEventListener('dataavailable', event => {
          if (current.canceled || current.failed || !event.data?.size) return;
          current.chunks.push(event.data);
          call('onChunk', event.data, {tMs: now(current)});
        });
        current.media.addEventListener('stop', () => { finish(current).catch(error => fail(current, error)); });
        current.media.addEventListener('error', event => {
          if (current.canceled || current.failed) return;
          current.discontinuities.push({tMs: now(current), reason: 'recorder-error'});
          call('onError', event.error || recorderError('recording_interrupted'));
          if (current.startMs != null) requestStop(current);
          else fail(current, event.error || recorderError('recording_failed'));
        });
        for (const track of current.stream.getAudioTracks()) {
          for (const [event, reason] of [['ended', 'track-ended'], ['mute', 'track-muted']]) {
            const handler = () => {
              if (current.canceled || current.failed || current.stopRequested) return;
              current.discontinuities.push({tMs: now(current), reason});
              if (current.startMs != null) requestStop(current);
              else {
                try { current.media.stop(); } catch {}
                fail(current, recorderError('microphone_disconnected'));
              }
            };
            track.addEventListener(event, handler);
            current.trackCleanup.push(() => track.removeEventListener(event, handler));
          }
        }
        if (options.onLevel && window.requestAnimationFrame) {
          current.source = current.audio.createMediaStreamSource(current.stream);
          current.analyser = current.audio.createAnalyser();
          current.analyser.fftSize = 256;
          current.source.connect(current.analyser);
          const samples = new Float32Array(current.analyser.fftSize);
          const level = () => {
            if (run !== current || current.canceled || current.stopRequested) return;
            current.analyser.getFloatTimeDomainData(samples);
            let sum = 0;
            for (const sample of samples) sum += sample * sample;
            call('onLevel', Math.min(1, Math.sqrt(sum / samples.length)));
            current.frame = window.requestAnimationFrame(level);
          };
          current.audio.resume().catch(() => {});
          current.frame = window.requestAnimationFrame(level);
        }
        current.startRequestMs = now(current);
        current.media.start(1000);
        return await current.started.promise;
      } catch (cause) {
        if (current.canceled || run !== current) throw recorderError('recording_canceled', cause);
        const code = cause.name === 'NotAllowedError' || cause.name === 'SecurityError' ? 'microphone_denied' :
          cause.name === 'NotFoundError' || cause.name === 'DevicesNotFoundError' ? 'microphone_missing' :
          cause.name === 'NotReadableError' ? 'microphone_unavailable' :
          typeof cause.code === 'string' && cause.code ? cause.code : 'recording_failed';
        const error = typeof cause.code === 'string' && cause.code ? cause : recorderError(code, cause);
        try { if (current.media?.state && current.media.state !== 'inactive') current.media.stop(); } catch {}
        await fail(current, error);
        throw error;
      }
    };
    const stop = async () => {
      const current = run;
      if (!current) return null;
      if (state === 'requesting') { await cancel(); return null; }
      if (state === 'recording') requestStop(current);
      return current.completed.promise;
    };
    return {start, stop, cancel, markContext,
      get state() { return state; }, get isRecording() { return state === 'recording'; }};
  }

  window.CueviaRecorder = {create, encodeWav};
})();
