export type ScanSource = "keyboard" | "emulator" | "camera";

export type ScanCodeType = "ttn" | "cargoplace" | "box" | "sku" | "product" | "bin" | "buffer" | "dispatch";

export type ScanCode = { type: ScanCodeType; label: string; value: string };
