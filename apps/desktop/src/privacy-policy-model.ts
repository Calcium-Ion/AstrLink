export type PrivacyDetector = "regex" | "local_model";
export type PrivacyAction = "allow" | "warn" | "block" | "redact";
export type PrivacyModelAdapter =
  | "openai_bioes_viterbi"
  | "hf_token_classification"
  | "astrlink_sensitive_guard";
export type CanonicalPrivacyKind =
  | "email"
  | "phone"
  | "account"
  | "payment_card"
  | "ip_address"
  | "url"
  | "common_secret"
  | "private_address"
  | "private_date"
  | "private_person";
export type PrivacyModelInstallationPhase =
  | "downloading"
  | "ready"
  | "error";
export type PrivacyModelInstallationError =
  | "download_failed"
  | "integrity_failed"
  | "incompatible_model";

export type PrivacyPolicyMatch = Record<string, never>;
export type PrivacyLabelMapping = Record<
  string,
  CanonicalPrivacyKind | null
>;

export interface PrivacyPolicy {
  id: string;
  name: string;
  enabled: boolean;
  priority: number;
  detector: PrivacyDetector;
  local_model_id: string | null;
  min_confidence: number;
  request_action: PrivacyAction;
  response_action: PrivacyAction;
  response_restore: boolean;
  match: PrivacyPolicyMatch;
}

export interface PrivacyPolicyPage {
  items: PrivacyPolicy[];
  next_cursor: null;
}

export interface PrivacyPolicyRecord {
  policy: PrivacyPolicy;
  etag: string;
}

export type PrivacyPolicyPatch = Partial<
  Pick<
    PrivacyPolicy,
    | "enabled"
    | "detector"
    | "local_model_id"
    | "min_confidence"
    | "request_action"
    | "response_restore"
  >
>;

export type PrivacyDryRunProtocol =
  | "openai.chat"
  | "openai.completions"
  | "openai.responses"
  | "openai.responses.compact"
  | "anthropic.messages"
  | "google.generate_content";

/** Matches core/contract.MaxPolicyDryRunSampleBytes (UTF-8 byte length). */
export const MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES = 256 * 1024;
const MAX_PRIVACY_DRY_RUN_BODY_CHARS = 512 * 1024;

export interface PrivacyDryRunInput {
  protocol: PrivacyDryRunProtocol;
  sample_text: string;
  policy?: PrivacyPolicyPatch;
}

export interface PrivacyDryRunFinding {
  kind: CanonicalPrivacyKind;
  path: string;
  start: number;
  end: number;
  confidence: number;
}

export interface PrivacyDryRunRedaction {
  placeholder: string;
  kind: CanonicalPrivacyKind;
  value: string;
}

export interface PrivacyDryRunResult {
  decision: PrivacyAction;
  findings_summary: string;
  findings: PrivacyDryRunFinding[];
  suppressed_findings: PrivacyDryRunFinding[];
  redactions?: PrivacyDryRunRedaction[];
  redacted_body?: string;
  inspected_body: string;
}

export interface PrivacyModelVariant {
  id: string;
  name: string;
  quantization: string;
  bytes_total: number;
  estimated_ram_bytes: number;
  recommended: boolean;
  supported: boolean;
  unsupported_reason: null | "cpu_only";
}

export interface PrivacyCatalogModel {
  id: string;
  name: string;
  summary: string;
  source: "official" | "community";
  repo_id: string;
  revision: string;
  license: string;
  languages: string[];
  adapter: PrivacyModelAdapter;
  variants: PrivacyModelVariant[];
}

export interface PrivacyModelCatalog {
  items: PrivacyCatalogModel[];
}

export interface PrivacyModelProbeLabel {
  label: string;
  suggested_kind: CanonicalPrivacyKind | null;
}

export interface PrivacyModelProbe {
  repo_id: string;
  requested_revision: string;
  revision: string;
  name: string;
  license: string | null;
  languages: string[];
  adapter: PrivacyModelAdapter;
  variants: PrivacyModelVariant[];
  labels: PrivacyModelProbeLabel[];
  requires_label_mapping: boolean;
}

export interface PrivacyModelProbeInput {
  repo_id: string;
  revision: string;
}

export interface LocalProbeInput {
  path: string;
}

export interface PrivacyModelInstallInput {
  repo_id: string;
  revision: string;
  variant_id: string;
  label_mapping: PrivacyLabelMapping;
}

export interface PrivacyModelInstallation {
  id: string;
  source: "catalog" | "custom" | "local";
  catalog_id: string | null;
  catalog_source: "official" | "community" | null;
  name: string;
  license: string | null;
  languages: string[];
  repo_id: string;
  revision: string;
  variant_id: string;
  variant_name: string;
  quantization: string;
  adapter: PrivacyModelAdapter;
  status: PrivacyModelInstallationPhase;
  bytes_downloaded: number;
  bytes_total: number;
  estimated_ram_bytes: number;
  error: PrivacyModelInstallationError | null;
  label_mapping: PrivacyLabelMapping;
  installed_at: string | null;
}

export interface PrivacyModelInstallationList {
  items: PrivacyModelInstallation[];
}

type JsonObject = Record<string, unknown>;

const resourceIDPattern = /^[a-z][a-z0-9_-]{2,95}$/;
const catalogIDPattern = /^catalog_[a-z0-9_]{3,80}$/;
const installationIDPattern = /^model_[0-9a-f]{32}$/;
const repoIDPattern =
  /^[A-Za-z0-9][A-Za-z0-9._-]{0,95}\/[A-Za-z0-9][A-Za-z0-9._-]{0,95}$/;
const revisionPattern = /^[0-9a-f]{40}$/;
const requestedRevisionPattern = /^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$/;
const variantIDPattern = /^[a-z][a-z0-9_]{1,63}$/;
const tokenPattern = /^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$/;
const labelPattern = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;
const etagPattern = /^"sha256:[0-9a-f]{64}"$/;
const detectors = new Set<PrivacyDetector>(["regex", "local_model"]);
const actions = new Set<PrivacyAction>([
  "allow",
  "warn",
  "block",
  "redact",
]);
const adapters = new Set<PrivacyModelAdapter>([
  "openai_bioes_viterbi",
  "hf_token_classification",
  "astrlink_sensitive_guard",
]);
const canonicalKinds = new Set<CanonicalPrivacyKind>([
  "email",
  "phone",
  "account",
  "payment_card",
  "ip_address",
  "url",
  "common_secret",
  "private_address",
  "private_date",
  "private_person",
]);
const dryRunProtocols = new Set<PrivacyDryRunProtocol>([
  "openai.chat",
  "openai.completions",
  "openai.responses",
  "openai.responses.compact",
  "anthropic.messages",
  "google.generate_content",
]);
const installationPhases = new Set<PrivacyModelInstallationPhase>([
  "downloading",
  "ready",
  "error",
]);
const installationErrors = new Set<PrivacyModelInstallationError>([
  "download_failed",
  "integrity_failed",
  "incompatible_model",
]);

function invalid(path: string, message: string): never {
  throw new Error(`Invalid privacy-policy IPC response at ${path}: ${message}`);
}

function objectAt(value: unknown, path: string): JsonObject {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return invalid(path, "expected an object");
  }
  return value as JsonObject;
}

function keysAt(
  object: JsonObject,
  required: readonly string[],
  optional: readonly string[],
  path: string,
): void {
  const allowed = new Set([...required, ...optional]);
  for (const key of Object.keys(object)) {
    if (!allowed.has(key)) invalid(`${path}.${key}`, "unexpected field");
  }
  for (const key of required) {
    if (!Object.hasOwn(object, key)) invalid(`${path}.${key}`, "missing field");
  }
}

function stringAt(value: unknown, path: string, min: number, max: number): string {
  if (typeof value !== "string") {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  const length = [...value].length;
  if (length < min || length > max) {
    return invalid(path, `expected ${min} to ${max} characters`);
  }
  return value;
}

function metadataStringAt(
  value: unknown,
  path: string,
  min: number,
  max: number,
): string {
  const text = stringAt(value, path, min, max);
  if (text.trim() !== text || /\p{Cc}/u.test(text)) {
    invalid(path, "must be trimmed and contain no control characters");
  }
  return text;
}

function booleanAt(value: unknown, path: string): boolean {
  if (typeof value !== "boolean") invalid(path, "expected a boolean");
  return value;
}

function safeIntegerAt(
  value: unknown,
  path: string,
  minimum = 0,
): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum) {
    invalid(path, `expected a safe integer greater than or equal to ${minimum}`);
  }
  return value as number;
}

function unitIntervalAt(value: unknown, path: string): number {
  if (
    typeof value !== "number" ||
    !Number.isFinite(value) ||
    value < 0 ||
    value > 1
  ) {
    invalid(path, "expected a number between 0 and 1");
  }
  return value;
}

function rfc3339At(value: unknown, path: string): string {
  const timestamp = stringAt(value, path, 20, 35);
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|([+-])(\d{2}):(\d{2}))$/.exec(
      timestamp,
    );
  if (match === null) invalid(path, "invalid RFC3339 timestamp");
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4]);
  const minute = Number(match[5]);
  const second = Number(match[6]);
  const offsetHour = match[8] === undefined ? 0 : Number(match[8]);
  const offsetMinute = match[9] === undefined ? 0 : Number(match[9]);
  const leapYear =
    year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const days = [
    31,
    leapYear ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];
  if (
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > days[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    offsetHour > 23 ||
    offsetMinute > 59
  ) {
    invalid(path, "invalid RFC3339 timestamp");
  }
  return timestamp;
}

function resourceIDAt(value: unknown, path: string): string {
  const id = stringAt(value, path, 3, 96);
  if (!resourceIDPattern.test(id)) invalid(path, "invalid resource ID");
  return id;
}

function catalogIDAt(value: unknown, path: string): string {
  const id = stringAt(value, path, 11, 88);
  if (!catalogIDPattern.test(id)) invalid(path, "invalid catalog ID");
  return id;
}

function installationIDAt(value: unknown, path: string): string {
  const id = stringAt(value, path, 38, 38);
  if (!installationIDPattern.test(id)) invalid(path, "invalid installation ID");
  return id;
}

function repoIDAt(value: unknown, path: string): string {
  const repoID = stringAt(value, path, 3, 193);
  if (!repoIDPattern.test(repoID) || repoID.includes("..")) {
    invalid(path, "invalid Hugging Face repository ID");
  }
  return repoID;
}

function revisionAt(value: unknown, path: string): string {
  const revision = stringAt(value, path, 1, 128);
  if (!revisionPattern.test(revision)) {
    invalid(path, "expected a 40-character lowercase commit");
  }
  return revision;
}

function tokenAt(value: unknown, path: string): string {
  const token = stringAt(value, path, 1, 96);
  if (!tokenPattern.test(token)) invalid(path, "invalid identifier");
  return token;
}

function variantIDAt(value: unknown, path: string): string {
  const id = stringAt(value, path, 2, 64);
  if (!variantIDPattern.test(id)) invalid(path, "invalid variant ID");
  return id;
}

function requestedRevisionAt(value: unknown, path: string): string {
  const revision = stringAt(value, path, 1, 128);
  if (
    !requestedRevisionPattern.test(revision) ||
    revision.includes("..") ||
    revision.includes("//") ||
    revision.endsWith("/")
  ) {
    invalid(path, "invalid requested revision");
  }
  return revision;
}

function adapterAt(value: unknown, path: string): PrivacyModelAdapter {
  if (
    typeof value !== "string" ||
    !adapters.has(value as PrivacyModelAdapter)
  ) {
    invalid(path, "unknown model adapter");
  }
  return value as PrivacyModelAdapter;
}

function canonicalKindAt(
  value: unknown,
  path: string,
): CanonicalPrivacyKind {
  if (
    typeof value !== "string" ||
    !canonicalKinds.has(value as CanonicalPrivacyKind)
  ) {
    invalid(path, "unknown canonical privacy kind");
  }
  return value as CanonicalPrivacyKind;
}

function stringArrayAt(
  value: unknown,
  path: string,
  maximumItems: number,
): string[] {
  if (!Array.isArray(value) || value.length > maximumItems) {
    invalid(path, `expected an array of at most ${maximumItems} strings`);
  }
  const parsed = value.map((item, index) =>
    metadataStringAt(item, `${path}[${index}]`, 1, 64),
  );
  if (new Set(parsed).size !== parsed.length) {
    invalid(path, "duplicate value");
  }
  return parsed;
}

function parseMatch(value: unknown, path: string): PrivacyPolicyMatch {
  const match = objectAt(value, path);
  keysAt(match, [], [], path);
  return {};
}

function parseLabelMapping(
  value: unknown,
  path: string,
): PrivacyLabelMapping {
  const mapping = objectAt(value, path);
  if (Object.keys(mapping).length > 256) {
    invalid(path, "too many label mappings");
  }
  const parsed: PrivacyLabelMapping = {};
  for (const [label, kind] of Object.entries(mapping)) {
    if (!labelPattern.test(label)) {
      invalid(`${path} key`, "invalid model label");
    }
    parsed[label] =
      kind === null ? null : canonicalKindAt(kind, `${path}.${label}`);
  }
  return parsed;
}

function parseVariant(value: unknown, path: string): PrivacyModelVariant {
  const variant = objectAt(value, path);
  keysAt(
    variant,
    [
      "id",
      "name",
      "quantization",
      "bytes_total",
      "estimated_ram_bytes",
      "recommended",
      "supported",
      "unsupported_reason",
    ],
    [],
    path,
  );
  const supported = booleanAt(variant.supported, `${path}.supported`);
  const unsupportedReason =
    variant.unsupported_reason === null
      ? null
      : variant.unsupported_reason === "cpu_only"
        ? "cpu_only"
        : invalid(`${path}.unsupported_reason`, "unknown unsupported reason");
  if (supported !== (unsupportedReason === null)) {
    invalid(path, "supported state and unsupported reason are inconsistent");
  }
  const bytesTotal = safeIntegerAt(
    variant.bytes_total,
    `${path}.bytes_total`,
  );
  if (supported && bytesTotal === 0) {
    invalid(`${path}.bytes_total`, "supported variants must have content");
  }
  return {
    id: variantIDAt(variant.id, `${path}.id`),
    name: metadataStringAt(variant.name, `${path}.name`, 1, 64),
    quantization: metadataStringAt(
      variant.quantization,
      `${path}.quantization`,
      1,
      32,
    ),
    bytes_total: bytesTotal,
    estimated_ram_bytes: safeIntegerAt(
      variant.estimated_ram_bytes,
      `${path}.estimated_ram_bytes`,
    ),
    recommended: booleanAt(variant.recommended, `${path}.recommended`),
    supported,
    unsupported_reason: unsupportedReason,
  };
}

function parseVariants(value: unknown, path: string): PrivacyModelVariant[] {
  if (!Array.isArray(value) || value.length === 0 || value.length > 32) {
    invalid(path, "expected 1 to 32 variants");
  }
  const variants = value.map((variant, index) =>
    parseVariant(variant, `${path}[${index}]`),
  );
  if (new Set(variants.map((variant) => variant.id)).size !== variants.length) {
    invalid(path, "duplicate variant ID");
  }
  return variants;
}

export function parsePrivacyPolicy(
  value: unknown,
  path = "$",
): PrivacyPolicy {
  const policy = objectAt(value, path);
  keysAt(
    policy,
    [
      "id",
      "name",
      "enabled",
      "priority",
      "detector",
      "local_model_id",
      "min_confidence",
      "request_action",
      "response_action",
      "response_restore",
      "match",
    ],
    [],
    path,
  );
  const id = resourceIDAt(policy.id, `${path}.id`);
  const name = stringAt(policy.name, `${path}.name`, 1, 128);
  if (id !== "policy_privacy_default") {
    invalid(`${path}.id`, "unexpected singleton policy ID");
  }
  if (name !== "隐私保护") {
    invalid(`${path}.name`, "unexpected singleton policy name");
  }
  const priority = safeIntegerAt(policy.priority, `${path}.priority`);
  if (priority !== 0) {
    invalid(`${path}.priority`, "unexpected singleton policy priority");
  }
  if (
    typeof policy.detector !== "string" ||
    !detectors.has(policy.detector as PrivacyDetector)
  ) {
    invalid(`${path}.detector`, "unknown detector");
  }
  if (
    typeof policy.request_action !== "string" ||
    !actions.has(policy.request_action as PrivacyAction)
  ) {
    invalid(`${path}.request_action`, "unknown action");
  }
  if (
    typeof policy.response_action !== "string" ||
    !actions.has(policy.response_action as PrivacyAction)
  ) {
    invalid(`${path}.response_action`, "unknown action");
  }
  if (policy.response_action !== "allow") {
    invalid(
      `${path}.response_action`,
      "response inspection is not enabled in this version",
    );
  }
  const detector = policy.detector as PrivacyDetector;
  const localModelID =
    policy.local_model_id === null
      ? null
      : installationIDAt(policy.local_model_id, `${path}.local_model_id`);
  if (
    (detector === "regex" && localModelID !== null) ||
    (detector === "local_model" && localModelID === null)
  ) {
    invalid(path, "detector and local model selection are inconsistent");
  }
  return {
    id,
    name,
    enabled: booleanAt(policy.enabled, `${path}.enabled`),
    priority,
    detector,
    local_model_id: localModelID,
    min_confidence: unitIntervalAt(
      policy.min_confidence,
      `${path}.min_confidence`,
    ),
    request_action: policy.request_action as PrivacyAction,
    response_action: policy.response_action as PrivacyAction,
    response_restore: booleanAt(policy.response_restore, `${path}.response_restore`),
    match: parseMatch(policy.match, `${path}.match`),
  };
}

export function parsePrivacyPolicyPage(value: unknown): PrivacyPolicyPage {
  const page = objectAt(value, "$");
  keysAt(page, ["items", "next_cursor"], [], "$");
  if (!Array.isArray(page.items)) invalid("$.items", "expected an array");
  if (page.items.length !== 1) invalid("$.items", "expected singleton policy");
  if (page.next_cursor !== null) invalid("$.next_cursor", "expected null");
  return {
    items: page.items.map((policy, index) =>
      parsePrivacyPolicy(policy, `$.items[${index}]`),
    ),
    next_cursor: null,
  };
}

export function parsePrivacyPolicyRecord(
  value: unknown,
): PrivacyPolicyRecord {
  const record = objectAt(value, "$");
  keysAt(record, ["policy", "etag"], [], "$");
  const etag = stringAt(record.etag, "$.etag", 3, 128);
  if (!etagPattern.test(etag)) invalid("$.etag", "invalid strong entity tag");
  return {
    policy: parsePrivacyPolicy(record.policy, "$.policy"),
    etag,
  };
}

function parsePrivacyDryRunFinding(
  value: unknown,
  path: string,
): PrivacyDryRunFinding {
  const finding = objectAt(value, path);
  keysAt(
    finding,
    ["kind", "path", "start", "end", "confidence"],
    [],
    path,
  );
  if (
    typeof finding.kind !== "string" ||
    !canonicalKinds.has(finding.kind as CanonicalPrivacyKind)
  ) {
    invalid(`${path}.kind`, "unknown privacy kind");
  }
  const start = safeIntegerAt(finding.start, `${path}.start`);
  const end = safeIntegerAt(finding.end, `${path}.end`, 1);
  if (end <= start) {
    invalid(path, "end must be greater than start");
  }
  return {
    kind: finding.kind as CanonicalPrivacyKind,
    path: stringAt(finding.path, `${path}.path`, 1, 512),
    start,
    end,
    confidence: unitIntervalAt(finding.confidence, `${path}.confidence`),
  };
}

export function parsePrivacyDryRunResult(
  value: unknown,
): PrivacyDryRunResult {
  const result = objectAt(value, "$");
  keysAt(
    result,
    [
      "decision",
      "findings_summary",
      "findings",
      "suppressed_findings",
      "inspected_body",
    ],
    ["redacted_body", "redactions"],
    "$",
  );
  if (
    typeof result.decision !== "string" ||
    !actions.has(result.decision as PrivacyAction)
  ) {
    invalid("$.decision", "unknown action");
  }
  if (!Array.isArray(result.findings) || result.findings.length > 4_096) {
    invalid("$.findings", "expected at most 4096 findings");
  }
  const findings = result.findings.map((finding, index) =>
    parsePrivacyDryRunFinding(finding, `$.findings[${index}]`),
  );
  if (
    !Array.isArray(result.suppressed_findings) ||
    result.suppressed_findings.length > 4_096
  ) {
    invalid("$.suppressed_findings", "expected at most 4096 findings");
  }
  const suppressedFindings = result.suppressed_findings.map(
    (finding, index) =>
      parsePrivacyDryRunFinding(
        finding,
        `$.suppressed_findings[${index}]`,
      ),
  );
  const parsed: PrivacyDryRunResult = {
    decision: result.decision as PrivacyAction,
    findings_summary: stringAt(result.findings_summary, "$.findings_summary", 0, 4_096),
    findings,
    suppressed_findings: suppressedFindings,
    inspected_body: stringAt(
      result.inspected_body,
      "$.inspected_body",
      2,
      MAX_PRIVACY_DRY_RUN_BODY_CHARS,
    ),
  };
  if (Object.hasOwn(result, "redactions")) {
    if (!Array.isArray(result.redactions) || result.redactions.length > 4_096) {
      invalid("$.redactions", "expected at most 4096 redactions");
    }
    parsed.redactions = result.redactions.map((item, index) => {
      const path = `$.redactions[${index}]`;
      const redaction = objectAt(item, path);
      keysAt(redaction, ["placeholder", "kind", "value"], [], path);
      if (
        typeof redaction.kind !== "string" ||
        !canonicalKinds.has(redaction.kind as CanonicalPrivacyKind)
      ) {
        invalid(`${path}.kind`, "unknown privacy kind");
      }
      return {
        placeholder: stringAt(redaction.placeholder, `${path}.placeholder`, 1, 128),
        kind: redaction.kind as CanonicalPrivacyKind,
        value: stringAt(redaction.value, `${path}.value`, 0, MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES),
      };
    });
  }
  if (Object.hasOwn(result, "redacted_body")) {
    if (typeof result.redacted_body !== "string") {
      invalid("$.redacted_body", "expected a string");
    }
    parsed.redacted_body = stringAt(
      result.redacted_body,
      "$.redacted_body",
      2,
      MAX_PRIVACY_DRY_RUN_BODY_CHARS,
    );
  }
  return parsed;
}

export function utf8ByteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

export function validatePrivacyDryRunInput(
  input: PrivacyDryRunInput,
): PrivacyDryRunInput {
  if (
    typeof input.protocol !== "string" ||
    !dryRunProtocols.has(input.protocol)
  ) {
    invalid("$.protocol", "unsupported dry-run protocol");
  }
  if (typeof input.sample_text !== "string") {
    throw new Error("样例文本无效。");
  }
  const sample = input.sample_text;
  if (sample.length === 0) {
    throw new Error("请输入用于试运行的样例文本。");
  }
  const bytes = utf8ByteLength(sample);
  if (bytes > MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES) {
    throw new Error(
      `样例过长（${bytes} 字节，上限 ${MAX_PRIVACY_DRY_RUN_SAMPLE_BYTES} 字节 / 256 KiB），请缩短后再试。`,
    );
  }
  const validated: PrivacyDryRunInput = {
    protocol: input.protocol,
    sample_text: sample,
  };
  if (input.policy !== undefined) {
    validated.policy = validatePrivacyPolicyPatch(input.policy);
  }
  return validated;
}

function validatePrivacyPolicyPatch(
  patch: PrivacyPolicyPatch,
): PrivacyPolicyPatch {
  const object = objectAt(patch, "$.policy");
  keysAt(
    object,
    [],
    [
      "enabled",
      "detector",
      "local_model_id",
      "min_confidence",
      "request_action",
      "response_restore",
    ],
    "$.policy",
  );
  if (Object.keys(object).length === 0) {
    invalid("$.policy", "must change at least one field");
  }
  const validated: PrivacyPolicyPatch = {};
  if (Object.hasOwn(object, "enabled")) {
    validated.enabled = booleanAt(object.enabled, "$.policy.enabled");
  }
  if (Object.hasOwn(object, "detector")) {
    if (
      typeof object.detector !== "string" ||
      !detectors.has(object.detector as PrivacyDetector)
    ) {
      invalid("$.policy.detector", "unknown detector");
    }
    validated.detector = object.detector as PrivacyDetector;
  }
  if (Object.hasOwn(object, "local_model_id")) {
    validated.local_model_id =
      object.local_model_id === null
        ? null
        : installationIDAt(object.local_model_id, "$.policy.local_model_id");
  }
  if (Object.hasOwn(object, "min_confidence")) {
    validated.min_confidence = unitIntervalAt(
      object.min_confidence,
      "$.policy.min_confidence",
    );
  }
  if (Object.hasOwn(object, "request_action")) {
    if (
      typeof object.request_action !== "string" ||
      !actions.has(object.request_action as PrivacyAction)
    ) {
      invalid("$.policy.request_action", "unknown action");
    }
    validated.request_action = object.request_action as PrivacyAction;
  }
  if (Object.hasOwn(object, "response_restore")) {
    validated.response_restore = booleanAt(
      object.response_restore,
      "$.policy.response_restore",
    );
  }
  return validated;
}

export function parsePrivacyModelCatalog(
  value: unknown,
): PrivacyModelCatalog {
  const catalog = objectAt(value, "$");
  keysAt(catalog, ["items"], [], "$");
  if (!Array.isArray(catalog.items) || catalog.items.length > 100) {
    invalid("$.items", "expected at most 100 catalog models");
  }
  const items = catalog.items.map((item, index) => {
    const path = `$.items[${index}]`;
    const model = objectAt(item, path);
    keysAt(
      model,
      [
        "id",
        "name",
        "summary",
        "source",
        "repo_id",
        "revision",
        "license",
        "languages",
        "adapter",
        "variants",
      ],
      [],
      path,
    );
    if (model.source !== "official" && model.source !== "community") {
      invalid(`${path}.source`, "unknown catalog source");
    }
    return {
      id: catalogIDAt(model.id, `${path}.id`),
      name: metadataStringAt(model.name, `${path}.name`, 1, 128),
      summary: metadataStringAt(model.summary, `${path}.summary`, 1, 512),
      source: model.source,
      repo_id: repoIDAt(model.repo_id, `${path}.repo_id`),
      revision: revisionAt(model.revision, `${path}.revision`),
      license: metadataStringAt(model.license, `${path}.license`, 1, 64),
      languages: stringArrayAt(model.languages, `${path}.languages`, 32),
      adapter: adapterAt(model.adapter, `${path}.adapter`),
      variants: parseVariants(model.variants, `${path}.variants`),
    } satisfies PrivacyCatalogModel;
  });
  if (new Set(items.map((item) => item.id)).size !== items.length) {
    invalid("$.items", "duplicate catalog model ID");
  }
  return { items };
}

export function parsePrivacyModelProbe(value: unknown): PrivacyModelProbe {
  const probe = objectAt(value, "$");
  keysAt(
    probe,
    [
      "repo_id",
      "requested_revision",
      "revision",
      "name",
      "license",
      "languages",
      "adapter",
      "variants",
      "labels",
      "requires_label_mapping",
    ],
    [],
    "$",
  );
  if (
    !Array.isArray(probe.labels) ||
    probe.labels.length === 0 ||
    probe.labels.length > 256
  ) {
    invalid("$.labels", "expected 1 to 256 labels");
  }
  const labels = probe.labels.map((item, index) => {
    const path = `$.labels[${index}]`;
    const label = objectAt(item, path);
    keysAt(label, ["label", "suggested_kind"], [], path);
    return {
      label: (() => {
        const sourceLabel = stringAt(label.label, `${path}.label`, 1, 128);
        if (!labelPattern.test(sourceLabel)) {
          invalid(`${path}.label`, "invalid model label");
        }
        return sourceLabel;
      })(),
      suggested_kind:
        label.suggested_kind === null
          ? null
          : canonicalKindAt(label.suggested_kind, `${path}.suggested_kind`),
    };
  });
  if (new Set(labels.map((item) => item.label)).size !== labels.length) {
    invalid("$.labels", "duplicate model label");
  }
  const requiresLabelMapping = booleanAt(
    probe.requires_label_mapping,
    "$.requires_label_mapping",
  );
  if (
    requiresLabelMapping !==
    labels.some((label) => label.suggested_kind === null)
  ) {
    invalid(
      "$.requires_label_mapping",
      "inconsistent with label suggestions",
    );
  }
  return {
    repo_id: repoIDAt(probe.repo_id, "$.repo_id"),
    requested_revision: requestedRevisionAt(
      probe.requested_revision,
      "$.requested_revision",
    ),
    revision: revisionAt(probe.revision, "$.revision"),
    name: metadataStringAt(probe.name, "$.name", 1, 128),
    license:
      probe.license === null
        ? null
        : metadataStringAt(probe.license, "$.license", 1, 64),
    languages: stringArrayAt(probe.languages, "$.languages", 32),
    adapter: adapterAt(probe.adapter, "$.adapter"),
    variants: parseVariants(probe.variants, "$.variants"),
    labels,
    requires_label_mapping: requiresLabelMapping,
  };
}

export function parsePrivacyModelInstallation(
  value: unknown,
  path = "$",
): PrivacyModelInstallation {
  const installation = objectAt(value, path);
  keysAt(
    installation,
    [
      "id",
      "source",
      "catalog_id",
      "catalog_source",
      "name",
      "license",
      "languages",
      "repo_id",
      "revision",
      "variant_id",
      "variant_name",
      "quantization",
      "adapter",
      "status",
      "bytes_downloaded",
      "bytes_total",
      "estimated_ram_bytes",
      "error",
      "label_mapping",
      "installed_at",
    ],
    [],
    path,
  );
  if (
    installation.source !== "catalog" &&
    installation.source !== "custom" &&
    installation.source !== "local"
  ) {
    invalid(`${path}.source`, "unknown installation source");
  }
  if (
    typeof installation.status !== "string" ||
    !installationPhases.has(
      installation.status as PrivacyModelInstallationPhase,
    )
  ) {
    invalid(`${path}.status`, "unknown installation status");
  }
  const catalogID =
    installation.catalog_id === null
      ? null
      : catalogIDAt(installation.catalog_id, `${path}.catalog_id`);
  const catalogSource =
    installation.catalog_source === null
      ? null
      : installation.catalog_source === "official" ||
          installation.catalog_source === "community"
        ? installation.catalog_source
        : invalid(`${path}.catalog_source`, "unknown catalog source");
  const repoID = repoIDAt(installation.repo_id, `${path}.repo_id`);
  if (
    (installation.source === "catalog") !== (catalogID !== null) ||
    (installation.source === "catalog") !== (catalogSource !== null)
  ) {
    invalid(path, "installation catalog provenance is inconsistent");
  }
  const localRepoID = /^local\/model-[0-9a-f]{12}$/.test(repoID);
  if ((installation.source === "local") !== localRepoID) {
    invalid(path, "installation local provenance is inconsistent");
  }
  const downloaded = safeIntegerAt(
    installation.bytes_downloaded,
    `${path}.bytes_downloaded`,
  );
  const total = safeIntegerAt(
    installation.bytes_total,
    `${path}.bytes_total`,
  );
  if (downloaded > total) invalid(path, "download progress is inconsistent");
  const error =
    installation.error === null
      ? null
      : typeof installation.error === "string" &&
          installationErrors.has(
            installation.error as PrivacyModelInstallationError,
          )
        ? (installation.error as PrivacyModelInstallationError)
        : invalid(`${path}.error`, "unknown installation error");
  const installedAt =
    installation.installed_at === null
      ? null
      : rfc3339At(installation.installed_at, `${path}.installed_at`);
  const status = installation.status as PrivacyModelInstallationPhase;
  if (
    (status === "ready" &&
      (total <= 0 ||
        downloaded !== total ||
        error !== null ||
        installedAt === null)) ||
    (status === "downloading" && (error !== null || installedAt !== null)) ||
    (status === "error" && (error === null || installedAt !== null))
  ) {
    invalid(path, "installation lifecycle fields are inconsistent");
  }
  return {
    id: installationIDAt(installation.id, `${path}.id`),
    source: installation.source,
    catalog_id: catalogID,
    catalog_source: catalogSource,
    name: metadataStringAt(installation.name, `${path}.name`, 1, 128),
    license:
      installation.license === null
        ? null
        : metadataStringAt(
            installation.license,
            `${path}.license`,
            1,
            64,
          ),
    languages: stringArrayAt(
      installation.languages,
      `${path}.languages`,
      32,
    ),
    repo_id: repoID,
    revision: revisionAt(installation.revision, `${path}.revision`),
    variant_id: variantIDAt(
      installation.variant_id,
      `${path}.variant_id`,
    ),
    variant_name: metadataStringAt(
      installation.variant_name,
      `${path}.variant_name`,
      1,
      64,
    ),
    quantization: metadataStringAt(
      installation.quantization,
      `${path}.quantization`,
      1,
      32,
    ),
    adapter: adapterAt(installation.adapter, `${path}.adapter`),
    status,
    bytes_downloaded: downloaded,
    bytes_total: total,
    estimated_ram_bytes: safeIntegerAt(
      installation.estimated_ram_bytes,
      `${path}.estimated_ram_bytes`,
    ),
    error,
    label_mapping: parseLabelMapping(
      installation.label_mapping,
      `${path}.label_mapping`,
    ),
    installed_at: installedAt,
  };
}

export function parsePrivacyModelInstallationList(
  value: unknown,
): PrivacyModelInstallationList {
  const list = objectAt(value, "$");
  keysAt(list, ["items"], [], "$");
  if (!Array.isArray(list.items) || list.items.length > 100) {
    invalid("$.items", "expected at most 100 installations");
  }
  const items = list.items.map((item, index) =>
    parsePrivacyModelInstallation(item, `$.items[${index}]`),
  );
  if (new Set(items.map((item) => item.id)).size !== items.length) {
    invalid("$.items", "duplicate installation ID");
  }
  return { items };
}

export function validatePrivacyModelProbeInput(
  input: PrivacyModelProbeInput,
): PrivacyModelProbeInput {
  return {
    repo_id: repoIDAt(input.repo_id, "$.repo_id"),
    revision: requestedRevisionAt(input.revision, "$.revision"),
  };
}

export function validateLocalProbeInput(
  input: LocalProbeInput,
): LocalProbeInput {
  if (typeof input.path !== "string") {
    invalid("$.path", "expected a local model path");
  }
  const path = input.path.trim();
  stringAt(path, "$.path", 1, 4096);
  if (/\p{Cc}/u.test(path)) {
    invalid("$.path", "must contain no control characters");
  }
  const hasURIScheme = /^[A-Za-z][A-Za-z0-9+.-]*:/.test(path);
  const isWindowsDrivePath = /^[A-Za-z]:[\\/]/.test(path);
  if (hasURIScheme && !isWindowsDrivePath) {
    invalid("$.path", "expected a local path, not a URI");
  }
  return { path };
}

export function validatePrivacyModelInstallInput(
  input: PrivacyModelInstallInput,
): PrivacyModelInstallInput {
  return {
    repo_id: repoIDAt(input.repo_id, "$.repo_id"),
    revision: revisionAt(input.revision, "$.revision"),
    variant_id: variantIDAt(input.variant_id, "$.variant_id"),
    label_mapping: parseLabelMapping(input.label_mapping, "$.label_mapping"),
  };
}

export function validatePrivacyModelInstallationID(
  installationID: string,
): string {
  return installationIDAt(installationID, "$.installation_id");
}

export function isResourceHeavyVariant(
  variant: Pick<
    PrivacyModelVariant,
    "bytes_total" | "estimated_ram_bytes"
  >,
): boolean {
  return (
    variant.bytes_total >= 1024 ** 3 ||
    variant.estimated_ram_bytes >= 2 * 1024 ** 3
  );
}
