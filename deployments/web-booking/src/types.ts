export interface D1Result<T = Record<string, unknown>> { results: T[]; success: boolean; meta: { changes: number } }
export interface D1Statement {
 bind(...values: unknown[]): D1Statement;
 first<T = Record<string, unknown>>(): Promise<T | null>;
 all<T = Record<string, unknown>>(): Promise<D1Result<T>>;
 run(): Promise<D1Result>;
}
export interface Database { prepare(sql: string): D1Statement; batch(statements: D1Statement[]): Promise<D1Result[]> }
export interface Env {
 DB: Database; ASSETS?: { fetch(request: Request): Promise<Response> };
 PUBLIC_ORIGIN: string; SESSION_SECRET: string;
 LEGACY_BOOKING_ACTIVE?: string;
 ALTEGIO_PARTNER_TOKEN: string; ALTEGIO_USER_TOKEN?: string;
 ALTEGIO_COMPANY_ID: string; ALTEGIO_CURRENCY?: string;
 TURNSTILE_SECRET: string; TURNSTILE_SITE_KEY?: string; SMSGATE_CREDENTIALS_JSON: string;
 SMS_DAILY_CAP: string; SMS_MONTHLY_CAP: string;
 SMS_CONFIRMATIONS_ENABLED?: string; BOOKING_ENABLED?: string;
 SMS_REMINDERS_ENABLED?: string;
 SMS_ALLOWED_COUNTRY_CODES?: string;
 ECOSYSTEM_GATEWAY_ORIGIN?: string;
 ECOSYSTEM_CONTEXT_KEY?: string;
}
export interface Session {
 id: string; created_at: number; expires_at: number; phone: string | null;
 otp_hash: string | null; otp_phone: string | null; otp_expires: number | null; otp_attempts: number;
}
export interface Summary {
 id: string; serviceId: string; serviceName: string; staffId: string; staffName: string;
 datetime: string; name: string; durationMinutes: number; price: string;
 reservedMinutes: number;
 priceMin: number; priceMax: number; currency: string; consent: true;
 locale?:'ru'|'en'|'hy'; email?:string; comment?:string; reminderMinutes?:number;
 extraId?:string; serviceIds?:string[];
 certificate?:{id:string;title:string;number:string};
}
export interface Appointment {
 id: string; serviceName: string; staffName: string; datetime: string; durationMinutes: number; price: string;
 priceMin: number; priceMax: number;
 calendarUrl?:string;
  certificate?:{id:string;title:string};
}
export interface Draft {
 id: string; session_id: string; phone: string; summary: string;
 status: string; created_at: number; expires_at: number; updated_at: number;
 appointment: string | null; management_hash: string | null; error_code: string | null;
}
export interface VerificationSend {id:string;status:string;expires_at:number;created_at:number;next_check_at:number;receipt_until:number}
export interface Runtime { fetch: typeof fetch; now: () => number }
export class Failure extends Error {
 code: string; status: number;
 constructor(code: string, status = 400, message = "Please try again.") { super(message); this.code = code; this.status = status; }
}
