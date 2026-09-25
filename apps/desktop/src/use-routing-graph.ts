import { useCallback, useEffect, useRef, useState } from "react";
import { getRoutingGraph, saveRoutingGraph } from "./bridge";
import { useWorkspaceSnapshot } from "./workspace-snapshots";
import {
  emptyGraph,
  type GraphDocument,
  type GraphLayout,
  type RoutingGraph,
} from "./routing-graph-model";

export interface GraphDraft {
  graph: RoutingGraph;
  layout: GraphLayout;
}
const signature = (draft: GraphDraft) => JSON.stringify(draft);

export function useRoutingGraph(
  ready: boolean,
  onDirtyChange: (dirty: boolean) => void,
) {
  // Cache acknowledged documents only; unpersisted edits remain local.
  const [cached, setCached] = useWorkspaceSnapshot<GraphDocument | null>(
    "routing-graph",
    null,
  );
  const [document, setDocument] = useState<GraphDocument | null>(cached);
  const [draft, setDraft] = useState<GraphDraft>(() => ({
    graph: cached?.draft ?? emptyGraph(),
    layout: cached?.layout ?? {},
  }));
  const [baseline, setBaseline] = useState(() =>
    cached ? signature({ graph: cached.draft, layout: cached.layout }) : "",
  );
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [past, setPast] = useState<GraphDraft[]>([]);
  const [future, setFuture] = useState<GraphDraft[]>([]);
  const latest = useRef(draft);
  latest.current = draft;
  const docRef = useRef(document);
  docRef.current = document;
  const busy = useRef(false);
  const loadVersion = useRef(0);
  const mutationVersion = useRef(0);
  const mounted = useRef(true);
  const dirty = document !== null && signature(draft) !== baseline;
  const unpublished =
    document !== null &&
    JSON.stringify(draft.graph) !== JSON.stringify(document.active);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      loadVersion.current++;
    };
  }, []);
  useEffect(() => {
    onDirtyChange(dirty);
    return () => onDirtyChange(false);
  }, [dirty, onDirtyChange]);

  const reload = useCallback(async () => {
    const version = ++loadVersion.current;
    const mutations = mutationVersion.current;
    try {
      const doc = await getRoutingGraph();
      if (
        !mounted.current ||
        version !== loadVersion.current ||
        mutations !== mutationVersion.current
      )
        return;
      const loaded = { graph: doc.draft, layout: doc.layout };
      docRef.current = doc;
      latest.current = loaded;
      setDocument(doc);
      setCached(doc);
      setDraft(loaded);
      setBaseline(signature(loaded));
      setPast([]);
      setFuture([]);
      setError(null);
    } catch (error) {
      if (mounted.current && version === loadVersion.current)
        setError(String(error));
    }
  }, [setCached]);
  useEffect(() => {
    if (ready) void reload();
  }, [ready, reload]);

  const persist = useCallback(
    async (apply = false) => {
      if (!ready || !docRef.current || busy.current) return false;
      busy.current = true;
      mutationVersion.current++;
      setSaving(true);
      setError(null);
      const submitted = latest.current,
        version = loadVersion.current;
      try {
        const saved = await saveRoutingGraph(
          submitted.graph,
          submitted.layout,
          docRef.current.etag,
          apply,
        );
        if (!mounted.current || loadVersion.current !== version) return false;
        docRef.current = saved;
        setDocument(saved);
        setCached(saved);
        const acknowledged = { graph: saved.draft, layout: saved.layout };
        // Go omits empty optional fields. Adopt its canonical document only if
        // no newer edit exists; otherwise the next save keeps those newer edits.
        if (latest.current === submitted) {
          latest.current = acknowledged;
          setDraft(acknowledged);
        }
        setBaseline(signature(acknowledged));
        return true;
      } catch (error) {
        if (mounted.current) setError(String(error));
        return false;
      } finally {
        busy.current = false;
        if (mounted.current) setSaving(false);
      }
    },
    [ready, setCached],
  );

  useEffect(() => {
    if (!dirty || saving || error || !ready) return;
    const timer = setTimeout(() => void persist(), 600);
    return () => clearTimeout(timer);
  }, [dirty, draft, saving, error, ready, persist]);

  const edit = useCallback(
    (change: (draft: GraphDraft) => GraphDraft, remember = true) => {
      const previous = latest.current,
        next = change(previous);
      if (signature(previous) === signature(next)) return;
      mutationVersion.current++;
      if (remember) {
        setPast((items) => [...items.slice(-49), previous]);
        setFuture([]);
      }
      latest.current = next;
      setDraft(next);
      setError(null);
    },
    [],
  );
  const undo = () => {
    const previous = past.at(-1);
    if (!previous) return;
    mutationVersion.current++;
    setFuture((items) => [latest.current, ...items]);
    setPast(past.slice(0, -1));
    latest.current = previous;
    setDraft(previous);
    setError(null);
  };
  const redo = () => {
    const next = future[0];
    if (!next) return;
    mutationVersion.current++;
    setPast((items) => [...items, latest.current]);
    setFuture(future.slice(1));
    latest.current = next;
    setDraft(next);
    setError(null);
  };
  return {
    document,
    draft,
    dirty,
    unpublished,
    saving,
    error,
    setError,
    edit,
    undo,
    redo,
    canUndo: past.length > 0,
    canRedo: future.length > 0,
    apply: () => persist(true),
    retry: () => persist(),
    reload,
  };
}
