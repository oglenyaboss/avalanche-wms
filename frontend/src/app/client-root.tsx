"use client";

import { useEffect, useState, type ReactNode } from "react";

import { mocksEnabled } from "@/lib/env";

async function startMocks() {
  if (!mocksEnabled) return;
  const { worker } = await import("@mocks/browser");
  await worker.start({ onUnhandledFrame: "bypass", quiet: true });
}

export function ClientRoot({ children }: { children: ReactNode }) {
  const [ready, setReady] = useState(false);

  useEffect(() => {
    startMocks().then(() => setReady(true));
  }, []);

  return ready ? children : null;
}
