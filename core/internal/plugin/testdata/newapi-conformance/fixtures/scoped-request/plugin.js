// A request processing plugin that declares meta.scope, which no official
// new-api plugin does yet.
export const meta = {
  apiVersion: 1,
  key: "scoped-request",
  name: "Scoped request",
  version: "1.0.0",
  author: { name: "Test" },
  scope: { models: ["gpt-scoped"], channelTypes: [1, 14] },
};

export function ingress() {
  return { action: "continue" };
}
