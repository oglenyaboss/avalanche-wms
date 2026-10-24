"use client";

import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";

import { createScanRegistry, type ScanTarget } from "./scan-registry";
import type { ScanSource } from "./types";

type ScanRegistry = ReturnType<typeof createScanRegistry>;

type ScanContextValue = Omit<ScanRegistry, "active"> & { active: ScanTarget | null };

const ScanContext = createContext<ScanContextValue | null>(null);

export function ScanProvider({ children }: { children: ReactNode }) {
  const [registry] = useState(() => createScanRegistry());
  const active = useSyncExternalStore(registry.subscribe, registry.active, registry.active);
  const value = useMemo(() => ({ ...registry, active }), [registry, active]);
  return <ScanContext.Provider value={value}>{children}</ScanContext.Provider>;
}

function useScanContext(): ScanContextValue {
  const value = useContext(ScanContext);
  if (!value) throw new Error("Поля скана и эмулятор работают только внутри ScanProvider");
  return value;
}

export function useScanRegistration({
  id,
  label,
  disabled = false,
  scan,
}: {
  id: string;
  label: string;
  disabled?: boolean;
  scan: (value: string, source: ScanSource) => void;
}): { activate: () => void } {
  const { register, activate } = useScanContext();

  useEffect(() => {
    if (disabled) return undefined;
    return register({ id, label, scan });
  }, [register, id, label, disabled, scan]);

  return { activate: () => activate(id) };
}

export function useScanDispatch(): {
  active: ScanTarget | null;
  dispatch: (value: string, source: ScanSource) => boolean;
} {
  const { active, dispatch } = useScanContext();
  return { active, dispatch };
}
