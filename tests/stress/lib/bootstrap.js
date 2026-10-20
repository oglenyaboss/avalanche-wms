// lib/bootstrap.js — подготовка товаров с реальной on-chain историей.
//
// Товары, вставленные в БД напрямую (stress-seed.sql), на контракте остаются
// Status.None → PutAway/Pick/Ship отклоняются (WrongItemStatus). Единственный
// легальный путь их «оживить» — пройти подписанный receiving/putaway:
//   receiveProducts — грузоместо → scan-* → подписанный close-cargoplace (Receive: None→Accepted)
//   putawayProducts — буфер → scan-product → подписанный scan-storage-bin (PutAway: Accepted→PutAway)
// Последовательность вызовов 1-в-1 как в 07-full-flow.js / 09-throughput.js.
// Новых эндпоинтов нет; async — signDraft/signedPost возвращают Promise, поэтому
// звать только из setup() (в k6 1.8 setup может быть async).
import http from 'k6/http';
import { BASE_URL } from './config.js';
import { signedPost } from './signedflow.js';

// Максимум товаров на одно подписанное действие (WMS-лимит — 2000; берём с запасом).
const PUTAWAY_CHUNK = 200;

// data — .data из JSON-конверта WMS, null если тело не разобралось.
function data(res) {
  try { return JSON.parse(res.body).data || null; } catch (_) { return null; }
}

// receiveProducts — создаёт товары через receiving table и подписанный close-cargoplace.
// Возвращает product_id только тех грузомест, чей close-cargoplace/submit вернул 200:
// у остальных Receive не записан, товары не имеют цепочечной истории.
// opts: {receivingBinId, barcode, qrPrefix, perCp}
export async function receiveProducts(H, signer, cargoplaceIds, opts) {
  const { receivingBinId, barcode, qrPrefix, perCp } = opts;
  const productIds = [];

  for (let cpIndex = 0; cpIndex < cargoplaceIds.length; cpIndex++) {
    const cargoplaceId = cargoplaceIds[cpIndex];
    const boxBarcode = `${qrPrefix}-BOX-${cpIndex}`;
    const cpProducts = [];

    const cpRes = http.post(`${BASE_URL}/receiving/table/scan-cargoplace`,
      JSON.stringify({ cargoplace_id: cargoplaceId }), { headers: H });
    if (cpRes.status !== 200) {
      console.warn(`bootstrap: scan-cargoplace ${cargoplaceId} → ${cpRes.status}`);
      continue;
    }

    const boxRes = http.post(`${BASE_URL}/receiving/table/scan-box`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_barcode: boxBarcode }), { headers: H });
    const boxId = boxRes.status === 200 ? (data(boxRes) || {}).box_id : null;
    if (!boxId) {
      console.warn(`bootstrap: scan-box ${boxBarcode} → ${boxRes.status}`);
      continue;
    }

    const skuRes = http.post(`${BASE_URL}/receiving/table/scan-sku`,
      JSON.stringify({ cargoplace_id: cargoplaceId, box_id: boxId, barcode }), { headers: H });
    const skuId = skuRes.status === 200 ? (data(skuRes) || {}).sku_id : null;
    if (!skuId) {
      console.warn(`bootstrap: scan-sku ${barcode} → ${skuRes.status}`);
      continue;
    }

    let scanned = true;
    for (let i = 0; i < perCp; i++) {
      const qrRes = http.post(`${BASE_URL}/receiving/table/scan-qr`,
        JSON.stringify({
          cargoplace_id: cargoplaceId,
          box_id: boxId,
          sku_id: skuId,
          qr_code: `${qrPrefix}-${cpIndex}-${i}`,
        }), { headers: H });
      const productId = qrRes.status === 200 ? (data(qrRes) || {}).product_id : null;
      if (!productId) {
        console.warn(`bootstrap: scan-qr ${qrPrefix}-${cpIndex}-${i} → ${qrRes.status}`);
        scanned = false;
        break;
      }
      cpProducts.push(productId);
    }
    if (!scanned) continue;

    http.post(`${BASE_URL}/receiving/table/close-box`,
      JSON.stringify({ box_id: boxId }), { headers: H });

    const bufRes = http.post(`${BASE_URL}/receiving/table/scan-buffer`,
      JSON.stringify({ cargoplace_id: cargoplaceId, buffer_bin_id: receivingBinId }), { headers: H });
    if (bufRes.status !== 200) {
      console.warn(`bootstrap: scan-buffer ${cargoplaceId} → ${bufRes.status}`);
      continue;
    }

    const close = await signedPost(H, signer,
      '/receiving/table/close-cargoplace/draft', '/receiving/table/close-cargoplace/submit',
      { cargoplace_id: cargoplaceId });
    if (!close.submitRes || close.submitRes.status !== 200) {
      const code = close.draftError
        || (close.submitRes && close.submitRes.status)
        || (close.draftRes && close.draftRes.status);
      console.warn(`bootstrap: close-cargoplace ${cargoplaceId} → ${code}`);
      continue;
    }

    for (const productId of cpProducts) productIds.push(productId);
  }

  return productIds;
}

// putawayProducts — размещает RECEIVED-товары из буфера в ячейку хранения
// подписанными scan-storage-bin. Возвращает число размещённых товаров —
// сумму products_placed по частям (чанки по PUTAWAY_CHUNK).
// opts: {receivingBinId, storageBinId}
export async function putawayProducts(H, signer, productIds, opts) {
  const { receivingBinId, storageBinId } = opts;

  http.post(`${BASE_URL}/putaway/scan-buffer`,
    JSON.stringify({ buffer_bin_id: receivingBinId }), { headers: H });

  const inCart = [];
  for (const productId of productIds) {
    const res = http.post(`${BASE_URL}/putaway/scan-product`,
      JSON.stringify({ product_id: productId, buffer_bin_id: receivingBinId }), { headers: H });
    if (res.status === 200) inCart.push(productId);
    else console.warn(`bootstrap: scan-product ${productId} → ${res.status}`);
  }

  let placed = 0;
  for (let i = 0; i < inCart.length; i += PUTAWAY_CHUNK) {
    const chunk = inCart.slice(i, i + PUTAWAY_CHUNK);
    const store = await signedPost(H, signer,
      '/putaway/scan-storage-bin/draft', '/putaway/scan-storage-bin/submit',
      { product_ids: chunk, storage_bin_id: storageBinId });
    if (!store.submitRes || store.submitRes.status !== 200) {
      const code = store.draftError
        || (store.submitRes && store.submitRes.status)
        || (store.draftRes && store.draftRes.status);
      console.warn(`bootstrap: scan-storage-bin chunk ${i} → ${code}`);
      continue;
    }
    const chunkPlaced = (data(store.submitRes) || {}).products_placed || 0;
    placed += chunkPlaced;
  }

  return placed;
}
