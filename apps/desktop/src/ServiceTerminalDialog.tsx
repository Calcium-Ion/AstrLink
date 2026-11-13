import { useEffect, useRef, useState, type FormEvent } from "react";

import { ChoiceCard } from "@/components/ChoiceCard";
import { Field } from "@/components/Field";
import { FormMessage } from "@/components/FormMessage";
import { ModelSelect } from "@/components/ModelSelect";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { RadioGroup } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import {
  getClientCatalog,
  getClientConfigStatus,
  listTerminalApps,
  openServiceTerminal,
  pickTerminalDirectory,
} from "./bridge";
import { clientSetupClients } from "./client-setup-clients";
import { useT } from "./i18n";
import { notify } from "./notify";
import type { Service } from "./service-model";
import {
  readTerminalMemory,
  rememberTerminalSession,
  serviceModelOptions,
  suggestedClient,
  suggestedModel,
  terminalClients,
  type TerminalApp,
  type TerminalChoice,
  type TerminalClient,
} from "./service-terminal-model";

/**
 * Opens a terminal whose Claude Code or Codex uses this provider alone. The
 * client starts in a folder the user picks, since a coding client works on
 * the folder it starts in.
 */
export function ServiceTerminalDialog({
  service,
  inferenceURL,
  onClose,
}: {
  service: Service;
  inferenceURL: string;
  onClose: () => void;
}) {
  const t = useT();
  const [memory, setMemory] = useState(readTerminalMemory);
  const [terminals, setTerminals] = useState<TerminalChoice[] | null>(null);
  // Clients installed on this computer; null until the check answers.
  const [installed, setInstalled] = useState<TerminalClient[] | null>(null);
  const [client, setClient] = useState<TerminalClient>(
    () => memory.clients[service.id] ?? suggestedClient(service),
  );
  // What the user picked per client; a client not touched starts suggested.
  const [models, setModels] = useState<Partial<Record<TerminalClient, string>>>(
    {},
  );
  const [directory, setDirectory] = useState<string | null>(
    memory.directories[0] ?? null,
  );
  const [terminal, setTerminal] = useState<TerminalApp | null>(null);
  const [busy, setBusy] = useState<"pick" | "open" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const active = useRef(true);

  useEffect(() => {
    active.current = true;
    void listTerminalApps().then(
      (choices) => active.current && setTerminals(choices),
      () => active.current && setTerminals([]),
    );
    void Promise.resolve()
      .then(() => getClientCatalog())
      .then((catalog) => getClientConfigStatus(null, catalog))
      .then(
        (overview) =>
          active.current &&
          setInstalled(
            terminalClients.filter((id) =>
              overview.clients.some(
                (status) =>
                  status.client === id &&
                  status.wsl === null &&
                  status.detected,
              ),
            ),
          ),
        // An unreadable status says nothing about the clients; offer both.
        () => active.current && setInstalled([...terminalClients]),
      );
    return () => {
      active.current = false;
    };
  }, []);

  // Fall back to an installed client once the check answers.
  const chosen: TerminalClient | null =
    installed === null || installed.includes(client)
      ? client
      : (installed[0] ?? null);
  const model =
    chosen === null
      ? ""
      : (models[chosen] ?? suggestedModel(service, chosen, memory));
  // The pick, else the last one used, as long as it is still installed.
  const chosenTerminal =
    [terminal, memory.terminal]
      .map((id) => terminals?.find((choice) => choice.id === id)?.id)
      .find((id) => id !== undefined) ??
    terminals?.[0]?.id ??
    null;
  const directories = [
    ...new Set([...(directory ? [directory] : []), ...memory.directories]),
  ];
  const working = busy !== null;
  const canOpen =
    chosen !== null &&
    chosenTerminal !== null &&
    directory !== null &&
    inferenceURL !== "" &&
    !working;

  const pick = async () => {
    setBusy("pick");
    setError(null);
    try {
      const picked = await pickTerminalDirectory(directory);
      if (active.current && picked) setDirectory(picked);
    } catch (cause) {
      if (active.current) setError(errorText(cause));
    } finally {
      if (active.current) setBusy(null);
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!canOpen || chosen === null || !chosenTerminal || !directory) return;
    setBusy("open");
    setError(null);
    const request = {
      service_id: service.id,
      service_name: service.name,
      client: chosen,
      directory,
      model: model.trim() || null,
      terminal: chosenTerminal,
      inference_url: inferenceURL,
    };
    try {
      await openServiceTerminal(request);
      setMemory(rememberTerminalSession(memory, request));
      notify.success(
        t("serviceTerminal.opened", {
          client: clientName(chosen),
          terminal:
            terminals?.find((choice) => choice.id === chosenTerminal)?.name ??
            chosenTerminal,
        }),
      );
      onClose();
    } catch (cause) {
      if (active.current) {
        setError(t("serviceTerminal.failed", { error: errorText(cause) }));
        setBusy(null);
      }
    }
  };

  const unusable = !service.enabled
    ? t("serviceTerminal.disabled")
    : service.subscription && service.subscription.status !== "connected"
      ? t("serviceTerminal.signedOut")
      : null;

  return (
    <Dialog open onOpenChange={(open) => !open && !working && onClose()}>
      <DialogContent showCloseButton={!working} className="sm:max-w-lg">
        <DialogHeader className="text-left">
          <DialogTitle>
            {t("serviceTerminal.title", { name: service.name })}
          </DialogTitle>
          <DialogDescription>
            {t("serviceTerminal.description")}
          </DialogDescription>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={(event) => void submit(event)}>
          {unusable ? (
            <FormMessage tone="warning">{unusable}</FormMessage>
          ) : null}
          <Field group label={t("serviceTerminal.client")}>
            <RadioGroup
              aria-label={t("serviceTerminal.client")}
              className="grid grid-cols-2 gap-2"
              value={chosen ?? ""}
              onValueChange={(value) => setClient(value as TerminalClient)}
              disabled={working}
            >
              {clientSetupClients
                .filter(({ id }) =>
                  (terminalClients as readonly string[]).includes(id),
                )
                .map(({ id, label, Icon }) => {
                  const missing =
                    installed !== null &&
                    !installed.includes(id as TerminalClient);
                  return (
                    <ChoiceCard
                      key={id}
                      id={`service-terminal-${id}`}
                      value={id}
                      label={label}
                      description={
                        missing ? t("clientSetup.notInstalled") : undefined
                      }
                      selected={chosen === id}
                      disabled={working || missing}
                      layout="tile"
                      icon={<Icon size={24} />}
                    />
                  );
                })}
            </RadioGroup>
          </Field>
          {installed !== null && chosen === null ? (
            <FormMessage tone="notice">
              {t("serviceTerminal.noClient")}
            </FormMessage>
          ) : null}
          <Field
            group
            label={t("serviceTerminal.directory")}
            hint={t("serviceTerminal.directoryHint")}
          >
            <div className="flex min-w-0 gap-2">
              {directories.length > 0 ? (
                <Select
                  value={directory ?? ""}
                  // Radix reports a blank value while its options mount.
                  onValueChange={(value) => value && setDirectory(value)}
                  disabled={working}
                >
                  <SelectTrigger
                    aria-label={t("serviceTerminal.directory")}
                    className="min-w-0 flex-1"
                  >
                    <SelectValue />
                  </SelectTrigger>
                  {/* Long paths end in an ellipsis inside the field's width. */}
                  <SelectContent className="max-w-(--radix-select-trigger-width)">
                    {directories.map((dir) => {
                      const { name, parent } = splitDirectory(dir);
                      return (
                        <SelectItem key={dir} value={dir} title={dir}>
                          <span className="min-w-0 truncate">
                            {name}
                            {parent ? (
                              <span className="ml-2 text-muted-foreground">
                                {parent}
                              </span>
                            ) : null}
                          </span>
                        </SelectItem>
                      );
                    })}
                  </SelectContent>
                </Select>
              ) : null}
              <Button
                type="button"
                variant="outline"
                className={directories.length > 0 ? "shrink-0" : "w-full"}
                disabled={working}
                onClick={() => void pick()}
              >
                {t("serviceTerminal.chooseDirectory")}
              </Button>
            </div>
          </Field>
          {chosen ? (
            <Field
              htmlFor="service-terminal-model"
              label={t("serviceTerminal.model")}
              hint={
                chosen === "claude"
                  ? t("serviceTerminal.claudeModelHint")
                  : undefined
              }
            >
              <ModelSelect
                id="service-terminal-model"
                aria-label={t("serviceTerminal.model")}
                options={serviceModelOptions(service)}
                value={model}
                placeholder={t(
                  chosen === "claude"
                    ? "serviceTerminal.claudeModelPlaceholder"
                    : "serviceTerminal.codexModelPlaceholder",
                )}
                onValueChange={(value) =>
                  setModels((current) => ({ ...current, [chosen]: value }))
                }
                maxLength={256}
                disabled={working}
              />
            </Field>
          ) : null}
          <Field group label={t("serviceTerminal.terminal")}>
            {terminals !== null && terminals.length === 0 ? (
              <FormMessage tone="notice">
                {t("serviceTerminal.noTerminal")}
              </FormMessage>
            ) : (
              <Select
                value={chosenTerminal ?? ""}
                onValueChange={(value) =>
                  value && setTerminal(value as TerminalApp)
                }
                disabled={working || terminals === null}
              >
                <SelectTrigger
                  aria-label={t("serviceTerminal.terminal")}
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(terminals ?? []).map((choice) => (
                    <SelectItem key={choice.id} value={choice.id}>
                      {choice.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </Field>
          {error ? <FormMessage tone="error">{error}</FormMessage> : null}
          <DialogFooter className="border-t pt-4 min-[540px]:flex-row min-[540px]:justify-end">
            <Button
              type="button"
              variant="outline"
              disabled={working}
              onClick={onClose}
            >
              {t("common.cancel")}
            </Button>
            <Button type="submit" disabled={!canOpen}>
              {busy === "open"
                ? t("serviceTerminal.opening")
                : t("serviceTerminal.open")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A folder's own name, then where it sits. A long path keeps the name in view,
 * which tells projects apart.
 */
function splitDirectory(dir: string): { name: string; parent: string } {
  const trimmed = dir.replace(/[\\/]+$/, "");
  const cut = Math.max(trimmed.lastIndexOf("/"), trimmed.lastIndexOf("\\"));
  if (trimmed === "" || cut < 0) return { name: dir, parent: "" };
  return {
    name: trimmed.slice(cut + 1),
    // A folder at the root keeps the separator, such as `/` for `/tmp`.
    parent: trimmed.slice(0, cut) || trimmed.slice(0, cut + 1),
  };
}

function clientName(client: TerminalClient): string {
  return clientSetupClients.find(({ id }) => id === client)?.label ?? client;
}

function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}
