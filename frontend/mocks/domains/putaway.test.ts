import { describe, expect, it } from "vitest";

import { api, unwrap } from "@/lib/api/client";
import { itemId } from "@/lib/signing/adr";
import { signAndSubmit, type SignedSubmit } from "@/lib/signing/sign-and-submit";

import { setChainMode } from "../chain";
import { putawayIds } from "../data/fixtures/putaway";
import { idGroup, mockId, seedIds, seededCredentialIds } from "../data/fixture-kit";
import { loginAs, setupMockApi, waitForChain } from "../testing";
import { setAuthenticatorMode } from "../virtual-authenticator";

setupMockApi();

const buffer = seedIds.bins["BUFFER-01"];
const storage = seedIds.bins["A-01-01"];
const p = putawayIds.products;
const storedProduct = mockId(idGroup.product, 3);
const unknownProduct = mockId(idGroup.product, 999999);
const unknownBin = mockId(idGroup.bin, 999999);

const scanBuffer = (buffer_bin_id: string) => unwrap(api.POST("/putaway/scan-buffer", { body: { buffer_bin_id } }));
const scanProduct = (product_id: string) => unwrap(api.POST("/putaway/scan-product", { body: { product_id, buffer_bin_id: buffer } }));
const draft = (product_ids: string[], storage_bin_id = storage) =>
  unwrap(api.POST("/putaway/scan-storage-bin/draft", { body: { product_ids, storage_bin_id } }));
const submit = (body: SignedSubmit) => unwrap(api.POST("/putaway/scan-storage-bin/submit", { body }));
const place = async (productIds: string[]) => signAndSubmit(await draft(productIds), submit);
const status = (operation_id: string) => unwrap(api.GET("/signed-actions/operations/{operation_id}", { params: { path: { operation_id } } }));

describe("сканирование буфера и товара", () => {
  it("буфер отдаёт только RECEIVED товары по времени создания", async () => {
    await loginAs("operator");
    const result = await scanBuffer(buffer);
    expect(result.buffer_code).toBe("BUFFER-01");
    expect(result.products.map((product) => product.qr_code)).toEqual(["QR-PUT-0001", "QR-PUT-0002", "QR-PUT-0003", "QR-PUT-0004", "QR-PUT-0005"]);
    expect(result.total_products).toBe(5);
  });

  it("ячейка хранения вместо буфера — BUFFER_BIN_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(scanBuffer(storage)).rejects.toMatchObject({ status: 404, code: "BUFFER_BIN_NOT_FOUND", message: "Буферная ячейка не найдена" });
  });

  it("не UUID — INVALID_REQUEST с именем поля", async () => {
    await loginAs("operator");
    await expect(scanBuffer("BUFFER-01")).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "buffer_bin_id должен быть UUID" });
  });

  it("лишнее поле в теле — INVALID_REQUEST", async () => {
    await loginAs("operator");
    const response = unwrap(api.POST("/putaway/scan-buffer", { body: { buffer_bin_id: buffer, extra: 1 } as never }));
    await expect(response).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message: "Невалидный JSON в теле запроса" });
  });

  it("админ — FORBIDDEN, раскладка только для оператора", async () => {
    await loginAs("admin");
    await expect(scanBuffer(buffer)).rejects.toMatchObject({ status: 403, code: "FORBIDDEN", message: "Только оператор может выполнять это действие" });
  });

  it("товар из буфера добавляется, cart_size — все RECEIVED в буфере", async () => {
    await loginAs("operator");
    await expect(scanProduct(p["QR-PUT-0001"])).resolves.toEqual({
      product_id: p["QR-PUT-0001"],
      sku_name: "Кроссовки Nike Air Max 90",
      qr_code: "QR-PUT-0001",
      cart_size: 5,
    });
  });

  it.each([
    ["неизвестный товар", unknownProduct, 404, "PRODUCT_NOT_FOUND"],
    ["товар в ячейке хранения", storedProduct, 409, "PRODUCT_NOT_IN_BUFFER"],
    ["товар в буфере не в RECEIVED", p["QR-PUT-0006"], 409, "PRODUCT_NOT_RECEIVED"],
  ])("скан товара: %s — %s", async (_, productId, httpStatus, code) => {
    await loginAs("operator");
    await expect(scanProduct(productId)).rejects.toMatchObject({ status: httpStatus, code });
  });

  it("неизвестная буферная ячейка — BUFFER_BIN_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(scanBuffer(unknownBin)).rejects.toMatchObject({ status: 404, code: "BUFFER_BIN_NOT_FOUND", message: "Буферная ячейка не найдена" });
    await expect(unwrap(api.POST("/putaway/scan-product", { body: { product_id: p["QR-PUT-0001"], buffer_bin_id: unknownBin } }))).rejects.toMatchObject({
      status: 404,
      code: "BUFFER_BIN_NOT_FOUND",
      message: "Буферная ячейка не найдена",
    });
  });
});

describe("черновик размещения", () => {
  it("дубликаты схлопываются, товары по возрастанию itemId, подпись ключом оператора", async () => {
    await loginAs("operator");
    const result = await draft([p["QR-PUT-0001"], p["QR-PUT-0002"], p["QR-PUT-0001"]]);
    expect(result.action_type).toBe(2);
    expect(result.allow_credentials).toEqual([{ type: "public-key", id: seededCredentialIds.operator }]);
    expect(result.parts).toHaveLength(1);
    const expected = [p["QR-PUT-0001"], p["QR-PUT-0002"]].sort((a, b) => (itemId(a) < itemId(b) ? -1 : 1));
    expect(result.parts[0].item_ids).toEqual(expected);
  });

  it.each([
    ["пустой список", [], "product_ids не может быть пустым"],
    ["больше 200 товаров", Array.from({ length: 201 }, () => p["QR-PUT-0001"]), "product_ids не может содержать более 200 элементов"],
  ])("%s — INVALID_REQUEST", async (_, productIds, message) => {
    await loginAs("operator");
    await expect(draft(productIds)).rejects.toMatchObject({ status: 400, code: "INVALID_REQUEST", message });
  });

  it.each([
    ["буфер приёмки", seedIds.bins["BUFFER-01"]],
    ["буфер отгрузки", seedIds.bins["BIN-SHIP-5"]],
    ["ячейка с нулевым объёмом", putawayIds.zeroVolumeBin],
  ])("ячейка-цель %s — STORAGE_BIN_NOT_FOUND", async (_, binId) => {
    await loginAs("operator");
    await expect(draft([p["QR-PUT-0001"]], binId)).rejects.toMatchObject({ status: 404, code: "STORAGE_BIN_NOT_FOUND" });
  });

  it.each([
    ["товар не найден", unknownProduct, 404, "PRODUCT_NOT_FOUND"],
    ["товар не в буфере", storedProduct, 409, "PRODUCT_NOT_IN_BUFFER"],
    ["товар не RECEIVED", p["QR-PUT-0006"], 409, "PRODUCT_NOT_RECEIVED"],
    ["грузоместо не закрыто", p["QR-PUT-0005"], 409, "CARGOPLACE_NOT_CLOSED"],
    ["приёмка FAILED в цепи", p["QR-PUT-0004"], 409, "CHAIN_EVENT_REJECTED"],
  ])("%s — %s", async (_, productId, httpStatus, code) => {
    await loginAs("operator");
    await expect(draft([p["QR-PUT-0001"], productId])).rejects.toMatchObject({ status: httpStatus, code });
  });

  it("у оператора нет ключа — SIGNER_KEY_NOT_ACTIVE", async () => {
    await loginAs("operator2");
    await expect(draft([p["QR-PUT-0001"]])).rejects.toMatchObject({ status: 409, code: "SIGNER_KEY_NOT_ACTIVE", message: "Ключ оператора не активен в реестре" });
  });

  it("неизвестная ячейка-цель — STORAGE_BIN_NOT_FOUND", async () => {
    await loginAs("operator");
    await expect(draft([p["QR-PUT-0001"]], unknownBin)).rejects.toMatchObject({ status: 404, code: "STORAGE_BIN_NOT_FOUND", message: "Ячейка хранения не найдена" });
  });
});

describe("подпись и отправка", () => {
  it("размещает товары, пишет действие в цепь, статус SENT → APPLIED", async () => {
    await loginAs("operator");
    const result = await place([p["QR-PUT-0001"], p["QR-PUT-0002"]]);
    expect(result).toMatchObject({ storage_bin_id: storage, storage_bin_code: "A-01-01", products_placed: 2, outbox_events_created: 1 });
    expect((await scanBuffer(buffer)).total_products).toBe(3);
    await expect(scanProduct(p["QR-PUT-0001"])).rejects.toMatchObject({ code: "PRODUCT_NOT_IN_BUFFER" });

    expect((await status(result.operation_id)).parts.map((part) => part.state)).toEqual(["SENT"]);
    await waitForChain();
    expect((await status(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "APPLIED", reject_reason: null })]);
  });

  it("повторная отправка — OPERATION_ALREADY_SUBMITTED", async () => {
    await loginAs("operator");
    const operation = await draft([p["QR-PUT-0001"]]);
    let body: SignedSubmit | undefined;
    await signAndSubmit(operation, (signed) => {
      body = signed;
      return submit(signed);
    });
    await expect(submit(body!)).rejects.toMatchObject({ status: 409, code: "OPERATION_ALREADY_SUBMITTED" });
  });

  it("подпись не сходится — INVALID_ASSERTION, товары остаются в буфере", async () => {
    await loginAs("operator");
    setAuthenticatorMode("corrupt");
    await expect(place([p["QR-PUT-0001"]])).rejects.toMatchObject({ status: 400, code: "INVALID_ASSERTION", message: "Подтверждение не прошло проверку" });
    expect((await scanBuffer(buffer)).total_products).toBe(5);
  });

  it("товар ушёл из буфера между черновиком и отправкой — DRAFT_STALE", async () => {
    await loginAs("operator");
    const stale = await draft([p["QR-PUT-0001"], p["QR-PUT-0002"]]);
    await place([p["QR-PUT-0001"]]);
    await expect(signAndSubmit(stale, submit)).rejects.toMatchObject({ status: 409, code: "DRAFT_STALE" });
    expect((await scanBuffer(buffer)).products.map((product) => product.qr_code)).toContain("QR-PUT-0002");
  });

  it("чужая или несуществующая операция — OPERATION_NOT_FOUND", async () => {
    await loginAs("operator");
    const operation = await draft([p["QR-PUT-0001"]]);
    await expect(submit({ operation_id: "01929c5e-0000-7000-800b-999999999999", assertions: [] })).rejects.toMatchObject({ status: 404, code: "OPERATION_NOT_FOUND" });
    await loginAs("admin");
    await expect(status(operation.operation_id)).rejects.toMatchObject({ status: 404, code: "OPERATION_NOT_FOUND" });
  });

  it("статус неотправленной операции — OPERATION_NOT_SUBMITTED", async () => {
    await loginAs("operator");
    const operation = await draft([p["QR-PUT-0001"]]);
    await expect(status(operation.operation_id)).rejects.toMatchObject({ status: 409, code: "OPERATION_NOT_SUBMITTED" });
  });

  it("цепь отклонила — REJECTED с кодом причины", async () => {
    await loginAs("operator");
    setChainMode("reject:12");
    const result = await place([p["QR-PUT-0001"]]);
    await waitForChain();
    expect((await status(result.operation_id)).parts).toEqual([expect.objectContaining({ state: "REJECTED", reject_reason: 12 })]);
  });
});
