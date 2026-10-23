import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { getRequestAuditContent } from "./bridge";
import { i18n } from "./i18n";
import {
  conversationResponsePart,
  parseCallContent,
  type ParsedCall,
} from "./request-conversation-model";
import { holdsRawPart, type RequestRecord } from "./request-record-model";
import { parseResponsePreview } from "./response-preview-model";

/** Decrypts in flight at once. Bodies run to megabytes, so two is plenty. */
const CONCURRENCY = 2;
/** Conversations a page keeps read for the next time they are opened. */
const KEPT_CONVERSATIONS = 3;

interface KeptCall {
  version: string;
  call: ParsedCall;
}

/**
 * What earlier views read, kept by the page that shows them and keyed like
 * `resetKey`. Reopening a conversation starts from it: reading every call
 * again made finished turns count their tools up as if they were still
 * running. Like the page's audit cache it holds no raw part and no running
 * call, and the page clears it whenever it clears that cache.
 */
export type ConversationContentCache = Map<string, Map<string, KeptCall>>;

export interface ConversationContent {
  parsed: ReadonlyMap<string, ParsedCall>;
  reading: ReadonlySet<string>;
  errors: ReadonlyMap<string, string>;
  /**
   * Reads these calls' content in the order given, skipping ones already
   * read. Each call sets the order of everything still waiting.
   */
  request: (records: RequestRecord[]) => void;
}

// A pending call's content grows until it settles, and a capture flag that
// flips means Core now holds a body it did not have. Either reads again.
function versionOf(record: RequestRecord): string {
  return [
    record.status,
    record.audit.request_body_captured,
    record.audit.response_content_captured,
    record.audit.upstream_response_content_captured,
  ].join(":");
}

/** The calls kept for one conversation, now its most recently opened. */
function keptCalls(
  cache: ConversationContentCache | undefined,
  key: string,
): Map<string, KeptCall> | null {
  if (!cache) return null;
  const kept = cache.get(key) ?? new Map<string, KeptCall>();
  cache.delete(key);
  cache.set(key, kept);
  for (const oldest of cache.keys()) {
    if (cache.size <= KEPT_CONVERSATIONS) break;
    cache.delete(oldest);
  }
  return kept;
}

/**
 * Reads call content for the conversation view on demand and keeps only the
 * parsed result. The original bodies are let go as soon as they are parsed,
 * so an agent turn with hundreds of calls costs the view its summaries, not
 * hundreds of megabytes. `resetKey` drops everything but what `cache` kept
 * under the new key, which is how an unlock that ends takes raw-derived text
 * off the screen.
 */
export function useConversationContent(
  resetKey: string,
  cache?: ConversationContentCache,
): ConversationContent {
  const [parsed, setParsed] = useState<ReadonlyMap<string, ParsedCall>>(
    () => new Map(),
  );
  const [reading, setReading] = useState<ReadonlySet<string>>(() => new Set());
  const [errors, setErrors] = useState<ReadonlyMap<string, string>>(
    () => new Map(),
  );
  const generationRef = useRef(0);
  const queueRef = useRef<RequestRecord[]>([]);
  const inFlightRef = useRef(new Set<string>());
  const versionRef = useRef(new Map<string, string>());
  const keptRef = useRef<Map<string, KeptCall> | null>(null);

  useEffect(() => {
    generationRef.current += 1;
    queueRef.current = [];
    inFlightRef.current = new Set();
    const kept = keptCalls(cache, resetKey);
    keptRef.current = kept;
    const entries = [...(kept ?? [])];
    versionRef.current = new Map(
      entries.map(([id, entry]) => [id, entry.version]),
    );
    setParsed(new Map(entries.map(([id, entry]) => [id, entry.call])));
    setReading(new Set());
    setErrors(new Map());
  }, [resetKey, cache]);

  useEffect(
    () => () => {
      generationRef.current += 1;
    },
    [],
  );

  const pump = useCallback(() => {
    const generation = generationRef.current;
    while (
      inFlightRef.current.size < CONCURRENCY &&
      queueRef.current.length > 0
    ) {
      const record = queueRef.current.shift()!;
      if (inFlightRef.current.has(record.id)) continue;
      inFlightRef.current.add(record.id);
      setReading((current) => new Set(current).add(record.id));
      const version = versionOf(record);
      const finish = () => {
        if (generationRef.current !== generation) return;
        inFlightRef.current.delete(record.id);
        setReading((current) => {
          const next = new Set(current);
          next.delete(record.id);
          return next;
        });
        pump();
      };
      void getRequestAuditContent(record.id)
        .then(async (content) => {
          const part = conversationResponsePart(content);
          const outputs = part
            ? (await parseResponsePreview(part)).outputs
            : [];
          if (generationRef.current !== generation) return;
          const call = parseCallContent(record, content, outputs);
          versionRef.current.set(record.id, version);
          // A running call's content still grows, and a raw part must not
          // outlive the unlock that returned it.
          if (record.status !== "pending" && !holdsRawPart(content)) {
            keptRef.current?.set(record.id, { version, call });
          }
          setParsed((current) => new Map(current).set(record.id, call));
          setErrors((current) => {
            if (!current.has(record.id)) return current;
            const next = new Map(current);
            next.delete(record.id);
            return next;
          });
        })
        .catch((error: unknown) => {
          if (generationRef.current !== generation) return;
          const message =
            error instanceof Error && error.message
              ? error.message
              : i18n.t("records.auditContentFailed");
          setErrors((current) => new Map(current).set(record.id, message));
        })
        .finally(finish);
    }
  }, []);

  const request = useCallback(
    (records: RequestRecord[]) => {
      const queue: RequestRecord[] = [];
      const queued = new Set<string>();
      for (const record of records) {
        if (queued.has(record.id)) continue;
        if (versionRef.current.get(record.id) === versionOf(record)) continue;
        if (inFlightRef.current.has(record.id)) continue;
        queued.add(record.id);
        queue.push(record);
      }
      queueRef.current = queue;
      pump();
    },
    [pump],
  );

  return useMemo(
    () => ({ parsed, reading, errors, request }),
    [parsed, reading, errors, request],
  );
}
