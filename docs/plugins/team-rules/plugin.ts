// An example plugin: it adds the team's rules to every session's system
// prompt, and lists them on the plugin page. Its configuration is
// {"rules": ["…", "…"]}.

interface Config {
  rules?: string[];
}

export const meta: AstrLinkPlugin.Meta = {
  apiVersion: 1,
  key: "team-rules",
  name: "Team rules",
  version: "1.0.0",
  capabilities: ["context.inject", "ui.views"],
  notice:
    "Adds the rules in this plugin's configuration to the system prompt of every session.",
};

function rules(): string[] {
  const config = astrlink.config.get() as Config;
  return (config.rules ?? []).filter(
    (rule) => typeof rule === "string" && rule.trim() !== "",
  );
}

// The block stays the same for the whole session, so the prompt cache holds.
export function prepareSession():
  | AstrLinkPlugin.PrepareSessionResult
  | undefined {
  const items = rules().map((rule) => "- " + rule.trim());
  if (items.length === 0) return undefined;
  return { system: { open: "<team-rules>", items, close: "</team-rules>" } };
}

export function describeStatus(): AstrLinkPlugin.ViewBlock[] {
  const list = rules();
  if (list.length === 0)
    return [
      { type: "text", text: "No rules yet: add them to the configuration." },
    ];
  return [
    {
      type: "table",
      columns: ["#", "Rule"],
      rows: list.map((rule, index) => [index + 1, rule]),
    },
  ];
}
