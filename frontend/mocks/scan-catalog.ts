import type { ScanCode } from "@/lib/scan/types";

export function scanCodes(): ScanCode[] {
  return [
    { type: "ttn", label: "ТТН-0001", value: "TTN-0001" },
    { type: "cargoplace", label: "Грузоместо CP-0001", value: "CP-0001" },
    { type: "box", label: "Коробка BOX-0001", value: "BOX-0001" },
    { type: "sku", label: "Ноутбук Lenovo X1", value: "4601234567890" },
    { type: "product", label: "QR-0001", value: "QR-0001" },
    { type: "bin", label: "Ячейка M2-A-03", value: "77777777-7777-7777-7777-777777777777" },
    { type: "buffer", label: "Буфер BUF-01", value: "66666666-6666-6666-6666-666666666666" },
    { type: "dispatch", label: "Рейс D-0001", value: "D-0001" },
  ];
}
