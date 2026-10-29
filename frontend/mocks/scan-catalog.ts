import type { ScanCode, ScanCodeType } from "@/lib/scan/types";

import { db } from "./db";

const bufferSections = new Set(["BUFFER", "SHIPPING_BUFFER"]);

const code = (type: ScanCodeType, label: string, value: string): ScanCode => ({ type, label, value });

export function scanCodes(): ScanCode[] {
  const state = db();
  const skuName = new Map(state.skus.map((sku) => [sku.sku_id, sku.name]));
  return [
    ...state.inbound_shipments.map((row) => code("ttn", row.ttn_code, row.ttn_code)),
    ...state.cargoplaces.flatMap((row) => [
      code("cargoplace", `Грузоместо ${row.cargoplace_code}`, row.cargoplace_code),
      code("cargoplace", `Грузоместо ${row.cargoplace_code} · id`, row.cargoplace_id),
    ]),
    ...state.boxes.map((row) => code("box", `Коробка ${row.box_barcode}`, row.box_barcode)),
    ...state.sku_barcodes.map((row) => code("sku", skuName.get(row.sku_id) ?? row.barcode, row.barcode)),
    ...state.products.map((row) => code("product", `${row.qr_code} · ${row.status}`, row.qr_code)),
    ...state.bins.map((row) =>
      bufferSections.has(row.section ?? "") ? code("buffer", `Буфер ${row.code}`, row.bin_id) : code("bin", `Ячейка ${row.code}`, row.bin_id),
    ),
    ...state.outbound_dispatches.map((row) => code("dispatch", `Рейс ${row.dispatch_code} · ${row.status}`, row.dispatch_code)),
  ];
}
