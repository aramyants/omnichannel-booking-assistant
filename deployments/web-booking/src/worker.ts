import { Failure } from './types.ts';
import type { Appointment, Draft, Env, Runtime, Session, Summary } from './types.ts';
import { Altegio, dateValue, identifier, staffIdentifier, localDate, localDatetime, safeText } from './altegio.ts';
import { body, configured, constantEqual, cookie, hash, hmac, ip, normalizePhone, onlyKeys, origin, randomToken, setCookie, smsCap, verifyTurnstile } from './security.ts';
import { allowedDestination, credentials, destinationCodes, sendSms, smsReceipt } from './sms.ts';
import { Store } from './store.ts';
import { runNotifications } from './notifications.ts';
import {ecosystemContext,otpMessage} from './ecosystem.ts';
import {publicReviews,reviewVisits,submitReview} from './reviews.ts';
import {giftCertificateCatalog} from './gift-certificates.ts';

const prefix='/api/booking';
const responseHeaders={'Content-Type':'application/json; charset=utf-8','Cache-Control':'no-store','X-Content-Type-Options':'nosniff','Referrer-Policy':'no-referrer','X-Frame-Options':'DENY'};
function json(value:unknown,status=200,headers:Record<string,string>={}):Response {return new Response(JSON.stringify(value),{status,headers:{...responseHeaders,...headers}});}
function validDraftId(raw:unknown):string {if(typeof raw!=='string'||!/^[a-f0-9]{32}$/.test(raw))throw new Failure('invalid_selection');return raw;}
function nowSeconds(runtime:Runtime):number{return Math.floor(runtime.now()/1000);}
export function requestLanguage(request:Request):'hy'|'ru'|'en'{
 const explicit=request.headers.get('X-Booking-Language')||request.headers.get('X-Booking-Locale');
 const valid=(raw:string):'hy'|'ru'|'en'|undefined=>{const language=raw.trim().toLowerCase().split('-')[0];return language==='hy'||language==='ru'||language==='en'?language:undefined;};
 if(explicit&&valid(explicit))return valid(explicit)!;
 const weighted=(request.headers.get('Accept-Language')||'').slice(0,1024).split(',').slice(0,20).map((entry,index)=>{
  const [tag,...parameters]=entry.trim().split(';'),q=parameters.find(p=>p.trim().startsWith('q='));
  const weight=q?Number(q.trim().slice(2)):1;return {language:valid(tag),weight:Number.isFinite(weight)&&weight>=0&&weight<=1?weight:0,index};
 }).filter(x=>x.language&&x.weight>0).sort((a,b)=>b.weight-a.weight||a.index-b.index);
 return weighted[0]?.language||'ru';
}
async function verificationStatus(store:Store,session:Session,env:Env,runtime:Runtime):Promise<unknown>{
 const now=nowSeconds(runtime),row=await store.verificationSend(session.id);
 if(!row)return {status:'unconfirmed',expiresIn:0,retryAfter:0};
 if(!session.phone&&row.expires_at>now&&!['Delivered','Failed','Cancelled','rejected'].includes(row.status)&&row.receipt_until>now&&await store.reserveReceiptRead(row.id,now,10)){
  const provider=await smsReceipt(env,runtime,row.id);
  if(provider!=='unconfirmed'){
   await store.smsStatus(row.id,provider);
   const current=await store.verificationSend(session.id);if(current?.id===row.id)row.status=current.status;
  }
 }
 const status=['Failed','Cancelled','rejected'].includes(row.status)?'failed':row.expires_at<=now?'expired':row.status==='Delivered'?'delivered':row.status==='Sent'?'sent':['Pending','Processed'].includes(row.status)?'queued':'unconfirmed';
 return {status,expiresIn:session.phone?0:Math.max(0,row.expires_at-now),retryAfter:Math.max(0,row.created_at+60-now),phoneVerified:!!session.phone};
}
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
async function status(store:Store,session:Session,id:string,now:number,env?:Env,runtime?:Runtime,request?:Request):Promise<unknown> {
 let draft=await store.draft(session.id,id);
 if(draft.phone!==session.phone)throw new Failure('verification_required',403,'Verify your booking phone again.');
 // A crashed reservation must never be automatically retried, even after expiry.
 if(['checking','sending'].includes(draft.status)&&draft.updated_at<now-90){await store.state(id,draft.status,'uncertain',now);draft=await store.draft(session.id,id);}
 if(draft.status==='confirmed'){const appointment=JSON.parse(draft.appointment!) as Appointment;if(!appointment.calendarUrl&&env?.ECOSYSTEM_GATEWAY_ORIGIN&&runtime&&request){try{await optionalConfirmation(store,env,runtime,session,request,draft,appointment);}catch{/* The confirmed appointment remains valid; only its idempotent context handoff may repeat. */}}const {certificate:_retired,...view}=appointment;return {status:'confirmed',appointment:view};}
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
 if(env.ECOSYSTEM_GATEWAY_ORIGIN){
  const result=await ecosystemContext(env,runtime,{action:'confirmed',phone:draft.phone,recordId:appointment.id,managementProof:draft.management_hash||undefined});
  if(result.calendarUrl){appointment.calendarUrl=result.calendarUrl;await store.appointmentDetails(draft.id,appointment);}
  return;
 }
 if(env.SMS_CONFIRMATIONS_ENABLED!=='true')return;
 const {id,expiry}=await smsReservation(store,env,runtime,session,request,'confirmation',undefined,draft.phone,draft.id);
 const text=`E-Motion Concept: booking confirmed ${appointment.datetime.slice(0,10)} at ${appointment.datetime.slice(11,16)}. Ref ${appointment.id}. Questions: +37494768067.`;
 const receipt=await sendSms(env,runtime,id,draft.phone,text,expiry,0);await store.smsStatus(id,receipt);
}
async function book(store:Store,env:Env,runtime:Runtime,session:Session,request:Request,id:string):Promise<unknown> {
 if(env.BOOKING_ENABLED!=='true')throw new Failure('booking_paused',503,'Online booking is temporarily unavailable. Please contact the studio.');
 const now=nowSeconds(runtime),draft=await store.draft(session.id,id);
 if(!session.phone||draft.phone!==session.phone)throw new Failure('verification_required',403,'Verify your booking phone first.');
 if(draft.status!=='ready')return status(store,session,id,now,env,runtime,request);
 if(draft.expires_at<=now)throw new Failure('draft_expired',409,'Please select your time again.');
 if(!await store.claim(id,session.id,now)){
  const active=await store.unresolvedPhone(draft.phone);
  if(active&&active.id!==id)throw new Failure('booking_needs_review',409,'We are checking an earlier booking. Please contact the studio.');
  return status(store,session,id,now,env,runtime,request);
 }
 const client=new Altegio(env,runtime),summary=JSON.parse(draft.summary) as Summary;
 try {
  // Old pending drafts cannot silently apply retired native credit requests.
  if(summary.certificate)throw new Failure('certificate_unavailable',409);
  const live=await client.summary(summary.serviceId,summary.staffId,localDatetime(summary.datetime,runtime.now()),summary.name,id,summary.extraId);
  if(live.priceMin!==summary.priceMin||live.priceMax!==summary.priceMax||live.durationMinutes!==summary.durationMinutes||live.reservedMinutes!==summary.reservedMinutes||live.serviceName!==summary.serviceName||live.staffName!==summary.staffName)
   throw new Failure('selection_changed',409,'The appointment details changed. Please review a fresh summary.');
  await client.check(live);
 }catch(error){
  const failure=error instanceof Failure?error:new Failure('booking_unavailable',503);
  await store.state(id,'checking','rejected',nowSeconds(runtime),failure.code);throw failure;
 }
 // This CAS is the durable send intent. Never reset or retry a 'sending' record.
 if(!await store.state(id,'checking','sending',nowSeconds(runtime)))return status(store,session,id,nowSeconds(runtime),env,runtime,request);
 let created:{id:string;managementHash:string};
 try {created=await client.create(summary,draft.phone);}catch(error){
  const failure=error instanceof Failure?error:new Failure('outcome_unknown',503);
  if(failure.code==='selection_rejected'){
   await store.state(id,'sending','rejected',nowSeconds(runtime),failure.code);throw failure;
  }
  await store.state(id,'sending','uncertain',nowSeconds(runtime));return {status:'uncertain',message:'Please contact the studio before booking again. We are checking the result.'};
 }
 const appointment:Appointment={id:created.id,serviceName:summary.serviceName,staffName:summary.staffName,datetime:summary.datetime,durationMinutes:summary.durationMinutes,price:summary.price,priceMin:summary.priceMin,priceMax:summary.priceMax};
 const reminderLead=summary.reminderMinutes??120;
 try {await store.confirmed(id,appointment,created.managementHash,nowSeconds(runtime),env.SMS_REMINDERS_ENABLED==='true'&&reminderLead>0?Date.parse(summary.datetime)/1000-reminderLead*60:undefined);}catch{return {status:'uncertain',message:'Please contact the studio to check your booking before trying again.'};}
 // Booking success survives a notification/configuration failure. No provider retry.
 draft.management_hash=created.managementHash;
 try{await optionalConfirmation(store,env,runtime,session,request,draft,appointment);}catch{/* durable provider attempts remain available for operator review */}
 return {status:'confirmed',appointment};
}
async function handleRequest(request:Request,env:Env,runtime:Runtime):Promise<Response> {
 try {
  const url=new URL(request.url);
  if(['e-motion.rest','www.e-motion.rest'].includes(url.hostname)&&env.PUBLIC_ORIGIN==='https://book.motionconcept.rest'){
   if(!['GET','HEAD'].includes(request.method))return new Response('Method not allowed',{status:405,headers:{Allow:'GET, HEAD','Cache-Control':'no-store'}});
   const path=/^\/(?:en\/|hy\/)?(?:privacy|terms|data-deletion)\/?$/.test(url.pathname)?url.pathname:'/';
   return new Response(null,{status:308,headers:{Location:env.PUBLIC_ORIGIN+path,'Cache-Control':'no-store','Referrer-Policy':'no-referrer'}});
  }
  if(url.origin===env.PUBLIC_ORIGIN&&url.pathname.startsWith('/calendar/')&&env.ECOSYSTEM_GATEWAY_ORIGIN){
   if(!['GET','HEAD'].includes(request.method)||!/^\/calendar\/[1-9]\d{0,14}$/.test(url.pathname))return new Response('Not found',{status:404});
   const target=new URL(env.ECOSYSTEM_GATEWAY_ORIGIN+url.pathname);for(const field of ['key','lang','format']){const value=url.searchParams.get(field);if(value)target.searchParams.set(field,value);}
   if(!/^[A-Za-z0-9_-]{43}$/.test(target.searchParams.get('key')||''))return new Response('Not found',{status:404});
   const result=await runtime.fetch(target,{method:request.method,headers:{Accept:request.headers.get('Accept')||'text/html'},redirect:'manual',signal:AbortSignal.timeout(12000)});
   const headers=new Headers(result.headers);headers.set('Cache-Control','private, no-store');headers.set('Referrer-Policy','no-referrer');return new Response(result.body,{status:result.status,headers});
  }
  if(request.method==='GET'&&env.PUBLIC_ORIGIN==='https://book.motionconcept.rest'){
   const bookingEntry=url.pathname==='/'||/^\/(?:booking|(?:hy|ru|en)\/booking)\/?$/.test(url.pathname);
   if(url.hostname==='booking.motionconcept.rest'&&env.LEGACY_BOOKING_ACTIVE!=='true')return new Response(null,{status:308,headers:{Location:env.PUBLIC_ORIGIN+(bookingEntry?'/':url.pathname),'Cache-Control':'no-store','Referrer-Policy':'no-referrer'}});
   if(url.hostname==='book.motionconcept.rest'){
    if(url.pathname==='/'){
     const assetURL=new URL(request.url);assetURL.search='';assetURL.hash='';
     return env.ASSETS?await env.ASSETS.fetch(new Request(assetURL,request)):new Response('Not found',{status:404});
    }
    if(bookingEntry)return new Response(null,{status:308,headers:{Location:env.PUBLIC_ORIGIN+'/','Cache-Control':'no-store','Referrer-Policy':'no-referrer'}});
   }
  }
  if(!url.pathname.startsWith('/api/'))return env.ASSETS?await env.ASSETS.fetch(request):new Response('Not found',{status:404});
  if(!url.pathname.startsWith(prefix+'/'))throw new Failure('not_found',404);
  configured(env);const mutation=request.method!=='GET';origin(request,env,mutation);
  if(!['GET','POST'].includes(request.method))throw new Failure('method_not_allowed',405);
  const store=new Store(env.DB),now=nowSeconds(runtime),ipHash=await hmac(env.SESSION_SECRET,'ip:'+ip(request));
  await store.rate('api-global',300,60,now);
  await store.rate('api-ip:'+ipHash,90,60,now);
  const path=url.pathname.slice(prefix.length);
  if(path==='/config'&&request.method==='GET')return json({turnstileSiteKey:env.TURNSTILE_SITE_KEY||'',bookingEnabled:env.BOOKING_ENABLED==='true',smsEnabled:!!(smsCap(env.SMS_DAILY_CAP)!==0&&smsCap(env.SMS_MONTHLY_CAP)!==0&&env.SMSGATE_CREDENTIALS_JSON&&env.TURNSTILE_SECRET&&env.TURNSTILE_SITE_KEY),countryCodes:destinationCodes(env),currency:env.ALTEGIO_CURRENCY||'AMD',timezone:'Asia/Yerevan'});
  if(path==='/session'&&request.method==='POST'){
   if(request.body)onlyKeys(await body(request),[]);
   let raw=cookie(request),session=/^[a-f0-9]{64}$/.test(raw)?await store.session(await hash(raw),now):null;
   if(!session){await store.rate('session-ip:'+ipHash,10,3600,now);await store.rate('session-global',100,60,now);raw=randomToken();session=await store.newSession(await hash(raw),now);}
   const unresolved=session.phone?await store.unresolvedPhone(session.phone):null;
   const preference=session.phone?await ecosystemContext(env,runtime,{action:'context',phone:session.phone,locale:requestLanguage(request),explicit:request.headers.get('X-Booking-Language-Source')==='explicit'}):undefined;
   return json({csrfToken:await hmac(env.SESSION_SECRET,'csrf:'+raw),phoneVerified:!!session.phone,...(session.phone?{phone:session.phone,preferredLocale:preference?.locale}:{}),...(unresolved?.session_id===session.id?{unresolvedDraftId:unresolved.id}:{})},200,{'Set-Cookie':setCookie(raw)});
  }
  const client=new Altegio(env,runtime);
  if(path==='/certificate-types'&&request.method==='GET')return json(giftCertificateCatalog(await client.catalog()));
  if(path==='/benefits'&&request.method==='GET')throw new Failure('certificate_selection_disabled',410);
  if(path==='/reviews'&&request.method==='GET')return json(await publicReviews(env,url.searchParams.get('staff_id')));
  if(request.method==='GET'&&['/services','/staff','/dates','/slots'].includes(path)){
   if(path==='/services'){const catalog=await client.catalog();return json({services:catalog.filter(s=>s.kind!=='extra'),extras:catalog.filter(s=>s.kind==='extra')});}
   const serviceId=identifier(url.searchParams.get('service_id'));
   const extraId=url.searchParams.has('extra_id')?identifier(url.searchParams.get('extra_id')):undefined;
   if(path==='/staff'){await client.selection(serviceId,extraId);const [staff,anyStaff]=await Promise.all([client.staff(serviceId,extraId),client.anyStaffEnabled().catch(()=>false)]);return json({staff,anyStaff});}
   const staffId=staffIdentifier(url.searchParams.get('staff_id'));
   if(staffId==='0'){
    const quote=await client.anyQuote(serviceId,extraId);
    if(path==='/dates')return json({dates:await client.dates(serviceId,staffId,extraId),selection:quote});
    const date=dateValue(url.searchParams.get('date'));
    if(date<localDate(runtime.now())||new Date(date+'T00:00:00+04:00').getTime()>runtime.now()+183*86400000)throw new Failure('invalid_date');
    return json({slots:(await client.slots(serviceId,staffId,date,extraId)).filter(slot=>!extraId||slot.durationMinutes>=quote.durationMinutes).map(({datetime,label})=>({datetime,label}))});
   }
   const pair=await client.validatePair(serviceId,staffId,extraId);
   if(path==='/dates')return json({dates:await client.dates(serviceId,staffId,extraId),selection:{serviceName:pair.items.map(item=>item.name).join(' + '),durationMinutes:pair.items.reduce((sum,item)=>sum+item.durationMinutes,0),priceMin:pair.items.reduce((sum,item)=>sum+item.priceMin,0),priceMax:pair.items.reduce((sum,item)=>sum+item.priceMax,0)}});
   const date=dateValue(url.searchParams.get('date'));
   if(date<localDate(runtime.now())||new Date(date+'T00:00:00+04:00').getTime()>runtime.now()+183*86400000)throw new Failure('invalid_date');
   const minimum=pair.items.reduce((sum,item)=>sum+item.durationMinutes,0);
   return json({slots:(await client.slots(serviceId,staffId,date,extraId)).filter(slot=>!extraId||slot.durationMinutes>=minimum).map(({datetime,label})=>({datetime,label}))});
  }
  const {session}=await authenticated(request,env,store,runtime,mutation);
  if(request.method==='GET'&&path==='/review-visits'){
   await store.rate('reviews-read:'+session.id,6,60,now);return json(await reviewVisits(env,runtime,session));
  }
  if(request.method==='GET'&&path==='/verification/status')return json(await verificationStatus(store,session,env,runtime));
  if(request.method==='GET'&&path.startsWith('/appointments/'))return json(await status(store,session,validDraftId(path.slice('/appointments/'.length)),now,env,runtime,request));
  if(request.method!=='POST')throw new Failure('not_found',404);
  const data=await body(request);
  if(path==='/reviews'){
   await store.rate('reviews-write:'+session.id,5,3600,now);
   if(session.phone)await store.rate('reviews-phone:'+await hmac(env.SESSION_SECRET,session.phone),10,86400,now);
   return json(await submitReview(env,runtime,session,data,requestLanguage(request)));
  }
  if(path==='/preferences'){
   onlyKeys(data,[]);if(!session.phone)throw new Failure('verification_required',403);
   const preference=await ecosystemContext(env,runtime,{action:'context',phone:session.phone,locale:requestLanguage(request),explicit:request.headers.get('X-Booking-Language-Source')==='explicit'});
   return json({preferredLocale:preference.locale});
  }
  if(path==='/verification/request'){
   onlyKeys(data,['phone','turnstileToken']);
   if(env.BOOKING_ENABLED!=='true')throw new Failure('booking_paused',503,'Online booking is temporarily unavailable. Please contact the studio.');
   const phone=normalizePhone(data.phone);allowedDestination(phone,env);
   if(smsCap(env.SMS_DAILY_CAP)===0||smsCap(env.SMS_MONTHLY_CAP)===0)throw new Failure('verification_unavailable',503,'Phone verification is temporarily unavailable. Please contact the studio.');
   credentials(env);await verifyTurnstile(data.turnstileToken,request,env,runtime);
   const otp=code(),digest=await hmac(env.SESSION_SECRET,`otp:${session.id}:${phone}:${otp}`);
   const reservation=await smsReservation(store,env,runtime,session,request,'otp',digest,phone);
   const receipt=await sendSms(env,runtime,reservation.id,phone,otpMessage(otp,requestLanguage(request)),reservation.expiry);
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
   onlyKeys(data,['serviceId','staffId','datetime','name','consent','email','comment','extraId','certificateId','reminderMinutes']);
   if(data.reminderMinutes!==undefined&&![0,60,120,1440].includes(data.reminderMinutes as number))throw new Failure('invalid_reminder');
   if(!session.phone)throw new Failure('verification_required',403,'Verify your booking phone first.');
   if(data.consent!==true)throw new Failure('consent_required',400,'Please confirm the booking request.');
   if(data.certificateId!==undefined)throw new Failure('certificate_unavailable',409);
   const name=safeText(data.name,120);
   if(!name||name.length<2||typeof data.name!=='string'||data.name.length>120||/[<>\u0000-\u001f]/.test(data.name))throw new Failure('invalid_name',400,'Enter your first and last name.');
   await store.rate('draft-session:'+session.id,10,3600,now);
   const staffId=staffIdentifier(data.staffId);
   const summary=await (staffId==='0'?client.anySummary(identifier(data.serviceId),localDatetime(data.datetime,runtime.now()),name,randomToken(16),data.extraId===undefined?undefined:identifier(data.extraId)):client.summary(identifier(data.serviceId),staffId,localDatetime(data.datetime,runtime.now()),name,randomToken(16),data.extraId===undefined?undefined:identifier(data.extraId)));
   const email=typeof data.email==='string'?data.email.trim():'';
   const comment=typeof data.comment==='string'?data.comment.trim():'';
   if((data.email!==undefined&&typeof data.email!=='string')||email.length>254||(email&&!/^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(email))||/[\u0000-\u001f\u007f]/.test(email))throw new Failure('invalid_email');
   if((data.comment!==undefined&&typeof data.comment!=='string')||comment.length>1000||/[<>\u0000-\u0008\u000b-\u001f\u007f]/.test(comment))throw new Failure('invalid_comment');
   await client.check(summary);
   const preference=await ecosystemContext(env,runtime,{action:'prepare',phone:session.phone,locale:requestLanguage(request),explicit:request.headers.get('X-Booking-Language-Source')==='explicit',consent:true});
   summary.locale=preference.locale;summary.email=email;summary.comment=comment;summary.reminderMinutes=data.reminderMinutes===undefined?120:data.reminderMinutes as number;
   await store.saveDraft(session,summary,now);const {reservedMinutes:_privateReservation,certificate,...publicSummary}=summary;return json({draft:{...publicSummary,...(certificate?{certificate:{id:certificate.id,title:certificate.title}}:{})},phoneVerified:true});
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
export async function handle(request:Request,env:Env,runtime:Runtime={fetch:globalThis.fetch.bind(globalThis),now:Date.now}):Promise<Response>{
 const response=await handleRequest(request,env,runtime);
 if(new URL(request.url).pathname.startsWith(prefix+'/'))response.headers.set('Content-Language',requestLanguage(request));
 return response;
}
export default {
 async fetch(request:Request,env:Env):Promise<Response>{return handle(request,env);},
 async scheduled(_controller:unknown,env:Env):Promise<void>{
  if(!env.DB)return;
  await runNotifications(env,{fetch:globalThis.fetch.bind(globalThis),now:Date.now});
  const now=new Date();if(now.getUTCHours()===1&&now.getUTCMinutes()<5)await new Store(env.DB).cleanup(Math.floor(now.getTime()/1000));
 }
};
