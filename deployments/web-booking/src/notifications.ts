import { Altegio } from './altegio.ts';
import { hmac, normalizePhone, randomToken, smsCap } from './security.ts';
import { allowedDestination, credentials, sendSms, smsReceipt } from './sms.ts';
import { Store } from './store.ts';
import type { Appointment, Draft, Env, Runtime, Summary } from './types.ts';
import {ecosystemContext,locale,reminderMessage} from './ecosystem.ts';

function samePhone(raw:string,expected:string):boolean{
 try{return normalizePhone(raw.startsWith('+')?raw:'+'+raw)===expected;}catch{return false;}
}
export async function runNotifications(env:Env,runtime:Runtime):Promise<void>{
 const store=new Store(env.DB),now=Math.floor(runtime.now()/1000);
 // Provider GETs are safe to repeat; provider POSTs are never retried.
 for(const candidate of await store.receiptCandidates(now)){
  if(!await store.reserveReceiptRead(candidate.id,now,candidate.purpose==='otp'?10:300))continue;
  try{
   const receipt=await smsReceipt(env,runtime,candidate.id);
   if(receipt!=='unconfirmed')await store.smsStatus(candidate.id,receipt);
  }catch{/* Unknown reads never become delivered or failed and never resend. */}
 }
 if(env.SMS_REMINDERS_ENABLED!=='true')return;
 for(const draft of await store.remindersDue(now))await reminder(env,runtime,store,draft,now);
}
async function reminder(env:Env,runtime:Runtime,store:Store,draft:Draft,now:number):Promise<void>{
 if(!await store.reminderState(draft.id,'pending','checking',now))return;
 const summary=JSON.parse(draft.summary) as Summary,appointment=JSON.parse(draft.appointment!) as Appointment;
 const due=Date.parse(summary.datetime)/1000-7200;
 if(now>due+900||Date.parse(summary.datetime)/1000<=now||!summary.consent||!draft.management_hash||!env.ALTEGIO_USER_TOKEN){await store.reminderState(draft.id,'checking','skipped',now);return;}
 const daily=smsCap(env.SMS_DAILY_CAP),monthly=smsCap(env.SMS_MONTHLY_CAP);
 if(daily===0||monthly===0){await store.reminderState(draft.id,'checking','skipped',now);return;}
 try{
  credentials(env);allowedDestination(draft.phone,env);
  const client=new Altegio(env,runtime),live=await client.readOwned(appointment.id,draft.management_hash);
  if(live.deleted||live.datetime!==summary.datetime||live.staffId!==summary.staffId||live.serviceIds.length!==1||live.serviceIds[0]!==summary.serviceId||live.reservedMinutes!==summary.reservedMinutes||(live.phone&&!samePhone(live.phone,draft.phone))){
   await store.reminderState(draft.id,'checking','skipped',now);return;
  }
  // The public capability response can omit its current recipient. Never infer
  // that it is still the original phone after a receptionist reassigns a visit.
  if(env.ECOSYSTEM_GATEWAY_ORIGIN){
   const context=await ecosystemContext(env,runtime,{action:'record-context',phone:draft.phone,recordId:appointment.id});
   if(context.eligible!==true){await store.reminderState(draft.id,'checking','skipped',now);return;}
   summary.locale=context.locale;
  }else if(await client.currentPhone(appointment.id)!==draft.phone){await store.reminderState(draft.id,'checking','skipped',now);return;}
 }catch{
  // No customer send intent exists yet; retry only the read within the due window.
  await store.reminderState(draft.id,'checking','pending',now);return;
 }
 const id=randomToken(16),expiry=Math.min(now+900,Date.parse(summary.datetime)/1000);
 try{
  await store.reserveSms({id,sessionId:draft.session_id,phoneHash:await hmac(env.SESSION_SECRET,'phone:'+draft.phone),ipHash:await hmac(env.SESSION_SECRET,'scheduled:'+draft.id),purpose:'reminder',draftId:draft.id,now,expiry,daily,monthly});
 }catch{await store.reminderState(draft.id,'checking','skipped',now);return;}
 // The unique outbox row is now the irreversible send reservation. A crash or
 // timeout after it can never produce another reminder POST.
 await store.reminderState(draft.id,'checking','sending',now);
 const text=reminderMessage(summary.datetime,locale(summary.locale));
 const receipt=await sendSms(env,runtime,id,draft.phone,text,expiry,0);
 await store.smsStatus(id,receipt);await store.reminderState(draft.id,'sending','done',now);
}
