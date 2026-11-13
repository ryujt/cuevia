import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../web/frame.js', import.meta.url), 'utf8');
const signature = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10]);
function setup(config = {}) {
  const calls = [], images = [], canvases = [], stateStack = [];
  const ctx = {font: '', fillStyle: '', strokeStyle: '', lineWidth: 1, dash: [],
    textAlign: 'start', textBaseline: 'alphabetic', lineCap: 'butt', lineJoin: 'miter'};
  const properties = ['font', 'fillStyle', 'strokeStyle', 'lineWidth', 'dash', 'textAlign', 'textBaseline', 'lineCap', 'lineJoin'];
  const state = () => Object.fromEntries(properties.map(name => [name, ctx[name]]));
  for (const name of ['drawImage', 'beginPath', 'moveTo', 'lineTo', 'arc', 'rect', 'fill', 'stroke', 'fillRect', 'fillText']) {
    ctx[name] = (...args) => {
      if (config.drawError && name === 'drawImage') throw config.drawError;
      calls.push({name, args, ...state()});
    };
  }
  ctx.save = () => stateStack.push(state());
  ctx.restore = () => Object.assign(ctx, stateStack.pop());
  ctx.setLineDash = dash => { ctx.dash = [...dash]; };
  ctx.measureText = text => ({width: [...text].length * 6});
  class Image {
    naturalWidth = config.width ?? 1200;
    naturalHeight = config.height ?? 760;
    constructor() { images.push(this); }
    async decode() { if (config.decodeError) throw config.decodeError; }
  }
  const doc = {createElement(name) {
    assert.equal(name, 'canvas');
    const canvas = {width: 0, height: 0,
      getContext(type) { assert.equal(type, '2d'); return config.noContext ? null : ctx; },
      toBlob(callback, type) {
        if (config.exportError) throw config.exportError;
        this.exportType = type;
        this.exportWidth = this.width; this.exportHeight = this.height;
        queueMicrotask(() => callback(config.nullBlob ? null : new Blob([signature], {type: 'image/png'})));
      }};
    canvases.push(canvas); return canvas;
  }};
  const sandbox = {URL, Image, document: doc, Set, Number, Math, Promise,
    window: {location: {href: 'http://127.0.0.1:8742/?demo=1', origin: 'http://127.0.0.1:8742'}}};
  vm.runInNewContext(source, sandbox, {filename: 'frame.js'});
  return {api: sandbox.window.CueviaFrame, ctx, calls, images, canvases};
}
const scene = annotations => ({imageUrl: '/captures/screen.png', width: 1, height: 1, annotations});
const close = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9, `${actual} differs from ${expected}`);

test('exports decoded native pixels to detached PNG canvas, ignoring display metadata and device scale', async () => {
  const env = setup({width: 2400, height: 1520});
  const result = await env.api.render(scene([]));
  assert.equal(result.type, 'image/png');
  assert.deepEqual(new Uint8Array(await result.arrayBuffer()), signature);
  assert.equal(env.images[0].src, 'http://127.0.0.1:8742/captures/screen.png');
  assert.equal(env.images[0].decoding, 'async');
  assert.deepEqual(env.calls[0].args.slice(1), [0, 0, 2400, 1520]);
  assert.equal(env.canvases[0].exportWidth, 2400);
  assert.equal(env.canvases[0].exportHeight, 1520);
  assert.equal(env.canvases[0].exportType, 'image/png');
  assert.equal(env.canvases[0].width, 0);
  assert.equal(env.canvases[0].height, 0);
});

test('supported source numbers match the overlay contract while hidden and legacy pen marks emit no ink', async () => {
  const env = setup({width: 2000, height: 1000});
  const input = scene([
    {id: 'pen-before', type: 'pen', points: [{x: .1, y: .1}, {x: .9, y: .9}]},
    {id: 'hidden-point', type: 'point', x: .1, y: .2, hidden: true},
    {id: 'rectangle', type: 'rect', x: .3, y: .4, w: .2, h: .3, label: 'Selected area'},
    {id: 'pen-between', type: 'pen', x: .2, y: .3},
    {id: 'arrow', type: 'arrow', x: .6, y: .6, x2: .8, y2: .8, label: 'Long text must not cover the content'},
    {id: 'unknown', type: 'ellipse'}, null, 'invalid', []
  ]);
  const before = JSON.stringify(input);
  assert.deepEqual([...env.api.supportedAnnotations(input)].map(mark => mark.id), ['hidden-point', 'rectangle', 'arrow']);
  await env.api.render(input);
  assert.deepEqual(env.calls.find(call => call.name === 'rect').args, [600, 400, 400, 300]);
  assert.deepEqual(env.calls.filter(call => call.name === 'fillText').map(call => call.args[0]), ['2', '3']);
  assert.equal(env.calls.filter(call => call.name === 'arc').length, 2, 'no hidden point, pen path/dot, or pen badge may remain');
  assert.ok(env.calls.every(call => call.dash.length === 0));
  assert.equal(env.calls.filter(call => call.name === 'fillRect').length, 0, 'numeric badges need no label text boxes');
  assert.equal(JSON.stringify(input), before);
});

test('all-pen, unsupported and hidden-only scenes export the untouched source image', async () => {
  for (const annotations of [
    [{type: 'pen', points: [{x: .1, y: .1}, {x: .9, y: .9}]}],
    [{type: 'point', hidden: true, x: .1, y: .1}, {type: 'rect', hidden: true, x: .2, y: .2, w: .5, h: .5}],
    [{type: 'polygon'}, null, 'invalid'], []
  ]) {
    const env = setup();
    await env.api.render(scene(annotations));
    assert.deepEqual(env.calls.map(call => call.name), ['drawImage']);
  }
});

test('point, rectangle and arrow share their filtered number sequence without generic or custom text', async () => {
  const env = setup();
  await env.api.render(scene([
    {id: 'point', x: .1, y: .2, label: 'Point to explain'},
    {id: 'arrow', type: 'arrow', x: .3, y: .4, x2: .4, y2: .5, label: 'Pointed location'},
    {id: 'rect', type: 'rect', x: .6, y: .6, w: .1, h: .1, label: 'Selected area'}
  ]));
  assert.deepEqual(env.calls.filter(call => call.name === 'fillText').map(call => call.args[0]), ['1', '2', '3']);
});

test('native portrait Retina geometry projects each axis independently without double scale', async () => {
  const env = setup({width: 3008, height: 3324});
  await env.api.render({...scene([
    {type: 'rect', x: .625, y: .65, w: .025, h: .03},
    {type: 'arrow', x: .2, y: .3, x2: .4, y2: .5}
  ]), width: 1504, height: 1662});
  const rect = env.calls.find(call => call.name === 'rect');
  [1880, 2160.6, 75.2, 99.72].forEach((value, index) => close(rect.args[index], value));
  const start = env.calls.find(call => call.name === 'moveTo');
  const end = env.calls.find(call => call.name === 'lineTo');
  close(start.args[0], 601.6); close(start.args[1], 997.2);
  close(end.args[0], 1203.2); close(end.args[1], 1662);
  assert.equal(env.canvases[0].exportWidth, 3008); assert.equal(env.canvases[0].exportHeight, 3324);
});

test('arrowhead follows the actual native-pixel endpoint direction', async () => {
  const env = setup({width: 800, height: 400});
  await env.api.render(scene([{type: 'arrow', x: .25, y: .25, x2: .75, y2: .75}]));
  const moves = env.calls.filter(call => call.name === 'moveTo');
  const lines = env.calls.filter(call => call.name === 'lineTo');
  assert.deepEqual(moves[0].args, [200, 100]);
  assert.deepEqual(lines[0].args, [600, 300]);
  const angle = Math.atan2(200, 400);
  close(moves[1].args[0], 600 - Math.cos(angle - .48) * 13);
  close(moves[1].args[1], 300 - Math.sin(angle - .48) * 13);
  assert.deepEqual(lines[1].args, [600, 300]);
});

test('dense numbers separate at center and image corners without moving rectangle or arrow geometry', async () => {
  for (const anchor of [{x: .5, y: .4}, {x: 0, y: 0}, {x: 1, y: 1}]) {
    const env = setup({width: 800, height: 400});
    const marks = Array.from({length: 5}, (_, index) => ({id: `dense-${index}`, type: index % 2 ? 'arrow' : 'rect', ...anchor, w: .1, h: .1, x2: .7, y2: .7}));
    await env.api.render(scene(marks));
    for (const rect of env.calls.filter(call => call.name === 'rect')) {
      assert.deepEqual(rect.args, [anchor.x * 800, anchor.y * 400, 80, 40]);
    }
    const circles = env.calls.filter(call => call.name === 'arc');
    assert.equal(circles.length, 5);
    circles.forEach((circle, index) => {
      const [x, y, radius] = circle.args;
      assert.ok(x >= radius && x <= 800 - radius && y >= radius && y <= 400 - radius);
      assert.ok(circles.slice(0, index).every(other => Math.hypot(x - other.args[0], y - other.args[1]) >= radius + other.args[2]));
      if (Math.hypot(x - anchor.x * 800, y - anchor.y * 400) > 1e-9) {
        const connector = env.calls.findIndex(call => call.name === 'lineTo' && call.args[0] === x && call.args[1] === y);
        const from = env.calls.slice(0, connector).findLast(call => call.name === 'moveTo');
        assert.deepEqual(from.args, [anchor.x * 800, anchor.y * 400]);
      }
    });
    const lastInk = env.calls.findLastIndex(call => call.name === 'rect' || (call.name === 'lineTo' && call.args[0] === 560 && call.args[1] === 280));
    const firstNumber = env.calls.findIndex(call => call.name === 'fillText');
    assert.ok(firstNumber > lastInk);
  }
});

test('edge point keeps the exact target dot and connects its fully visible number badge', async () => {
  const env = setup();
  await env.api.render(scene([{type: 'point', x: 0, y: 1}]));
  const circles = env.calls.filter(call => call.name === 'arc');
  assert.deepEqual(circles[0].args.slice(0, 2), [0, 760]);
  assert.deepEqual(circles[1].args.slice(0, 3), [12, 748, 12]);
  assert.deepEqual(env.calls.find(call => call.name === 'moveTo').args, [0, 760]);
  assert.deepEqual(env.calls.find(call => call.name === 'lineTo').args, [12, 748]);
});

test('fallback assets stay same-origin and unsafe source URLs never load', async () => {
  const fallback = setup();
  await fallback.api.render({asset: 'orbit-settings', annotations: []});
  assert.equal(fallback.images[0].src, 'http://127.0.0.1:8742/assets/orbit-settings.svg');
  for (const imageUrl of ['https://example.com/image.png', '//example.com/image.png', 'data:image/png;base64,eA==', 'javascript:alert(1)', 'file:///tmp/image.png', 'http://user:secret@127.0.0.1:8742/screen.png']) {
    const env = setup();
    await assert.rejects(env.api.render({imageUrl}), {code: 'frame_source_invalid'});
    assert.equal(env.images.length, 0); assert.equal(env.canvases.length, 0);
  }
  await assert.rejects(fallback.api.render({asset: '../outside'}), {code: 'frame_source_invalid'});
});

test('decode, canvas, taint and null-PNG failures reject and release canvas storage', async () => {
  for (const [config, code] of [
    [{decodeError: new Error('decode failed')}, 'frame_image_decode_failed'],
    [{width: 0}, 'frame_image_decode_failed'],
    [{noContext: true}, 'frame_canvas_unavailable'],
    [{drawError: new Error('image failed')}, 'frame_export_failed'],
    [{exportError: new Error('tainted canvas')}, 'frame_export_failed'],
    [{nullBlob: true}, 'frame_export_failed']
  ]) {
    const env = setup(config);
    await assert.rejects(env.api.render(scene([])), {code});
    for (const canvas of env.canvases) { assert.equal(canvas.width, 0); assert.equal(canvas.height, 0); }
  }
});
