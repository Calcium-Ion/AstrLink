import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { getRequestAuditContent } from "./bridge";
import { i18n } from "./i18n";
import {
  conversationResponsePart,
  parseCallContent,
  type ParsedCall,
} from "./request-conversation-model";
import type { RequestRecord } from "./request-record-model";
import { parseResponsePreview } from "./response-preview-model";

/** Decrypts in flight at once. Bodies run to megabytes, so two is plenty. */
const CONCURRENCY = 2;

export interface ConversationContent {
  parsed: ReadonlyMap<string, ParsedCall>;
  reading: ReadonlySet<string>;
  errors: ReadonlyMap<string, string>;
  /** Reads these calls' content, in the order given, skipping ones already read. */
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

/**
 * Reads call content for the conversation view on demand and keeps only the
 * parsed result. The original bodies are let go as soon as they are parsed,
 * so an agent turn with hundreds of calls costs the view its summaries, not
 * hundreds of megabytes. `resetKey` drops everything, which is how an unlock
 * that ends takes raw-derived text off the screen.
 */
export function useConversationContent(resetKey: string): ConversationContent {
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

  useEffect(() => {
    generationRef.current += 1;
    queueRef.current = [];
    inFlightRef.current = new Set();
    versionRef.current = new Map();
    setParsed(new Map());
    setReading(new Set());
    setErrors(new Map());
  }, [resetKey]);

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
      let queued = false;
      for (const record of records) {
        const version = versionOf(record);
        if (versionRef.current.get(record.id) === version) continue;
        if (inFlightRef.current.has(record.id)) continue;
        if (queueRef.current.some((queued) => queued.id === record.id))
          continue;
        queueRef.current.push(record);
        queued = true;
      }
      if (queued) pump();
    },
    [pump],
  );

  return useMemo(
    () => ({ parsed, reading, errors, request }),
    [parsed, reading, errors, request],
  );
}
