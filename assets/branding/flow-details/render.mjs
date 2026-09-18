import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

const dir = dirname(fileURLToPath(import.meta.url));
const base = readFileSync(join(dir, "../loop-refinements/2c-flow.svg"), "utf8");
const variants = [
  { code: "2C", name: "ORIGINAL", file: "2c-original.svg", description: "Selected flow loop, unchanged.", features: [] },
  { code: "2C-1", name: "STAR", file: "2c-1-star.svg", description: "A four-point star above the returning ribbon.", features: ["star"] },
  { code: "2C-2", name: "NODES", file: "2c-2-nodes.svg", description: "Two open connection nodes integrated into the ribbon.", features: ["nodes"] },
  { code: "2C-3", name: "TRAIL", file: "2c-3-trail.svg", description: "A secondary orbit and two trailing points beneath the main loop.", features: ["trail"] },
  { code: "2C-4", name: "NETWORK", file: "2c-4-network.svg", description: "A small constellation of three connected nodes beside the A.", features: ["network"] },
  { code: "2C-5", name: "COMBINED", file: "2c-5-combined.svg", description: "Star, connection nodes and a secondary orbit together.", features: ["star", "nodes", "trail"] },
];
const asImage = source => `data:image/svg+xml;base64,${Buffer.from(source).toString("base64")}`;

const star = `<path d="M359 140C363 157 369 163 386 167C369 171 363 177 359 194C355 177 349 171 332 167C349 163 355 157 359 140Z" fill="#8B88DD"/>`;
const nodes = `<circle cx="119" cy="308" r="18" fill="#20BBC7" stroke="#F9FAFE" stroke-width="7"/>
    <circle cx="394" cy="225" r="18" fill="#DA7BAF" stroke="#F9FAFE" stroke-width="7"/>`;
const network = `<path d="M120 218L163 168L192 199" stroke="#8495C3" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/>
    <circle cx="120" cy="218" r="9" fill="#20BBC7"/>
    <circle cx="163" cy="168" r="10" fill="#627BB9"/>
    <circle cx="192" cy="199" r="8" fill="#9A88D3"/>`;

const previews = [];
for (const v of variants) {
  if (v.features.length === 0) {
    writeFileSync(join(dir, v.file), base);
    previews.push({ ...v, data: asImage(base) });
    continue;
  }
  const id = v.code.toLowerCase();
  let svg = base.replaceAll("loop-2c", `detail-${id}`);
  svg = svg.replace(/<title>.*?<\/title>/, `<title>AstrLink - ${v.code} ${v.name}</title>`);
  svg = svg.replace(/<desc>.*?<\/desc>/, `<desc>${v.description} The underlying A and flow ribbon retain the selected 2C geometry.</desc>`);
  const additions = [];
  if (v.features.includes("star")) additions.push(star);
  if (v.features.includes("nodes")) additions.push(nodes);
  if (v.features.includes("network")) additions.push(network);
  const trail = v.features.includes("trail") ? `<g stroke-linecap="round">
    <path d="M103 350C119 381 185 371 249 349" stroke="url(#detail-${id}-color)" stroke-width="7"/>
    <circle cx="269" cy="341" r="5" fill="#9290DF"/>
    <circle cx="286" cy="333" r="3.5" fill="#B185CA"/>
  </g>` : "";
  const maskName = `detail-${id}-node-holes`;
  if (v.features.includes("nodes")) {
    svg = svg.replace("</defs>", `<mask id="${maskName}" x="80" y="100" width="352" height="310" maskUnits="userSpaceOnUse">
      <rect x="80" y="100" width="352" height="310" fill="#FFFFFF"/>
      <circle cx="119" cy="308" r="7" fill="#000000"/>
      <circle cx="394" cy="225" r="7" fill="#000000"/>
    </mask>
  </defs>`);
  }
  const maskAttribute = v.features.includes("nodes") ? ` mask="url(#${maskName})"` : "";
  svg = svg.replace(`<g id="detail-${id}-mark">`, `<g id="detail-${id}-mark"${maskAttribute}>
    ${trail}`);
  svg = svg.replace("  </g>\n</svg>", `    ${additions.join("\n    ")}
  </g>\n</svg>`);
  writeFileSync(join(dir, v.file), svg);
  previews.push({ ...v, data: asImage(svg) });
}

const cards = previews.map((v, index) => `<g transform="translate(${36 + index % 3 * 464} ${136 + Math.floor(index / 3) * 450})">
  <text x="14" y="25" font-size="18" font-weight="600" fill="#304074">${v.code} / ${v.name}</text>
  <image href="${v.data}" x="-4" y="45" width="350" height="350"/>
  <image href="${v.data}" x="354" y="158" width="64" height="64"/>
  <text x="386" y="247" text-anchor="middle" font-size="11" fill="#78828B">64 PX</text>
  <image href="${v.data}" x="370" y="288" width="32" height="32"/>
  <text x="386" y="345" text-anchor="middle" font-size="11" fill="#78828B">32 PX</text>
  <line x1="14" y1="416" x2="426" y2="416" stroke="#E3E7E9"/>
</g>`);

writeFileSync(join(dir, "comparison.svg"), `<svg xmlns="http://www.w3.org/2000/svg" width="1440" height="1050" viewBox="0 0 1440 1050">
  <title>AstrLink - 2C Detail Studies</title>
  <desc>The original 2C logo compared with five treatments adding stars, nodes, trails and constellation elements. Every variant retains the same A and primary ribbon.</desc>
  <rect width="1440" height="1050" fill="#FFFFFF"/>
  <g font-family="Arial, Helvetica, sans-serif">
    <text x="50" y="56" font-size="24" font-weight="700" fill="#252D35">ASTRLINK / 2C DETAIL STUDIES</text>
    <text x="50" y="85" font-size="13" fill="#78828B">ONE BASE / FIVE ADDITIONS</text>
    <text x="1390" y="56" text-anchor="end" font-size="12" fill="#78828B">SVG / COMPARISON</text>
    <line x1="50" y1="114" x2="1390" y2="114" stroke="#E3E7E9"/>
    ${cards.join("\n")}
  </g>
</svg>\n`);
execFileSync("rsvg-convert", ["-o", join(dir, "comparison.png"), join(dir, "comparison.svg")]);
