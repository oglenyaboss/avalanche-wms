import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function NotFound() {
  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-4 p-4 text-center">
      <h1 className="text-2xl font-semibold">Страница не найдена</h1>
      <p className="text-muted-foreground">Такого раздела в терминале нет.</p>
      <Button asChild size="lg">
        <Link href="/">На главную</Link>
      </Button>
    </main>
  );
}
