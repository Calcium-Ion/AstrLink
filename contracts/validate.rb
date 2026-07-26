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

endpoint = openapi.dig("components", "schemas", "Endpoint")
endpoint_create = openapi.dig("components", "schemas", "EndpointCreate")
raise "Endpoint responses must preserve auth configuration" unless endpoint.fetch("required").include?("auth")
raise "EndpointCreate must require auth configuration" unless endpoint_create.fetch("required").include?("auth")
raise "legacy lossy auth_scheme field remains" if endpoint_create.fetch("properties").key?("auth_scheme")
raise "unpersisted Endpoint extensions remain" if endpoint_create.fetch("properties").key?("extensions")

credential_ref_schema = schema.dig("$defs", "CredentialRef")
endpoint_credential_ref = endpoint.dig("properties", "credential_ref")
expected_credential_ref = "./protocol-capabilities.schema.json#/$defs/CredentialRef"
raise "Endpoint credential_ref must use the shared schema" unless endpoint_credential_ref.fetch("$ref") == expected_credential_ref
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

%w[
  GET\ /control/v1/policies
  GET\ /control/v1/policies/{policy_id}
  PATCH\ /control/v1/policies/{policy_id}
  GET\ /control/v1/privacy-model-catalog
  POST\ /control/v1/privacy-models/probe
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
privacy_collection_methods = openapi.dig("paths", "/control/v1/privacy-models").keys
raise "privacy-model collection methods drifted: #{privacy_collection_methods}" unless privacy_collection_methods == %w[get post]
privacy_item_methods = openapi.dig("paths", "/control/v1/privacy-models/{installation_id}").keys
raise "privacy-model item methods drifted: #{privacy_item_methods}" unless privacy_item_methods == %w[parameters get delete]

policy = openapi.dig("components", "schemas", "Policy")
raise "Policy must require detector" unless policy.fetch("required").include?("detector") &&
                                           policy.dig("properties", "detector", "$ref") == "#/components/schemas/PolicyDetector"
raise "Policy must reserve a nullable local model selection" unless policy.fetch("required").include?("local_model_id") &&
                                                                   policy.dig("properties").key?("local_model_id")
raise "PolicyAction wire values drifted" unless openapi.dig("components", "schemas", "PolicyAction", "enum") == %w[allow warn block redact]
raise "PolicyDetector wire values drifted" unless openapi.dig("components", "schemas", "PolicyDetector", "enum") == %w[regex local_model]
policy_patch_fields = openapi.dig("components", "schemas", "PolicyPatch", "properties").keys
raise "fixed policy patch fields drifted: #{policy_patch_fields}" unless policy_patch_fields == %w[enabled detector local_model_id request_action response_restore]
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
raise "privacy model adapter values drifted" unless adapter_values == %w[openai_bioes_viterbi hf_token_classification]

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

puts "validated #{reference_count} local $ref values, frozen fixtures, Alpha relay invariants, and audit patch semantics"
