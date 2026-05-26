import { Resvg } from '@resvg/resvg-js';
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname } from 'node:path';
const OUT = dirname(fileURLToPath(import.meta.url));

// ---- palette --------------------------------------------------------------
const BLUE = '#326CE5';   // Kubernetes blue — the cluster + the leash
const INK  = '#1A2332';   // the AI agent
const LIGHT = '#F5F8FF';

// ---- official Kubernetes icon (vendored, unmodified) ----------------------
// Source: github.com/cncf/artwork projects/kubernetes/icon/color. Embedded
// verbatim as a nested <svg>; we never redraw the helm.
const ICON_RAW = readFileSync(`${OUT}/kubernetes-icon-color.svg`, 'utf8');
const VB = { minX: -0.17, minY: 0.08, span: 230.10 };
function k8sIcon(x, y, w) {
  const h = w * (223.35 / 230.10);
  return ICON_RAW.replace(
    '<svg xmlns="http://www.w3.org/2000/svg" role="img" viewBox="-0.17 0.08 230.10 223.35">',
    `<svg xmlns="http://www.w3.org/2000/svg" x="${x}" y="${y}" width="${w}" height="${h.toFixed(2)}" viewBox="-0.17 0.08 230.10 223.35">`);
}
const topV = (x, y, w) => { const s = w / VB.span; return [x + (115 - VB.minX) * s, y + (1 - VB.minY) * s]; };

// ---- the mark: an ink AI agent on a blue leash, tethered to the helm -------
// Composed in a 512x512 space. Returns { svg, bbox }.
function drawMark() {
  // helm (cluster) at the base
  const iw = 192, ix = 256 - 115.17 * (iw / VB.span), iy = 300;
  const [, ty] = topV(ix, iy, iw);

  // bot agent (ink): rounded head, antenna, side "ears", two eyes
  const hcx = 256, hcy = 138, hw = 138, hh = 122, hr = 34;
  const hx = hcx - hw / 2, hy = hcy - hh / 2;
  const eyeR = 15, eyeDx = hw * 0.21, eyeY = hcy + 4, antTop = hy - 30;
  const bot = `
    <line x1="${hcx}" y1="${antTop}" x2="${hcx}" y2="${hy + 6}" stroke="${INK}" stroke-width="11" stroke-linecap="round"/>
    <circle cx="${hcx}" cy="${antTop}" r="11" fill="${INK}"/>
    <rect x="${hx}" y="${hy}" width="${hw}" height="${hh}" rx="${hr}" fill="${INK}"/>
    <rect x="${hx - 14}" y="${hcy - 20}" width="14" height="40" rx="7" fill="${INK}"/>
    <rect x="${hx + hw}" y="${hcy - 20}" width="14" height="40" rx="7" fill="${INK}"/>
    <circle cx="${(hcx - eyeDx).toFixed(1)}" cy="${eyeY}" r="${eyeR}" fill="#FFFFFF"/>
    <circle cx="${(hcx + eyeDx).toFixed(1)}" cy="${eyeY}" r="${eyeR}" fill="#FFFFFF"/>`;

  // collar + slack leash + clip (blue = the kubeleash restraint)
  const neckY = hy + hh + 8, collarH = 16;
  const collar = `<rect x="${hcx - 42}" y="${neckY}" width="84" height="${collarH}" rx="8" fill="${BLUE}"/>`;
  const leadTop = neckY + collarH + 2, leadEnd = ty - 4, dl = leadEnd - leadTop;
  const lead = `<path d="M ${hcx} ${leadTop.toFixed(1)} C ${hcx - 24} ${(leadTop + dl * 0.34).toFixed(1)}, ${hcx + 24} ${(leadTop + dl * 0.66).toFixed(1)}, ${hcx} ${leadEnd.toFixed(1)}" fill="none" stroke="${BLUE}" stroke-width="13" stroke-linecap="round"/>
    <circle cx="${hcx}" cy="${leadTop.toFixed(1)}" r="7.5" fill="${BLUE}"/>`;

  const svg = `${bot}${collar}${lead}${k8sIcon(ix, iy, iw)}`;
  const bbox = { x0: ix + 4, y0: antTop - 12, x1: ix + iw - 4, y1: iy + iw * (223.35 / 230.10) - 6 };
  return { svg, bbox };
}

const MARK = drawMark();
function place(scale, cxT, cyT) {
  const b = MARK.bbox, cx = (b.x0 + b.x1) / 2, cy = (b.y0 + b.y1) / 2;
  return `<g transform="translate(${(cxT - scale * cx).toFixed(1)} ${(cyT - scale * cy).toFixed(1)}) scale(${scale})">${MARK.svg}</g>`;
}

// ---- 1. logo-mark.svg (transparent; for light backgrounds) ----------------
const logoMark = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512">
${place(0.94, 256, 256)}
</svg>`;

// ---- 2. avatar.svg (white rounded square + mark) --------------------------
const avatar = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512">
  <rect width="512" height="512" rx="96" fill="#FFFFFF"/>
  <rect x="6" y="6" width="500" height="500" rx="92" fill="none" stroke="#E4EAF6" stroke-width="3"/>
  ${place(0.86, 256, 256)}
</svg>`;

// ---- 3. social-preview.svg (1280x640) -------------------------------------
const FONT = 'Helvetica Neue, Helvetica, Arial, sans-serif';
const social = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 640" width="1280" height="640">
  <rect width="1280" height="640" fill="${LIGHT}"/>
  <rect x="0" y="0" width="1280" height="10" fill="${BLUE}"/>
  ${place(0.86, 250, 330)}
  <g transform="translate(452 0)">
    <text x="0" y="292" font-family="${FONT}" font-size="100" font-weight="800" fill="${INK}" letter-spacing="-3">kubeleash</text>
    <text x="3" y="350" font-family="${FONT}" font-size="31" font-weight="600" fill="${BLUE}">Guardrails for AI agents on your cluster.</text>
    <text x="3" y="402" font-family="${FONT}" font-size="26" font-weight="400" fill="#55607A">A policy-gated Kubernetes MCP server.</text>
  </g>
</svg>`;

// ---- write + render -------------------------------------------------------
writeFileSync(`${OUT}/logo-mark.svg`, logoMark);
writeFileSync(`${OUT}/avatar.svg`, avatar);
writeFileSync(`${OUT}/social-preview.svg`, social);
function render(svg, width, outfile) {
  const r = new Resvg(svg, { fitTo: { mode: 'width', value: width }, font: { loadSystemFonts: true }, background: 'rgba(0,0,0,0)' });
  writeFileSync(outfile, r.render().asPng());
}
render(avatar, 512, `${OUT}/avatar.png`);
render(logoMark, 512, `${OUT}/logo-mark.png`);
render(social, 1280, `${OUT}/social-preview.png`);
console.log('wrote logo-mark, avatar, social-preview (svg+png)');
