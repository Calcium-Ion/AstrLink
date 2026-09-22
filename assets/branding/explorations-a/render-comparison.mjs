import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync } from "node:child_process";

const dir = dirname(fileURLToPath(import.meta.url));
const concepts = [
  ["01", "BRIDGE A", "01-bridge.svg", "#304374"],
  ["02", "FOLD A", "02-fold.svg", "#155548"],
  ["03", "ARCH A", "03-arch.svg", "#CA6258"],
  ["04", "AL LIGATURE", "04-ligature.svg", "#303640"],
];
const asImage = (source) =>
  `data:image/svg+xml;base64,${Buffer.from(source).toString("base64")}`;
const groups = concepts.map(([number, name, file, color], index) => {
  const data = asImage(readFileSync(join(dir, file)));
  const mark = execFileSync(
    "xmllint",
    ["--xpath", '/*[local-name()="svg"]/*[local-name()="g"]', join(dir, file)],
    { encoding: "utf8" },
  );
  const mono = asImage(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" fill="none"><defs><filter id="ink" x="0" y="0" width="512" height="512" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB"><feFlood flood-color="#303740"/><feComposite in2="SourceGraphic" operator="in"/></filter></defs><g filter="url(#ink)">${mark}</g></svg>`,
  );
  const x = 48 + (index % 2) * 544;
  const y = 104 + Math.floor(index / 2) * 426;
  return `<g transform="translate(${x} ${y})">
    <text x="0" y="30" font-size="16" font-weight="600" fill="${color}">${number} / ${name}</text>
    <image href="${data}" x="0" y="48" width="280" height="280"/>
    <image href="${mono}" x="340" y="66" width="100" height="100"/>
    <text x="390" y="190" text-anchor="middle" font-size="12" fill="#78828B">ONE INK</text>
    <image href="${data}" x="308" y="244" width="64" height="64"/>
    <image href="${data}" x="416" y="268" width="32" height="32"/>
    <text x="340" y="342" text-anchor="middle" font-size="12" fill="#78828B">64 PX</text>
    <text x="432" y="342" text-anchor="middle" font-size="12" fill="#78828B">32 PX</text>
    <line x1="0" y1="388" x2="488" y2="388" stroke="#E3E7E9"/>
  </g>`;
});
writeFileSync(
  join(dir, "comparison.svg"),
  `<svg xmlns="http://www.w3.org/2000/svg" width="1120" height="984" viewBox="0 0 1120 984">
  <title>AstrLink - Four A Monograms</title>
  <desc>Four A-based SVG concepts shown in color, one ink, and small icon sizes.</desc>
  <rect width="1120" height="984" fill="#FFFFFF"/>
  <g font-family="Arial, Helvetica, sans-serif">
    <text x="48" y="52" font-size="20" font-weight="700" fill="#252D35">ASTRLINK / A MONOGRAMS</text>
    <text x="1080" y="52" text-anchor="end" font-size="12" fill="#78828B">SVG / ROUND 02</text>
    <line x1="48" y1="78" x2="1080" y2="78" stroke="#E3E7E9"/>
    ${groups.join("\n")}
    <text x="48" y="960" font-size="12" fill="#78828B">EXPLORATIONS / 01-04</text>
  </g>
</svg>\n`,
);
execFileSync("rsvg-convert", [
  "-o",
  join(dir, "comparison.png"),
  join(dir, "comparison.svg"),
]);
