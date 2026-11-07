(() => {
  'use strict';

  const COLOR = '#e08353';
  const types = ['point', 'arrow', 'rect'];
  const supportedAnnotations = scene => window.CueviaModel?.supportedAnnotations?.(scene) ??
    (Array.isArray(scene?.annotations) ? scene.annotations.filter(mark => mark && typeof mark === 'object' && !Array.isArray(mark) && types.includes(mark.type === '' ? 'point' : mark.type ?? 'point')) : []);
  const clamp = value => Math.max(0, Math.min(1, Number(value) || 0));
  const error = (code, cause) => Object.assign(new Error(code, cause ? {cause} : undefined), {code});
  const pixel = (point, width, height) => ({x: clamp(point?.x) * width, y: clamp(point?.y) * height});

  // Same placement rule as the live annotation overlay. Connect relocated
  // badges to the original anchor without changing the target or shape.
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

  function badge(ctx, mark, index, anchor, width, height, stroke, placed) {
    const point = (mark.type || 'point') === 'point';
    const radius = point ? Math.max(12, stroke * 4) : Math.max(9, stroke * 3);
    const fontSize = point ? Math.max(11, stroke * 3.67) : Math.max(9, stroke * 3);
    const {x, y} = placeBadge(anchor, width, height, radius, placed);
    const color = mark.color || COLOR;
    ctx.save();
    ctx.strokeStyle = color; ctx.lineWidth = stroke; ctx.setLineDash([]);
    if (x !== anchor.x || y !== anchor.y) {
      ctx.beginPath(); ctx.moveTo(anchor.x, anchor.y); ctx.lineTo(x, y); ctx.stroke();
    }
    ctx.beginPath(); ctx.arc(x, y, radius, 0, Math.PI * 2);
    ctx.fillStyle = color; ctx.fill(); ctx.strokeStyle = '#ffffff';
    ctx.lineWidth = Math.max(2, stroke * .67); ctx.stroke();
    ctx.font = `650 ${fontSize}px system-ui, sans-serif`;
    ctx.textAlign = 'center'; ctx.textBaseline = 'middle'; ctx.fillStyle = '#ffffff';
    ctx.fillText(String(index + 1), x, y);
    ctx.restore();
  }

  function drawAnnotation(ctx, mark, width, height, stroke) {
    const start = pixel(mark, width, height), type = mark.type || 'point';
    ctx.save();
    ctx.strokeStyle = mark.color || COLOR; ctx.fillStyle = mark.color || COLOR;
    ctx.lineWidth = stroke; ctx.lineCap = 'round'; ctx.lineJoin = 'round'; ctx.setLineDash([]);
    if (type === 'rect') {
      ctx.beginPath(); ctx.rect(start.x, start.y, clamp(mark.w) * width, clamp(mark.h) * height); ctx.stroke();
    } else if (type === 'arrow') {
      const end = pixel({x: mark.x2, y: mark.y2}, width, height);
      ctx.beginPath(); ctx.moveTo(start.x, start.y); ctx.lineTo(end.x, end.y); ctx.stroke();
      const angle = Math.atan2(end.y - start.y, end.x - start.x);
      const size = Math.min(stroke * 13 / 3, Math.max(stroke * 2, Math.hypot(end.x - start.x, end.y - start.y) * .4));
      ctx.beginPath();
      ctx.moveTo(end.x - Math.cos(angle - .48) * size, end.y - Math.sin(angle - .48) * size);
      ctx.lineTo(end.x, end.y);
      ctx.lineTo(end.x - Math.cos(angle + .48) * size, end.y - Math.sin(angle + .48) * size);
      ctx.stroke();
    } else {
      ctx.beginPath(); ctx.arc(start.x, start.y, Math.max(2, stroke), 0, Math.PI * 2); ctx.fill();
    }
    ctx.restore();
    return start;
  }

  async function render(scene) {
    let source;
    try {
      if (!scene || (!scene.imageUrl && !/^[a-z0-9_-]+$/i.test(scene.asset || ''))) throw error('frame_source_invalid');
      source = new URL(scene.imageUrl || `/assets/${scene.asset}.svg`, window.location.href);
      if (!['http:', 'https:'].includes(source.protocol) || source.origin !== window.location.origin || source.username || source.password) {
        throw error('frame_source_invalid');
      }
    } catch (cause) { throw error('frame_source_invalid', cause); }
    const image = new Image();
    image.decoding = 'async';
    image.src = source.href;
    try { await image.decode(); }
    catch (cause) { throw error('frame_image_decode_failed', cause); }
    const width = image.naturalWidth, height = image.naturalHeight;
    if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1) {
      throw error('frame_image_decode_failed');
    }
    const canvas = document.createElement('canvas');
    canvas.width = width; canvas.height = height;
    let ctx;
    try { ctx = canvas.getContext('2d'); }
    catch (cause) { canvas.width = 0; canvas.height = 0; throw error('frame_canvas_unavailable', cause); }
    if (!ctx) { canvas.width = 0; canvas.height = 0; throw error('frame_canvas_unavailable'); }
    try {
      ctx.drawImage(image, 0, 0, width, height);
      const stroke = Math.max(3, width / 400);
      const badges = [], placed = [];
      supportedAnnotations(scene).forEach((mark, index) => {
        if (!mark.hidden) badges.push({mark, index, anchor: drawAnnotation(ctx, mark, width, height, stroke)});
      });
      // IDs stay above every shape; labels contain only the shared number.
      badges.forEach(({mark, index, anchor}) => badge(ctx, mark, index, anchor, width, height, stroke, placed));
      return await new Promise((resolve, reject) => {
        canvas.toBlob(blob => blob?.size && blob.type === 'image/png' ? resolve(blob) : reject(error('frame_export_failed')), 'image/png');
      });
    } catch (cause) { throw cause.code === 'frame_export_failed' ? cause : error('frame_export_failed', cause); }
    finally { canvas.width = 0; canvas.height = 0; }
  }

  window.CueviaFrame = {render, supportedAnnotations};
})();
