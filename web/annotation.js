(() => {
  'use strict';

  const NS = 'http://www.w3.org/2000/svg';
  const COLOR = '#e08353';
  const labels = { point: '설명할 위치', arrow: '가리킨 위치', rect: '선택한 영역' };
  const clamp = value => Math.max(0, Math.min(1, Number(value) || 0));
  let sequence = 0;

  const supportedAnnotations = scene => window.CueviaModel?.supportedAnnotations?.(scene) ??
    (Array.isArray(scene?.annotations) ? scene.annotations.filter(mark => mark && typeof mark === 'object' && !Array.isArray(mark) && Object.hasOwn(labels, mark.type === '' ? 'point' : mark.type ?? 'point')) : []);

  // Keep target geometry fixed; move only colliding number badges and connect
  // them back to their exact image-relative anchors. Shared with frame.js.
  function placeBadge(anchor, width, height, radius, placed) {
    const fit = point => ({
      x: Math.max(Math.min(radius, width / 2), Math.min(Math.max(width - radius, width / 2), point.x)),
      y: Math.max(Math.min(radius, height / 2), Math.min(Math.max(height - radius, height / 2), point.y))
    });
    const gap = Math.max(2, radius / 4), step = radius * 2 + gap;
    const available = point => placed.every(other => Math.hypot(point.x - other.x, point.y - other.y) >= radius + other.radius + gap);
    let result = fit(anchor);
    if (!available(result)) {
      search: for (let ring = 1; ring <= Math.ceil(Math.hypot(width, height) / step); ring++) {
        const samples = Math.max(8, Math.ceil(Math.PI * 2 * ring));
        for (let i = 0; i < samples; i++) {
          const angle = -Math.PI / 2 + i * Math.PI * 2 / samples;
          const candidate = fit({x: anchor.x + Math.cos(angle) * step * ring, y: anchor.y + Math.sin(angle) * step * ring});
          if (available(candidate)) { result = candidate; break search; }
        }
      }
    }
    placed.push({...result, radius});
    return result;
  }

  function mount({ element, scene, tool = 'point', onCommit, onSelect, beforeDraw, selectedId = null }) {
    const img = element?.querySelector('img');
    if (!img) return () => {};

    const doc = element.ownerDocument;
    const view = doc.defaultView;
    const originalPosition = element.style.position;
    if (view.getComputedStyle(element).position === 'static') element.style.position = 'relative';
    const make = (name, attrs = {}) => {
      const node = doc.createElementNS(NS, name);
      Object.entries(attrs).forEach(([key, value]) => node.setAttribute(key, String(value)));
      return node;
    };
    const svg = make('svg', { 'aria-label': '화면 위에 표시하기', preserveAspectRatio: 'none' });
    Object.assign(svg.style, {
      position: 'absolute', display: 'block', overflow: 'visible', zIndex: '2',
      touchAction: 'none', userSelect: 'none', cursor: 'crosshair'
    });
    const marks = make('g');
    const badges = make('g');
    const preview = make('g', { 'pointer-events': 'none', opacity: '.95' });
    marks.dataset.layer = 'marks'; badges.dataset.layer = 'badges'; preview.dataset.layer = 'preview';
    svg.append(marks, badges, preview);
    element.append(svg);

    let width = 1, height = 1, active = null, destroyed = false, badgePositions = [];
    const px = point => ({ x: clamp(point.x) * width, y: clamp(point.y) * height });
    const pointAt = event => {
      const rect = svg.getBoundingClientRect();
      return { x: clamp((event.clientX - rect.left) / (rect.width || 1)), y: clamp((event.clientY - rect.top) / (rect.height || 1)) };
    };

    function draw(annotation, index, parent, draft = false) {
      const type = annotation.type || 'point';
      const color = annotation.color || COLOR;
      const group = make('g');
      if (!draft) {
        group.dataset.annotationId = annotation.id;
        group.setAttribute('role', 'button');
        group.setAttribute('tabindex', '0');
        group.setAttribute('aria-label', `표시 ${index + 1}: ${annotation.label || labels[type] || labels.point}`);
        group.style.cursor = 'pointer';
      }
      const start = px(annotation);
      const stroke = { fill: 'none', stroke: color, 'stroke-width': 3, 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'vector-effect': 'non-scaling-stroke' };
      const appendStroke = (name, attrs) => {
        if (!draft) group.append(make(name, { ...attrs, ...stroke, stroke: 'transparent', 'stroke-width': 15, 'pointer-events': 'stroke' }));
        group.append(make(name, { ...attrs, ...stroke, 'pointer-events': 'none' }));
      };
      if (type === 'rect') {
        appendStroke('rect', { x: start.x, y: start.y, width: clamp(annotation.w) * width, height: clamp(annotation.h) * height, rx: 2 });
      } else if (type === 'arrow') {
        const end = px({ x: annotation.x2, y: annotation.y2 });
        appendStroke('line', { x1: start.x, y1: start.y, x2: end.x, y2: end.y });
        const angle = Math.atan2(end.y - start.y, end.x - start.x);
        const size = Math.min(13, Math.max(6, Math.hypot(end.x - start.x, end.y - start.y) * .4));
        const a = { x: end.x - Math.cos(angle - .48) * size, y: end.y - Math.sin(angle - .48) * size };
        const b = { x: end.x - Math.cos(angle + .48) * size, y: end.y - Math.sin(angle + .48) * size };
        appendStroke('path', { d: `M${a.x} ${a.y}L${end.x} ${end.y}L${b.x} ${b.y}` });
      } else {
        if (annotation.id === selectedId) group.append(make('circle', { cx: start.x, cy: start.y, r: 18, fill: color, opacity: '.18' }));
        group.append(make('circle', { cx: start.x, cy: start.y, r: 3, fill: color }));
      }
      if (!draft && annotation.id === selectedId && type !== 'point') {
        group.setAttribute('filter', 'drop-shadow(0 0 3px rgba(224, 131, 83, .65))');
      }
      parent.append(group);
      return start;
    }

    function drawBadge(annotation, index, anchor, parent, draft = false) {
      const type = annotation.type || 'point', color = annotation.color || COLOR;
      const radius = type === 'point' ? 12 : 9;
      const center = placeBadge(anchor, width, height, radius, draft ? [...badgePositions] : badgePositions);
      const group = make('g');
      if (!draft) { group.dataset.annotationId = annotation.id; group.style.cursor = 'pointer'; }
      if (center.x !== anchor.x || center.y !== anchor.y) {
        group.append(make('line', {x1: anchor.x, y1: anchor.y, x2: center.x, y2: center.y, stroke: color, 'stroke-width': 1.5, 'pointer-events': 'none'}));
      }
      group.append(make('circle', {cx: center.x, cy: center.y, r: radius, fill: color, stroke: '#fff', 'stroke-width': 2}));
      const text = make('text', {x: center.x, y: center.y, fill: '#fff', 'font-size': type === 'point' ? 11 : 9, 'font-weight': 650, 'font-family': 'system-ui, sans-serif', 'text-anchor': 'middle', 'dominant-baseline': 'central', 'pointer-events': 'none'});
      text.textContent = String(index + 1);
      group.append(text);
      parent.append(group);
    }

    function rectShape(start, end) {
      const w = Math.max(.01, Math.abs(end.x - start.x));
      const h = Math.max(.01, Math.abs(end.y - start.y));
      return { x: Math.min(Math.min(start.x, end.x), 1 - w), y: Math.min(Math.min(start.y, end.y), 1 - h), w, h };
    }

    function currentShape() {
      if (!active) return null;
      const { start, end, type } = active;
      if (type === 'rect') return { type, ...rectShape(start, end) };
      if (type === 'arrow') return { type, ...start, x2: end.x, y2: end.y };
      return { type: 'point', ...end };
    }

    function paintPreview() {
      preview.replaceChildren();
      const shape = currentShape();
      if (shape) {
        const index = supportedAnnotations(scene).length;
        drawBadge(shape, index, draw(shape, index, preview, true), preview, true);
      }
    }

    function layout() {
      if (destroyed) return;
      const outer = element.getBoundingClientRect();
      const rect = img.getBoundingClientRect();
      let left = rect.left, top = rect.top;
      width = rect.width;
      height = rect.height;
      const style = view.getComputedStyle(img);
      // A fixed image box may letterbox a native capture. Align to its pixels.
      if (img.naturalWidth && img.naturalHeight && ['contain', 'scale-down'].includes(style.objectFit)) {
        let scale = Math.min(width / img.naturalWidth, height / img.naturalHeight);
        if (style.objectFit === 'scale-down') scale = Math.min(1, scale);
        const fittedWidth = img.naturalWidth * scale, fittedHeight = img.naturalHeight * scale;
        const position = style.objectPosition.split(' ');
        const offset = (value, remaining) => value?.endsWith('%') ? remaining * parseFloat(value) / 100 : value === 'left' || value === 'top' ? 0 : value === 'right' || value === 'bottom' ? remaining : remaining / 2;
        left += offset(position[0], width - fittedWidth);
        top += offset(position[1], height - fittedHeight);
        width = fittedWidth;
        height = fittedHeight;
      }
      Object.assign(svg.style, {
        left: `${left - outer.left + element.scrollLeft - element.clientLeft}px`,
        top: `${top - outer.top + element.scrollTop - element.clientTop}px`,
        width: `${width}px`, height: `${height}px`
      });
      svg.setAttribute('viewBox', `0 0 ${width || 1} ${height || 1}`);
      marks.replaceChildren();
      badges.replaceChildren();
      badgePositions = [];
      supportedAnnotations(scene).forEach((annotation, index) => {
        if (!annotation.hidden) drawBadge(annotation, index, draw(annotation, index, marks), badges);
      });
      paintPreview();
    }

    function down(event) {
      if (active || event.button !== 0 || event.isPrimary === false || !Object.hasOwn(labels, tool)) return;
      const target = event.target.closest('[data-annotation-id]');
      event.preventDefault();
      event.stopPropagation();
      const type = tool;
      // Drawing tools own the gesture even over an existing mark's hit area.
      // Selecting marks remains available through the point tool and keyboard.
      if (target && type === 'point') {
        onSelect?.(target.dataset.annotationId);
        return;
      }
      if (!width || !height || beforeDraw?.() === false) return;
      const start = pointAt(event);
      active = { pointerId: event.pointerId, pointerType: event.pointerType, type, start, end: start };
      svg.setPointerCapture(event.pointerId);
      paintPreview();
    }

    function update(event) {
      if (!active || active.pointerId !== event.pointerId) return;
      active.end = pointAt(event);
      paintPreview();
    }

    function release(pointerId) {
      if (svg.hasPointerCapture(pointerId)) svg.releasePointerCapture(pointerId);
    }

    function up(event) {
      if (!active || active.pointerId !== event.pointerId) return;
      event.preventDefault();
      event.stopPropagation();
      update(event);
      const shape = currentShape();
      if (shape.type === 'arrow' && Math.hypot((shape.x2 - shape.x) * width, (shape.y2 - shape.y) * height) < 2) {
        shape.x2 = clamp(shape.x + (shape.x > .97 ? -.03 : .03));
      }
      const annotation = {
        id: `annotation-${Date.now().toString(36)}-${(++sequence).toString(36)}`,
        ...shape, label: labels[shape.type], color: COLOR
      };
      active = null;
      release(event.pointerId);
      preview.replaceChildren();
      // The host owns history, persistence, and the next render.
      onCommit?.(annotation);
    }

    function cancel(event) {
      if (!active || active.pointerId !== event.pointerId) return;
      const pointerId = active.pointerId;
      active = null;
      release(pointerId);
      preview.replaceChildren();
    }

    function lostCapture(event) {
      if (!active || active.pointerId !== event.pointerId) return;
      // Chrome can report released mouse capture before its delayed pointerup.
      // Finalize only that completed mouse gesture through the ordinary path;
      // held buttons, other pointer types, and genuine cancellation still abort.
      if (!destroyed && event.type === 'lostpointercapture' && active.pointerType === 'mouse' &&
          event.pointerType === 'mouse' && event.buttons === 0 && beforeDraw?.() !== false) {
        up(event);
        return;
      }
      cancel(event);
    }

    function selectByKey(event) {
      if (event.isComposing || !['Enter', ' '].includes(event.key)) return;
      const target = event.target.closest('[data-annotation-id]');
      if (!target) return;
      event.preventDefault();
      event.stopPropagation();
      onSelect?.(target.dataset.annotationId);
    }

    const stopClick = event => event.stopPropagation();
    const listeners = { pointerdown: down, pointermove: update, pointerup: up, pointercancel: cancel, lostpointercapture: lostCapture, keydown: selectByKey, click: stopClick };
    Object.entries(listeners).forEach(([name, handler]) => svg.addEventListener(name, handler));
    img.addEventListener('load', layout);
    const observer = new view.ResizeObserver(layout);
    observer.observe(img);
    observer.observe(element);
    layout();

    return () => {
      destroyed = true;
      if (active) {
        const pointerId = active.pointerId;
        active = null;
        release(pointerId);
      }
      observer.disconnect();
      img.removeEventListener('load', layout);
      Object.entries(listeners).forEach(([name, handler]) => svg.removeEventListener(name, handler));
      svg.remove();
      element.style.position = originalPosition;
    };
  }

  window.CueviaAnnotation = { mount, supportedAnnotations };
})();
