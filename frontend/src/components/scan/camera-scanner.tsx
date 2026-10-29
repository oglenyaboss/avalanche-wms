"use client";

import { useEffect, useRef, useState } from "react";

import type { BarcodeDetector as BarcodeDetectorPonyfill, BarcodeFormat } from "barcode-detector/ponyfill";

import { Alert, AlertTitle } from "@/components/ui/alert";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";

const scanFormats: BarcodeFormat[] = ["qr_code", "code_128", "ean_13", "data_matrix"];

type BarcodeDetectorLike = Pick<BarcodeDetectorPonyfill, "detect">;
type BarcodeDetectorCtor = new (options?: { formats?: BarcodeFormat[] }) => BarcodeDetectorLike;

export function CameraScanner({
  open,
  onOpenChange,
  onDetected,
  onCloseAutoFocus,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onDetected: (value: string) => void;
  onCloseAutoFocus: (event: Event) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="bottom" className="mx-auto max-w-xl" onCloseAutoFocus={onCloseAutoFocus}>
        <SheetHeader>
          <SheetTitle>Сканирование камерой</SheetTitle>
          <SheetDescription>Наведите камеру на штрихкод или QR-код</SheetDescription>
        </SheetHeader>
        <CameraView onDetected={onDetected} onOpenChange={onOpenChange} />
      </SheetContent>
    </Sheet>
  );
}

function CameraView({
  onDetected,
  onOpenChange,
}: {
  onDetected: (value: string) => void;
  onOpenChange: (open: boolean) => void;
}) {
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const onDetectedRef = useRef(onDetected);
  const onOpenChangeRef = useRef(onOpenChange);
  const [accessDenied, setAccessDenied] = useState(false);

  useEffect(() => {
    onDetectedRef.current = onDetected;
  });
  useEffect(() => {
    onOpenChangeRef.current = onOpenChange;
  });

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setInterval> | undefined;
    let stream: MediaStream | null = null;

    const teardown = () => {
      if (timer !== undefined) clearInterval(timer);
      timer = undefined;
      stream?.getTracks().forEach((track) => track.stop());
      stream = null;
    };

    const detect = async (detector: BarcodeDetectorLike) => {
      const video = videoRef.current;
      if (!video) return;
      const results = await detector.detect(video).catch(() => []);
      if (stopped) return;
      const first = results.find((code) => code.rawValue);
      if (!first) return;
      stopped = true;
      teardown();
      onDetectedRef.current(first.rawValue);
      onOpenChangeRef.current(false);
    };

    const start = async () => {
      let media: MediaStream;
      try {
        media = await navigator.mediaDevices.getUserMedia({ video: { facingMode: "environment" } });
      } catch {
        if (!stopped) setAccessDenied(true);
        return;
      }
      if (stopped) {
        media.getTracks().forEach((track) => track.stop());
        return;
      }
      stream = media;
      const video = videoRef.current;
      if (!video) {
        teardown();
        return;
      }
      video.srcObject = media;
      await video.play().catch(() => undefined);
      // ponytail: wasm zxing грузится с CDN по умолчанию; для офлайн-терминала — prepareZXingModule с локальным файлом
      const Detector =
        (globalThis as { BarcodeDetector?: BarcodeDetectorCtor }).BarcodeDetector ??
        (await import("barcode-detector/ponyfill")).BarcodeDetector;
      if (stopped) {
        teardown();
        return;
      }
      const detector = new Detector({ formats: scanFormats });
      timer = setInterval(() => {
        void detect(detector);
      }, 250);
    };

    void start();
    return teardown;
  }, []);

  return (
    <div className="flex flex-col gap-3 px-6 pb-6">
      {accessDenied ? (
        <Alert variant="destructive">
          <AlertTitle>Нет доступа к камере</AlertTitle>
        </Alert>
      ) : (
        <video ref={videoRef} playsInline muted autoPlay className="aspect-video w-full rounded-xl bg-muted object-cover" />
      )}
    </div>
  );
}
