/**
 * Incho — New API task plugin (music generation)
 *
 * Incho is a self-developed music generation model (multilingual vocals, 44.1 kHz).
 * Public API documentation: https://platform.yinchaoyongxian.com/docs
 *
 * Contributed by @yyzhang806. The 1.0.0 marketplace release incorporates the new-api
 * maintainers' review fixes: alias-safe model echo, readable errors, code-point length
 * limits, strict n validation, fixed Accept header, and structured upstream error details.
 * 1.0.1 declares the public API host as the default base URL, so binding a Task Plugin
 * channel no longer requires typing it. The Incho brand logo ships as the sidecar
 * plugins/tasks/incho/icon.svg; meta.icon stays a text fallback for gateways that
 * predate sidecar logos.
 * 1.0.2 adds localized action names for pricing displays.
 *
 * Upstream API surface used here:
 *   Base URL   https://open.yinchaoyongxian.com
 *   Submit     POST /api/v1/song/generate  -> { id, task_type, choices: [], create_at }
 *   Query      GET  /api/v1/task/query?task_id=<uuid>
 *   Song state pending | cancelled | running | stream | done | fail
 *   Artifacts  audio_url / pipe_url / title / lyric / duration / size / error / error_code
 *   Errors     HTTP 4xx/5xx with { "detail": "..." }
 *
 * Notes on contract choices:
 *   - fetchMode is "per_task": upstream also exposes GET /api/v1/task/querys?task_ids=a,b
 *     ({tasks:[...]}), but v1 intentionally stays per_task. Batch polling semantics
 *     and URL-length chunking are host follow-ups.
 *   - Bind a Task Plugin channel using task_plugin_key=incho. No legacy channel type
 *     is claimed.
 *   - Outgoing requests carry "X-Incho-Source: plugins" and a plugin User-Agent so that
 *     traffic arriving through this plugin can be told apart from direct integrations.
 *     No user data is added; the gateway operator's own API key is used unchanged.
 *
 * Scope is deliberately narrow: prompt-to-song only (task_type=normal, exposed
 * as "incho_music"). The default model is v4.0; v3.5 is also supported through
 * channel model_mapping or the native body / Responses metadata model field.
 * Reference and extend modes currently support v3.5 only; lyrics generation is a
 * synchronous endpoint that returns no task id and therefore does not fit the async
 * task contract. Both are planned for a later version.
 */

export const meta = {
  apiVersion: 1,
  key: "incho",
  name: "Incho",
  sortPriority: 100,
  icon: "text",
  description: {
    en: "Incho music generation (prompt-to-song, multilingual vocals)",
    zh: "音潮 Incho 音乐生成（提示词生成歌曲、多语种人声）",
  },
  version: "1.0.2",
  author: { name: "yyzhang806" },
  website: "https://platform.yinchaoyongxian.com/?register_channel=new",
  baseUrl: "https://open.yinchaoyongxian.com",
  models: ["incho_music"],
  fetchMode: "per_task",
  usageSchema: {
    clips: {
      type: "number",
      unit: "count",
      description: { en: "Song generation unit price", zh: "生成歌曲单价" },
    },
    action: { enum: ["music"], enumLabels: { music: { en: "Generate songs", zh: "生成歌曲" } }, description: { en: "Generate songs", zh: "生成歌曲" } },
  },
  protocols: [{ name: "openai_responses", supports: ["stream", "sync", "background"] }],
  routes: [
    { method: "POST", path: "/incho/submit/:action", type: "submit", decode: "decodeSubmit", render: "renderSubmit" },
    { method: "GET", path: "/incho/fetch/:task_id", type: "query", render: "renderTask" },
  ],
};

const API_PREFIX = "/api/v1";
// Identifies traffic that reaches Incho through this plugin, so the channel can be
// measured separately from direct API integrations.
const SOURCE_TAG = "plugins";
const USER_AGENT = "incho-newapi-plugin/1.0.0";
const DEFAULT_MODEL = "v4.0";
const MAX_PROMPT = 1000;
const MAX_LYRIC = 3000;

function trimmed(value) {
  return String(value || "").trim();
}

function responsesText(req) {
  const texts = [];
  const input = req.input;
  if (typeof input === "string") texts.push(input);
  else if (Array.isArray(input)) {
    for (const item of input) {
      if (typeof item === "string") {
        texts.push(item);
        continue;
      }
      if (!item || typeof item !== "object" || Array.isArray(item)) continue;
      const content = item.content === undefined ? [item] : Array.isArray(item.content) ? item.content : [item.content];
      for (const part of content) {
        if (typeof part === "string") {
          texts.push(part);
          continue;
        }
        if (!part || typeof part !== "object" || Array.isArray(part)) continue;
        if (["input_text", "text"].includes(part.type) && typeof part.text === "string") texts.push(part.text);
      }
    }
  }
  return texts
    .filter(function (text) {
      return trimmed(text);
    })
    .join("\n");
}

function actionName(ctx) {
  return String((ctx.params || {}).action || ctx.action || "").toUpperCase();
}

function authHeaders(ctx) {
  return {
    "Content-Type": "application/json",
    Accept: "application/json",
    Authorization: "Bearer " + ctx.apiKey,
    "X-Incho-Source": SOURCE_TAG,
    "User-Agent": USER_AGENT,
  };
}

// --- native decode -----------------------------------------------------------

function decodeNativeSubmit(ctx) {
  if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
  const action = actionName(ctx);
  if (action !== "MUSIC") throw new Error("action must be music; use POST /incho/submit/music");
  return { kind: "submit", model: "incho_music", action: action, requestBody: ctx.body.value };
}

/**
 * Upstream constraints for /song/generate:
 *   model      required (normal mode supports v3.5 / v4.0)
 *   task_type  fixed to "normal" here
 *   prompt     max 1000 chars; lyric max 3000 chars (model writes lyrics when omitted)
 *   n          1-2, defaults to 2
 */
function validateAndNormalize(ctx) {
  const incoming = Object.assign({}, ctx.requestBody || {});
  if (actionName(ctx) !== "MUSIC") throw new Error("action must be music; use POST /incho/submit/music");

  const prompt = trimmed(incoming.prompt || incoming.gpt_description_prompt);
  const lyric = trimmed(incoming.lyric);
  if (!prompt && !lyric) throw new Error("prompt or lyric is required");
  if (Array.from(prompt).length > MAX_PROMPT) throw new Error("prompt must be at most 1000 characters");
  if (Array.from(lyric).length > MAX_LYRIC) throw new Error("lyric must be at most 3000 characters");

  let n = 2;
  if (incoming.n != null) {
    n = typeof incoming.n === "number" || typeof incoming.n === "string" ? Number(incoming.n) : NaN;
    if (!Number.isInteger(n) || n < 1 || n > 2) throw new Error("n must be 1 or 2");
  }

  // A channel mapping is authoritative. Without a mapping, accept the public
  // model name as the default and preserve explicit native vendor versions.
  const upstreamModel = trimmed(ctx.upstreamModel);
  const mappedModel = upstreamModel && upstreamModel !== trimmed(ctx.model) ? upstreamModel : "";
  let model = mappedModel || trimmed(incoming.model) || DEFAULT_MODEL;
  if (model === "incho_music") model = DEFAULT_MODEL;
  if (!["v3.5", "v4.0"].includes(model)) throw new Error("model must be v3.5 or v4.0");

  const body = { model: model, task_type: "normal", n: n };
  if (prompt) body.prompt = prompt;
  if (lyric) body.lyric = lyric;

  return { action: "MUSIC", body: body, n: n };
}

// --- submit ------------------------------------------------------------------

export function buildSubmitRequest(ctx) {
  const normalized = validateAndNormalize(ctx);
  return {
    url: ctx.baseUrl + API_PREFIX + "/song/generate",
    method: "POST",
    headers: authHeaders(ctx),
    body: normalized.body,
    action: normalized.action,
  };
}

/** A successful submit returns { id, task_type, choices: [], create_at }. The task id field is `id`. */
export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const detail = body.detail;
  if (detail != null && detail !== "") {
    let message = "";
    if (typeof detail === "string") message = trimmed(detail);
    else if (Array.isArray(detail))
      message = detail
        .map(function (item) {
          if (item && typeof item === "object" && !Array.isArray(item) && typeof item.msg === "string") return item.msg;
          return JSON.stringify(item);
        })
        .join("; ");
    else if (typeof detail === "object") message = JSON.stringify(detail);
    else message = String(detail);
    if (trimmed(message)) throw new Error(message);
  }
  const taskId = trimmed(body.id);
  if (!taskId) throw new Error("upstream response did not include a task id");
  return { taskId: taskId, taskData: { task_type: trimmed(body.task_type), create_at: body.create_at || 0 } };
}

export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  let n = Number((ctx.requestBody || {}).n);
  if (!Number.isFinite(n)) n = 2;
  return { clips: n === 1 ? 1 : 2, action: "music" };
}

// --- query -------------------------------------------------------------------

export function buildQueryRequest(ctx) {
  const taskId = trimmed(ctx.taskId || (ctx.params || {}).task_id);
  if (!taskId) throw new Error("task_id is empty");
  return {
    url: ctx.baseUrl + API_PREFIX + "/task/query?task_id=" + encodeURIComponent(taskId),
    method: "GET",
    headers: authHeaders(ctx),
  };
}

/**
 * One upstream task holds n songs (choices). Task-level status:
 *   all terminal and at least one done -> SUCCESS (partial success still succeeds;
 *                                        failed songs are reported in `reason`)
 *   all terminal and none done         -> FAILURE
 *   otherwise                          -> IN_PROGRESS / QUEUED
 */
export function parseTaskResult(ctx, body) {
  const task = body;
  if (!task || typeof task !== "object" || Array.isArray(task) || !trimmed(task.id) || !Array.isArray(task.choices))
    return { status: "UNKNOWN", reason: "Unrecognized Incho task response" };
  const choices = task.choices;
  if (
    choices.some(function (song) {
      return !song || !["pending", "cancelled", "running", "stream", "done", "fail"].includes(trimmed(song.status));
    })
  )
    return { status: "UNKNOWN", reason: "Unrecognized Incho song status" };

  const done = choices.filter(function (s) {
    return trimmed(s.status) === "done";
  });
  const terminal = choices.filter(function (s) {
    return ["done", "fail", "cancelled"].includes(trimmed(s.status));
  });
  const failed = choices.filter(function (s) {
    return ["fail", "cancelled"].includes(trimmed(s.status));
  });

  let status;
  if (choices.length === 0) {
    status = "QUEUED";
  } else if (terminal.length < choices.length) {
    status = choices.some(function (s) {
      return ["running", "stream"].includes(trimmed(s.status));
    })
      ? "IN_PROGRESS"
      : "QUEUED";
  } else {
    status = done.length > 0 ? "SUCCESS" : "FAILURE";
  }

  const reason = failed
    .map(function (s) {
      return trimmed(s.error) + (s.error_code ? " (" + s.error_code + ")" : "");
    })
    .filter(Boolean)
    .join("; ");

  return {
    taskId: trimmed(task.id),
    status: status,
    reason: reason,
    progress: choices.length ? Math.round((done.length / choices.length) * 100) + "%" : "0%",
  };
}

// --- artifacts ---------------------------------------------------------------

function songData(data) {
  // Per-task polling persists the entire upstream response, not a parse hook's
  // data field. Continue reading arrays / individual songs from older tasks.
  if (Array.isArray(data)) return data;
  if (!data || typeof data !== "object") return [];
  if (Array.isArray(data.choices)) return data.choices;
  return data.status ? [data] : [];
}

function artifactKey(type, song) {
  return type + "-" + utils.hmacSHA256(String(song.id), "new-api:incho:artifact-key");
}

/** Incho produces audio only; there is no cover image artifact. */
export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  const artifacts = [];
  for (const song of songData(task.data)) {
    if (!song || !trimmed(song.id)) continue;
    if (trimmed(song.audio_url)) {
      artifacts.push({ key: artifactKey("audio", song), type: "audio", mimeType: "audio/mpeg" });
    }
  }
  return artifacts;
}

export function buildContentRequest(ctx) {
  const song = songData(ctx.data).find(function (item) {
    if (!item || !trimmed(item.id)) return false;
    return artifactKey("audio", item) === ctx.artifactKey;
  });
  if (!song) throw new Error("the requested audio artifact was not found on this task");
  const url = trimmed(song.audio_url);
  if (!url) throw new Error("the requested audio artifact was not found on this task");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

/** Billed per song that actually completed. Upstream is the source of truth; this only reports. */
export function extractUsageOnComplete(task, taskResult, body) {
  const values = songData(body);
  if (values.length === 0) return null;
  const done = values.filter(function (item) {
    return item && trimmed(item.status) === "done" && trimmed(item.audio_url);
  });
  return { clips: done.length, action: "music" };
}

// --- openai_responses --------------------------------------------------------

function escapedAttribute(value) {
  return trimmed(value).replace(/&/g, "&amp;").replace(/"/g, "&quot;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function responseContent(ctx, task) {
  const songs = songData(task.data);
  const lyrics = [];
  for (const song of songs) {
    if (!song) continue;
    const text = trimmed(song.lyric);
    if (!text) continue;
    const title = trimmed(song.title);
    lyrics.push(title ? title + "\n" + text : text);
  }

  const content = [{ type: "output_text", text: lyrics.join("\n\n") || "Music generation completed.", annotations: [], logprobs: [] }];

  for (const song of songs) {
    if (!song || !trimmed(song.audio_url)) continue;
    const key = artifactKey("audio", song);
    const artifact = ctx && ctx.artifacts && ctx.artifacts[key];
    const url = trimmed(artifact && artifact.url);
    if (!url) throw new Error("audio artifact is unavailable");
    content.push({
      type: "output_text",
      text: '<audio controls src="' + escapedAttribute(url) + '"></audio>',
      annotations: [],
      logprobs: [],
    });
  }
  return content;
}

function responseText(ctx, task) {
  return responseContent(ctx, task)
    .map(function (part) {
      return part.text;
    })
    .join("\n\n");
}

export const protocols = {
  openai_responses: {
    decodeRequest: function (ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const req = ctx.body.value;
      if (!req || typeof req !== "object" || Array.isArray(req)) throw new Error("request body must be an object");
      const declared = trimmed(ctx.upstreamModel || ctx.model);
      if (declared !== "incho_music") throw new Error("model must be incho_music or a channel alias mapped to it");
      if (req.input !== undefined && typeof req.input !== "string" && !Array.isArray(req.input)) throw new Error("input must be a string or array");
      if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
        throw new Error("metadata must be an object");

      const input = responsesText(req);
      const requestBody = Object.assign({}, req.metadata || {});
      if (!trimmed(requestBody.prompt)) requestBody.prompt = input || trimmed(req.prompt);
      if (!trimmed(requestBody.prompt) && !trimmed(requestBody.lyric)) throw new Error("input is required");

      return { kind: "submit", model: ctx.model, action: "MUSIC", requestBody: requestBody };
    },

    renderEvents: function (ctx, task, previousState) {
      const status = String(task.status || "UNKNOWN").toUpperCase();
      const value = Number(String(task.progress || "").replace("%", ""));
      const progress = Number.isFinite(value) && value >= 0 && value <= 100 ? value : null;
      const state = { status: status, progress: progress };

      if (status === "SUCCESS") {
        const text = responseText(ctx, task);
        const events = previousState && previousState.status === status ? [] : [{ type: "output", data: text }];
        return { events: events, state: state, done: true };
      }
      if (status === "FAILURE") {
        return {
          events: [{ type: "error", code: "task_failed", message: task.fail_reason || "task failed" }],
          state: state,
          done: true,
        };
      }
      if (previousState && previousState.status === status && previousState.progress === progress) return { events: [], state: state, done: false };

      const event = { type: "progress", message: status.toLowerCase() };
      if (progress !== null) event.progress = progress;
      return { events: [event], state: state, done: false };
    },

    renderFinal: function (ctx, task) {
      return {
        output: [{ type: "message", status: "completed", role: "assistant", content: responseContent(ctx, task) }],
        metadata: { vendor: "incho" },
      };
    },
  },
};

// --- native ------------------------------------------------------------------

function nativeTask(task) {
  return {
    created_at: task.created_at || 0,
    updated_at: task.updated_at || 0,
    task_id: task.task_id || "",
    platform: task.platform || "incho",
    status: task.status || "",
    fail_reason: task.fail_reason || "",
    submit_time: task.created_at || 0,
    finish_time: task.finished_at || 0,
    progress: task.progress || "",
    data: task.data === undefined ? null : task.data,
  };
}

export const native = {
  decodeSubmit: decodeNativeSubmit,
  renderSubmit: function (ctx, task) {
    return { code: "success", message: "", data: String(task.task_id || "") };
  },
  renderTask: function (ctx, task) {
    return { code: "success", message: "", data: nativeTask(task) };
  },
  error: function (ctx, error) {
    return { code: error.code, message: error.message, data: null };
  },
};
