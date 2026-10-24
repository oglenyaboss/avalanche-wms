"use client";

import { BarcodeScanIcon, Cancel01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import Link from "next/link";
import { useEffect, useMemo, useRef, useState, type FormEvent, type MouseEvent } from "react";

import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { useScanDispatch } from "@/lib/scan/scan-context";
import type { ScanCode, ScanCodeType } from "@/lib/scan/types";

const groupTitles: Record<ScanCodeType, string> = {
  ttn: "ТТН",
  cargoplace: "Грузоместа",
  box: "Коробки",
  sku: "Штрихкоды SKU",
  product: "Товары (QR)",
  bin: "Ячейки хранения",
  buffer: "Буферы",
  dispatch: "Рейсы",
};

const keepFocus = (event: MouseEvent<HTMLButtonElement>) => {
  event.preventDefault();
};

export function ScannerEmulator() {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button
        variant="ghost"
        size="icon-lg"
        aria-label="Эмулятор сканера"
        aria-pressed={open}
        onClick={() => setOpen((value) => !value)}
      >
        <HugeiconsIcon icon={BarcodeScanIcon} className="size-5" />
      </Button>
      {open && <EmulatorPanel onClose={() => setOpen(false)} />}
    </>
  );
}

function EmulatorPanel({ onClose }: { onClose: () => void }) {
  const { active, dispatch } = useScanDispatch();
  const [codes, setCodes] = useState<ScanCode[]>([]);
  const [search, setSearch] = useState("");
  const manualRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    if (codes.length > 0) return;
    let cancelled = false;
    void import("@mocks/scan-catalog").then(({ scanCodes }) => {
      if (!cancelled) setCodes(scanCodes());
    });
    return () => {
      cancelled = true;
    };
  }, [codes.length]);

  const groups = useMemo(() => {
    const query = search.trim().toLowerCase();
    const matched = (code: ScanCode) =>
      query.length === 0 || code.label.toLowerCase().includes(query) || code.value.toLowerCase().includes(query);
    return (Object.keys(groupTitles) as ScanCodeType[])
      .map((type) => ({
        type,
        title: groupTitles[type],
        codes: codes.filter((code) => code.type === type && matched(code)),
      }))
      .filter((group) => group.codes.length > 0);
  }, [codes, search]);

  const handleManual = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const input = manualRef.current;
    const value = input?.value ?? "";
    if (!active || value.trim().length === 0) return;
    dispatch(value, "emulator");
    if (input) input.value = "";
  };

  return (
    <aside aria-label="Эмулятор сканера" className="fixed bottom-4 right-4 z-40 w-80 max-sm:left-4 max-sm:w-auto">
      <Card size="sm" className="max-h-[70vh]">
        <div className="flex items-start justify-between gap-2 px-4">
          {active ? (
            <p className="text-sm">
              Сканирует в: <span className="font-medium">{active.label}</span>
            </p>
          ) : (
            <p className="text-sm text-muted-foreground">Нет активного поля скана</p>
          )}
          <Button variant="ghost" size="icon-sm" aria-label="Закрыть эмулятор" onClick={onClose}>
            <HugeiconsIcon icon={Cancel01Icon} className="size-4" />
          </Button>
        </div>
        <form onSubmit={handleManual} className="flex items-center gap-2 px-4">
          <Input aria-label="Код для скана" placeholder="Код вручную" className="min-w-0 flex-1" ref={manualRef} />
          <Button type="submit" variant="outline" disabled={!active} onMouseDown={keepFocus}>
            Сканировать
          </Button>
        </form>
        <div className="px-4">
          <Input
            aria-label="Поиск кода"
            placeholder="Поиск"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </div>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-4">
          {groups.map((group) => (
            <div key={group.type} className="flex flex-col gap-1.5">
              <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{group.title}</p>
              {group.codes.map((code) => (
                <Button
                  key={`${code.type}:${code.value}`}
                  variant="outline"
                  className="h-auto flex-col items-start gap-0.5 rounded-xl px-3 py-2 text-left"
                  disabled={!active}
                  onMouseDown={keepFocus}
                  onClick={() => dispatch(code.value, "emulator")}
                >
                  <span>{code.label}</span>
                  <span className="text-xs font-normal text-muted-foreground">{code.value}</span>
                </Button>
              ))}
            </div>
          ))}
        </div>
        <div className="px-4">
          <Link href="/scanner-check" className="text-sm text-primary underline-offset-4 hover:underline">
            Проверка сканера
          </Link>
        </div>
      </Card>
    </aside>
  );
}
