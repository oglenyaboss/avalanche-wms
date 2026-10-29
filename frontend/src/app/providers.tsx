"use client";

import dynamic from "next/dynamic";

export const Providers = dynamic(() => import("./client-root").then((m) => m.ClientRoot), { ssr: false });
