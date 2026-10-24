"use client";

import { useCallback, useRef, useState } from "react";

import { ScanField } from "@/components/scan/scan-field";
import type { ScanSource } from "@/lib/scan/types";

const sourceLabels: Record<ScanSource, string> = {
  keyboard: "сканер",
  emulator: "эмулятор",
  camera: "камера",
};

type ScanRecord = { id: number; value: string; source: ScanSource };

export default function ScannerCheckPage() {
  const [scans, setScans] = useState<ScanRecord[]>([]);
  const nextIdRef = useRef(0);

  const handleScan = useCallback((value: string, source: ScanSource) => {
    nextIdRef.current += 1;
    const id = nextIdRef.current;
    setScans((previous) => [{ id, value, source }, ...previous].slice(0, 10));
  }, []);

  return (
    <>
      <div className="flex flex-col gap-1">
        <h1 className="text-2xl font-semibold">Проверка сканера</h1>
        <p className="text-muted-foreground">Отсканируйте код сканером-клавиатурой, эмулятором или камерой.</p>
      </div>
      <div className="flex max-w-md flex-col gap-4">
        <ScanField label="Код" onScan={handleScan} />
        <ol aria-label="Последние сканы" className="flex flex-col gap-2">
          {scans.map((scan) => (
            <li key={scan.id} className="rounded-xl border bg-card px-3 py-2 text-sm">
              {scan.value} — {sourceLabels[scan.source]}
            </li>
          ))}
        </ol>
      </div>
    </>
  );
}
