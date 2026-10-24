"use client";

import { Camera01Icon } from "@hugeicons/core-free-icons";
import { HugeiconsIcon } from "@hugeicons/react";
import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useScanRegistration } from "@/lib/scan/scan-context";
import type { ScanSource } from "@/lib/scan/types";

import { CameraScanner } from "./camera-scanner";

type ScanFieldProps = {
  label: string;
  onScan: (value: string, source: ScanSource) => void | Promise<unknown>;
  placeholder?: string;
  disabled?: boolean;
  autoFocus?: boolean;
};

export function ScanField({ label, onScan, placeholder, disabled = false, autoFocus = true }: ScanFieldProps) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const onScanRef = useRef(onScan);
  const pendingRef = useRef(false);
  const [busy, setBusy] = useState(false);
  const [cameraOpen, setCameraOpen] = useState(false);
  const [hasCamera] = useState(
    () => typeof navigator !== "undefined" && Boolean(navigator.mediaDevices?.getUserMedia),
  );

  useEffect(() => {
    onScanRef.current = onScan;
  });

  const handleValue = useCallback((raw: string, source: ScanSource) => {
    const value = raw.trim();
    if (!value || pendingRef.current) return;
    const result = onScanRef.current(value, source);
    const input = inputRef.current;
    if (input) {
      input.value = "";
      input.focus();
    }
    if (result instanceof Promise) {
      pendingRef.current = true;
      setBusy(true);
      const release = () => {
        pendingRef.current = false;
        setBusy(false);
      };
      result.then(release, release);
    }
  }, []);

  const { activate } = useScanRegistration({ id, label, disabled, scan: handleValue });

  useEffect(() => {
    if (autoFocus) inputRef.current?.focus();
  }, [autoFocus]);

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    handleValue(inputRef.current?.value ?? "", "keyboard");
  };

  return (
    <>
      <form className="flex flex-col gap-2" onSubmit={handleSubmit}>
        <Label htmlFor={id}>{label}</Label>
        <div className="flex items-center gap-2">
          <Input
            id={id}
            ref={inputRef}
            className="min-w-0 flex-1"
            placeholder={placeholder}
            disabled={disabled}
            aria-busy={busy || undefined}
            onFocus={activate}
          />
          {hasCamera && (
            <Button
              type="button"
              variant="outline"
              size="icon"
              aria-label="Сканировать камерой"
              disabled={disabled}
              onClick={() => setCameraOpen(true)}
            >
              <HugeiconsIcon icon={Camera01Icon} className="size-5" />
            </Button>
          )}
        </div>
      </form>
      <CameraScanner
        open={cameraOpen}
        onOpenChange={setCameraOpen}
        onDetected={(value) => handleValue(value, "camera")}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          inputRef.current?.focus();
        }}
      />
    </>
  );
}
