// lib/signedflow.js — общие куски подписанного потока (draft → P-256 подпись →
// submit) для функциональных скриптов 05–08. Эталон — 09-throughput.js.
import http from 'k6/http';
import { BASE_URL } from './config.js';
import { signDraft } from './signer.js';

// Путь к операторам с активными ключами (stress-provision). import.meta.resolve
// считается от файла модуля (lib/ → tests/stress/.signed), а не от cwd.
const OPERATORS_PATH = import.meta.resolve('../.signed/operators.json');

// loadOperators — читает operators.json. open() доступен только в init-контексте,
// поэтому вызывать только в глобальной области скрипта. Нет файла — пустой список
// (внятный аборт делает setup()).
export function loadOperators() {
  try {
    return JSON.parse(open(OPERATORS_PATH));
  } catch (_) {
    return [];
  }
}

// signedPost — draft → программная подпись частей → submit. Возвращает
// {draftRes, submitRes, draftError}: submitRes=null, если draft не 200 или без
// частей; draftError — код из error-конверта draft (например PICKING_NOT_CONFIRMED).
// tags (необязательный) ставятся на оба запроса, чтобы порог по step: покрывал пару.
export async function signedPost(H, signer, draftPath, submitPath, body, tags) {
  const opts = tags ? { headers: H, tags } : { headers: H };
  const draftRes = http.post(`${BASE_URL}${draftPath}`, JSON.stringify(body), opts);
  if (draftRes.status !== 200) {
    let draftError = null;
    try { draftError = (JSON.parse(draftRes.body).error || {}).code || null; } catch (_) {}
    return { draftRes, submitRes: null, draftError };
  }
  let draft = null;
  try { draft = JSON.parse(draftRes.body).data || null; } catch (_) {}
  if (!draft || !draft.parts || draft.parts.length === 0) {
    return { draftRes, submitRes: null, draftError: null };
  }
  const submitBody = await signDraft(signer, draft);
  const submitRes = http.post(`${BASE_URL}${submitPath}`, JSON.stringify(submitBody), opts);
  return { draftRes, submitRes, draftError: null };
}
