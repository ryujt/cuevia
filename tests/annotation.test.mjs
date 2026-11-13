import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../web/annotation.js', import.meta.url), 'utf8');
const modelSource = await readFile(new URL('../web/model.js', import.meta.url), 'utf8');
const frameSource = await readFile(new URL('../web/frame.js', import.meta.url), 'utf8');

function mountFixture({tool = 'point', annotations = [], beforeDraw, releaseEmitsLost = true, imageStyle = {}, imageBox, naturalWidth = 1200, naturalHeight = 600} = {}) {
  const commits = [], selections = [], observers = [], created = [];
  class Node {
    constructor(name, box = {left: 0, top: 0, width: 0, height: 0}) {
      this.name = name; this.box = box; this.children = []; this.parentNode = null;
      this.style = {}; this.dataset = {}; this.attributes = new Map(); this.listeners = new Map(); this.captured = new Set();
      this.scrollLeft = 0; this.scrollTop = 0; this.clientLeft = 0; this.clientTop = 0;
    }
    setAttribute(key, value) { this.attributes.set(key, String(value)); }
    append(...nodes) { for (const node of nodes) { node.parentNode = this; this.children.push(node); } }
    replaceChildren(...nodes) { for (const child of this.children) child.parentNode = null; this.children = []; this.append(...nodes); }
    remove() { if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(node => node !== this); this.parentNode = null; }
    closest(selector) {
      assert.equal(selector, '[data-annotation-id]');
      for (let node = this; node; node = node.parentNode) if (node.dataset.annotationId) return node;
      return null;
    }
    querySelector(selector) { assert.equal(selector, 'img'); return this.children.find(node => node.name === 'img') || null; }
    addEventListener(name, callback) { this.listeners.set(name, callback); }
    removeEventListener(name, callback) { if (this.listeners.get(name) === callback) this.listeners.delete(name); }
    dispatch(name, event) { this.listeners.get(name)?.(event); }
    setPointerCapture(id) { this.captured.add(id); }
    hasPointerCapture(id) { return this.captured.has(id); }
    releasePointerCapture(id) {
      this.captured.delete(id);
      if (releaseEmitsLost) this.dispatch('lostpointercapture', {pointerId: id});
    }
    getBoundingClientRect() {
      if (this.name !== 'svg') return this.box;
      return {left: element.box.left + parseFloat(this.style.left), top: element.box.top + parseFloat(this.style.top), width: parseFloat(this.style.width), height: parseFloat(this.style.height)};
    }
  }
  const element = new Node('capture-body', {left: 100, top: 200, width: 400, height: 300});
  const img = new Node('img', imageBox || {left: 120, top: 230, width: 300, height: 150});
  img.naturalWidth = naturalWidth; img.naturalHeight = naturalHeight;
  element.append(img);
  const doc = {
    createElementNS(namespace, name) { assert.equal(namespace, 'http://www.w3.org/2000/svg'); const node = new Node(name); created.push(node); return node; },
    defaultView: {
      getComputedStyle: node => node === img ? {objectFit: 'fill', objectPosition: '50% 50%', ...imageStyle} : {position: 'static'},
      ResizeObserver: class {
        constructor(callback) { this.callback = callback; observers.push(this); }
        observe() {}
        disconnect() { this.disconnected = true; }
      }
    }
  };
  element.ownerDocument = doc;
  const sandbox = {window: {}};
  vm.runInNewContext(source, sandbox, {filename: 'web/annotation.js'});
  const scene = {id: 'scene-real', annotations};
  let cleanup;
  const remount = nextTool => {
    cleanup?.();
    cleanup = sandbox.window.CueviaAnnotation.mount({element, scene, tool: nextTool || tool, beforeDraw,
      onCommit: annotation => commits.push(annotation), onSelect: id => selections.push(id)});
    return element.children.find(node => node.name === 'svg');
  };
  remount();
  const env = {element, img, scene, commits, selections, observers, created, remount, cleanup: () => cleanup(), api: sandbox.window.CueviaAnnotation, get svg() { return element.children.find(node => node.name === 'svg'); }, get marks() { return this.svg.children.find(node => node.dataset.layer === 'marks'); }, get badges() { return this.svg.children.find(node => node.dataset.layer === 'badges'); }, get preview() { return this.svg.children.find(node => node.dataset.layer === 'preview'); }};
  env.event = (type, x, y, extra = {}) => {
    const event = {type, pointerId: 7, pointerType: 'mouse', isPrimary: true, button: 0, buttons: type === 'pointerup' ? 0 : 1,
      clientX: x, clientY: y, timeStamp: 0, target: env.svg, defaultPrevented: false,
      preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.propagationStopped = true; }, ...extra};
    env.svg.dispatch(type, event);
    return event;
  };
  return env;
}
const close = (actual, expected) => assert.ok(Math.abs(actual - expected) < 1e-9, `${actual} differs from ${expected}`);
const pathNodes = node => [node, ...node.children.flatMap(pathNodes)].filter(node => node.name === 'path');

test('live overlay, frame export and prompt model share the exact supported mark filter', () => {
  const sandbox = {window: {}};
  vm.runInNewContext(modelSource, sandbox, {filename: 'model.js'});
  vm.runInNewContext(source, sandbox, {filename: 'annotation.js'});
  vm.runInNewContext(frameSource, sandbox, {filename: 'frame.js'});
  const scene = {annotations: [
    {id: 'pen', type: 'pen'}, {id: 'old-point'}, {id: 'hidden-arrow', type: 'arrow', hidden: true},
    {id: 'rectangle', type: 'rect'}, {id: 'empty-point', type: ''}, {id: 'null-point', type: null},
    {id: 'invalid-zero', type: 0}, {id: 'invalid-false', type: false}, null, 'invalid', []
  ]};
  const expected = ['old-point', 'hidden-arrow', 'rectangle', 'empty-point', 'null-point'];
  const model = sandbox.window.CueviaModel;
  assert.deepEqual([...model.supportedAnnotations(scene)].map(mark => mark.id), expected);
  let calls = 0;
  sandbox.window.CueviaModel = {...model, supportedAnnotations(value) { calls++; return model.supportedAnnotations(value); }};
  for (const api of [sandbox.window.CueviaAnnotation, sandbox.window.CueviaFrame]) {
    assert.deepEqual([...api.supportedAnnotations(scene)].map(mark => mark.id), expected);
  }
  assert.equal(calls, 2, 'both renderers must delegate to the production prompt filter');
});

test('point, arrow and rectangle gestures commit their own normalized type exactly once', () => {
  for (const tool of ['point', 'arrow', 'rect']) {
    const env = mountFixture({tool});
    env.event('pointerdown', 150, 260);
    env.event('pointermove', 180, 290);
    env.event('pointerup', 210, 305);
    env.event('pointerup', 210, 305);
    assert.equal(env.commits.length, 1);
    const mark = env.commits[0];
    assert.equal(mark.type, tool);
    assert.equal(mark.points, undefined);
    assert.match(mark.id, /^annotation-/);
    close(mark.x, tool === 'point' ? .3 : .1);
    close(mark.y, tool === 'point' ? .5 : .2);
    if (tool === 'arrow') { close(mark.x2, .3); close(mark.y2, .5); }
    if (tool === 'rect') { close(mark.w, .2); close(mark.h, .3); }
    assert.equal(env.svg.hasPointerCapture(7), false);
    assert.equal(env.preview.children.length, 0);
    env.scene.annotations.push(mark);
    env.remount();
    assert.equal(env.marks.children.length, 1);
    assert.equal(env.badges.children[0].children.find(node => node.name === 'text').textContent, '1');
  }
});

test('fast taps and tiny releases remain usable supported marks without freehand samples', () => {
  for (const tool of ['point', 'arrow', 'rect']) {
    for (const movement of [0, .2]) {
      const env = mountFixture({tool});
      env.event('pointerdown', 150, 260);
      env.event('pointerup', 150 + movement, 260 + movement / 2);
      const mark = env.commits[0];
      assert.equal(mark.type, tool);
      assert.equal(mark.points, undefined);
      if (tool === 'point') { close(mark.x, (30 + movement) / 300); close(mark.y, (30 + movement / 2) / 150); }
      if (tool === 'arrow') assert.ok(Math.hypot(mark.x2 - mark.x, mark.y2 - mark.y) > 0);
      if (tool === 'rect') { close(mark.w, .01); close(mark.h, .01); }
    }
  }
});

test('removed pen and unknown tools neither draw nor fall back to point creation', () => {
  for (const tool of ['pen', 'unknown', null]) {
    let starts = 0;
    const env = mountFixture({tool, beforeDraw: () => { starts++; }});
    env.event('pointerdown', 150, 260);
    env.event('pointermove', 180, 290);
    env.event('pointerup', 210, 305);
    assert.equal(starts, 0);
    assert.equal(env.commits.length, 0);
    assert.equal(env.selections.length, 0);
    assert.equal(env.preview.children.length, 0);
    assert.equal(env.svg.hasPointerCapture(7), false);
  }
});

test('supported source-order numbers exclude legacy pen while hidden marks keep their slots', () => {
  const annotations = [
    {id: 'legacy-pen', type: 'pen', points: [{x: .1, y: .1}, {x: .9, y: .9}]},
    {id: 'hidden-point', type: 'point', x: .1, y: .2, hidden: true},
    {id: 'arrow', type: 'arrow', x: .3, y: .4, x2: .5, y2: .6, label: 'Pointed location'},
    {id: 'legacy-pen-two', type: 'pen', x: .2, y: .3},
    {id: 'rectangle', type: 'rect', x: .6, y: .6, w: .2, h: .2},
    {id: 'legacy-point', x: .8, y: .3},
    {id: 'unsupported', type: 'ellipse'}, null, 'invalid', []
  ];
  const env = mountFixture({annotations});
  assert.deepEqual([...env.api.supportedAnnotations(env.scene)].map(mark => mark.id), ['hidden-point', 'arrow', 'rectangle', 'legacy-point']);
  assert.deepEqual(env.marks.children.map(node => node.dataset.annotationId), ['arrow', 'rectangle', 'legacy-point']);
  const numbers = () => env.badges.children.map(group => group.children.find(node => node.name === 'text').textContent);
  assert.deepEqual(numbers(), ['2', '3', '4']);
  assert.deepEqual(env.badges.children.flatMap(group => group.children.filter(node => node.name === 'text').map(node => node.textContent)), ['2', '3', '4']);
  annotations[2].hidden = true; // The host owns undo and clear history.
  env.remount();
  assert.deepEqual(numbers(), ['3', '4']);
  annotations.forEach(mark => { if (mark && typeof mark === 'object') mark.hidden = true; });
  env.remount();
  assert.equal(env.marks.children.length, 0);
  assert.equal(env.badges.children.length, 0);
  annotations[4].hidden = false;
  env.remount();
  assert.deepEqual(numbers(), ['3']);
});

test('number badges separate dense supported marks and stay above all shape ink', () => {
  const annotations = Array.from({length: 5}, (_, index) => ({id: `dense-${index}`, type: index % 2 ? 'arrow' : 'rect', x: .5, y: .5, x2: .7, y2: .6, w: .2, h: .1}));
  const env = mountFixture({annotations});
  assert.ok(env.svg.children.indexOf(env.badges) > env.svg.children.indexOf(env.marks));
  const circles = env.badges.children.map(group => group.children.find(node => node.name === 'circle'));
  circles.forEach((circle, index) => {
    const x = Number(circle.attributes.get('cx')), y = Number(circle.attributes.get('cy')), radius = Number(circle.attributes.get('r'));
    assert.ok(circles.slice(0, index).every(other => Math.hypot(x - Number(other.attributes.get('cx')), y - Number(other.attributes.get('cy'))) >= radius + Number(other.attributes.get('r'))));
    if (x !== 150 || y !== 75) {
      assert.ok(env.badges.children[index].children.some(node => node.name === 'line' && node.attributes.get('x1') === '150' && node.attributes.get('y1') === '75'));
    }
  });
});

test('letterboxed image pixels normalize all three supported tools consistently', () => {
  for (const tool of ['point', 'arrow', 'rect']) {
    const env = mountFixture({tool, imageBox: {left: 120, top: 230, width: 300, height: 300}, imageStyle: {objectFit: 'contain', objectPosition: '50% 50%'}});
    assert.deepEqual(env.svg.getBoundingClientRect(), {left: 120, top: 305, width: 300, height: 150});
    env.event('pointerdown', 150, 335);
    env.event('pointerup', 210, 380);
    const mark = env.commits[0];
    close(mark.x, tool === 'point' ? .3 : .1); close(mark.y, tool === 'point' ? .5 : .2);
    if (tool === 'arrow') { close(mark.x2, .3); close(mark.y2, .5); }
    if (tool === 'rect') { close(mark.w, .2); close(mark.h, .3); }
  }
});

test('portrait Retina rectangles reconstruct saved normalized geometry after resizing', () => {
  const env = mountFixture({tool: 'rect', naturalWidth: 3008, naturalHeight: 3324, imageBox: {left: 120, top: 230, width: 376, height: 415.5}});
  env.event('pointerdown', 120 + 376 * .625, 230 + 415.5 * .65);
  env.event('pointerup', 120 + 376 * .65, 230 + 415.5 * .68);
  const mark = env.commits[0];
  close(mark.x, .625); close(mark.y, .65); close(mark.w, .025); close(mark.h, .03);
  env.scene.annotations.push(mark);
  env.img.box = {left: 120, top: 230, width: 752, height: 831};
  env.remount();
  const rect = env.marks.children[0].children.find(node => node.name === 'rect' && node.attributes.get('stroke') === '#e08353');
  close(Number(rect.attributes.get('x')), 470); close(Number(rect.attributes.get('y')), 540.15);
  close(Number(rect.attributes.get('width')), 18.8); close(Number(rect.attributes.get('height')), 24.93);
});

test('point tool selects supported ink or its badge; arrow/rectangle gestures draw over prior marks', () => {
  const annotations = [{id: 'existing', type: 'arrow', x: .1, y: .2, x2: .2, y2: .3}];
  const selection = mountFixture({annotations});
  for (const target of [selection.marks.children[0].children[0], selection.badges.children[0].children[0]]) {
    selection.event('pointerdown', 150, 260, {target});
    selection.event('pointerup', 150, 260);
  }
  assert.deepEqual(selection.selections, ['existing', 'existing']);
  assert.equal(selection.commits.length, 0);
  const target = selection.marks.children[0];
  selection.event('keydown', 0, 0, {target, key: 'Enter'});
  assert.deepEqual(selection.selections, ['existing', 'existing', 'existing']);
  for (const tool of ['arrow', 'rect']) {
    const env = mountFixture({tool, annotations});
    env.event('pointerdown', 150, 260, {target: env.badges.children[0].children[0]});
    env.event('pointerup', 210, 305);
    assert.equal(env.commits[0].type, tool);
    assert.equal(env.selections.length, 0);
  }
});

test('cancel, host rejection, unrelated pointers and cleanup cannot save interrupted gestures', () => {
  for (const tool of ['point', 'arrow', 'rect']) {
    for (const type of ['pointercancel', 'lostpointercapture']) {
      const env = mountFixture({tool});
      env.event('pointerdown', 150, 260);
      env.event(type, 180, 290, {buttons: 1});
      env.event('pointerup', 210, 305);
      assert.equal(env.commits.length, 0);
      assert.equal(env.preview.children.length, 0);
    }
  }
  const rejected = mountFixture({beforeDraw: () => false});
  rejected.event('pointerdown', 150, 260); rejected.event('pointerup', 210, 305);
  assert.equal(rejected.commits.length, 0);
  const env = mountFixture();
  env.event('pointerdown', 150, 260, {button: 2}); env.event('pointerup', 210, 305);
  assert.equal(env.commits.length, 0);
  env.event('pointerdown', 150, 260); env.event('pointerup', 210, 305, {pointerId: 9});
  assert.equal(env.commits.length, 0);
  const svg = env.svg;
  env.cleanup();
  assert.equal(svg.listeners.size, 0);
  assert.equal(svg.hasPointerCapture(7), false);
  assert.equal(env.commits.length, 0);
});

test('released mouse capture commits actual arrow endpoint once before delayed pointerup', () => {
  const env = mountFixture({tool: 'arrow'});
  env.event('pointerdown', 150, 260);
  env.event('pointermove', 180, 290);
  env.event('lostpointercapture', 210, 305, {buttons: 0});
  env.event('pointerup', 210, 305);
  assert.equal(env.commits.length, 1);
  close(env.commits[0].x2, .3); close(env.commits[0].y2, .5);
});
