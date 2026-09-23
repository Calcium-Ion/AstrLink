import {
  createContext,
  useContext,
  useMemo,
  useSyncExternalStore,
  type Dispatch,
  type ReactNode,
  type SetStateAction,
} from "react";

class Snapshot<T> {
  private listeners = new Set<() => void>();

  constructor(private value: T) {}

  read = () => this.value;

  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  write: Dispatch<SetStateAction<T>> = (next) => {
    const value =
      typeof next === "function"
        ? (next as (current: T) => T)(this.value)
        : next;
    if (Object.is(value, this.value)) return;
    this.value = value;
    this.listeners.forEach((listener) => listener());
  };
}

type SnapshotScope = "core" | "desktop";
const WorkspaceSnapshots = createContext<Record<
  SnapshotScope,
  Map<string, unknown>
> | null>(null);

/** Keep display data across page mounts, never drafts, secrets or live effects. */
export function WorkspaceSnapshotProvider({
  sessionKey,
  children,
}: {
  sessionKey: string | null;
  children: ReactNode;
}) {
  // A new Core session must not inherit records or ETags from the old one.
  const desktop = useMemo(() => new Map<string, unknown>(), []);
  const core = useMemo(() => new Map<string, unknown>(), [sessionKey]);
  const snapshots = useMemo(() => ({ desktop, core }), [desktop, core]);
  return <WorkspaceSnapshots value={snapshots}>{children}</WorkspaceSnapshots>;
}

export function useWorkspaceSnapshot<T>(
  key: string,
  initial: T | (() => T),
  scope: SnapshotScope = "core",
): [T, Dispatch<SetStateAction<T>>] {
  const snapshots = useContext(WorkspaceSnapshots)?.[scope];
  const snapshot = useMemo(() => {
    const cached = snapshots?.get(key) as Snapshot<T> | undefined;
    if (cached) return cached;
    const created = new Snapshot(
      typeof initial === "function" ? (initial as () => T)() : initial,
    );
    snapshots?.set(key, created);
    return created;
    // Like useState, the initializer only runs for a new scope/key.
  }, [snapshots, key]);
  return [
    useSyncExternalStore(snapshot.subscribe, snapshot.read, snapshot.read),
    snapshot.write,
  ];
}
