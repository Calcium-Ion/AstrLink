# frozen_string_literal: true

require "json"
require "pathname"
require "yaml"

ROOT = Pathname.new(__dir__).realpath
OPENAPI_PATH = ROOT.join("control-api.openapi.yaml")
SCHEMA_PATH = ROOT.join("protocol-capabilities.schema.json")
FIXTURE_PATH = ROOT.join("examples/capabilities.alpha.json")
CREDENTIAL_REF_FIXTURE_PATH = ROOT.join("examples/credential-refs.v1.json")

def load_document(path)
  case path.extname
  when ".json"
    JSON.parse(path.read)
  when ".yaml", ".yml"
    YAML.load_file(path.to_s)
  else
    raise "unsupported contract document: #{path}"
  end
end

def each_ref(value, &block)
  case value
  when Hash
    value.each do |key, child|
      block.call(child) if key == "$ref"
      each_ref(child, &block)
    end
  when Array
    value.each { |child| each_ref(child, &block) }
  end
end

def resolve_pointer(document, fragment, source)
  return document if fragment.empty?
  raise "invalid JSON pointer in #{source}: ##{fragment}" unless fragment.start_with?("/")

  fragment.split("/").drop(1).reduce(document) do |current, raw_token|
    token = raw_token.gsub("~1", "/").gsub("~0", "~")
    case current
    when Hash
      raise "unresolved $ref token #{token.inspect} in #{source}" unless current.key?(token)
      current.fetch(token)
    when Array
      index = Integer(token, 10)
      raise "array $ref index #{index} out of bounds in #{source}" unless index.between?(0, current.length - 1)
      current.fetch(index)
    else
      raise "$ref descends through a scalar at #{token.inspect} in #{source}"
    end
  end
end

documents = {
  OPENAPI_PATH => load_document(OPENAPI_PATH),
  SCHEMA_PATH => load_document(SCHEMA_PATH)
}
reference_count = 0

documents.keys.each do |source_path|
  document = documents.fetch(source_path)
  each_ref(document) do |reference|
    raise "non-string $ref in #{source_path}" unless reference.is_a?(String)
    file_part, separator, fragment = reference.partition("#")
    raise "remote $ref is not frozen locally: #{reference}" if file_part.match?(%r{\Ahttps?://})

    target_path = if file_part.empty?
                    source_path
                  else
                    source_path.dirname.join(file_part).cleanpath.realpath
                  end
    documents[target_path] ||= load_document(target_path)
    resolve_pointer(documents.fetch(target_path), separator.empty? ? "" : fragment, reference)
    reference_count += 1
  end
end

raise "contracts unexpectedly contain no $ref values" if reference_count.zero?

openapi = documents.fetch(OPENAPI_PATH)
schema = documents.fetch(SCHEMA_PATH)
fixture = load_document(FIXTURE_PATH)
credential_ref_fixture = load_document(CREDENTIAL_REF_FIXTURE_PATH)
openapi_example = openapi.dig(
  "paths", "/control/v1/capabilities", "get", "responses", "200",
  "content", "application/json", "example"
)
raise "OpenAPI capability example differs from frozen fixture" unless openapi_example == fixture

expected_protocols = %w[
  openai.responses
  openai.responses.compact
  anthropic.messages
  google.generate_content
  openai.chat
  openai.completions
  openai.models
  google.models
  openai.embeddings
  google.embeddings
  rerank
]
actual_protocols = fixture.fetch("protocols").map { |protocol| protocol.fetch("id") }
raise "Alpha protocol registry drifted" unless actual_protocols == expected_protocols

plans = fixture.fetch("plan_types").to_h { |plan| [plan.fetch("id"), plan] }
raise "native must be protocol-preserving in Alpha" unless plans.dig("native", "available_in_alpha") && !plans.dig("native", "uses_local_conversion")
raise "delegated must be protocol-preserving in Alpha" unless plans.dig("delegated", "available_in_alpha") && !plans.dig("delegated", "uses_local_conversion")
raise "RelayKit must remain unavailable in Alpha" unless plans.dig("relaykit", "available_in_alpha") == false && plans.dig("relaykit", "uses_local_conversion")

engine = fixture.fetch("conversion_engine")
raise "Alpha conversion engine must be unavailable" unless engine == {
  "name" => "relaykit", "version" => nil, "available" => false, "edges" => []
}

ready_schema = openapi.dig("components", "schemas", "ReadyEvent", "properties")
%w[inference_url control_url].each do |field|
  pattern = Regexp.new(ready_schema.fetch(field).fetch("pattern"))
  %w[http://127.0.0.1:1 http://127.0.0.1:65535].each do |url|
    raise "#{field} rejects valid loopback URL #{url}" unless pattern.match?(url)
  end
  %w[
    http://127.0.0.1:0
    http://127.0.0.1:65536
    http://127.0.0.1:99999
    http://localhost:8317
    http://127.0.0.1:8317/
  ].each do |url|
    raise "#{field} accepts invalid loopback URL #{url}" if pattern.match?(url)
  end
  raise "#{field} accepts a trailing newline" if pattern.match?("http://127.0.0.1:8317\n")
end

client_url_pattern = Regexp.new(ready_schema.fetch("client_inference_url").fetch("pattern"))
%w[http://127.0.0.1:1 http://localhost:8317 http://localhost:65535].each do |url|
  raise "client_inference_url rejects valid loopback URL #{url}" unless client_url_pattern.match?(url)
end
%w[
  http://localhost:0
  http://localhost:65536
  http://[::1]:8317
  http://localhost:8317/
  http://localhost.example:8317
].each do |url|
  raise "client_inference_url accepts invalid loopback URL #{url}" if client_url_pattern.match?(url)
end
raise "client_inference_url accepts a trailing newline" if client_url_pattern.match?("http://localhost:8317\n")

audit_properties = schema.dig("$defs", "AuditSettings", "properties")
%w[request_body_enabled response_content_enabled].each do |setting|
  raise "#{setting} must default to false" unless audit_properties.dig(setting, "default") == false
end
unless audit_properties.dig("http_meta_enabled", "default") == true
  raise "http_meta_enabled must default to true (ADR 0008)"
end
unless audit_properties.dig("agent_raw_access_enabled", "default") == true
  raise "agent_raw_access_enabled must default to true; each raw read still needs desktop approval"
end

audit_content = openapi.dig("components", "schemas", "AuditContent")
unless audit_content.fetch("required").include?("http_meta")
  raise "AuditContent must require http_meta so its absence is always explicit null"
end
%w[view privacy_findings].each do |field|
  raise "AuditContent must require #{field}" unless audit_content.fetch("required").include?(field)
end
withheld_part = openapi.dig("components", "schemas", "AuditWithheldPart")
if withheld_part.fetch("properties").key?("content") || withheld_part.fetch("additionalProperties") != false
  raise "AuditWithheldPart must never carry content"
end
raw_sealing_summary = openapi.dig("components", "schemas", "RawSealingSummary")
unless raw_sealing_summary.fetch("properties").keys == %w[raw_available] && raw_sealing_summary.fetch("additionalProperties") == false
  raise "RawSealingSummary is the observer view and must carry only raw_available"
end
policy_summary = openapi.dig("components", "schemas", "PolicySummary", "properties")
if policy_summary.key?("allowlist_rules") || policy_summary.dig("custom_regex_rules", "type") != "object"
  raise "PolicySummary is the observer view and must report allowlist and custom regex rules as counts only"
end
raw_sealing_fields = openapi.dig("components", "schemas", "RawSealingStatusFields", "properties").keys
# The raw password is mandatory on every platform; clients read this to ask for it.
unless openapi.dig("components", "schemas", "RawSealingStatusFields", "required").include?("password_required")
  raise "RawSealingStatus must require password_required"
end
# A public key fingerprint identifies the key without being key material.
leaked_key_fields = raw_sealing_fields.grep(/(?<!pass)key(?!_verified|_fingerprint)|secret|salt|nonce|envelope_/)
raise "RawSealingStatus must not carry key material: #{leaked_key_fields.join(", ")}" unless leaked_key_fields.empty?
# The raw password is the only proof that opens the raw key.
unless openapi.dig("components", "schemas", "RawProof", "properties")&.keys&.sort == %w[kind password]
  raise "RawProof must carry only the raw password"
end
# A wrong password on the desktop's own unlock names no grant or request.
raw_event_required = openapi.dig("components", "schemas", "RawAccessEvent", "required")
raise "RawAccessEvent must not require grant_id or request_id" unless (raw_event_required & %w[grant_id request_id]).empty?
# Raw password and key changes are visible to the operator, whoever made them.
raw_event_kinds = openapi.dig("components", "schemas", "RawAccessEvent", "properties", "kind", "enum")
missing_key_events = %w[raw_password_set raw_password_changed raw_key_reset] - raw_event_kinds
raise "RawAccessEvent must record #{missing_key_events.join(", ")}" unless missing_key_events.empty?
unless openapi.dig("components", "schemas", "ObserversResponse", "required").include?("raw_password_required")
  raise "ObserversResponse must require raw_password_required so the tray can point at the setup"
end
http_meta_choices = audit_content.dig("properties", "http_meta", "oneOf")
unless http_meta_choices.is_a?(Array) && http_meta_choices.include?({ "type" => "null" })
  raise "AuditContent http_meta must be nullable for records predating capture"
end
%w[AuditHTTPMeta AuditHeader].each do |name|
  definition = openapi.dig("components", "schemas", name)
  raise "#{name} must reject unknown properties" unless definition&.fetch("additionalProperties") == false
end

audit_patch = openapi.dig("components", "schemas", "AuditSettingsPatch")
audit_patch_properties = audit_patch.fetch("properties")
raise "AuditSettingsPatch must reject unknown properties" unless audit_patch.fetch("additionalProperties") == false

extensions_choices = audit_patch_properties.dig("extensions", "oneOf")
extensions_ref = "./protocol-capabilities.schema.json#/$defs/Extensions"
unless extensions_choices.is_a?(Array) &&
       extensions_choices.include?({ "$ref" => extensions_ref }) &&
       extensions_choices.include?({ "type" => "null" })
  raise "AuditSettingsPatch extensions must support both Extensions and RFC 7386 null deletion"
end

patch_defaults = audit_patch_properties.each_with_object([]) do |(name, definition), defaults|
  defaults << name if definition.is_a?(Hash) && definition.key?("default")
end
raise "AuditSettingsPatch must not materialize defaults: #{patch_defaults.join(', ')}" unless patch_defaults.empty?

routing_settings = openapi.dig("components", "schemas", "RoutingSettings", "properties")
routing_patch = openapi.dig("components", "schemas", "RoutingSettingsPatch", "properties")
raise "RoutingSettingsPatch must cover every routing setting" unless routing_patch.keys.sort == routing_settings.keys.sort
routing_patch_defaults = routing_patch.select { |_, definition| definition.is_a?(Hash) && definition.key?("default") }.keys
raise "RoutingSettingsPatch must not materialize defaults: #{routing_patch_defaults.join(', ')}" unless routing_patch_defaults.empty?
%w[official_client_passthrough claude_identity_auto_learn codex_identity_auto_learn grok_identity_auto_learn].each do |setting|
  raise "#{setting} must default to true" unless routing_settings.dig(setting, "default") == true
end
identity_version = Regexp.new(openapi.dig("components", "schemas", "ClientIdentityVersion", "pattern"))
%w[2.1.300 0.144.0 2.2.0-beta.1 1.0.0+build.5].each do |version|
  raise "client identity version rejects #{version}" unless identity_version.match?(version)
end
["", "2.1", "v2.1.300", "claude-cli/2.1.300", "2.1.300 (external, cli)", "2.1.300\n"].each do |version|
  raise "client identity version accepts #{version.inspect}" if identity_version.match?(version)
end
%w[claude_identity_version codex_identity_version grok_identity_version].each do |setting|
  choices = routing_patch.dig(setting, "oneOf")
  raise "#{setting} patch must accept an empty string to clear the override" unless choices.is_a?(Array) &&
                                                                                  choices.include?({ "type" => "string", "const" => "" })
end

service = openapi.dig("components", "schemas", "Service")
service_create = openapi.dig("components", "schemas", "ServiceCreate")
expected_service_fields = %w[id name kind enabled models capabilities created_at updated_at]
raise "Service response shape drifted" unless service.fetch("required") == expected_service_fields
raise "Service must expose both connection variants" unless service.fetch("properties").key?("http") &&
                                                       service.fetch("properties").key?("subscription")
raise "disabled_models remains on Service" if service.fetch("properties").key?("disabled_models")

service_patch = openapi.dig("components", "schemas", "ServicePatch")
%w[ServiceCreate ServicePatch].each do |name|
  properties = openapi.dig("components", "schemas", name).fetch("properties")
  if properties.key?("disabled_models")
    raise "#{name} still accepts disabled_models"
  end
end
raise "ServicePatch must keep models patchable" unless service_patch.fetch("properties").key?("models")
raise "ServiceCreate must require name and kind" unless service_create.fetch("required") == %w[name kind]
raise "ServiceCreate must model subscription and HTTP variants" unless service_create.fetch("oneOf").length == 2
raise "legacy lossy auth_scheme field remains" if service_create.fetch("properties").key?("auth_scheme")
raise "unpersisted Service extensions remain" if service_create.fetch("properties").key?("extensions")

service_capability = schema.dig("$defs", "ServiceCapability")
raise "capability-level model filters remain" if service_capability.fetch("properties").key?("models")

http_connection_input = openapi.dig("components", "schemas", "HTTPServiceConnectionInput")
raise "HTTP service input must require auth" unless http_connection_input.fetch("required").include?("auth")

authorization_session = openapi.dig("components", "schemas", "AuthorizationSession")
raise "authorization sessions must be service-scoped" unless authorization_session.fetch("required").include?("service_id") &&
                                                          authorization_session.fetch("properties").key?("service_id") &&
                                                          !authorization_session.fetch("properties").key?("account_id")
raise "authorization sessions must identify their flow" unless authorization_session.fetch("required").include?("flow") &&
                                                          authorization_session.dig("properties", "flow", "$ref") == "#/components/schemas/AuthorizationFlow"
raise "authorization sessions must support Device Code instructions" unless authorization_session.fetch("properties").key?("device_code")
authorization_flow = openapi.dig("components", "schemas", "AuthorizationFlow")
raise "authorization flow choices drifted" unless authorization_flow.fetch("enum") == %w[browser device_code authorization_code]
authorization_start = openapi.dig("components", "schemas", "AuthorizationStartRequest")
raise "authorization start must reject unknown fields" unless authorization_start.fetch("additionalProperties") == false &&
                                                               authorization_start.fetch("required") == ["flow"] &&
                                                               authorization_start.dig("properties", "flow", "$ref") == "#/components/schemas/AuthorizationFlow"

auto_model = openapi.dig("components", "schemas", "AstrLinkAutoModel")
raise "reserved automatic model identifier drifted" unless auto_model.fetch("type") == "string" &&
                                                          auto_model.fetch("const") == "astrlink/auto" &&
                                                          !auto_model.fetch("description").empty?

retired_routing_paths = %w[
  /control/v1/routes
  /control/v1/routes/{route_id}
  /control/v1/recovery-paths
  /control/v1/recovery-paths/{path_id}
  /control/v1/recovery-paths/preview
]
retired_routing_paths.each do |retired_path|
  operations = openapi.dig("paths", retired_path)&.slice("get", "post", "patch", "delete")
  raise "retired routing path missing: #{retired_path}" if operations.nil? || operations.empty?
  operations.each do |method, operation|
    raise "retired routing operation must only answer 410: #{method.upcase} #{retired_path}" unless operation["deprecated"] == true &&
                                                                                                   !operation.key?("requestBody") &&
                                                                                                   operation.fetch("responses").keys.sort == %w[401 410]
  end
end

credential_ref_schema = schema.dig("$defs", "CredentialRef")
service_credential_ref = openapi.dig("components", "schemas", "HTTPServiceConnection", "properties", "credential_ref")
raise "HTTP Service credential_ref must be canonical" unless service_credential_ref.fetch("pattern") ==
                                                         "^local://service/[a-z][a-z0-9_-]{2,95}$"
raise "credential reference contract must preserve v1 versions" unless credential_ref_fixture.values_at("control_api_version", "protocol_contract_version") == %w[v1 v1]

credential_ref_pattern = Regexp.new(credential_ref_schema.fetch("pattern"))
credential_ref_fixture.fetch("accepted").each do |reference|
  raise "CredentialRef rejects accepted fixture #{reference}" unless credential_ref_pattern.match?(reference)
end
credential_ref_fixture.fetch("rejected").each do |reference|
  raise "CredentialRef accepts rejected fixture #{reference}" if credential_ref_pattern.match?(reference)
end

implemented_operations = openapi.dig("x-astrlink-implementation", "implemented_operations")

public_operations = %w[/control/v1/health /control/v1/version /control/v1/capabilities]
# Host addresses tell a client where to reach the gateway from another
# machine; only the operator who exposes the gateway needs them.
operator_reads = %w[
  /control/v1/access-tokens/{token_id}/secret
  /control/v1/services/{service_id}/authorization
  /control/v1/builtin-tools/{kind}/credential
  /control/v1/routing/classifier/credential
  /control/v1/audit/raw-access
  /control/v1/network-addresses
]
# An agent may ask for raw access and give up its own grant; only the
# operator can approve it.
observer_writes = %w[/control/v1/requests/{request_id}/audit/raw-access /control/v1/audit/raw-grant]
openapi.fetch("paths").each do |path, item|
  item.slice("get", "head", "post", "put", "patch", "delete").each do |method, operation|
    role = operation["x-astrlink-role"]
    expected = if public_operations.include?(path)
                 "public"
               elsif observer_writes.include?(path)
                 "observer"
               elsif !%w[get head].include?(method) || operator_reads.include?(path)
                 "operator"
               else
                 "observer"
               end
    raise "#{method.upcase} #{path} must declare x-astrlink-role: #{expected} (got #{role.inspect})" unless role == expected
  end
end
%w[
  GET\ /control/v1/services
  POST\ /control/v1/services
  GET\ /control/v1/services/{service_id}
  PATCH\ /control/v1/services/{service_id}
  DELETE\ /control/v1/services/{service_id}
  POST\ /control/v1/services/{service_id}/authorization
  GET\ /control/v1/services/{service_id}/authorization
  DELETE\ /control/v1/services/{service_id}/authorization
  POST\ /control/v1/services/{service_id}/logout
  GET\ /control/v1/services/{service_id}/usage
  POST\ /control/v1/services/{service_id}/usage/reset
  POST\ /control/v1/services/{service_id}/risk/clear
  GET\ /control/v1/services/{service_id}/risk-events
].each do |operation|
  raise "missing implemented service operation #{operation}" unless implemented_operations.include?(operation)
end
%w[/control/v1/endpoints /control/v1/subscription-accounts].each do |retired_path|
  raise "retired split service path remains: #{retired_path}" if openapi.fetch("paths").key?(retired_path)
end
%w[
  GET\ /control/v1/access-tokens
  GET\ /control/v1/access-token-usage
  GET\ /control/v1/usage-summary
  POST\ /control/v1/access-tokens
  GET\ /control/v1/access-tokens/{token_id}/secret
  DELETE\ /control/v1/access-tokens/{token_id}
].each do |operation|
  raise "missing implemented access-token operation #{operation}" unless implemented_operations.include?(operation)
end

access_token = openapi.dig("components", "schemas", "AccessToken")
raise "access-token metadata shape drifted" unless access_token.fetch("required") == %w[id name hint created_at]
raise "access-token source remains on the public contract" if access_token.fetch("properties").key?("source")

access_token_list = openapi.dig("components", "schemas", "AccessTokenList")
raise "access-token list must reserve a null cursor" unless access_token_list.fetch("required") == %w[items next_cursor] &&
                                                        access_token_list.dig("properties", "next_cursor", "type") == "null"

access_token_secret = openapi.dig("components", "schemas", "AccessTokenSecret")
raise "create access-token response shape drifted" unless access_token_secret.fetch("required") == %w[token access_token] &&
                                                         access_token_secret.dig("properties", "token", "$ref") == "#/components/schemas/AccessToken"

request_record = openapi.dig("components", "schemas", "RequestRecord")
raise "request records must reserve local access-token attribution" unless request_record.fetch("required").include?("local_access_token_id") &&
                                                                        request_record.fetch("properties").key?("local_access_token_id")
raise "request records must use service_id" unless request_record.fetch("required").include?("service_id") &&
                                                   request_record.fetch("properties").key?("service_id") &&
                                                   !request_record.fetch("properties").key?("endpoint_id")
raise "request records must expose privacy restore diagnostics" unless request_record.fetch("required").include?("privacy_restore") &&
                                                                        request_record.dig("properties", "privacy_restore", "oneOf")&.any? { |entry| entry["$ref"] == "#/components/schemas/PrivacyRestoreSummary" }
routing_decision = openapi.dig("components", "schemas", "RequestRoutingDecision")
raise "request records must explain the provider choice" unless request_record.dig("properties", "routing_decision", "$ref") == "#/components/schemas/RequestRoutingDecision" &&
                                                               routing_decision.fetch("required") == %w[skipped] &&
                                                               routing_decision.dig("properties", "skipped", "maxItems") == 64
raise "RoutingSelection wire values drifted" unless openapi.dig("components", "schemas", "RoutingSelection", "enum") == %w[priority session_binding response_affinity websocket_connection failover client_pin]
raise "RoutingSkipReason wire values drifted" unless openapi.dig("components", "schemas", "RoutingSkipReason", "enum") == %w[
  disabled not_connected risk_paused model_not_listed protocol_unsupported streaming_unsupported
  conversion_unavailable circuit_open rate_limited websocket_disabled websocket_unsupported
]

policy_match = openapi.dig("components", "schemas", "PolicyMatch")
raise "policy matches must use service_ids" unless policy_match.fetch("properties").key?("service_ids") &&
                                                     !policy_match.fetch("properties").key?("endpoint_ids")

%w[
  GET\ /control/v1/policies
  GET\ /control/v1/policies/{policy_id}
  PATCH\ /control/v1/policies/{policy_id}
  GET\ /control/v1/privacy-model-catalog
  GET\ /control/v1/privacy-model-catalog/releases
  POST\ /control/v1/privacy-models/probe
  POST\ /control/v1/privacy-models/local/probe
  GET\ /control/v1/privacy-models
  POST\ /control/v1/privacy-models
  GET\ /control/v1/privacy-models/{installation_id}
  DELETE\ /control/v1/privacy-models/{installation_id}
].each do |operation|
  raise "missing implemented privacy operation #{operation}" unless implemented_operations.include?(operation)
end
privacy_catalog_methods = openapi.dig("paths", "/control/v1/privacy-model-catalog").keys
raise "privacy-model catalog methods drifted: #{privacy_catalog_methods}" unless privacy_catalog_methods == %w[get]
privacy_release_methods = openapi.dig("paths", "/control/v1/privacy-model-catalog/releases").keys
raise "privacy-model release methods drifted: #{privacy_release_methods}" unless privacy_release_methods == %w[get]
privacy_probe_methods = openapi.dig("paths", "/control/v1/privacy-models/probe").keys
raise "privacy-model probe methods drifted: #{privacy_probe_methods}" unless privacy_probe_methods == %w[post]
local_privacy_probe_methods = openapi.dig("paths", "/control/v1/privacy-models/local/probe").keys
raise "local privacy-model probe methods drifted: #{local_privacy_probe_methods}" unless local_privacy_probe_methods == %w[post]
privacy_collection_methods = openapi.dig("paths", "/control/v1/privacy-models").keys
raise "privacy-model collection methods drifted: #{privacy_collection_methods}" unless privacy_collection_methods == %w[get post]
privacy_item_methods = openapi.dig("paths", "/control/v1/privacy-models/{installation_id}").keys
raise "privacy-model item methods drifted: #{privacy_item_methods}" unless privacy_item_methods == %w[parameters get delete]

%w[
  POST\ /control/v1/auto-classifier/local/probe
  GET\ /control/v1/auto-classifier
  POST\ /control/v1/auto-classifier
  POST\ /control/v1/auto-classifier/classify-preview
].each do |operation|
  raise "missing implemented auto-classifier operation #{operation}" unless implemented_operations.include?(operation)
end
auto_probe_methods = openapi.dig("paths", "/control/v1/auto-classifier/local/probe").keys
raise "auto-classifier local probe methods drifted: #{auto_probe_methods}" unless auto_probe_methods == %w[post]
auto_collection_methods = openapi.dig("paths", "/control/v1/auto-classifier").keys
raise "auto-classifier collection methods drifted: #{auto_collection_methods}" unless auto_collection_methods == %w[get post]
auto_preview_methods = openapi.dig("paths", "/control/v1/auto-classifier/classify-preview").keys
raise "auto-classifier preview methods drifted: #{auto_preview_methods}" unless auto_preview_methods == %w[post]
raise "auto-classifier taxonomy must freeze coding" unless openapi.dig("components", "schemas", "AutoClassifierProbe", "properties", "id2label", "items", "enum") == %w[general research coding architect]

policy = openapi.dig("components", "schemas", "Policy")
raise "Policy must require detector" unless policy.fetch("required").include?("detector") &&
                                           policy.dig("properties", "detector", "$ref") == "#/components/schemas/PolicyDetector"
raise "Policy must reserve a nullable local model selection" unless policy.fetch("required").include?("local_model_id") &&
                                                                   policy.dig("properties").key?("local_model_id")
min_confidence = policy.dig("properties", "min_confidence")
raise "Policy min_confidence contract drifted" unless policy.fetch("required").include?("min_confidence") &&
                                                      min_confidence.fetch("type") == "number" &&
                                                      min_confidence.fetch("minimum") == 0 &&
                                                      min_confidence.fetch("maximum") == 1 &&
                                                      min_confidence.fetch("default") == 0.8
raise "PolicyAction wire values drifted" unless openapi.dig("components", "schemas", "PolicyAction", "enum") == %w[allow warn block redact]
raise "PolicyDetector wire values drifted" unless openapi.dig("components", "schemas", "PolicyDetector", "enum") == %w[regex local_model]
policy_patch_fields = openapi.dig("components", "schemas", "PolicyPatch", "properties").keys
raise "fixed policy patch fields drifted: #{policy_patch_fields}" unless policy_patch_fields == %w[
  enabled detector local_model_id min_confidence regex_source custom_regex_rules
  kind_rules allowlist_rules request_action response_restore
  restore_tool_arguments placeholder_notice
]
raise "PlaceholderStyle wire values drifted" unless openapi.dig("components", "schemas", "PlaceholderStyle", "enum") == %w[natural token]
raise "PrivacyKind wire values drifted" unless openapi.dig("components", "schemas", "PrivacyKind", "enum") == %w[
  common_secret payment_card account email phone url ip_address
  private_person private_address private_date
]
policy_kind_rule = openapi.dig("components", "schemas", "PolicyKindRule")
raise "PolicyKindRule contract drifted" unless policy_kind_rule.fetch("required") == %w[kind enabled style] &&
                                              policy_kind_rule.dig("properties", "style", "$ref") == "#/components/schemas/PlaceholderStyle" &&
                                              policy_kind_rule.dig("properties", "notify", "default") == true
raise "PolicyAllowlistRule type values drifted" unless openapi.dig("components", "schemas", "PolicyAllowlistRule", "properties", "type", "enum") == %w[literal domain_suffix cidr]
# A natural stand-in that is never restored inside a tool argument is acted on by
# the local agent as if it were real, so the two fields must stay paired.
policy_properties = policy.fetch("properties")
raise "Policy must carry per-kind rules and tool-argument restoration" unless policy.fetch("required").include?("kind_rules") &&
                                                                             policy.fetch("required").include?("restore_tool_arguments") &&
                                                                             policy_properties.dig("restore_tool_arguments", "default") == true
policy_patch_confidence = openapi.dig("components", "schemas", "PolicyPatch", "properties", "min_confidence")
raise "PolicyPatch min_confidence contract drifted" unless policy_patch_confidence == {
  "type" => "number",
  "minimum" => 0,
  "maximum" => 1
}
dry_run_finding = openapi.dig("components", "schemas", "PolicyDryRunFinding")
raise "dry-run finding confidence drifted" unless dry_run_finding.fetch("required").include?("confidence") &&
                                                  dry_run_finding.dig("properties", "confidence") == {
                                                    "type" => "number",
                                                    "minimum" => 0,
                                                    "maximum" => 1,
                                                    "description" => "Detector confidence retained from the local worker; Regex findings use 1."
                                                  }
dry_run_response = openapi.dig("components", "schemas", "PolicyDryRunResponse")
raise "dry-run suppressed findings drifted" unless dry_run_response.fetch("required").include?("suppressed_findings") &&
                                                    dry_run_response.dig("properties", "suppressed_findings", "items", "$ref") == "#/components/schemas/PolicyDryRunFinding"
response_action = policy.dig("properties", "response_action")
raise "response action must remain output-only allow" unless response_action == {
  "type" => "string",
  "const" => "allow",
  "readOnly" => true
}
policy_create = openapi.dig("components", "schemas", "PolicyCreate")
raise "response action must not be writable on create" if policy_create.fetch("required").include?("response_action") ||
                                                       policy_create.fetch("properties").key?("response_action")

catalog = openapi.dig("components", "schemas", "PrivacyModelCatalog")
raise "privacy model catalog must expose items" unless catalog.fetch("required") == %w[items]

adapter_values = openapi.dig("components", "schemas", "PrivacyModelAdapter", "enum")
raise "privacy model adapter values drifted" unless adapter_values == %w[openai_bioes_viterbi hf_token_classification pplx_bioes_viterbi astrlink_sensitive_guard]

canonical_kinds = openapi.dig("components", "schemas", "PrivacyCanonicalKind", "enum")
expected_kinds = %w[email phone account payment_card ip_address url common_secret private_address private_date private_person]
raise "privacy canonical kinds drifted" unless canonical_kinds == expected_kinds

installation = openapi.dig("components", "schemas", "PrivacyModelInstallation")
expected_installation_fields = %w[
  id source catalog_id catalog_source name license languages repo_id revision
  variant_id variant_name quantization adapter status bytes_downloaded bytes_total
  estimated_ram_bytes error label_mapping installed_at preparation
]
raise "privacy installation shape drifted" unless installation.fetch("required") == expected_installation_fields &&
                                                   installation.fetch("properties").keys == expected_installation_fields
catalog_source_values = installation.dig("properties", "catalog_source", "oneOf", 0, "enum")
raise "privacy catalog provenance values drifted" unless catalog_source_values == %w[official community]
preparation = openapi.dig("components", "schemas", "PrivacyModelPreparation")
raise "privacy model preparation shape drifted" unless preparation.fetch("required") == %w[state fallback] &&
                                                       preparation.fetch("properties").keys == %w[state fallback] &&
                                                       preparation.dig("properties", "state", "enum") == %w[preparing ready failed] &&
                                                       preparation.dig("properties", "fallback", "oneOf", 0, "enum") == %w[local_model regex]
installation_source_values = installation.dig("properties", "source", "enum")
raise "privacy installation source values drifted" unless installation_source_values == %w[catalog custom local]

local_probe = openapi.dig("components", "schemas", "PrivacyModelLocalProbeRequest")
raise "local privacy model probe must accept only one path" unless local_probe.fetch("required") == %w[path] &&
                                                               local_probe.fetch("properties").keys == %w[path] &&
                                                               local_probe.fetch("additionalProperties") == false

# --- model routing / classifier: Go is authoritative, the spec must follow -----
#
# The routing and classifier schemas are closed documents (`additionalProperties:
# false` for the settings and condition shapes), so an OpenAPI face that omits an
# accepted property is not a cosmetic gap: it rejects a write the Go validator
# accepts, and a symmetric guard elsewhere means nothing covers it. Each
# assertion below derives its expectation from the Go source itself, so the next
# drift fails here instead of shipping.

MODEL_ROUTING_PATH = ROOT.join("../core/contract/model_routing.go")
CLASSIFIER_SETTINGS_PATH = ROOT.join("../core/contract/classifier_settings.go")
model_routing_source = MODEL_ROUTING_PATH.read
classifier_settings_source = CLASSIFIER_SETTINGS_PATH.read

# JSON names a Go struct declares, in declaration order, split by `omitempty`.
def go_struct_fields(source, struct_name)
  body = source[/type #{struct_name} struct \{(.*?)\n\}/m, 1]
  raise "no Go struct #{struct_name}" if body.nil?

  required = []
  optional = []
  body.scan(/json:"([^",]+)(,omitempty)?"/) { |name, omitempty| (omitempty ? optional : required) << name }
  [required, optional]
end

def go_string_constants(source, block)
  constants = {}
  block.each_line do |line|
    match = line.match(/^\s*(\w+)\s*=\s*"([^"]*)"/)
    constants[match[1]] = match[2] if match
  end
  constants
end

def json_names_at(node, *path)
  Array(node.dig(*path)).map { |entry| entry.fetch("json") }
end

drifts = []

# 1. Every fact the Go field table knows is a writable member of the condition
#    enum, and every condition property is a Go field or logical node.
go_model_routing_source = model_routing_source[/var modelRoutingFields = map\[string\]struct \{\n\s*kind string\n\}\{(.*?)\n\}/m, 1]
raise "no Go modelRoutingFields table" if go_model_routing_source.nil?

go_facts = go_model_routing_source.scan(/"([a-z0-9_]+)":\s*\{kind:/).flatten.sort

# A condition carries exactly one shape at a time, so every member is omitempty
# and the whole wire shape — not just a "required" half — is the comparison set.
go_condition = go_struct_fields(model_routing_source, "ModelRoutingCondition").flatten.sort

condition = openapi.dig("components", "schemas", "ModelRoutingCondition")
# An absent schema reports as its own drift rather than crashing the guard: a
# crash is not a message about what drifted.
if condition.nil?
  drifts << "ModelRoutingCondition: not defined"
else
  condition_properties = condition.fetch("properties", {})
  condition_keys = condition_properties.keys.sort
  if go_condition != condition_keys
    drifts << "ModelRoutingCondition.properties drift = #{((go_condition - condition_keys) | (condition_keys - go_condition)).inspect}"
  end

  # The field admits two disjoint name sets: the built-in facts Go registers,
  # and the name of a dimension the classifier settings declare — a name that
  # lives in a separate resource, so it cannot be enumerated here. The built-in
  # half stays a closed enum compared to Go term by term, so the guard that
  # catches a renamed or retired fact is unchanged; the second branch must
  # negate exactly that set, or a name would fall through both branches.
  field = condition_properties["field"]
  if field.nil?
    drifts << "ModelRoutingCondition.field: not defined"
  else
    branches = Array(field["oneOf"])
    unless branches.length == 2
      drifts << "ModelRoutingCondition.field.oneOf = #{branches.length} branches, want 2"
    end

    builtin_names = Array((branches[0] || {})["enum"]).sort
    unless go_facts == builtin_names
      drifts << "ModelRoutingCondition.field enum drift = #{((go_facts - builtin_names) | (builtin_names - go_facts)).inspect}"
    end

    negated = Array(branches.dig(1, "not", "enum")).sort
    unless builtin_names == negated
      drifts << "ModelRoutingCondition.field not.enum drift = #{((builtin_names - negated) | (negated - builtin_names)).inspect}"
    end
  end

  # `within` is the time leaf. The schema is closed, so omitting the property
  # rejects a time rule the Go validator accepts.
  drifts << "ModelRoutingCondition.within prop = false" unless condition_properties.key?("within")

  # 2. `within`'s shape is a fact of its own: exactly two HH:mm values, a
  #    half-open [start, end) window. Asserting that the property merely exists
  #    let `type: array` become `string` and let `items.pattern` widen to `^.*$`
  #    with the guard still green, so the shape is compared term by term.
  within = condition_properties["within"]
  if within.nil?
    drifts << "ModelRoutingCondition.within: not defined"
  else
    unless within["type"] == "array"
      drifts << "ModelRoutingCondition.within.type = #{within['type'].inspect}, want \"array\""
    end

    # Go enforces the window length at its one comparison site rather than
    # through a named constant, so the bound is read from that comparison
    # instead of being repeated as a literal here.
    go_window = model_routing_source[/len\(condition\.Within\)\s*!=\s*(\d+)/, 1]
    raise "no Go within length comparison" if go_window.nil?

    %w[minItems maxItems].each do |bound|
      unless within[bound].to_s == go_window
        drifts << "ModelRoutingCondition.within.#{bound} = #{within[bound].inspect}, want #{go_window}"
      end
    end

    items = within["items"]
    if items.nil?
      drifts << "ModelRoutingCondition.within.items: not defined"
    else
      drifts << "ModelRoutingCondition.within.items.type = #{items['type'].inspect}" unless items["type"] == "string"

      # Go validates the time-of-day format rather than an extra property, so
      # the guard reads `validRoutingTimeOfDay` itself: the hour and minute
      # ceilings are parsed out of the comparisons that enforce them, and the
      # spec pattern must then accept exactly the set Go accepts. A hand-copied
      # regex would drift the moment Go's bounds moved.
      validator = model_routing_source[/func validRoutingTimeOfDay.*?\n\}\n/m].to_s
      ceiling = ->(name) { validator[/#{name}\s*<=\s*(\d+)/, 1]&.to_i }
      max_hour = ceiling.call("hour")
      max_minute = ceiling.call("minute")
      if max_hour.nil? || max_minute.nil?
        drifts << "ModelRoutingCondition.within: cannot read validRoutingTimeOfDay bounds"
      else
        pattern_source = items["pattern"]
        if pattern_source.nil?
          drifts << "ModelRoutingCondition.within.items.pattern: not defined"
        else
          pattern = begin
            Regexp.new(pattern_source)
          rescue RegexpError => error
            drifts << "ModelRoutingCondition.within.items.pattern is not a regex: #{error.message}"
            nil
          end
          if pattern
            # Every HH:mm Go accepts must be matched by the spec pattern.
            accepted = (0..max_hour).flat_map do |hour|
              (0..max_minute).map { |minute| format("%02d:%02d", hour, minute) }
            end
            rejected = accepted.reject { |value| pattern.match?(value) }
            unless rejected.empty?
              drifts << "ModelRoutingCondition.within.items.pattern rejects valid #{rejected.first(3).inspect}"
            end
            # And the spec must not accept what Go refuses.
            ["24:00", "9:00", "09:60", "09-00", "0900", "", "09:00 ", "09:000"] .each do |value|
              drifts << "ModelRoutingCondition.within.items.pattern accepts invalid #{value.inspect}" if pattern.match?(value)
            end
          end
        end
      end
    end
  end
end

# 3. Both classifier documents carry every source the Go validator accepts, and
#    the response carries the existing_model shape as a reference.
classifier_sources = go_string_constants(classifier_settings_source, classifier_settings_source[/^\s*(ClassifierSourceInline.*?)\n\n/m, 1].to_s).values
classifier_settings_required, classifier_settings_optional = go_struct_fields(classifier_settings_source, "ClassifierSettings")
classifier_existing_keys = go_struct_fields(classifier_settings_source, "ClassifierExistingModelSource").first
classifier_existing = openapi.dig("components", "schemas", "ClassifierExistingModelSource")
existing_properties = classifier_existing&.fetch("properties", {})

%w[ClassifierSettings ClassifierSettingsResponse].each do |schema_name|
  schema = openapi.dig("components", "schemas", schema_name)
  if schema.nil?
    drifts << "#{schema_name}: not defined"
    next
  end

  enum = schema.dig("properties", "classifier_source", "enum")
  drift = classifier_sources - Array(enum)
  drifts << "#{schema_name}: drift=#{drift.inspect}" unless drift.empty?

  raised = schema.dig("properties", "existing_model", "$ref")
  drifts << "#{schema_name}: existing_model prop = false" unless raised == "#/components/schemas/ClassifierExistingModelSource"

  # The settings face is closed in Go as well (decodePolicy with
  # DisallowUnknownFields), so an omitted property costs the same here as it
  # does below: the schema would reject a write the Go validator accepts.
  # `properties` lists the whole wire shape, required and omitempty alike, so
  # it is compared against both halves; `required` is compared against the
  # half that carries no omitempty. The response adds credential_configured,
  # a fact about the credential store rather than a settings field, so that
  # one name is the only thing expected on top of the Go struct.
  missing_settings = (classifier_settings_required + classifier_settings_optional) - schema.fetch("properties", {}).keys
  drifts << "#{schema_name}: properties drift=#{missing_settings.inspect}" unless missing_settings.empty?

  expected_required = classifier_settings_required.sort
  expected_required = (expected_required + ["credential_configured"]).sort if schema_name == "ClassifierSettingsResponse"
  wire_required = Array(schema.fetch("required", [])).sort
  unless expected_required == wire_required
    drifts << "#{schema_name}.required drift = #{((expected_required - wire_required) | (wire_required - expected_required)).inspect}"
  end
end

if classifier_existing.nil?
  drifts << "ClassifierExistingModelSource: not defined"
else
  # `properties` may be a superset of the Go wire shape — the spec stays open to
  # extra data — but an omitted member is the asymmetry that rejects a write the
  # Go validator accepts, which is what this guard exists to catch.
  missing = classifier_existing_keys - existing_properties.keys
  drifts << "ClassifierExistingModelSource: drift=#{missing.inspect}" unless missing.empty?
end

# 4. A request record and the dry-run preview describe an evaluation with the
#    same Go structs, so the spec keeps one component per struct and both faces
#    reference it. Each component, and the dry-run response itself, is compared
#    with its Go struct in both directions: these are closed output shapes, so
#    a member Go writes that the spec lacks makes a real record fail the
#    published contract, and a member only the spec lists documents something
#    nothing sends. `required` is the Go half that carries no omitempty, so a
#    member that always rides the wire (`chosen: false`) is never optional.
def struct_drift(label, schema, required, optional)
  return ["#{label}: not defined"] if schema.nil?

  drift = []
  go_names = (required + optional).sort
  names = schema.fetch("properties", {}).keys.sort
  drift << "#{label}.properties drift = #{((go_names - names) | (names - go_names)).inspect}" unless go_names == names
  wire_required = Array(schema.fetch("required", [])).sort
  unless required.sort == wire_required
    drift << "#{label}.required drift = #{((required.sort - wire_required) | (wire_required - required.sort)).inspect}"
  end
  drift
end

schemas = openapi.dig("components", "schemas")
%w[
  ModelRoutingDecision ModelRoutingFieldObservation ModelRoutingCandidateObservation ModelRoutingRuleVerdict
  ModelRoutingClassifierAnswers ModelRoutingDimensionAnswer ModelRoutingBindingReset ModelRoutingExecution
  ModelRoutingPrivacyAttempt
].each do |name|
  drifts.concat(struct_drift(name, schemas[name], *go_struct_fields(model_routing_source, name)))
end

dry_run_source = ROOT.join("../core/internal/controlapi/classifier_settings.go").read
dry_run = schemas["ClassifierDryRunResponse"]
{
  "ClassifierDryRunResponse" => [dry_run, "classifierDryRunResponse"],
  "ClassifierDryRunResponse.decision" => [dry_run&.dig("properties", "decision"), "classifierDryRunDecision"],
  "ClassifierDryRunResponse.dimensions" => [dry_run&.dig("properties", "dimensions", "additionalProperties"), "dryRunDimensionScore"],
}.each do |label, (schema, struct_name)|
  drifts.concat(struct_drift(label, schema, *go_struct_fields(dry_run_source, struct_name)))
end

{
  %w[RequestRecord model_routing] => "ModelRoutingDecision",
  %w[ClassifierDryRunResponse fields items] => "ModelRoutingFieldObservation",
  %w[ClassifierDryRunResponse rules items] => "ModelRoutingRuleVerdict",
  %w[ModelRoutingDecision fields items] => "ModelRoutingFieldObservation",
  %w[ModelRoutingDecision rules items] => "ModelRoutingRuleVerdict",
  %w[ModelRoutingDecision classifier] => "ModelRoutingClassifierAnswers",
  %w[ModelRoutingDecision binding_reset] => "ModelRoutingBindingReset",
  %w[ModelRoutingDecision execution] => "ModelRoutingExecution",
  %w[ModelRoutingDecision privacy_attempts items] => "ModelRoutingPrivacyAttempt",
  %w[ModelRoutingRuleVerdict fields items] => "ModelRoutingFieldObservation",
  %w[ModelRoutingFieldObservation candidates items] => "ModelRoutingCandidateObservation",
  %w[ModelRoutingClassifierAnswers dimensions items] => "ModelRoutingDimensionAnswer",
}.each do |(schema_name, *path), component|
  reference = schemas.dig(schema_name, "properties", *path, "$ref")
  next if reference == "#/components/schemas/#{component}"

  drifts << "#{([schema_name] + path).join('.')} must reference #{component} (got #{reference.inspect})"
end

# 5. The built-in field table has three hand-written mirrors — the Go
#    registry the checks above read, the desktop model's operator table, and
#    the Tauri sidecar's match arms — and a name added to one of them alone
#    is a field that saves in one place and is unsupported in another. Only
#    the name sets are compared: the three express kinds differently (the
#    desktop model folds keyword into its label kind and has no keyword kind
#    at all), so comparing kinds would report a false finding.
desktop_routing_path = ROOT.join("../apps/desktop/src/model-routing-group-model.ts")
desktop_routing_source = desktop_routing_path.read
desktop_operators = desktop_routing_source[/export const modelRoutingFieldOperators = \{(.*?)\n\} as const;/m, 1]
raise "no TS modelRoutingFieldOperators table" if desktop_operators.nil?

desktop_facts = desktop_operators.scan(/^  ([a-z0-9_]+):/).flatten.sort

tauri_routing_path = ROOT.join("../apps/desktop/src-tauri/src/failure_policy.rs")
tauri_routing_source = tauri_routing_path.read
tauri_kind = tauri_routing_source[/fn routing_field_kind\(.*?\n\}\n/m].to_s
raise "no Rust routing_field_kind table" if tauri_kind.empty?

# Only the literal arms carry a field name: the wildcard arms that follow
# them resolve a runtime dimension and refuse an unsupported name, and a
# kind literal on the right-hand side of an arm is a kind rather than a
# field. Each arm is therefore split into its pattern side and its result
# side, and only the pattern side is read.
tauri_facts = tauri_kind.scan(/((?:"[a-z0-9_]+"\s*\|?\s*)+)\s*=>/).flatten.flat_map { |patterns| patterns.scan(/"([a-z0-9_]+)"/).flatten }.sort

[["desktop model-routing-group-model.ts", desktop_facts], ["Tauri failure_policy.rs", tauri_facts]].each do |label, facts|
  drift = (go_facts - facts) | (facts - go_facts)
  drifts << "#{label} routing field set drift = #{drift.inspect}" unless drift.empty?
end

raise "model routing contract drifted from Go:\n  #{drifts.join("\n  ")}" unless drifts.empty?

puts "validated #{reference_count} local $ref values, frozen fixtures, Alpha relay invariants, retired routing operations, audit patch semantics, and one routing field vocabulary across Go, TypeScript and Rust"
