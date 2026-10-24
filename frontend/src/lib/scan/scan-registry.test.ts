import { describe, expect, it, vi } from "vitest";

import { createScanRegistry, type ScanTarget } from "./scan-registry";

function target(id: string): ScanTarget {
  return { id, label: `Поле ${id}`, scan: vi.fn() };
}

describe("createScanRegistry", () => {
  it("register делает поле активным, dispatch вызывает его scan", () => {
    const registry = createScanRegistry();
    const a = target("A");
    registry.register(a);
    expect(registry.active()).toBe(a);
    expect(registry.dispatch("X", "emulator")).toBe(true);
    expect(a.scan).toHaveBeenCalledWith("X", "emulator");
  });

  it("активным становится последнее зарегистрированное; activate переключает активность", () => {
    const registry = createScanRegistry();
    const a = target("A");
    const b = target("B");
    registry.register(a);
    registry.register(b);
    expect(registry.active()).toBe(b);
    registry.activate("A");
    expect(registry.active()).toBe(a);
    registry.dispatch("X", "emulator");
    expect(a.scan).toHaveBeenCalledTimes(1);
    expect(b.scan).not.toHaveBeenCalled();
  });

  it("после снятия активного поля активным становится последнее из оставшихся, затем null", () => {
    const registry = createScanRegistry();
    const a = target("A");
    const b = target("B");
    const unregisterA = registry.register(a);
    const unregisterB = registry.register(b);
    unregisterB();
    expect(registry.active()).toBe(a);
    unregisterA();
    expect(registry.active()).toBeNull();
    expect(registry.dispatch("X", "emulator")).toBe(false);
    expect(a.scan).not.toHaveBeenCalled();
    expect(b.scan).not.toHaveBeenCalled();
  });

  it("снятие неактивного поля не меняет активное", () => {
    const registry = createScanRegistry();
    const a = target("A");
    const b = target("B");
    registry.register(a);
    const unregisterB = registry.register(b);
    registry.activate("A");
    unregisterB();
    expect(registry.active()).toBe(a);
  });

  it("activate с неизвестным id игнорируется", () => {
    const registry = createScanRegistry();
    const a = target("A");
    registry.register(a);
    registry.activate("нет");
    expect(registry.active()).toBe(a);
  });

  it("повторный register с тем же id заменяет цель: одно снятие убирает её совсем", () => {
    const registry = createScanRegistry();
    const first = target("A");
    const second = target("A");
    const unregisterFirst = registry.register(first);
    registry.register(second);
    expect(registry.active()).toBe(second);
    unregisterFirst();
    expect(registry.active()).toBeNull();
  });

  it("register, activate и снятие уведомляют подписчика; после отписки — нет", () => {
    const registry = createScanRegistry();
    const a = target("A");
    const b = target("B");
    const listener = vi.fn();
    const unsubscribe = registry.subscribe(listener);
    const unregisterA = registry.register(a);
    registry.activate("A");
    const unregisterB = registry.register(b);
    unregisterB();
    expect(listener).toHaveBeenCalledTimes(4);
    unsubscribe();
    unregisterA();
    registry.register(b);
    expect(listener).toHaveBeenCalledTimes(4);
  });
});
