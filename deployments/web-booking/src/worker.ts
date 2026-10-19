import { Failure } from './types.ts';
import type { Appointment, Draft, Env, Runtime, Session, Summary } from './types.ts';
import { Altegio, dateValue, identifier, localDate, localDatetime, safeText } from './altegio.ts';
import { body, configured, constantEqual, cookie, hash, hmac, ip, normalizePhone, onlyKeys, origin, randomToken, setCookie, smsCap, verifyTurnstile } from './security.ts';
import { allowedDestination, credentials, sendSms } from './sms.ts';
import { Store } from './store.ts';
import { runNotifications } from './notifications.ts';

const prefix='/api/booking';
const responseHeaders={'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer','X-Frame-Options':'DENY'};
function json(value:unknown,status=200,headers:Record<string,string>={}):Response {return new Response(JSON.stringify(value),{status,headers:{...responseHeaders,...headers}});}
function validDraftId(raw:unknown):string {if(typeof raw!=='string'||!/^[a-f0-9]{32}$/.test(raw))throw new Failure('invalid_selection');return raw;}
function nowSeconds(runtime:Runtime):number{return Math.floor(runtime.now()/1000);}
function code():string {
 // Rejection sampling avoids modulo bias in the six-digit secret.
 let n:number; do {n=crypto.getRandomValues(new Uint32Array(1))[0];} while(n>=4294000000);
 return (n%1000000).toString().padStart(6,'0');
}
async function authenticated(request:Request,env:Env,store:Store,runtime:Runtime,mutation:boolean):Promise<{session:Session;raw:string;csrf:string}> {
 const raw=cookie(request);
 if(!/^[a-f0-9]{64}$/.test(raw))throw new Failure('session_required',401,'Please refresh the booking page.');
 const session=await store.session(await hash(raw),nowSeconds(runtime));
 if(!session)throw new Failure('session_required',401,'Please refresh the booking page.');
 const csrf=await hmac(env.SESSION_SECRET,'csrf:'+raw);
 if(mutation&&!constantEqual(request.headers.get('X-CSRF-Token')||'',csrf))throw new Failure('csrf_rejected',403,'Please refresh the booking page.');
 return {session,raw,csrf};
}
async function status(store:Store,session:Session,id:string,now:number):Promise<unknown> {
 let draft=await store.draft(session.id,id);
 if(draft.phone!==session.phone)throw new Failure('verification_required',403,'Verify your booking phone again.');
 // A crashed reservation must never be automatically retried, even after expiry.
 if(['checking','sending'].includes(draft.status)&&draft.updated_at<now-90){await store.state(id,draft.status,'uncertain',now);draft=await store.draft(session.id,id);}
 if(draft.status==='confirmed')return {status:'confirmed',appointment:JSON.parse(draft.appointment!)};
 if(draft.status==='uncertain')return {status:'uncertain',message:'We could not confirm the booking result. Please contact the studio before booking again.'};
 if(draft.status==='rejected'||draft.status==='superseded')throw new Failure(draft.error_code||'selection_rejected',409,'Please review availability and choose again.');
 if(draft.expires_at<=now)throw new Failure('draft_expired',409,'Please choose your time again.');
 return {status:'pending'};
}
async function smsReservation(store:Store,env:Env,runtime:Runtime,session:Session,request:Request,purpose:string,otpHash?:string,phone?:string,draftId?:string):Promise<{id:string;expiry:number}> {
 const daily=smsCap(env.SMS_DAILY_CAP),monthly=smsCap(env.SMS_MONTHLY_CAP);
 if(daily===0||monthly===0)throw new Failure('verification_unavailable',503,'Phone verification is temporarily unavailable. Please contact the studio.');
 credentials(env);
 const destination=phone||session.phone!;allowedDestination(destination,env);
 const id=randomToken(16),now=nowSeconds(runtime),expiry=now+(purpose==='otp'?300:900);
 await store.reserveSms({id,sessionId:session.id,phoneHash:await hmac(env.SESSION_SECRET,'phone:'+destination),ipHash:await hmac(env.SESSION_SECRET,'ip:'+ip(request)),purpose,draftId,now,expiry,daily,monthly,otpHash,phone:destination});
 return {id,expiry};
}
async function optionalConfirmation(store:Store,env:Env,runtime:Runtime,session:Session,request:Request,draft:Draft,appointment:Appointment):Promise<void> {
 if(env.SMS_CONFIRMATIONS_ENABLED!=='true')return;
 const {id,expiry}=await smsReservation(store,env,runtime,session,request,'confirmation',undefined,draft.phone,draft.id);
 const text=`E-Motion Concept: booking confirmed ${appointment.datetime.slice(0,10)} at ${appointment.datetime.slice(11,16)}. Ref ${appointment.id}. Questions: +37494768067.`;
 const receipt=await sendSms(env,runtime,id,draft.phone,text,expiry,0);await store.smsStatus(id,receipt);
}
async function book(store:Store,env:Env,runtime:Runtime,session:Session,request:Request,id:string):Promise<unknown> {
 if(env.BOOKING_ENABLED!=='true')throw new Failure('booking_paused',503,'Online booking is temporarily unavailable. Please contact the studio.');
 const now=nowSeconds(runtime),draft=await store.draft(session.id,id);
 if(!session.phone||draft.phone!==session.phone)throw new Failure('verification_required',403,'Verify your booking phone first.');
 if(draft.status!=='ready')return status(store,session,id,now);
 if(draft.expires_at<=now)throw new Failure('draft_expired',409,'Please select your time again.');
 if(!await store.claim(id,session.id,now)){
  const active=await store.unresolvedPhone(draft.phone);
  if(active&&active.id!==id)throw new Failure('booking_needs_review',409,'We are checking an earlier booking. Please contact the studio.');
  return status(store,session,id,now);
 }
 const client=new Altegio(env,runtime),summary=JSON.parse(draft.summary) as Summary;
 try {
  const live=await client.summary(summary.serviceId,summary.staffId,localDatetime(summary.datetime,runtime.now()),summary.name,id);
  if(live.priceMin!==summary.priceMin||live.priceMax!==summary.priceMax||live.durationMinutes!==summary.durationMinutes||live.reservedMinutes!==summary.reservedMinutes||live.serviceName!==summary.serviceName||live.staffName!==summary.staffName)
   throw new Failure('selection_changed',409,'The appointment details changed. Please review a fresh summary.');
  await client.check(live);
 }catch(error){
  const failure=error instanceof Failure?error:new Failure('booking_unavailable',503);
  await store.state(id,'checking','rejected',nowSeconds(runtime),failure.code);throw failure;
 }
 // This CAS is the durable send intent. Never reset or retry a 'sending' record.
 if(!await store.state(id,'checking','sending',nowSeconds(runtime)))return status(store,session,id,nowSeconds(runtime));
 let created:{id:string;managementHash:string};
 try {created=await client.create(summary,draft.phone);}catch(error){
  const failure=error instanceof Failure?error:new Failure('outcome_unknown',503);
  if(failure.code==='selection_rejected'){
   await store.state(id,'sending','rejected',nowSeconds(runtime),failure.code);throw failure;
  }
  await store.state(id,'sending','uncertain',nowSeconds(runtime));return {status:'uncertain',message:'Please contact the studio before booking again. We are checking the result.'};
 }
 const appointment:Appointment={id:created.id,serviceName:summary.serviceName,staffName:summary.staffName,datetime:summary.datetime,durationMinutes:summary.durationMinutes,price:summary.price,priceMin:summary.priceMin,priceMax:summary.priceMax};
 try {await store.confirmed(id,appointment,created.managementHash,nowSeconds(runtime),env.SMS_REMINDERS_ENABLED==='true'?Date.parse(summary.datetime)/1000-7200:undefined);}catch{return {status:'uncertain',message:'Please contact the studio to check your booking before trying again.'};}
 // Booking success survives a notification/configuration failure. No provider retry.
 try{await optionalConfirmation(store,env,runtime,session,request,draft,appointment);}catch{/* durable provider attempts remain available for operator review */}
 return {status:'confirmed',appointment};
}
export async function handle(request:Request,env:Env,runtime:Runtime={fetch:globalThis.fetch.bind(globalThis),now:Date.now}):Promise<Response> {
 try {
  const url=new URL(request.url);
  if(!url.pathname.startsWith('/api/'))return env.ASSETS?await env.ASSETS.fetch(request):new Response('Not found',{status:404});
  if(!url.pathname.startsWith(prefix+'/'))throw new Failure('not_found',404);
  configured(env);const mutation=request.method!=='GET';origin(request,env,mutation);
  if(!['GET','POST'].includes(request.method))throw new Failure('method_not_allowed',405);
  const store=new Store(env.DB),now=nowSeconds(runtime),ipHash=await hmac(env.SESSION_SECRET,'ip:'+ip(request));
  await store.rate('api-global',300,60,now);
  await store.rate('api-ip:'+ipHash,90,60,now);
  const path=url.pathname.slice(prefix.length);
  if(path==='/config'&&request.method==='GET')return json({turnstileSiteKey:env.TURNSTILE_SITE_KEY||'',bookingEnabled:env.BOOKING_ENABLED==='true',smsEnabled:!!(smsCap(env.SMS_DAILY_CAP)!==0&&smsCap(env.SMS_MONTHLY_CAP)!==0&&env.SMSGATE_CREDENTIALS_JSON&&env.TURNSTILE_SECRET&&env.TURNSTILE_SITE_KEY),countryCodes:(env.SMS_ALLOWED_COUNTRY_CODES||'374').split(','),currency:env.ALTEGIO_CURRENCY||'AMD',timezone:'Asia/Yerevan'});
  if(path==='/session'&&request.method==='POST'){
   if(request.body)onlyKeys(await body(request),[]);
   let raw=cookie(request),session=/^[a-f0-9]{64}$/.test(raw)?await store.session(await hash(raw),now):null;
   if(!session){await store.rate('session-ip:'+ipHash,10,3600,now);await store.rate('session-global',100,60,now);raw=randomToken();session=await store.newSession(await hash(raw),now);}
   const unresolved=session.phone?await store.unresolvedPhone(session.phone):null;
   return json({csrfToken:await hmac(env.SESSION_SECRET,'csrf:'+raw),phoneVerified:!!session.phone,...(session.phone?{phone:session.phone}:{}),...(unresolved?.session_id===session.id?{unresolvedDraftId:unresolved.id}:{})},200,{'Set-Cookie':setCookie(raw)});
  }
  const client=new Altegio(env,runtime);
  if(request.method==='GET'&&['/services','/staff','/dates','/slots'].includes(path)){
   if(path==='/services')return json({services:await client.services(),constraints:'Extra-time add-ons and treatments with two therapists are arranged directly with our team.'});
   const serviceId=identifier(url.searchParams.get('service_id'));
   if(path==='/staff'){await client.validateService(serviceId);return json({staff:await client.staff(serviceId)});}
   const staffId=identifier(url.searchParams.get('staff_id'));await client.validatePair(serviceId,staffId);
   if(path==='/dates')return json({dates:await client.dates(serviceId,staffId)});
   const date=dateValue(url.searchParams.get('date'));
   if(date<localDate(runtime.now())||new Date(date+'T00:00:00+04:00').getTime()>runtime.now()+183*86400000)throw new Failure('invalid_date');
   return json({slots:(await client.slots(serviceId,staffId,date)).map(({datetime,label})=>({datetime,label}))});
  }
  const {session}=await authenticated(request,env,store,runtime,mutation);
  if(request.method==='GET'&&path.startsWith('/appointments/'))return json(await status(store,session,validDraftId(path.slice('/appointments/'.length)),now));
  if(request.method!=='POST')throw new Failure('not_found',404);
  const data=await body(request);
  if(path==='/verification/request'){
   onlyKeys(data,['phone','turnstileToken']);
   if(env.BOOKING_ENABLED!=='true')throw new Failure('booking_paused',503,'Online booking is temporarily unavailable. Please contact the studio.');
   const phone=normalizePhone(data.phone);allowedDestination(phone,env);
   if(smsCap(env.SMS_DAILY_CAP)===0||smsCap(env.SMS_MONTHLY_CAP)===0)throw new Failure('verification_unavailable',503,'Phone verification is temporarily unavailable. Please contact the studio.');
   credentials(env);await verifyTurnstile(data.turnstileToken,request,env,runtime);
   const otp=code(),digest=await hmac(env.SESSION_SECRET,`otp:${session.id}:${phone}:${otp}`);
   const reservation=await smsReservation(store,env,runtime,session,request,'otp',digest,phone);
   const receipt=await sendSms(env,runtime,reservation.id,phone,`E-Motion Concept: ${otp} is your booking access code. Expires in 5 minutes. Do not share it.`,reservation.expiry);
   await store.smsStatus(reservation.id,receipt);
   if(['rejected','Failed','Cancelled'].includes(receipt))throw new Failure('sms_unavailable',503,'The code could not be sent. Please contact the studio or try later.');
   return json({status:'pending',expiresIn:300});
  }
  if(path==='/verification/confirm'){
   onlyKeys(data,['code']);
   if(typeof data.code!=='string'||!/^\d{6}$/.test(data.code))throw new Failure('invalid_code',400,'Enter the six-digit code.');
   const attempt=await store.consumeOtp(session.id,now);
   if(!attempt)throw new Failure('code_expired',400,'The code expired or has too many attempts. Request a new code.');
   const digest=await hmac(env.SESSION_SECRET,`otp:${session.id}:${attempt.otp_phone}:${data.code}`);
   if(!constantEqual(digest,attempt.otp_hash!)||!await store.verifyPhone(session.id,digest,now))throw new Failure('invalid_code',400,'The code is incorrect.');
   return json({phoneVerified:true,phone:attempt.otp_phone});
  }
  if(path==='/drafts'){
   onlyKeys(data,['serviceId','staffId','datetime','name','consent']);
   if(!session.phone)throw new Failure('verification_required',403,'Verify your booking phone first.');
   if(data.consent!==true)throw new Failure('consent_required',400,'Please confirm the booking request.');
   const name=safeText(data.name,120);
   if(!name||name.length<2||typeof data.name!=='string'||data.name.length>120||/[<>\u0000-\u001f]/.test(data.name))throw new Failure('invalid_name',400,'Enter your first and last name.');
   await store.rate('draft-session:'+session.id,10,3600,now);
   const summary=await client.summary(identifier(data.serviceId),identifier(data.staffId),localDatetime(data.datetime,runtime.now()),name,randomToken(16));
   await store.saveDraft(session,summary,now);const {reservedMinutes:_privateReservation,...publicSummary}=summary;return json({draft:publicSummary,phoneVerified:true});
  }
  if(path==='/appointments'){
   onlyKeys(data,['draftId']);return json(await book(store,env,runtime,session,request,validDraftId(data.draftId)));
  }
  throw new Failure('not_found',404);
 }catch(error){
  const failure=error instanceof Failure?error:new Failure('temporarily_unavailable',503,'Booking is temporarily unavailable. Please try later or contact the studio.');
  return json({error:{code:failure.code,message:failure.message}},failure.status, failure.status===429?{'Retry-After':'60'}:{});
 }
}
export default {
 async fetch(request:Request,env:Env):Promise<Response>{return handle(request,env);},
 async scheduled(_controller:unknown,env:Env):Promise<void>{
  if(!env.DB)return;
  await runNotifications(env,{fetch:globalThis.fetch.bind(globalThis),now:Date.now});
  const now=new Date();if(now.getUTCHours()===1&&now.getUTCMinutes()<5)await new Store(env.DB).cleanup(Math.floor(now.getTime()/1000));
 }
};
