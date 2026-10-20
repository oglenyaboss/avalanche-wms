// signer-selftest.js — офлайн-проверка программного подписанта (lib/signer.js)
// против testdata/signed-actions/vectors.v1.json. Стек не нужен.
//
// Проверки по первому действию с webauthn:
//   (a) наш authenticatorData == вектору (rpIdHash ‖ флаги ‖ signCount);
//   (b) наши байты clientDataJSON == clientDataJSONHex вектора;
//   (c) собственная подпись вектора (raw r‖s) верифицируется WebCrypto по
//       открытом ключу вектора над authData ‖ sha256(cdj) — доказывает, что мы
//       собираем ровно те байты, что подписывал генератор векторов;
//   (d) наша свежая подпись (DER → raw) верифицируется тем же ключом;
//   (e) rawToDer на фиксированном входе даёт точные DER-байты (INTEGER r с
//       0x00-префиксом при старшем байте ≥ 0x80, INTEGER s без ведущих нулей).
import { check } from 'k6';
import encoding from 'k6/encoding';
import { importSigner, signChallenge, rawToDer } from './lib/signer.js';

const VECTORS = JSON.parse(open('../../testdata/signed-actions/vectors.v1.json'));

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ['rate==1'],
  },
};

function hexToBytes(hex) {
  const h = hex.startsWith('0x') ? hex.slice(2) : hex;
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(h.substr(i * 2, 2), 16);
  return out;
}

function bytesToHex(bytes) {
  let s = '';
  for (let i = 0; i < bytes.length; i++) s += bytes[i].toString(16).padStart(2, '0');
  return s;
}

function b64urlDecode(s) {
  return new Uint8Array(encoding.b64decode(s, 'rawurl'));
}

function hexB64(hex) {
  return encoding.b64encode(hexToBytes(hex).buffer, 'rawurl');
}

// derToRaw — DER SEQUENCE{INTEGER, INTEGER} → raw r‖s (только короткая форма
// длины — у P-256 сигнатур длины всегда < 128). INTEGER может нести 0x00-префикс
// знака (33 байта) — его срезаем и выравниваем значение вправо в 32 байта.
function derToRaw(der) {
  const readInt = (at) => {
    const len = der[at + 1];
    let v = der.slice(at + 2, at + 2 + len);
    let start = 0;
    while (start < v.length - 1 && v[start] === 0) start++;
    v = v.slice(start);
    const out = new Uint8Array(32);
    out.set(v, 32 - v.length);
    return [out, at + 2 + len];
  };
  const [r, sAt] = readInt(2);
  const [s] = readInt(sAt);
  const raw = new Uint8Array(64);
  raw.set(r, 0);
  raw.set(s, 32);
  return raw;
}

// (e) фиксированный вход: r со старшим битом (нужен 0x00-префикс),
// s с двумя ведущими нулевыми байтами (срезаются, префикс не нужен).
const RAW_E = new Uint8Array(64);
RAW_E[0] = 0x80;
RAW_E[31] = 0x01;
RAW_E[32] = 0x00;
RAW_E[33] = 0x00;
RAW_E[34] = 0x7f;
for (let k = 0; k < 29; k++) RAW_E[35 + k] = k + 1; // 0x01..0x1d
const DER_E_HEX = '3043' +
  '022100' + '80' + '00'.repeat(30) + '01' +
  '021e' + '7f' + '0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d';

export default async function () {
  const vec = VECTORS.actions.find((a) => a.webauthn);
  const signerVec = VECTORS.signer;

  // Вектор хранит приватный скаляр + координаты — собираем из них JWK-оператора.
  const op = {
    credentialId: hexB64('0x01'),
    jwk: {
      kty: 'EC',
      crv: 'P-256',
      x: hexB64(signerVec.qx),
      y: hexB64(signerVec.qy),
      d: hexB64(signerVec.privateKey),
    },
  };
  const signer = await importSigner(op);
  const ours = await signChallenge(signer, hexB64(vec.challenge));

  const authData = b64urlDecode(ours.authenticator_data);
  const cdjBytes = b64urlDecode(ours.client_data_json);
  const clientHash = new Uint8Array(await crypto.subtle.digest('SHA-256', cdjBytes.buffer));
  const signedData = new Uint8Array(authData.length + clientHash.length);
  signedData.set(authData, 0);
  signedData.set(clientHash, authData.length);

  const pub = await crypto.subtle.importKey(
    'jwk',
    { kty: 'EC', crv: 'P-256', x: hexB64(signerVec.qx), y: hexB64(signerVec.qy) },
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['verify'],
  );

  const vecRawSig = new Uint8Array(64);
  vecRawSig.set(hexToBytes(vec.webauthn.r), 0);
  vecRawSig.set(hexToBytes(vec.webauthn.s), 32);
  const ourRawSig = derToRaw(b64urlDecode(ours.signature));

  const authOk = bytesToHex(authData) === vec.webauthn.authenticatorData.replace(/^0x/, '');
  const cdjOk = bytesToHex(cdjBytes) === vec.webauthn.clientDataJSONHex.replace(/^0x/, '');
  let vecSigOk = false;
  let ourSigOk = false;
  try {
    vecSigOk = await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, pub, vecRawSig.buffer, signedData.buffer);
  } catch (e) {
    console.warn('vector signature verify failed:', e.message);
  }
  try {
    ourSigOk = await crypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, pub, ourRawSig.buffer, signedData.buffer);
  } catch (e) {
    console.warn('our signature verify failed:', e.message);
  }
  const derOk = bytesToHex(rawToDer(RAW_E)) === DER_E_HEX;
  if (!authOk) console.warn('authData:', bytesToHex(authData));
  if (!cdjOk) console.warn('cdj:', bytesToHex(cdjBytes));
  if (!derOk) console.warn('der:', bytesToHex(rawToDer(RAW_E)), 'want', DER_E_HEX);

  check(null, {
    'authenticatorData == вектору': () => authOk,
    'clientDataJSON == вектору': () => cdjOk,
    'подпись вектора верифицируется по нашим байтам': () => vecSigOk,
    'наша подпись верифицируется (DER→raw)': () => ourSigOk,
    'rawToDer даёт точные DER-байты': () => derOk,
  });
}
