import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

const dir = dirname(fileURLToPath(import.meta.url));
const concepts = [
  ["01", "WEAVE", "01-weave.svg"],
  ["02", "LOOP", "02-loop.svg"],
  ["03", "FOLD", "03-fold.svg"],
];
const asImage = (source) => `data:image/svg+xml;base64,${Buffer.from(source).toString("base64")}`;
const groups = concepts.map(([number, name, file], index) => {
  const data = asImage(readFileSync(join(dir, file)));
  const defs = execFileSync("xmllint", ["--xpath", '/*[local-name()="svg"]/*[local-name()="defs"]', join(dir, file)], { encoding: "utf8" });
  const mark = execFileSync("xmllint", ["--xpath", '/*[local-name()="svg"]/*[local-name()="g"]', join(dir, file)], { encoding: "utf8" });
  const mono = asImage(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" fill="none">${defs}<defs><filter id="mono-ink" x="0" y="0" width="512" height="512" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB"><feFlood flood-color="#303740"/><feComposite in2="SourceGraphic" operator="in"/></filter></defs><g filter="url(#mono-ink)">${mark}</g></svg>`);
  return `<g transform="translate(${28 + index * 360} 100)">
    <text x="20" y="30" font-size="17" font-weight="600" fill="#304074">${number} / ${name}</text>
    <image href="${data}" x="0" y="62" width="336" height="336"/>
    <line x1="20" y1="438" x2="316" y2="438" stroke="#E3E7E9"/>
    <image href="${mono}" x="18" y="468" width="90" height="90"/>
    <image href="${data}" x="152" y="482" width="64" height="64"/>
    <image href="${data}" x="274" y="498" width="32" height="32"/>
    <text x="63" y="592" text-anchor="middle" font-size="11" fill="#78828B">ONE INK</text>
    <text x="184" y="592" text-anchor="middle" font-size="11" fill="#78828B">64 PX</text>
    <text x="290" y="592" text-anchor="middle" font-size="11" fill="#78828B">32 PX</text>
  </g>`;
});
writeFileSync(join(dir, "comparison.svg"), `<svg xmlns="http://www.w3.org/2000/svg" width="1120" height="756" viewBox="0 0 1120 756">
  <title>AstrLink - A, Connection and Ribbon</title>
  <desc>Three A monograms with a connecting ribbon, presented in color, one ink and small sizes.</desc>
  <rect width="1120" height="756" fill="#FFFFFF"/>
  <g font-family="Arial, Helvetica, sans-serif">
    <text x="48" y="52" font-size="20" font-weight="700" fill="#252D35">ASTRLINK / A + LINK + RIBBON</text>
    <text x="1072" y="52" text-anchor="end" font-size="12" fill="#78828B">SVG / ROUND 03</text>
    <line x1="48" y1="78" x2="1072" y2="78" stroke="#E3E7E9"/>
    ${groups.join("\n")}
  </g>
</svg>\n`);
execFileSync("rsvg-convert", ["-o", join(dir, "comparison.png"), join(dir, "comparison.svg")]);
