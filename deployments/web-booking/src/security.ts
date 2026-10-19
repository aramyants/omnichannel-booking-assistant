import { Failure } from './types.ts';
import type { Env, Runtime } from './types.ts';

const encoder = new TextEncoder();
export const cookieName = '__Host-motion_booking';
export function randomToken(bytes = 32): string {
 return Array.from(crypto.getRandomValues(new Uint8Array(bytes)), b => b.toString(16).padStart(2, '0')).join('');
}
export async function hash(value: string): Promise<string> {
 return Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(value))), b => b.toString(16).padStart(2, '0')).join('');
}
export async function hmac(secret: string, value: string): Promise<string> {
 const key = await crypto.subtle.importKey('raw', encoder.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign']);
 return Array.from(new Uint8Array(await crypto.subtle.sign('HMAC', key, encoder.encode(value))), b => b.toString(16).padStart(2, '0')).join('');
}
export function constantEqual(a: string, b: string): boolean {
 if (a.length !== b.length) return false;
 let result = 0; for (let i = 0; i < a.length; i++) result |= a.charCodeAt(i) ^ b.charCodeAt(i);
 return result === 0;
}
export function cookie(request: Request): string {
 return (request.headers.get('Cookie') || '').split(';').map(x => x.trim()).find(x => x.startsWith(cookieName + '='))?.slice(cookieName.length + 1) || '';
}
export function setCookie(raw: string): string { return `${cookieName}=${raw}; Path=/; Max-Age=86400; HttpOnly; Secure; SameSite=Strict`; }
export function ip(request: Request): string {
 // Cloudflare overwrites this header at its edge. Never use X-Forwarded-For.
 const value = request.headers.get('CF-Connecting-IP');
 if (!value || value.length > 64 || !/^[0-9a-f:.]+$/i.test(value)) throw new Failure('request_unavailable', 503, 'Booking is temporarily unavailable.');
 return value;
}
export function origin(request: Request, env: Env, mutation: boolean): void {
 let expected: URL;
 try { expected = new URL(env.PUBLIC_ORIGIN); } catch { throw new Failure('configuration', 503); }
 if (expected.protocol !== 'https:' || expected.origin !== env.PUBLIC_ORIGIN || expected.hostname.endsWith('.invalid') ||
     new URL(request.url).origin !== expected.origin) throw new Failure('configuration', 503);
 const actual = request.headers.get('Origin');
 if ((mutation && !actual) || (actual && actual !== expected.origin)) throw new Failure('origin_rejected', 403, 'Please open booking from the studio website.');
 if (request.headers.get('Sec-Fetch-Site') === 'cross-site') throw new Failure('origin_rejected', 403);
}
export function configured(env: Env): void {
 if (!env.DB || !env.SESSION_SECRET || env.SESSION_SECRET.length < 32 || !env.ALTEGIO_PARTNER_TOKEN ||
     !/^[1-9][0-9]{0,14}$/.test(env.ALTEGIO_COMPANY_ID || '')) throw new Failure('configuration', 503, 'Booking is temporarily unavailable.');
}
export async function body(request: Request): Promise<Record<string, unknown>> {
 if (!request.headers.get('Content-Type')?.toLowerCase().startsWith('application/json')) throw new Failure('invalid_request', 415);
 const reader = request.body?.getReader();
 if (!reader) throw new Failure('invalid_request');
 let size = 0; const chunks: Uint8Array[] = [];
 for (;;) {
  const result = await reader.read(); if (result.done) break;
  size += result.value.length;
  if (size > 8192) { await reader.cancel(); throw new Failure('invalid_request', 413); }
  chunks.push(result.value);
 }
 const data = new Uint8Array(size); let offset = 0;
 for (const chunk of chunks) { data.set(chunk, offset); offset += chunk.length; }
 try {
  const parsed = JSON.parse(new TextDecoder().decode(data));
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error();
  return parsed;
 } catch { throw new Failure('invalid_request'); }
}
export function onlyKeys(value: Record<string, unknown>, allowed: string[]): void {
 if (Object.keys(value).some(key => !allowed.includes(key))) throw new Failure('invalid_request', 400, 'Please review your booking details.');
}
export function normalizePhone(raw: unknown): string {
 if (typeof raw !== 'string' || raw.length > 40 || !/^[+0-9()\s.\-]+$/.test(raw)) throw new Failure('invalid_phone', 400, 'Enter your phone with country code.');
 let phone = raw.replace(/[()\s.\-]/g, '');
 if (phone.startsWith('00')) phone = '+' + phone.slice(2);
 if (/^374\d{8}$/.test(phone)) phone = '+' + phone;
 if (/^0\d{8}$/.test(phone)) phone = '+374' + phone.slice(1);
 if (/^\d{8}$/.test(phone)) phone = '+374' + phone;
 if (!/^\+[1-9]\d{7,14}$/.test(phone) || (phone.startsWith('+374') && !/^\+374\d{8}$/.test(phone))) throw new Failure('invalid_phone', 400, 'Enter your phone with country code.');
 return phone;
}
export function positiveCap(raw: string): number {
 const n = Number(raw); return Number.isSafeInteger(n) && n > 0 && n <= 1000 ? n : 0;
}
export function smsCap(raw:string):number|null {
 if(raw==='unlimited')return null;
 return positiveCap(raw);
}
export async function verifyTurnstile(token: unknown, request: Request, env: Env, runtime: Runtime): Promise<void> {
 if (!env.TURNSTILE_SECRET) throw new Failure('verification_unavailable', 503, 'Phone verification is temporarily unavailable.');
 if (typeof token !== 'string' || token.length < 10 || token.length > 2048) throw new Failure('human_verification_required', 400, 'Please complete the security check.');
 let result: { success?: boolean; hostname?: string; action?: string };
 try {
  const response = await runtime.fetch('https://challenges.cloudflare.com/turnstile/v0/siteverify', {
   method: 'POST', body: new URLSearchParams({secret: env.TURNSTILE_SECRET, response: token, remoteip: ip(request)}),
   redirect: 'manual', signal: AbortSignal.timeout(8000)
  });
  if (!response.ok) throw new Error(); result = await response.json();
 } catch { throw new Failure('human_verification_unavailable', 503); }
 if (!result.success || result.hostname !== new URL(env.PUBLIC_ORIGIN).hostname || result.action !== 'booking_otp') throw new Failure('human_verification_required', 400, 'Please complete the security check again.');
}
