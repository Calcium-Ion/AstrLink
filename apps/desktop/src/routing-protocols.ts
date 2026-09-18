import type { RoutableService } from "./service-model";
import type { ProtocolDescriptor } from "./service-presets";

const inferenceProtocolIDs = new Set([
  "openai.responses",
  "openai.responses.compact",
  "anthropic.messages",
  "google.generate_content",
  "openai.chat",
  "openai.completions",
]);

export function availableProtocolIDs(
  services: RoutableService[],
  protocols: ProtocolDescriptor[],
): string[] {
  const serviceProtocolIDs = new Set(
    services.flatMap((service) =>
      service.capabilities
        .filter((capability) => inferenceProtocolIDs.has(capability.protocol))
        .map((capability) => capability.protocol),
    ),
  );
  const ordered = protocols
    .filter(
      (protocol) =>
        protocol.phase === "alpha" &&
        inferenceProtocolIDs.has(protocol.id) &&
        serviceProtocolIDs.has(protocol.id),
    )
    .map((protocol) => protocol.id);
  for (const id of serviceProtocolIDs) {
    if (!ordered.includes(id)) ordered.push(id);
  }
  return ordered;
}
