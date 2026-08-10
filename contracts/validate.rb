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

audit_properties = schema.dig("$defs", "AuditSettings", "properties")
%w[request_body_enabled response_content_enabled].each do |setting|
  raise "#{setting} must default to false" unless audit_properties.dig(setting, "default") == false
end
unless audit_properties.dig("http_meta_enabled", "default") == true
  raise "http_meta_enabled must default to true (ADR 0008)"
end

audit_content = openapi.dig("components", "schemas", "AuditContent")
unless audit_content.fetch("required").include?("http_meta")
  raise "AuditContent must require http_meta so its absence is always explicit null"
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

service = openapi.dig("components", "schemas", "Service")
service_create = openapi.dig("components", "schemas", "ServiceCreate")
expected_service_fields = %w[id name kind enabled capabilities created_at updated_at]
raise "Service response shape drifted" unless service.fetch("required") == expected_service_fields
raise "Service must expose both connection variants" unless service.fetch("properties").key?("http") &&
                                                       service.fetch("properties").key?("subscription")
raise "ServiceCreate must require name and kind" unless service_create.fetch("required") == %w[name kind]
raise "ServiceCreate must model subscription and HTTP variants" unless service_create.fetch("oneOf").length == 2
raise "legacy lossy auth_scheme field remains" if service_create.fetch("properties").key?("auth_scheme")
raise "unpersisted Service extensions remain" if service_create.fetch("properties").key?("extensions")

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
raise "authorization flow choices drifted" unless authorization_flow.fetch("enum") == %w[browser device_code]
authorization_start = openapi.dig("components", "schemas", "AuthorizationStartRequest")
raise "authorization start must reject unknown fields" unless authorization_start.fetch("additionalProperties") == false &&
                                                               authorization_start.fetch("required") == ["flow"] &&
                                                               authorization_start.dig("properties", "flow", "$ref") == "#/components/schemas/AuthorizationFlow"

auto_model = openapi.dig("components", "schemas", "AstrLinkAutoModel")
raise "reserved automatic model identifier drifted" unless auto_model.fetch("type") == "string" &&
                                                          auto_model.fetch("const") == "astrlink/auto" &&
                                                          !auto_model.fetch("description").empty?

selection_modes = openapi.dig("components", "schemas", "RouteSelectionMode", "enum")
raise "route selection mode values drifted" unless selection_modes == %w[priority auto]

route_selection = openapi.dig("components", "schemas", "RouteSelection")
expected_taxonomy_condition = [{
  "if" => {
    "properties" => { "mode" => { "const" => "auto" } },
    "required" => %w[mode]
  },
  "then" => { "required" => %w[taxonomy_id] },
  "else" => { "not" => { "required" => %w[taxonomy_id] } }
}]
raise "RouteSelection classification shape drifted" unless route_selection.fetch("required") == %w[mode] &&
                                                           route_selection.fetch("properties").keys == %w[mode taxonomy_id] &&
                                                           route_selection.dig("properties", "mode", "$ref") == "#/components/schemas/RouteSelectionMode" &&
                                                           route_selection.fetch("allOf") == expected_taxonomy_condition &&
                                                           route_selection.fetch("additionalProperties") == false

classification_id_pattern = Regexp.new(route_selection.dig("properties", "taxonomy_id", "pattern"))
%w[astrlink-text-v1 code code.reasoning code_generation].each do |identifier|
  raise "classification identifier rejects #{identifier}" unless classification_id_pattern.match?(identifier)
end
["", "Code", "code generation", "-code", "code/reasoning"].each do |identifier|
  raise "classification identifier accepts #{identifier.inspect}" if classification_id_pattern.match?(identifier)
end

route_target = openapi.dig("components", "schemas", "RouteTarget")
raise "RouteTarget must not own a classification label" if route_target.fetch("properties").key?("category_id")
raise "RouteTarget must use service_id" unless route_target.fetch("required").include?("service_id") &&
                                                route_target.fetch("properties").key?("service_id") &&
                                                !route_target.fetch("properties").key?("endpoint_id")

route_category = openapi.dig("components", "schemas", "RouteCategory")
category_id = route_category.dig("properties", "category_id")
raise "RouteCategory shape drifted" unless route_category.fetch("required") == %w[category_id targets] &&
                                           route_category.fetch("properties").keys == %w[category_id targets] &&
                                           category_id.fetch("type") == "string" &&
                                           category_id.fetch("minLength") == 1 &&
                                           category_id.fetch("maxLength") == 64 &&
                                           category_id.fetch("pattern") == route_selection.dig("properties", "taxonomy_id", "pattern") &&
                                           route_category.dig("properties", "targets", "minItems") == 1 &&
                                           route_category.dig("properties", "targets", "items", "$ref") == "#/components/schemas/RouteTarget" &&
                                           route_category.fetch("additionalProperties") == false

expected_route_shape_condition = [{
  "if" => {
    "properties" => {
      "selection" => {
        "properties" => { "mode" => { "const" => "auto" } },
        "required" => %w[mode]
      }
    },
    "required" => %w[selection]
  },
  "then" => {
    "required" => %w[categories],
    "properties" => {
      "match" => {
        "properties" => {
          "model" => { "$ref" => "#/components/schemas/AstrLinkAutoModel" }
        },
        "required" => %w[model]
      }
    },
    "not" => { "required" => %w[targets] }
  },
  "else" => {
    "required" => %w[targets],
    "properties" => {
      "match" => {
        "not" => {
          "properties" => {
            "model" => { "$ref" => "#/components/schemas/AstrLinkAutoModel" }
          },
          "required" => %w[model]
        }
      }
    },
    "not" => { "required" => %w[categories] }
  }
}]

%w[Route RouteCreate].each do |schema_name|
  route_schema = openapi.dig("components", "schemas", schema_name)
  schema_required = route_schema.fetch("required")
  selection = route_schema.dig("properties", "selection")
  raise "#{schema_name} must expose optional RouteSelection" unless !schema_required.include?("selection") &&
                                                                    selection == {
                                                                      "$ref" => "#/components/schemas/RouteSelection"
                                                                    }
  raise "#{schema_name} priority/auto shape drifted" unless !schema_required.include?("targets") &&
                                                         !schema_required.include?("categories") &&
                                                         route_schema.dig("properties", "targets", "items", "$ref") == "#/components/schemas/RouteTarget" &&
                                                         route_schema.dig("properties", "categories", "items", "$ref") == "#/components/schemas/RouteCategory" &&
                                                         route_schema.fetch("allOf") == expected_route_shape_condition
end
route_patch_selection = openapi.dig("components", "schemas", "RoutePatch", "properties", "selection", "oneOf")
unless route_patch_selection.is_a?(Array) &&
       route_patch_selection.include?({ "$ref" => "#/components/schemas/RouteSelection" }) &&
       route_patch_selection.any? { |choice| choice["type"] == "null" }
  raise "RoutePatch selection must support RouteSelection and null reset"
end
route_patch = openapi.dig("components", "schemas", "RoutePatch")
{
  "targets" => ["#/components/schemas/RouteTarget", 1],
  "categories" => ["#/components/schemas/RouteCategory", 2]
}.each do |field, (item_ref, min_items)|
  choices = route_patch.dig("properties", field, "oneOf")
  array_choice = choices&.find { |choice| choice["type"] == "array" }
  null_choice = choices&.find { |choice| choice["type"] == "null" }
  raise "RoutePatch #{field} must support array and null" unless array_choice &&
                                                                  null_choice &&
                                                                  array_choice["minItems"] == min_items &&
                                                                  array_choice.dig("items", "$ref") == item_ref
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
].each do |operation|
  raise "missing implemented service operation #{operation}" unless implemented_operations.include?(operation)
end
%w[/control/v1/endpoints /control/v1/subscription-accounts].each do |retired_path|
  raise "retired split service path remains: #{retired_path}" if openapi.fetch("paths").key?(retired_path)
end
%w[
  GET\ /control/v1/access-tokens
  POST\ /control/v1/access-tokens
  GET\ /control/v1/access-tokens/{token_id}/secret
  DELETE\ /control/v1/access-tokens/{token_id}
].each do |operation|
  raise "missing implemented access-token operation #{operation}" unless implemented_operations.include?(operation)
end

access_token = openapi.dig("components", "schemas", "AccessToken")
raise "access-token metadata shape drifted" unless access_token.fetch("required") == %w[id name hint source created_at]
raise "access-token source wire values drifted" unless access_token.dig("properties", "source", "enum") == %w[system_default user]

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

policy_match = openapi.dig("components", "schemas", "PolicyMatch")
raise "policy matches must use service_ids" unless policy_match.fetch("properties").key?("service_ids") &&
                                                     !policy_match.fetch("properties").key?("endpoint_ids")

%w[
  GET\ /control/v1/policies
  GET\ /control/v1/policies/{policy_id}
  PATCH\ /control/v1/policies/{policy_id}
  GET\ /control/v1/privacy-model-catalog
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
privacy_probe_methods = openapi.dig("paths", "/control/v1/privacy-models/probe").keys
raise "privacy-model probe methods drifted: #{privacy_probe_methods}" unless privacy_probe_methods == %w[post]
local_privacy_probe_methods = openapi.dig("paths", "/control/v1/privacy-models/local/probe").keys
raise "local privacy-model probe methods drifted: #{local_privacy_probe_methods}" unless local_privacy_probe_methods == %w[post]
privacy_collection_methods = openapi.dig("paths", "/control/v1/privacy-models").keys
raise "privacy-model collection methods drifted: #{privacy_collection_methods}" unless privacy_collection_methods == %w[get post]
privacy_item_methods = openapi.dig("paths", "/control/v1/privacy-models/{installation_id}").keys
raise "privacy-model item methods drifted: #{privacy_item_methods}" unless privacy_item_methods == %w[parameters get delete]

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
                                                      min_confidence.fetch("default") == 0.6
raise "PolicyAction wire values drifted" unless openapi.dig("components", "schemas", "PolicyAction", "enum") == %w[allow warn block redact]
raise "PolicyDetector wire values drifted" unless openapi.dig("components", "schemas", "PolicyDetector", "enum") == %w[regex local_model]
policy_patch_fields = openapi.dig("components", "schemas", "PolicyPatch", "properties").keys
raise "fixed policy patch fields drifted: #{policy_patch_fields}" unless policy_patch_fields == %w[enabled detector local_model_id min_confidence request_action response_restore]
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
raise "privacy model adapter values drifted" unless adapter_values == %w[openai_bioes_viterbi hf_token_classification astrlink_sensitive_guard]

canonical_kinds = openapi.dig("components", "schemas", "PrivacyCanonicalKind", "enum")
expected_kinds = %w[email phone account payment_card ip_address url common_secret private_address private_date private_person]
raise "privacy canonical kinds drifted" unless canonical_kinds == expected_kinds

installation = openapi.dig("components", "schemas", "PrivacyModelInstallation")
expected_installation_fields = %w[
  id source catalog_id catalog_source name license languages repo_id revision
  variant_id variant_name quantization adapter status bytes_downloaded bytes_total
  estimated_ram_bytes error label_mapping installed_at
]
raise "privacy installation shape drifted" unless installation.fetch("required") == expected_installation_fields &&
                                                   installation.fetch("properties").keys == expected_installation_fields
catalog_source_values = installation.dig("properties", "catalog_source", "oneOf", 0, "enum")
raise "privacy catalog provenance values drifted" unless catalog_source_values == %w[official community]
installation_source_values = installation.dig("properties", "source", "enum")
raise "privacy installation source values drifted" unless installation_source_values == %w[catalog custom local]

local_probe = openapi.dig("components", "schemas", "PrivacyModelLocalProbeRequest")
raise "local privacy model probe must accept only one path" unless local_probe.fetch("required") == %w[path] &&
                                                               local_probe.fetch("properties").keys == %w[path] &&
                                                               local_probe.fetch("additionalProperties") == false

puts "validated #{reference_count} local $ref values, frozen fixtures, Alpha relay and classification-route invariants, and audit patch semantics"
