import { Failure } from './types.ts';
import type { Database, Draft, Session, Summary, VerificationSend } from './types.ts';

export class Store {
 db: Database;
 constructor(db: Database) { this.db=db; }
 async rate(key:string,limit:number,windowSeconds:number,now:number):Promise<void> {
  const bucket=Math.floor(now/windowSeconds),id=key+':'+bucket;
  const result=await this.db.prepare('INSERT INTO rate_buckets(id,n,expires_at) VALUES(?,1,?) ON CONFLICT(id) DO UPDATE SET n=n+1 WHERE n<?').bind(id,(bucket+1)*windowSeconds,limit).run();
  if (!result.meta.changes) throw new Failure('rate_limited',429,'Please wait before trying again.');
 }
 async session(id:string,now:number):Promise<Session|null> {
  return this.db.prepare('SELECT * FROM sessions WHERE id=? AND expires_at>?').bind(id,now).first<Session>();
 }
 async newSession(id:string,now:number):Promise<Session> {
  await this.db.prepare('INSERT INTO sessions(id,created_at,expires_at) VALUES(?,?,?)').bind(id,now,now+86400).run();
  return (await this.session(id,now))!;
 }
 async reserveSms(data:{id:string;sessionId:string;phoneHash:string;ipHash:string;purpose:string;draftId?:string;now:number;expiry:number;daily:number|null;monthly:number|null;otpHash?:string;phone?:string}):Promise<void> {
  const d=data;
  // One serialized SQLite INSERT checks and reserves every budget before any external send.
  // Failed, uncertain and reserved sends all consume budget; no automatic refunds.
  const day=Math.floor((d.now+14400)/86400)*86400-14400;
  const month=new Date((d.now+14400)*1000);month.setUTCDate(1);month.setUTCHours(0,0,0,0);
  const monthStart=month.getTime()/1000-14400;
  const statements=[this.db.prepare(`INSERT INTO sms_outbox(id,session_id,phone_hash,ip_hash,purpose,draft_id,created_at,expires_at,parts,status,next_check_at,receipt_until)
   SELECT ?,?,?,?,?,?,?,?,1,'reserved',?,?
   WHERE (? IS NULL OR (SELECT COALESCE(SUM(parts),0) FROM sms_outbox WHERE created_at>=?)<?)
   AND (? IS NULL OR (SELECT COALESCE(SUM(parts),0) FROM sms_outbox WHERE created_at>=?)<?)
   AND (?<>'otp' OR (SELECT COUNT(*) FROM sms_outbox WHERE phone_hash=? AND purpose='otp' AND created_at>?)<1)
   AND (SELECT COUNT(*) FROM sms_outbox WHERE phone_hash=? AND created_at>?)<5
   AND (SELECT COUNT(*) FROM sms_outbox WHERE session_id=? AND created_at>?)<5
   AND (SELECT COUNT(*) FROM sms_outbox WHERE ip_hash=? AND created_at>?)<10
   AND (?<>'otp' OR (SELECT COUNT(*) FROM sms_outbox WHERE session_id=? AND purpose='otp' AND created_at>?)<1)`)
   .bind(d.id,d.sessionId,d.phoneHash,d.ipHash,d.purpose,d.draftId||null,d.now,d.expiry,d.now+(d.purpose==='otp'?5:300),d.now+86400,d.daily,day,d.daily,d.monthly,monthStart,d.monthly,
    d.purpose,d.phoneHash,d.now-60,d.phoneHash,d.now-3600,d.sessionId,d.now-3600,d.ipHash,d.now-3600,d.purpose,d.sessionId,d.now-60)];
  if(d.otpHash) statements.push(this.db.prepare(`UPDATE sessions SET phone=NULL,otp_hash=?,otp_phone=?,otp_expires=?,otp_attempts=0
   WHERE id=? AND EXISTS(SELECT 1 FROM sms_outbox WHERE id=?)`).bind(d.otpHash,d.phone,d.expiry,d.sessionId,d.id));
  const results=await this.db.batch(statements);
  if(!results[0].meta.changes)throw new Failure('sms_limit',429,'Verification is temporarily limited. Please contact the studio or try later.');
 }
 async smsStatus(id:string,status:string):Promise<void>{await this.db.prepare("UPDATE sms_outbox SET status=? WHERE id=? AND status NOT IN ('Delivered','Failed','Cancelled','rejected')").bind(status,id).run();}
 async verificationSend(sessionId:string):Promise<VerificationSend|null>{
  return this.db.prepare("SELECT id,status,expires_at,created_at,next_check_at,receipt_until FROM sms_outbox WHERE session_id=? AND purpose='otp' ORDER BY created_at DESC LIMIT 1").bind(sessionId).first<VerificationSend>();
 }
 async receiptCandidates(now:number):Promise<{id:string;purpose:string}[]>{
  const result=await this.db.prepare(`SELECT id,purpose FROM sms_outbox WHERE receipt_until>? AND next_check_at<=? AND status NOT IN ('Delivered','Failed','Cancelled','rejected') ORDER BY next_check_at LIMIT 3`).bind(now,now).all<{id:string;purpose:string}>();return result.results;
 }
 async reserveReceiptRead(id:string,now:number,delaySeconds=300):Promise<boolean>{
  const result=await this.db.prepare('UPDATE sms_outbox SET next_check_at=? WHERE id=? AND next_check_at<=? AND receipt_until>?').bind(now+delaySeconds,id,now,now).run();return !!result.meta.changes;
 }
 async scheduleReminder(id:string,due:number,now:number):Promise<void>{
  if(due<now+300)return;
  await this.db.prepare("INSERT OR IGNORE INTO reminder_jobs(draft_id,state,due_at,expires_at,updated_at) VALUES(?,'pending',?,?,?)").bind(id,due,due+900,now).run();
 }
 async remindersDue(now:number):Promise<Draft[]>{
  const result=await this.db.prepare(`SELECT d.* FROM reminder_jobs r JOIN drafts d ON d.id=r.draft_id WHERE r.state='pending' AND r.due_at<=? AND d.status='confirmed' ORDER BY r.due_at LIMIT 2`).bind(now).all<Draft>();return result.results;
 }
 async reminderState(id:string,from:string,to:string,now:number):Promise<boolean>{
  const result=await this.db.prepare('UPDATE reminder_jobs SET state=?,updated_at=? WHERE draft_id=? AND state=?').bind(to,now,id,from).run();return !!result.meta.changes;
 }
 async consumeOtp(sessionId:string,now:number):Promise<Session|null> {
  return this.db.prepare(`UPDATE sessions SET otp_attempts=otp_attempts+1 WHERE id=? AND otp_hash IS NOT NULL AND otp_expires>? AND otp_attempts<5 RETURNING *`).bind(sessionId,now).first<Session>();
 }
 async verifyPhone(sessionId:string,otpHash:string,now:number):Promise<boolean> {
  const result=await this.db.prepare(`UPDATE sessions SET phone=otp_phone,otp_hash=NULL,otp_phone=NULL,otp_expires=NULL,otp_attempts=0
   WHERE id=? AND otp_hash=? AND otp_expires>? AND otp_attempts<=5`).bind(sessionId,otpHash,now).run();
  return !!result.meta.changes;
 }
 async saveDraft(session:Session,summary:Summary,now:number):Promise<void> {
  if(await this.unresolvedPhone(session.phone!))throw new Failure('booking_needs_review',409,'We are checking an earlier booking. Please contact the studio before making another.');
  await this.db.batch([
   this.db.prepare("UPDATE drafts SET status='superseded' WHERE session_id=? AND status='ready'").bind(session.id),
   this.db.prepare(`INSERT INTO drafts(id,session_id,phone,summary,status,created_at,expires_at,updated_at) VALUES(?,?,?,?,'ready',?,?,?)`)
    .bind(summary.id,session.id,session.phone,JSON.stringify(summary),now,now+900,now)
  ]);
 }
 async unresolvedPhone(phone:string):Promise<{id:string;session_id:string}|null>{
  return this.db.prepare("SELECT id,session_id FROM drafts WHERE phone=? AND status IN ('checking','sending','uncertain') ORDER BY created_at DESC LIMIT 1").bind(phone).first();
 }
 async draft(sessionId:string,id:string):Promise<Draft> {
  const value=await this.db.prepare('SELECT * FROM drafts WHERE id=? AND session_id=?').bind(id,sessionId).first<Draft>();
  if(!value)throw new Failure('not_found',404,'This booking could not be found.');return value;
 }
 async claim(id:string,sessionId:string,now:number):Promise<boolean> {
  const result=await this.db.prepare(`UPDATE drafts SET status='checking',updated_at=? WHERE id=? AND session_id=? AND status='ready' AND expires_at>?
   AND NOT EXISTS(SELECT 1 FROM drafts d WHERE d.phone=drafts.phone AND d.status IN ('checking','sending','uncertain'))`)
   .bind(now,id,sessionId,now).run();return !!result.meta.changes;
 }
 async state(id:string,from:string,to:string,now:number,errorCode:string|null=null):Promise<boolean> {
  const result=await this.db.prepare('UPDATE drafts SET status=?,updated_at=?,error_code=? WHERE id=? AND status=?').bind(to,now,errorCode,id,from).run();return !!result.meta.changes;
 }
 async confirmed(id:string,appointment:unknown,managementHash:string,now:number,reminderDue?:number):Promise<void> {
  const statements=[this.db.prepare(`UPDATE drafts SET status='confirmed',appointment=?,management_hash=?,updated_at=? WHERE id=? AND status='sending'`)
   .bind(JSON.stringify(appointment),managementHash,now,id)];
  if(reminderDue&&reminderDue>=now+300)statements.push(this.db.prepare(`INSERT OR IGNORE INTO reminder_jobs(draft_id,state,due_at,expires_at,updated_at)
   SELECT ?,'pending',?,?,? WHERE EXISTS(SELECT 1 FROM drafts WHERE id=? AND status='confirmed')`).bind(id,reminderDue,reminderDue+900,now,id));
  await this.db.batch(statements);
 }
 async appointmentDetails(id:string,appointment:unknown):Promise<void>{await this.db.prepare("UPDATE drafts SET appointment=? WHERE id=? AND status='confirmed'").bind(JSON.stringify(appointment),id).run();}
 async contextRecoveryCandidates(now:number):Promise<Draft[]>{
  const result=await this.db.prepare(`SELECT * FROM drafts WHERE status='confirmed' AND management_hash IS NOT NULL AND management_hash<>'' AND created_at>? AND updated_at<? AND json_extract(summary,'$.consent')=1 AND json_extract(summary,'$.locale') IN ('ru','en','hy') AND json_extract(appointment,'$.calendarUrl') IS NULL AND json_extract(appointment,'$.datetime')>? ORDER BY updated_at ASC LIMIT 2`)
   .bind(now-86400,now-60,new Date(now*1000+4*3600000).toISOString().slice(0,19)+'+04:00').all<Draft>();return result.results;
 }
 async reserveContextRecovery(draft:Draft,now:number):Promise<boolean>{
  const result=await this.db.prepare("UPDATE drafts SET updated_at=? WHERE id=? AND status='confirmed' AND updated_at=? AND json_extract(appointment,'$.calendarUrl') IS NULL").bind(now,draft.id,draft.updated_at).run();return result.meta.changes===1;
 }
 async cleanup(now:number):Promise<void> {
  await this.db.batch([
   this.db.prepare('DELETE FROM sessions WHERE expires_at<?').bind(now),
   this.db.prepare('DELETE FROM rate_buckets WHERE expires_at<?').bind(now),
   this.db.prepare(`DELETE FROM drafts WHERE created_at<? AND status NOT IN ('sending','checking','uncertain')
    AND NOT (status='confirmed' AND (json_extract(appointment,'$.datetime')>? OR EXISTS(SELECT 1 FROM reminder_jobs r WHERE r.draft_id=drafts.id AND r.expires_at>? AND r.state IN ('pending','checking','sending'))))`)
    .bind(now-30*86400,new Date(now*1000+4*3600000).toISOString().slice(0,19)+'+04:00',now),
   this.db.prepare('DELETE FROM sms_outbox WHERE created_at<?').bind(now-62*86400)
   ,this.db.prepare('DELETE FROM reminder_jobs WHERE expires_at<?').bind(now-30*86400)
  ]);
 }
}
