import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

const dir = dirname(fileURLToPath(import.meta.url));
const variants = [
  {
    code: "2A",
    name: "OPEN",
    file: "2a-open.svg",
    rx: 152,
    ry: 45,
    innerRx: 132,
    innerRy: 21,
    innerY: -3,
    angle: -13,
    cy: 280,
    leg: 44,
  },
  {
    code: "2B",
    name: "ASCEND",
    file: "2b-ascend.svg",
    rx: 154,
    ry: 43,
    innerRx: 134,
    innerRy: 20,
    innerY: -4,
    angle: -27,
    cy: 264,
    leg: 44,
  },
  { code: "2C", name: "FLOW", file: "2c-flow.svg", wave: true, leg: 44 },
  {
    code: "2D",
    name: "COMPACT",
    file: "2d-compact.svg",
    rx: 133,
    ry: 54,
    innerRx: 110,
    innerRy: 24,
    innerY: -5,
    angle: -20,
    cy: 274,
    leg: 48,
  },
];
const circleConstant = 0.5522847498;
const n = (value) => Math.round(value * 1000) / 1000;
function ellipseRing(v) {
  const { rx: r, ry: s, innerRx: u, innerRy: w, innerY: y } = v;
  const k = circleConstant;
  const full = `M${-r} 0A${r} ${s} 0 1 1 ${r} 0A${r} ${s} 0 1 1 ${-r} 0Z M${-u} ${y}A${u} ${w} 0 1 0 ${u} ${y}A${u} ${w} 0 1 0 ${-u} ${y}Z`;
  const front = `M${-r} 0C${-r} ${n(k * s)} ${n(-k * r)} ${s} 0 ${s}C${n(k * r)} ${s} ${r} ${n(k * s)} ${r} 0L${u} ${y}C${u} ${n(y + k * w)} ${n(k * u)} ${y + w} 0 ${y + w}C${n(-k * u)} ${y + w} ${-u} ${n(y + k * w)} ${-u} ${y}Z`;
  return {
    full,
    front,
    transform: `translate(256 ${v.cy}) rotate(${v.angle})`,
  };
}
function waveRing() {
  return {
    full: "M111 286C130 254 190 258 247 251C315 243 368 196 397 211C430 236 377 278 295 307C224 333 136 353 112 327C103 317 101 303 111 286Z M134 292C131 301 130 308 136 311C158 325 220 312 287 288C349 266 391 239 383 229C374 220 312 264 252 271C193 278 150 276 134 292Z",
    front:
      "M111 286C101 303 103 317 112 327C136 353 224 333 295 307C377 278 430 236 397 211L383 229C391 239 349 266 287 288C220 312 158 325 136 311C130 308 131 301 134 292Z",
    transform: "translate(0 0)",
  };
}
const asImage = (source) =>
  `data:image/svg+xml;base64,${Buffer.from(source).toString("base64")}`;
const marks = [];
for (const v of variants) {
  const id = `loop-${v.code.toLowerCase()}`;
  const shape = v.wave ? waveRing() : ellipseRing(v);
  const gradientAxis = v.wave
    ? 'x1="108" y1="318" x2="403" y2="224"'
    : `x1="${-v.rx}" y1="0" x2="${v.rx}" y2="0"`;
  const defs = `<defs>
    <linearGradient id="${id}-color" ${gradientAxis} gradientUnits="userSpaceOnUse">
      <stop stop-color="#20BBC7"/>
      <stop offset=".56" stop-color="#7F8FE8"/>
      <stop offset="1" stop-color="#DA7BAF"/>
    </linearGradient>
    <filter id="${id}-shadow" x="24" y="28" width="464" height="468" filterUnits="userSpaceOnUse">
      <feDropShadow dx="0" dy="8" stdDeviation="9" flood-color="#26345E" flood-opacity=".14"/>
    </filter>
    <path id="${id}-ring" d="${shape.full}" transform="${shape.transform}" fill-rule="evenodd"/>
    <path id="${id}-front" d="${shape.front}" transform="${shape.transform}"/>
    <mask id="${id}-crossing" x="88" y="104" width="340" height="306" maskUnits="userSpaceOnUse">
      <rect x="88" y="104" width="340" height="306" fill="#FFFFFF"/>
      <use href="#${id}-front" fill="#000000" stroke="#000000" stroke-width="14" stroke-linejoin="round"/>
    </mask>
  </defs>`;
  const mark = `<g id="${id}-mark">
    <use href="#${id}-ring" fill="url(#${id}-color)"/>
    <path d="M150 365L236 155Q256 117 276 155L362 365" stroke="#304074" stroke-width="${v.leg}" stroke-linecap="round" stroke-linejoin="round" mask="url(#${id}-crossing)"/>
    <use href="#${id}-front" fill="url(#${id}-color)"/>
  </g>`;
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="512" height="512" viewBox="0 0 512 512" fill="none">
  <title>AstrLink - ${v.code} ${v.name}</title>
  <desc>A refined A monogram threaded through a continuous cyan, periwinkle and rose ribbon. Variant ${v.code}: ${v.name.toLowerCase()} loop.</desc>
  ${defs}
  <rect x="52" y="52" width="408" height="408" rx="92" fill="#F9FAFE" filter="url(#${id}-shadow)"/>
  ${mark}
</svg>\n`;
  writeFileSync(join(dir, v.file), svg);
  const mono = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" fill="none">${defs}<defs><filter id="ink" x="0" y="0" width="512" height="512" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB"><feFlood flood-color="#303740"/><feComposite in2="SourceGraphic" operator="in"/></filter></defs><g filter="url(#ink)">${mark}</g></svg>`;
  marks.push({ ...v, data: asImage(svg), mono: asImage(mono) });
}

const original = asImage(
  readFileSync(join(dir, "../explorations-ribbon/02-loop.svg")),
);
const cards = marks.map(
  (
    v,
    index,
  ) => `<g transform="translate(${48 + (index % 2) * 544} ${132 + Math.floor(index / 2) * 422})">
  <text x="0" y="24" font-size="17" font-weight="600" fill="#304074">${v.code} / ${v.name}</text>
  <image href="${v.data}" x="0" y="44" width="300" height="300"/>
  <image href="${v.mono}" x="346" y="62" width="112" height="112"/>
  <text x="402" y="202" text-anchor="middle" font-size="11" fill="#78828B">ONE INK</text>
  <image href="${v.data}" x="326" y="260" width="64" height="64"/>
  <image href="${v.data}" x="432" y="276" width="32" height="32"/>
  <text x="358" y="358" text-anchor="middle" font-size="11" fill="#78828B">64 PX</text>
  <text x="448" y="358" text-anchor="middle" font-size="11" fill="#78828B">32 PX</text>
  <line x1="0" y1="390" x2="488" y2="390" stroke="#E3E7E9"/>
</g>`,
);
writeFileSync(
  join(dir, "comparison.svg"),
  `<svg xmlns="http://www.w3.org/2000/svg" width="1120" height="980" viewBox="0 0 1120 980">
  <title>AstrLink - Loop Refinements 2A to 2D</title>
  <desc>Four refinements of selected loop concept 2, with the original and monochrome and small-size views.</desc>
  <rect width="1120" height="980" fill="#FFFFFF"/>
  <g font-family="Arial, Helvetica, sans-serif">
    <text x="48" y="53" font-size="22" font-weight="700" fill="#252D35">ASTRLINK / LOOP REFINEMENTS</text>
    <text x="48" y="81" font-size="12" fill="#78828B">FOUR VARIATIONS ON CONCEPT 02</text>
    <text x="966" y="67" text-anchor="end" font-size="12" fill="#78828B">ORIGINAL 02</text>
    <image href="${original}" x="992" y="12" width="88" height="88"/>
    <line x1="48" y1="110" x2="1080" y2="110" stroke="#E3E7E9"/>
    ${cards.join("\n")}
  </g>
</svg>\n`,
);
execFileSync("rsvg-convert", [
  "-o",
  join(dir, "comparison.png"),
  join(dir, "comparison.svg"),
]);
