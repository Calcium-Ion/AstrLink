import { cp, readdir, rm, stat } from "node:fs/promises";
import { fileURLToPath } from "node:url";
const source = fileURLToPath(new URL("../dist-web/", import.meta.url));
const target = fileURLToPath(
  new URL("../../../core/internal/console/webui/", import.meta.url),
);
await stat(`${source}/index.html`);
for (const name of await readdir(target)) {
  if (name !== "placeholder.html")
    await rm(`${target}/${name}`, { recursive: true, force: true });
}
await cp(source, target, { recursive: true });
