// TypeSafe System One API: https://docs.typesafe.ai/api
// Jev accepts 64Ki input tokens per request, or 256Ki with the long-context
// header (https://docs.typesafe.ai/models#context-length). TypeSafe bills a
// 256-token request prefix that this limit leaves out, so a request reports at
// most 65,792 or 262,400 input tokens (measured on jev-1.13.0 and jev-1.14.0).
// Reserve that maximum without guessing the proprietary tokenizer; completion
// replaces it with upstream usage.input_tokens.
const MAX_INPUT_TOKENS = 65792;
const LONG_CONTEXT_INPUT_TOKENS = 262400;
const LONG_CONTEXT_HEADER = "TypeSafe-Danger-Zone-Long-Context";
const MODELS = ["jev-1.14.0", "jev-1.13.0", "jev-latest", "jev-preview"];
// TypeSafe rejects any other top-level request member.
const REQUEST_FIELDS = ["model", "state", "questions", "assets"];

export const meta = {
  apiVersion: 1,
  key: "typesafe",
  name: "TypeSafe",
  sortPriority: 90,
  version: "1.2.0",
  icon: "text:TS",
  author: { name: "QuantumNous", url: "https://github.com/QuantumNous" },
  website: "https://typesafe.ai",
  description: {
    en: "Typed decisions with TypeSafe Jev, input priced per million tokens and output free",
    zh: "TypeSafe Jev 结构化判断，输入按每百万 token 定价，输出免费",
  },
  baseUrl: "https://api.typesafe.ai",
  auth: "api_key",
  models: MODELS,
  upstreams: ["vendor", "new_api"],
  // Jev reads the state object as written, so member order changes its
  // answers; the host keeps the client's order only for plugins that ask.
  requiredCapabilities: ["json-order@1"],
  fetchMode: "per_task",
  routes: [
    {
      method: "POST",
      path: "/typesafe/v1/systemone",
      type: "submit",
      decode: "decodeSystemOne",
      render: "renderSystemOne",
      retainResult: false,
    },
    // The vendor path, so SDK clients can use the gateway root as base URL.
    {
      method: "POST",
      path: "/v1/systemone",
      type: "submit",
      decode: "decodeSystemOne",
      render: "renderSystemOne",
      retainResult: false,
    },
  ],
  usageSchema: {
    input_tokens: {
      type: "number",
      unit: "token",
      description: { en: "Input token unit price", zh: "输入 token 单价" },
    },
  },
  usageExamples: [{ label: "Jev", facts: { input_tokens: 1000 } }],
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

// Instructions, Choice option descriptions and Noul outcomes may be null; the
// state and Score levels may not (https://api.typesafe.ai/openapi.json).
function isEntry(value) {
  return value === null || typeof value === "string" || typeof value === "object";
}

function validateRequest(body) {
  if (!isObject(body)) throw new Error("Request body must be a JSON object");
  for (const field of Object.keys(body)) {
    if (!REQUEST_FIELDS.includes(field)) throw new Error("TypeSafe System One does not accept the request field " + field);
  }
  if (typeof body.model !== "string" || !body.model.trim()) throw new Error("model is required");
  if (body.state === null || !isEntry(body.state)) throw new Error("state must be text, an object, or an array");
  if (!isObject(body.questions) || Object.keys(body.questions).length === 0) throw new Error("questions must be a nonempty object");

  for (const id of Object.keys(body.questions)) {
    const question = body.questions[id];
    if (!isObject(question) || !["choice", "score", "noul", "box"].includes(question.type))
      throw new Error("Each question must have type choice, score, noul, or box");
    if (question.instructions !== undefined && !isEntry(question.instructions))
      throw new Error("Question instructions must be text, an object, an array, or null");

    // Box questions take no criteria, and TypeSafe ignores any sent.
    const criteria = question.criteria;
    if (question.type === "choice") {
      if (!isObject(criteria) || Object.keys(criteria).length === 0 || Object.keys(criteria).length > 255)
        throw new Error("Choice criteria must contain between 1 and 255 options");
      if (
        Object.values(criteria).some(function (value) {
          return !isEntry(value);
        })
      )
        throw new Error("Choice option descriptions must be text, objects, arrays, or null");
    } else if (question.type === "score") {
      if (!Array.isArray(criteria) || criteria.length < 1 || criteria.length > 10) throw new Error("Score criteria must contain between 1 and 10 levels");
      if (
        criteria.some(function (value) {
          return value === null || !isEntry(value);
        })
      )
        throw new Error("Score levels must be text, objects, or arrays");
    } else if (question.type === "noul" && criteria != null) {
      // TypeSafe reads true and false and ignores other members.
      if (
        !isObject(criteria) ||
        [criteria.true, criteria.false].some(function (value) {
          return value !== undefined && !isEntry(value);
        })
      )
        throw new Error("Noul criteria must describe true and false with text, objects, arrays, or null");
    }
  }
  return body;
}

// The body TypeSafe receives. Images travel in assets, keyed by the reference
// written where each image belongs, as base64 or as a multipart part name.
function systemOneBody(body, model) {
  const request = { model: model, state: body.state, questions: body.questions };
  if (body.assets !== undefined) request.assets = body.assets;
  return request;
}

function systemOneRequest(ctx) {
  const body = validateRequest(ctx.requestBody);
  const model = ctx.upstreamModel || ctx.model;
  if (!MODELS.includes(model)) throw new Error("Unsupported TypeSafe model; use a declared Jev model or map an alias to it");
  return systemOneBody(body, model);
}

// Header names arrive in canonical form, so they are compared case-insensitively.
function longContext(ctx) {
  const headers = ctx.requestHeaders || {};
  return Object.keys(headers).some(function (name) {
    return name.toLowerCase() === LONG_CONTEXT_HEADER.toLowerCase() && String(headers[name]).trim().toLowerCase() === "true";
  });
}

export function buildSubmitRequest(ctx) {
  const body = systemOneRequest(ctx);
  let baseUrl = ctx.baseUrl.replace(/\/+$/, "");
  // Accept channel base URLs with an optional /v1 suffix.
  if (baseUrl.endsWith("/v1")) baseUrl = baseUrl.slice(0, -3);
  const prefix = ctx.upstream && ctx.upstream.kind === "new_api" ? "/typesafe" : "";
  const headers = { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" };
  // TypeSafe answers images and Box questions only with this header set.
  const images =
    body.assets !== undefined ||
    Object.keys(body.questions).some(function (id) {
      return body.questions[id].type === "box";
    });
  if (images) headers["TypeSafe-Danger-Zone-Images"] = "true";
  if (longContext(ctx)) headers[LONG_CONTEXT_HEADER] = "true";
  const request = { url: baseUrl + prefix + "/v1/systemone", method: "POST", headers: headers };

  const files = ctx.files || [];
  if (files.length === 0) {
    headers["Content-Type"] = "application/json";
    request.body = body;
    return request;
  }
  // The SDKs upload images as file parts that assets name by part. TypeSafe
  // reads the JSON from the first part, named request, then the files as sent.
  request.bodyType = "multipart";
  request.parts = [{ name: "request", value: JSON.stringify(body) }].concat(
    files.map(function (file) {
      return { name: file.field, fileRef: file.ref, filename: file.filename };
    }),
  );
  return request;
}

export function parseSubmitResponse(ctx, response) {
  if (response.statusCode < 200 || response.statusCode >= 300) throw new Error("TypeSafe returned HTTP " + response.statusCode);
  const body = response.body;
  if (!isObject(body) || typeof body.model !== "string" || !body.model || !isObject(body.answers))
    throw new Error("TypeSafe response is missing model or answers");
  for (const id of Object.keys(ctx.requestBody.questions)) {
    const answer = Object.prototype.hasOwnProperty.call(body.answers, id) ? body.answers[id] : null;
    if (!isObject(answer) || answer.type !== ctx.requestBody.questions[id].type)
      throw new Error("TypeSafe response is missing a matching answer for a question");
  }
  if (!ctx.publicTaskId) throw new Error("Missing gateway request ID");
  return {
    taskId: ctx.publicTaskId,
    taskData: body,
    immediate: { status: "SUCCESS", progress: "100%" },
  };
}

export function extractUsage(ctx) {
  systemOneRequest(ctx);
  if (ctx.usagePurpose === "billing_ratios") return null;
  return { input_tokens: longContext(ctx) ? LONG_CONTEXT_INPUT_TOKENS : MAX_INPUT_TOKENS };
}

export function extractUsageOnComplete(ctx, result, body) {
  const tokens = body && body.usage && body.usage.input_tokens;
  // Throwing preserves the reservation and makes the host log invalid usage.
  // Never coerce a missing/invalid value to zero or bill free output tokens.
  // Completion cannot see the request headers, so the bound is the larger
  // long-context budget; TypeSafe enforces each request's own limit.
  if (!Number.isInteger(tokens) || tokens < 0 || tokens > LONG_CONTEXT_INPUT_TOKENS)
    throw new Error("TypeSafe input token usage is missing or outside the supported context budget");
  return { input_tokens: tokens };
}

// API v1 requires polling hooks, but System One always completes synchronously.
export function buildQueryRequest() {
  throw new Error("TypeSafe System One has no task retrieval endpoint");
}

export function parseTaskResult() {
  return { status: "UNKNOWN", reason: "TypeSafe System One has no asynchronous tasks" };
}

export const native = {
  decodeSystemOne: function (ctx) {
    let value;
    if (ctx.body && ctx.body.kind === "json") {
      value = ctx.body.value;
    } else if (ctx.body && ctx.body.kind === "multipart") {
      // The SDKs send requests with images as multipart: the JSON in a field
      // named request, each image in a file part that assets name.
      const fields = ctx.body.fields.request;
      if (!fields || fields.length !== 1) throw new Error("A multipart request needs one request field with the JSON request");
      try {
        value = JSON.parse(fields[0]);
      } catch (error) {
        throw new Error("The request field must hold the JSON request");
      }
    } else {
      throw new Error("JSON or multipart body required");
    }
    const body = validateRequest(value);
    return {
      kind: "submit",
      model: body.model,
      action: "systemone",
      requestBody: systemOneBody(body, body.model),
    };
  },
  renderSystemOne: function (ctx, task) {
    return task.data;
  },
  error: function (ctx, error) {
    // An error from TypeSafe reaches the client in TypeSafe's own shape.
    try {
      const body = JSON.parse(error.message);
      if (isObject(body) && body.detail !== undefined) return body;
    } catch (parseError) {
      // Gateway errors are sentences, not TypeSafe JSON.
    }
    return { detail: error.message };
  },
};
