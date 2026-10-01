// Never insert provider SVG into the application DOM. Accept only static SVG,
// then rasterize in an image context before displaying or sending it to a judge.
export function staticQuestionSVG(output: string): string {
  const start = output.indexOf("<svg");
  const end = output.lastIndexOf("</svg>");
  if (start < 0 || end < start || output.length > 1024 * 1024)
    throw new Error("没有完整的 SVG 图片");
  const doc = new DOMParser().parseFromString(
    output.slice(start, end + 6),
    "image/svg+xml",
  );
  if (
    doc.querySelector("parsererror") ||
    doc.documentElement.localName !== "svg"
  )
    throw new Error("SVG 格式错误");
  const allowed = new Set([
    "svg",
    "g",
    "defs",
    "path",
    "rect",
    "circle",
    "ellipse",
    "line",
    "polyline",
    "polygon",
    "text",
    "tspan",
    "textPath",
    "title",
    "desc",
    "linearGradient",
    "radialGradient",
    "stop",
    "clipPath",
    "mask",
    "pattern",
    "use",
    "symbol",
    "marker",
    "filter",
    "feGaussianBlur",
    "feOffset",
    "feBlend",
    "feColorMatrix",
    "feMerge",
    "feMergeNode",
    "feFlood",
    "feComposite",
    "style",
  ]);
  const unsafeURL = (value: string) =>
    [...value.matchAll(/url\s*\(([^)]*)\)/gi)].some(
      (match) =>
        !/^#[\w.-]+$/.test(match[1].trim().replace(/^["']|["']$/g, "")),
    ) || /@import|\\|expression\s*\(/i.test(value);
  for (const el of [
    doc.documentElement,
    ...doc.documentElement.querySelectorAll("*"),
  ]) {
    if (
      !allowed.has(el.localName) ||
      el.namespaceURI !== "http://www.w3.org/2000/svg"
    )
      throw new Error("SVG 包含非静态绘图内容");
    if (el.localName === "style" && unsafeURL(el.textContent ?? ""))
      throw new Error("SVG 不支持外部资源");
    for (const attr of el.attributes) {
      if (
        /^on/i.test(attr.name) ||
        attr.localName === "base" ||
        unsafeURL(attr.value) ||
        (attr.localName === "href" && !/^#[\w.-]+$/.test(attr.value))
      )
        throw new Error("SVG 包含脚本或外部资源");
    }
  }
  const root = doc.documentElement;
  const viewBox = root
    .getAttribute("viewBox")
    ?.trim()
    .split(/[\s,]+/)
    .map(Number);
  const width = viewBox?.[2] || parseFloat(root.getAttribute("width") ?? "800");
  const height =
    viewBox?.[3] || parseFloat(root.getAttribute("height") ?? "600");
  if (
    !(
      width > 0 &&
      height > 0 &&
      Number.isFinite(width) &&
      Number.isFinite(height)
    )
  )
    throw new Error("SVG 尺寸无效");
  const scale = Math.min(1, 1024 / width, 1024 / height);
  if (!viewBox) root.setAttribute("viewBox", `0 0 ${width} ${height}`);
  root.setAttribute("width", String(Math.max(1, Math.round(width * scale))));
  root.setAttribute("height", String(Math.max(1, Math.round(height * scale))));
  return new XMLSerializer().serializeToString(doc);
}

async function rasterize(blob: Blob): Promise<string> {
  // The desktop CSP permits data images, but intentionally excludes blob URLs.
  const url = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(new Error("图片读取失败"));
    reader.readAsDataURL(blob);
  });
  const img = new Image();
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("图片解码超时")), 10000);
    img.onload = () => {
      clearTimeout(timer);
      resolve();
    };
    img.onerror = () => {
      clearTimeout(timer);
      reject(new Error("图片无法渲染"));
    };
    img.src = url;
  });
  const canvas = document.createElement("canvas");
  let scale = Math.min(1, 1024 / img.naturalWidth, 1024 / img.naturalHeight);
  for (let attempt = 0; attempt < 5; attempt++) {
    canvas.width = Math.max(1, Math.round(img.naturalWidth * scale));
    canvas.height = Math.max(1, Math.round(img.naturalHeight * scale));
    const ctx = canvas.getContext("2d");
    if (!ctx) throw new Error("图片渲染不可用");
    ctx.fillStyle = "white";
    ctx.fillRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
    const png = canvas.toDataURL("image/png");
    if (png.length <= 349540) return png;
    scale *= 0.7;
  }
  throw new Error("图片过大，请使用更简单的图片");
}
export async function renderQuestionSVG(output: string) {
  return rasterize(
    new Blob([staticQuestionSVG(output)], { type: "image/svg+xml" }),
  );
}
export async function readReferenceImage(file: File) {
  if (
    !/^image\/(png|jpeg|webp)$/.test(file.type) ||
    file.size > 8 * 1024 * 1024
  )
    throw new Error("请选择 8 MB 以内的 PNG、JPEG 或 WebP 图片");
  return rasterize(file);
}
