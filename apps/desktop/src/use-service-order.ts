import { useEffect, useRef, useState } from "react";
import {
  getServiceOrder,
  updateServiceOrder,
  type ServiceOrderRecord,
} from "./bridge";
import type { Service } from "./service-model";

export function useServiceOrder(
  services: Service[],
  ready: boolean,
  onRefresh: () => void | Promise<void>,
) {
  const [record, setRecord] = useState<ServiceOrderRecord | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const [loaded, setLoaded] = useState(false);
  const locked = useRef(false);
  const generation = useRef(0);
  const identities = services
    .map((service) => service.id)
    .sort()
    .join("\0");
  useEffect(() => {
    const current = ++generation.current;
    setLoaded(false);
    if (!ready) return;
    void getServiceOrder()
      .then((value) => {
        if (current === generation.current) {
          setRecord(value);
          setLoaded(true);
        }
      })
      .catch((cause) => {
        if (current === generation.current) setError(String(cause));
      });
    return () => {
      generation.current++;
    };
  }, [ready, identities, revision]);
  const positions = new Map(
    record?.service_ids.map((id, index) => [id, index]),
  );
  const ordered = [...services].sort(
    (a, b) =>
      (positions.get(a.id) ?? Infinity) - (positions.get(b.id) ?? Infinity) ||
      a.id.localeCompare(b.id),
  );
  const complete =
    loaded &&
    record !== null &&
    record.service_ids.length === services.length &&
    services.every((service) => positions.has(service.id));
  const save = async (items: Service[]) => {
    if (!ready || !record || !complete || locked.current) return;
    const original = record;
    const current = generation.current;
    locked.current = true;
    setSaving(true);
    setError(null);
    setRecord({ ...record, service_ids: items.map((item) => item.id) });
    try {
      const saved = await updateServiceOrder(
        items.map((item) => item.id),
        record.etag,
      );
      if (current === generation.current) setRecord(saved);
    } catch (cause) {
      if (current === generation.current) {
        setRecord(original);
        setError(String(cause));
        // A fresh order/ETag is necessary after concurrent creates/deletes.
        try {
          const fresh = await getServiceOrder();
          if (current === generation.current) setRecord(fresh);
        } catch {
          /* keep rollback and error */
        }
        if (current === generation.current) void onRefresh();
      }
    } finally {
      locked.current = false;
      setSaving(false);
    }
  };
  return {
    ordered,
    hasOrder: record !== null,
    saving,
    error,
    complete,
    save,
    reload: () => {
      setError(null);
      setRevision((value) => value + 1);
      void onRefresh();
    },
  };
}
