// lib/signer.js — программный P-256 WebAuthn-подписант для k6 (soft-signer).
// Байты 1-в-1 с tests/e2e/softauthn_test.go softSign:
//   clientDataJSON    = {"type":"webauthn.get","challenge":"<b64url32>","origin":"http://localhost:5173","crossOrigin":false}
//   authenticatorData = sha256("localhost") ‖ 0x05 (UP|UV) ‖ BE uint32 1   (37 байт)
//   подпись           = ECDSA P-256, ASN.1 DER, поверх sha256(authData ‖ sha256(cdj))
// Сервер (wms/internal/signedaction/assertion.go) сам нормализует low-s.
import encoding from 'k6/encoding';

const ORIGIN = 'http://localhost:5173';
const RPID = 'localhost';
const FLAGS = 0x05; // UP | UV
const SIGN_COUNT_BE = [0x00, 0x00, 0x00, 0x01];

// rpIdHash = sha256("localhost") — лениво, один digest на VU (кэш промиса).
let rpIdHashPromise = null;
function getRpIdHash() {
  if (!rpIdHashPromise) {
    rpIdHashPromise = crypto.subtle.digest('SHA-256', strBytes(RPID).buffer);
  }
  return rpIdHashPromise;
}

// strBytes — ASCII-строка → Uint8Array (cdj содержит только ASCII).
function strBytes(s) {
  const out = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i) & 0xff;
  return out;
}

function b64urlDecode(s) {
  return new Uint8Array(encoding.b64decode(s, 'rawurl'));
}

function b64urlEncode(bytes) {
  return encoding.b64encode(bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength), 'rawurl');
}

// importSigner — импортирует JWK-ключ оператора (запись operators.json) для подписи.
export async function importSigner(op) {
  const key = await crypto.subtle.importKey(
    'jwk',
    { kty: op.jwk.kty, crv: op.jwk.crv, x: op.jwk.x, y: op.jwk.y, d: op.jwk.d },
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['sign'],
  );
  return { credentialId: op.credentialId, key };
}

// rawToDer — RAW r‖s (64 байта) → ASN.1 DER SEQUENCE{INTEGER r, INTEGER s}.
// Длины P-256 < 128 → короткая форма длины; ведущие 0x00 срезаются,
// 0x00-префикс ставится, если старший байт ≥ 0x80.
export function rawToDer(raw) {
  const r = intDerBytes(raw, 0, 32);
  const s = intDerBytes(raw, 32, 64);
  const body = new Uint8Array(r.length + s.length);
  body.set(r, 0);
  body.set(s, r.length);
  const der = new Uint8Array(2 + body.length);
  der[0] = 0x30; // SEQUENCE
  der[1] = body.length;
  der.set(body, 2);
  return der;
}

// intDerBytes — DER INTEGER из байт [from, to) с нормализацией длины.
function intDerBytes(raw, from, to) {
  let start = from;
  while (start < to - 1 && raw[start] === 0x00) start++;
  const pad = raw[start] >= 0x80 ? 1 : 0;
  const len = (to - start) + pad;
  const out = new Uint8Array(2 + len);
  out[0] = 0x02; // INTEGER
  out[1] = len;
  let o = 2;
  if (pad) out[o++] = 0x00;
  for (let i = start; i < to; i++) out[o++] = raw[i];
  return out;
}

// signChallenge — подписывает challenge черновика (base64url) и собирает
// утверждение в форме submit-API: все поля base64url без паддинга.
export async function signChallenge(signer, challengeB64url) {
  const challenge = b64urlDecode(challengeB64url);
  const cdj = `{"type":"webauthn.get","challenge":"${b64urlEncode(challenge)}","origin":"${ORIGIN}","crossOrigin":false}`;

  const rpHash = new Uint8Array(await getRpIdHash());
  const authData = new Uint8Array(37);
  authData.set(rpHash, 0);
  authData[32] = FLAGS;
  authData.set(SIGN_COUNT_BE, 33);

  const clientHash = new Uint8Array(await crypto.subtle.digest('SHA-256', strBytes(cdj).buffer));
  const signedData = new Uint8Array(authData.length + clientHash.length);
  signedData.set(authData, 0);
  signedData.set(clientHash, authData.length);

  const raw = new Uint8Array(await crypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, signer.key, signedData.buffer));
  return {
    credential_id: signer.credentialId,
    authenticator_data: b64urlEncode(authData),
    client_data_json: b64urlEncode(strBytes(cdj)),
    signature: b64urlEncode(rawToDer(raw)),
  };
}

// signDraft — подписывает каждую часть черновика и собирает тело submit-запроса
// {operation_id, assertions:[{action_id, credential_id, authenticator_data,
// client_data_json, signature}]}; одна часть → одно утверждение.
export async function signDraft(signer, draftData) {
  const assertions = [];
  for (const part of draftData.parts || []) {
    const assertion = await signChallenge(signer, part.challenge);
    assertion.action_id = part.action_id;
    assertions.push(assertion);
  }
  return { operation_id: draftData.operation_id, assertions };
}
