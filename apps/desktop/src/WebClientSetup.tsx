import { useState } from "react";
import { Button } from "./components/ui/button";
import { Textarea } from "./components/ui/textarea";
import { Tabs, TabsList, TabsTrigger } from "./components/ui/tabs";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "./components/ui/dialog";
import { FormMessage } from "./components/FormMessage";
import { useWebText } from "./web-copy";

export type WebClient =
  | "claude"
  | "codex"
  | "gemini"
  | "opencode"
  | "openclaw"
  | "pi";
const clients: Record<WebClient, string> = {
  claude: "Claude Code",
  codex: "Codex",
  gemini: "Gemini CLI",
  opencode: "OpenCode",
  openclaw: "OpenClaw",
  pi: "Pi",
};
export function webClientSnippet(client: WebClient, origin: string): string {
  const base = new URL(origin).origin;
  const model = "YOUR_MODEL",
    token = "YOUR_ACCESS_TOKEN";
  const json = (value: unknown) => JSON.stringify(value, null, 2);
  switch (client) {
    case "claude":
      return json({
        env: {
          ANTHROPIC_BASE_URL: base,
          ANTHROPIC_AUTH_TOKEN: token,
          ANTHROPIC_MODEL: model,
        },
      });
    case "codex":
      return `model_provider = "astrlink"\nmodel = "${model}"\n\n[model_providers.astrlink]\nname = "AstrLink"\nbase_url = "${base}/v1"\nwire_api = "responses"\nexperimental_bearer_token = "${token}"\n`;
    case "gemini":
      return `GOOGLE_GEMINI_BASE_URL=${base}\nGEMINI_API_KEY=${token}\nGEMINI_MODEL=${model}\n`;
    case "opencode":
      return json({
        provider: {
          astrlink: {
            npm: "@ai-sdk/openai-compatible",
            name: "AstrLink",
            options: { baseURL: `${base}/v1`, apiKey: token },
            models: { [model]: { name: model } },
          },
        },
        model: `astrlink/${model}`,
      });
    case "openclaw":
      return json({
        models: {
          providers: {
            astrlink: {
              baseUrl: `${base}/v1`,
              apiKey: token,
              api: "openai-completions",
              models: [{ id: model, name: model }],
            },
          },
        },
        agents: { defaults: { model: { primary: `astrlink/${model}` } } },
      });
    case "pi":
      return json({
        providers: {
          astrlink: {
            baseUrl: `${base}/v1`,
            apiKey: token,
            api: "openai-responses",
            models: [{ id: model, name: model }],
          },
        },
      });
  }
}
export function WebClientSetup({ onClose }: { onClose: () => void }) {
  const t = useWebText();
  const [client, setClient] = useState<WebClient>("claude");
  const [feedback, setFeedback] = useState("");
  const snippet = webClientSnippet(client, window.location.origin);
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("config")}</DialogTitle>
          <DialogDescription>{t("configHint")}</DialogDescription>
        </DialogHeader>
        <Tabs
          value={client}
          onValueChange={(value) => {
            setClient(value as WebClient);
            setFeedback("");
          }}
        >
          <TabsList className="h-auto flex-wrap">
            {Object.entries(clients).map(([key, label]) => (
              <TabsTrigger key={key} value={key}>
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
        <Textarea
          aria-label={t("config")}
          className="min-h-48 flex-1 font-mono text-xs"
          readOnly
          value={snippet}
          onFocus={(e) => e.currentTarget.select()}
        />
        {feedback && <FormMessage>{feedback}</FormMessage>}
        <Button
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(snippet);
              setFeedback(t("copied"));
            } catch {
              setFeedback(t("clipboard"));
            }
          }}
        >
          {t("copy")}
        </Button>
      </DialogContent>
    </Dialog>
  );
}
