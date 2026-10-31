import { useEffect, useRef, useState } from "react";
import {
  getServicePackages,
  updateServicePackageStrategy,
  type PackageStrategy,
  type ServicePackage,
} from "./bridge";
import type { UsageEntry } from "./service-package-model";
import type { Service } from "./service-model";
import { useWorkspaceSnapshot } from "./workspace-snapshots";

/**
 * Core's packages and their in-package strategy. The list is read again when
 * a package may gain or lose members: services added or removed, or a plan
 * read. Right after Core starts, accounts sit in a package with no plan until
 * Core reads their usage, so a newer read of such an account asks again.
 */
export function useServicePackages(
  ready: boolean,
  services: Service[],
  usage: Record<string, UsageEntry | undefined>,
) {
  const [saved, cache] = useWorkspaceSnapshot<ServicePackage[] | null>(
    "service-packages",
    null,
  );
  const [packages, setPackages] = useState(saved);
  const unplanned = new Set(
    (packages ?? [])
      .filter((item) => item.kind === "subscription" && !item.plan_type)
      .flatMap((item) => item.service_ids),
  );
  const membership = services
    .map((service) => {
      const read = usage[service.id]?.usage;
      return [
        service.id,
        read?.plan_type ?? "",
        unplanned.has(service.id) ? (read?.fetched_at ?? "") : "",
      ].join(":");
    })
    .sort()
    .join("\0");
  const [error, setError] = useState<string | null>(null);
  const [strategyError, setStrategyError] = useState<{
    id: string;
    message: string;
  } | null>(null);
  const [savingID, setSavingID] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const generation = useRef(0);
  useEffect(() => {
    const current = ++generation.current;
    if (!ready) return;
    void getServicePackages()
      .then((value) => {
        if (current !== generation.current) return;
        setPackages(value);
        setError(null);
      })
      .catch((cause) => {
        if (current === generation.current) setError(String(cause));
      });
    return () => {
      generation.current++;
    };
  }, [ready, membership, revision]);
  useEffect(() => {
    if (packages) cache(packages);
  }, [packages, cache]);

  // Only the strategy is taken from a save: membership comes from the list,
  // so one package's newer members cannot overlap another's.
  const setOwnStrategy = (id: string, strategy: PackageStrategy) =>
    setPackages(
      (current) =>
        current?.map((item) =>
          item.id === id ? { ...item, strategy } : item,
        ) ?? null,
    );

  const setStrategy = async (id: string, strategy: PackageStrategy) => {
    const original = packages?.find((item) => item.id === id);
    if (!original || original.strategy === strategy || savingID) return;
    setSavingID(id);
    setStrategyError(null);
    setOwnStrategy(id, strategy);
    try {
      setOwnStrategy(
        id,
        (await updateServicePackageStrategy(id, strategy)).strategy,
      );
    } catch (cause) {
      setOwnStrategy(id, original.strategy);
      setStrategyError({ id, message: String(cause) });
      // The package may have been re-derived meanwhile, for example once its
      // plan was read; a fresh list shows the packages that exist now.
      setRevision((value) => value + 1);
    } finally {
      setSavingID(null);
    }
  };

  return {
    packages,
    error,
    strategyError,
    savingID,
    setStrategy,
    reload: () => {
      setError(null);
      setRevision((value) => value + 1);
    },
  };
}
