import type { ScanSource } from "./types";

export type ScanTarget = {
  id: string;
  label: string;
  scan: (value: string, source: ScanSource) => void;
};

export function createScanRegistry() {
  const targets = new Map<string, ScanTarget>();
  const listeners = new Set<() => void>();
  let activeId: string | null = null;

  const notify = () => {
    for (const listener of listeners) listener();
  };

  const active = (): ScanTarget | null => (activeId === null ? null : (targets.get(activeId) ?? null));

  return {
    register(target: ScanTarget): () => void {
      targets.set(target.id, target);
      activeId = target.id;
      notify();
      return () => {
        targets.delete(target.id);
        if (activeId === target.id) {
          const ids = [...targets.keys()];
          activeId = ids.length > 0 ? ids[ids.length - 1]! : null;
        }
        notify();
      };
    },
    activate(id: string): void {
      if (!targets.has(id)) return;
      activeId = id;
      notify();
    },
    active,
    dispatch(value: string, source: ScanSource): boolean {
      const target = active();
      if (!target) return false;
      target.scan(value, source);
      return true;
    },
    subscribe(listener: () => void): () => void {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}
