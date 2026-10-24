"use client";

import { useEffect, useState, type ReactNode } from "react";

const mocksEnabled = process.env.NEXT_PUBLIC_API_MOCKS !== "off";

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
