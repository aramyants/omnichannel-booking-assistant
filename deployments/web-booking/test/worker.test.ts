import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { readFileSync, readdirSync } from 'node:fs';
import worker, { handle } from '../src/worker.ts';
import { Store } from '../src/store.ts';
import { normalizePhone, hash, smsCap } from '../src/security.ts';
import { dateValue, staffPhoto, treatmentMinutes } from '../src/altegio.ts';
import type { Database, D1Statement, Env, Runtime } from '../src/types.ts';
import { runNotifications } from '../src/notifications.ts';
import { allowedDestination, destinationCodes } from '../src/sms.ts';
import { origin, verifyTurnstile } from '../src/security.ts';
import { createHmac } from 'node:crypto';

class SQLiteD1 implements Database {
 db=new DatabaseSync(':memory:');
 constructor(){const dir=new URL('../migrations/',import.meta.url);for(const file of readdirSync(dir).filter(x=>x.endsWith('.sql')).sort())this.db.exec(readFileSync(new URL(file,dir),'utf8'));}
 prepare(sql:string):D1Statement{
  const db=this.db;
  function statement(values:unknown[]=[]):D1Statement{return {
   bind(...next:unknown[]){return statement(next);},
   async first<T>(){return (db.prepare(sql).all(...values as never[])[0] || null) as T|null;},
   async all<T>(){return {results:db.prepare(sql).all(...values as never[]) as T[],success:true,meta:{changes:0}};},
   async run(){const result=db.prepare(sql).run(...values as never[]);return {results:[],success:true,meta:{changes:Number(result.changes)}};}
  };}return statement();
 }
 async batch(statements:D1Statement[]){this.db.exec('BEGIN');try{const result=[];for(const s of statements)result.push(await s.run());this.db.exec('COMMIT');return result;}catch(e){this.db.exec('ROLLBACK');throw e;}}
}

test('verified reviews require phone ownership and a current completed visit, retain all ratings and remain unique',async()=>{
 const f=fixture();
 await f.session();
 assert.equal((await f.request('/review-visits')).response.status,403);
 assert.equal((await f.request('/reviews',{recordId:'42',rating:5,publish:true,displayName:'Guest'})).response.status,403);
 await f.verified();f.env.ECOSYSTEM_GATEWAY_ORIGIN='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app';
 const provider=f.runtime.fetch;let checks=0;
 f.runtime.fetch=async(input,init)=>{
  if(String(input).includes('/internal/booking-context')){const body=JSON.parse(String(init!.body));
   const visit={id:'42',staffId:'99',staffName:'Specialist',serviceName:'Treatment',datetime:'2026-10-20T10:00:00+04:00'};
   if(body.action==='review-visits')return Response.json({locale:'en',visits:[visit]});
   if(body.action==='review-check'){checks++;return body.recordId==='42'?Response.json({locale:'en',visit}):new Response('',{status:404})}
   return Response.json({locale:'en'});
  }return provider(input,init);
 };
 const visits=await f.request('/review-visits');assert.equal(visits.value.visits.length,1);assert.equal(visits.value.visits[0].reviewed,false);
 const draft={recordId:'42',rating:1,comment:'The session could be better.',publish:true,displayName:'Guest'};
 for(const invalid of [{rating:6},{rating:2.5},{comment:'<script>'},{comment:'client@example.com'},{comment:'+374 94 768067'},{displayName:''}]){await f.db.prepare("DELETE FROM rate_buckets WHERE id LIKE 'reviews-%'").run();assert.equal((await f.request('/reviews',{...draft,...invalid})).response.status,400);}
 await f.db.prepare("DELETE FROM rate_buckets WHERE id LIKE 'reviews-%'").run();
 assert.equal((await f.request('/reviews',{...draft,recordId:'43'})).response.status,503);
 assert.equal((await f.request('/reviews',draft)).response.status,200);
 // These intentional test submissions use fresh session rate buckets only.
 await f.db.prepare("DELETE FROM rate_buckets WHERE id LIKE 'reviews-%'").run();
 assert.equal((await f.request('/reviews',{...draft,rating:5})).response.status,200);
 const pub=await f.request('/reviews');assert.equal(pub.value.count,1);assert.equal(pub.value.average,1);assert.equal(pub.value.reviews[0].rating,1);assert(!JSON.stringify(pub.value).includes('+374'));assert(!JSON.stringify(pub.value).includes('phone_hash'));assert(!JSON.stringify(pub.value).includes('record_id'));
 assert.equal((await f.request('/review-visits')).value.visits[0].reviewed,true);assert.equal(checks,2);
});

test('private feedback is excluded from public ratings and reviews',async()=>{
 const f=fixture();await f.db.prepare('INSERT INTO verified_reviews VALUES(?,?,?,?,?,?,?,?,?,?)').bind('44','private-hash','99','Specialist',5,'Private feedback','',0,'en',1).run();
 const pub=await f.request('/reviews');assert.equal(pub.value.count,0);assert.equal(pub.value.reviews.length,0);assert.equal(pub.value.specialists.length,0);
});

test('optional contact fields validate and reach only the booking payload without native email sends',async()=>{
 const f=fixture();await f.verified();
 const base={serviceId:'13827004',staffId:'99',datetime:'2026-10-21T12:00:00+04:00',name:'Test Guest',consent:true};
 for(const extras of [{email:'bad email'},{email:22},{comment:'x'.repeat(1001)},{comment:'<script>test</script>'}])assert.equal((await f.request('/drafts',{...base,...extras})).response.status,400);
 assert.equal(f.counts.createCalls,0);
 const draft=await f.request('/drafts',{...base,email:'  client@example.com  ',comment:'Please focus on shoulders.'},{'X-Booking-Language':'en'});
 assert.equal(draft.response.status,200);assert.equal(draft.value.draft.locale,'en');
 const result=await f.request('/appointments',{draftId:draft.value.draft.id});assert.equal(result.response.status,200);
 assert.equal(f.createPayload?.email,'client@example.com');assert.match(String(f.createPayload?.comment),/Please focus on shoulders/);assert.equal(f.createPayload?.notify_by_email,0);
});
test('OTP text follows the supported customer language',async()=>{
 for(const [lang,pattern] of [['en',/booking code/],['ru',/код/],['hy',/կոդը/]] as const){
  const f=fixture();await f.session();const requested=await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'},{'X-Booking-Language':lang});assert.equal(requested.response.status,200);
  assert.match(f.smsTexts[0],pattern);assert(f.smsTexts[0].length<=201);
 }
});
test('shared bridge authenticates exact bodies and hands web confirmation to one sender',async()=>{
 const f=fixture();f.env.ECOSYSTEM_GATEWAY_ORIGIN='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app';f.env.SMS_CONFIRMATIONS_ENABLED='true';
 const provider=f.runtime.fetch;const actions:string[]=[];
 f.runtime.fetch=async(input,init)=>{
  if(String(input).includes('/internal/booking-context')){
   const headers=init!.headers as Record<string,string>,body=String(init!.body),time=headers['X-Emotion-Time'];
   const key=createHmac('sha256',f.env.ALTEGIO_USER_TOKEN!).update('emotion-ecosystem-auth-v1').digest();
   assert.equal(headers['X-Emotion-Signature'],createHmac('sha256',key).update(time+'\nPOST\n/internal/booking-context\n'+body).digest('hex'));
   const data=JSON.parse(body);actions.push(data.action);assert.equal(data.phone,'+37491123456');if(data.action==='confirmed')assert.equal(data.managementProof,'private-do-not-leak');
   return Response.json({locale:'en',...(data.action==='confirmed'?{calendarUrl:f.env.PUBLIC_ORIGIN+'/calendar/6789?key='+'a'.repeat(43)+'&lang=en'}:{})});
  }
  return provider(input,init);
 };
 await f.verified();const draft=await f.draft();assert.equal(draft.value.draft.locale,'en');
 const result=await f.request('/appointments',{draftId:draft.value.draft.id});assert.equal(result.value.status,'confirmed');assert(result.value.appointment.calendarUrl);
 await f.request('/appointments',{draftId:draft.value.draft.id});assert.deepEqual(actions,['prepare','confirmed']);
 assert.equal(f.counts.sentSms,1);assert.match(String(f.createPayload?.api_id),/^emotion-web-v2-/);
});
test('failed shared preferences cannot create an appointment with conflicting notification settings',async()=>{
 const f=fixture();await f.verified();f.env.ECOSYSTEM_GATEWAY_ORIGIN='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app';const provider=f.runtime.fetch;
 f.runtime.fetch=async(input,init)=>String(input).includes('/internal/booking-context')?new Response('Unavailable',{status:503}):provider(input,init);
 assert.equal((await f.draft()).response.status,503);assert.equal(f.counts.createCalls,0);
});

test('a failed confirmation context handoff recovers without repeating appointment creation or SMS',async()=>{
 const f=fixture();f.env.ECOSYSTEM_GATEWAY_ORIGIN='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app';
 const provider=f.runtime.fetch;let confirms=0;
 f.runtime.fetch=async(input,init)=>{
  if(String(input).includes('/internal/booking-context')){
   const data=JSON.parse(String(init!.body));
   if(data.action==='confirmed'&&++confirms===1)return new Response('Unavailable',{status:503});
   return Response.json({locale:'en',...(data.action==='confirmed'?{calendarUrl:f.env.PUBLIC_ORIGIN+'/calendar/6789?key='+'a'.repeat(43)+'&lang=en'}:{})});
  }
  return provider(input,init);
 };
 await f.verified();const prepared=await f.draft(),id=prepared.value.draft.id;
 const first=await f.request('/appointments',{draftId:id});assert.equal(first.value.status,'confirmed');assert.equal(first.value.appointment.calendarUrl,undefined);
 const recovered=await f.request('/appointments/'+id);assert.equal(recovered.value.status,'confirmed');assert(recovered.value.appointment.calendarUrl);
 assert.equal(f.counts.createCalls,1);assert.equal(f.counts.sentSms,1);assert.equal(confirms,2);
});

test('scheduled context recovery heals a confirmed receipt without booking or SMS retries',async()=>{
 const f=fixture();f.env.ECOSYSTEM_GATEWAY_ORIGIN='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app';
 const provider=f.runtime.fetch;let confirms=0;
 f.runtime.fetch=async(input,init)=>{
  if(String(input).includes('/internal/booking-context')){
   const data=JSON.parse(String(init!.body));
   if(data.action==='confirmed'&&++confirms===1)return new Response('Unavailable',{status:503});
   return Response.json({locale:'en',...(data.action==='confirmed'?{calendarUrl:f.env.PUBLIC_ORIGIN+'/calendar/6789?key='+'a'.repeat(43)+'&lang=en'}:{})});
  }
  return provider(input,init);
 };
 await f.verified();const prepared=await f.draft(),id=prepared.value.draft.id;
 await f.request('/appointments',{draftId:id});f.advance(301);await runNotifications(f.env,f.runtime);await runNotifications(f.env,f.runtime);
 const row=await f.db.prepare('SELECT appointment FROM drafts WHERE id=?').bind(id).first<{appointment:string}>();assert(JSON.parse(row!.appointment).calendarUrl);
 assert.equal(confirms,2);assert.equal(f.counts.createCalls,1);assert.equal(f.counts.sentSms,1);
});
function fixture(){
 const db=new SQLiteD1();
 const env:Env={DB:db,PUBLIC_ORIGIN:'https://booking.example.com',SESSION_SECRET:'test-only-secret-that-is-over-thirty-two-characters',ALTEGIO_PARTNER_TOKEN:'test-partner',ALTEGIO_USER_TOKEN:'test-private-user',ALTEGIO_COMPANY_ID:'1389810',TURNSTILE_SECRET:'test-turnstile',TURNSTILE_SITE_KEY:'test-sitekey',SMSGATE_CREDENTIALS_JSON:JSON.stringify({username:'test',password:'fake',device_id:'test-device',sim_number:1}),SMS_DAILY_CAP:'10',SMS_MONTHLY_CAP:'100',SMS_ALLOWED_COUNTRY_CODES:'374',BOOKING_ENABLED:'true'};
 let time=Date.parse('2026-10-20T08:00:00+04:00'),csrf='',cookie='',otp='',createCalls=0,checkCalls=0,turnstileHostname='booking.example.com',createMode='ok',createHook:undefined|(()=>Promise<void>);
 let createPayload:Record<string,unknown>|undefined;const smsTexts:string[]=[];let sentSms=0,receiptReads=0,price=18000,slotSeconds=4800,receiptState='Delivered',smsMode='ok',liveDeleted=false,liveStart='2026-10-21T12:00:00+04:00',liveReadFails=false,currentPhone='+37491123456';
 const runtime:Runtime={now:()=>time,fetch:async(input:RequestInfo|URL,init?:RequestInit)=>{
  // workerd rejects redirect:'error' before sending; Node's native fetch does not.
  assert.equal(init?.redirect,'manual','all provider fetches must use Workers-supported manual redirects');
  const url=String(input);
  if(url.startsWith('https://api.alteg.io/')&&!url.includes('/record/'))assert.equal((init!.headers as Record<string,string>).Authorization,'Bearer test-partner','private credentials must not reach public catalog or booking endpoints');
  if(url.includes('siteverify'))return Response.json({success:true,hostname:turnstileHostname,action:'booking_otp'});
  if(url.includes('sms-gate.app')&&!init?.body){receiptReads++;return Response.json({id:url.split('/').at(-1),state:receiptState});}
  if(url.includes('sms-gate.app')){const data=JSON.parse(init!.body as string);sentSms++;smsTexts.push(data.textMessage.text);otp=data.textMessage.text.match(/\b\d{6}\b/)?.[0]||'';if(smsMode==='timeout')throw new Error('simulated sms timeout');return Response.json({id:data.id,state:'Pending'},{status:202});}
  if(url.includes('/book_services/'))return Response.json({success:true,data:{category:[{id:1,title:'Motion Relax'},{id:2,title:'Add More Time'}],services:[{id:13827004,title:'80 min',active:1,category_id:1,price_min:price,price_max:price,seance_length:url.includes('staff_id')?4800:null,comment:'<p>Relax</p>'},{id:13827268,title:'20 min',active:1,category_id:2,price_min:5000,price_max:5000}]}});
  if(url.includes('/book_staff/'))return Response.json({success:true,data:[{id:99,name:'Test Specialist',specialization:'Massage',information:'<p>Care &amp; comfort.</p><img src=x onerror=alert(1)>',bookable:true,fired:0,avatar:'https://assets.alteg.io/masters/sm/test.png',avatar_big:'https://assets.alteg.io/masters/origin/test.png'},{id:100,name:'Unavailable',bookable:false}]});
  if(url.includes('/book_dates/'))return Response.json({success:true,data:{booking_dates:['2026-10-21']}});
  if(url.includes('/book_times/'))return Response.json({success:true,data:[{datetime:'2026-10-21T12:00:00+04:00',seance_length:slotSeconds}]});
  if(url.includes('/book_check/')){checkCalls++;return Response.json({success:true,data:{}});}
  if(url.includes('/book_record/')&&(!init?.method||init.method==='GET')){if(liveReadFails)throw new Error('calendar unavailable');return Response.json({success:true,data:{id:6789,datetime:liveStart,length:slotSeconds,deleted:liveDeleted,staff:{id:99,name:'Test Specialist'},services:[{id:13827004,title:'80 min'}]}});}
  if(url.includes('/record/')){assert.equal((init!.headers as Record<string,string>).Authorization,'Bearer test-partner, User test-private-user');return Response.json({success:true,data:{id:6789,company_id:1389810,deleted:liveDeleted,client:{phone:currentPhone}}});}
  if(url.includes('/book_record/')){createPayload=JSON.parse(String(init?.body));createCalls++;if(createHook)await createHook();if(createMode==='timeout')throw new Error('simulated timeout');if(createMode==='500')return new Response('bad',{status:500});return Response.json({success:true,data:[{record_id:6789,record_hash:'private-do-not-leak'}]});}
  throw new Error('unexpected fake endpoint '+url);
 }};
 async function request(path:string,data?:unknown,headers:Record<string,string>={}){
  const response=await handle(new Request(env.PUBLIC_ORIGIN+'/api/booking'+path,{method:data===undefined?'GET':'POST',headers:{Origin:env.PUBLIC_ORIGIN,'CF-Connecting-IP':'192.0.2.1',...(data===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(csrf?{'X-CSRF-Token':csrf}:{}),...headers},...(data===undefined?{}:{body:JSON.stringify(data)})}),env,runtime);
  const value=await response.json();return {response,value};
 }
 async function session(){const result=await request('/session',{});assert.equal(result.response.status,200);csrf=result.value.csrfToken;cookie=result.response.headers.get('set-cookie')!.split(';')[0];return result;}
 async function verified(){await session();assert.equal((await request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,200);assert.match(otp,/^\d{6}$/);assert.equal((await request('/verification/confirm',{code:otp})).response.status,200);}
 async function draft(){return request('/drafts',{serviceId:'13827004',staffId:'99',datetime:'2026-10-21T12:00:00+04:00',name:'Test Guest',consent:true});}
 return {db,env,runtime,request,session,verified,draft,get createPayload(){return createPayload;},get smsTexts(){return smsTexts;},get counts(){return {createCalls,checkCalls,sentSms,receiptReads};},get otp(){return otp;},get cookie(){return cookie;},get csrf(){return csrf;},advance(seconds:number){time+=seconds*1000;},set hostname(v:string){turnstileHostname=v;},set createMode(v:string){createMode=v;},set price(v:number){price=v;},set slotSeconds(v:number){slotSeconds=v;},set receiptState(v:string){receiptState=v;},set smsMode(v:string){smsMode=v;},set liveDeleted(v:boolean){liveDeleted=v;},set liveStart(v:string){liveStart=v;},set liveReadFails(v:boolean){liveReadFails=v;},set currentPhone(v:string){currentPhone=v;},set createHook(v:(()=>Promise<void>)|undefined){createHook=v;}};
}
test('sessions use secure cookies, exact origin and CSRF; no session ID in response',async()=>{
 const f=fixture(),s=await f.session();
 assert.match(s.response.headers.get('set-cookie')!,/HttpOnly; Secure; SameSite=Strict/);
 assert.equal(s.response.headers.get('cache-control'),'no-store');assert.equal(s.value.phoneVerified,false);
 const cross=await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'},{Origin:'https://evil.example'});assert.equal(cross.response.status,403);
 const csrf=await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'},{'X-CSRF-Token':'bad'});assert.equal(csrf.response.status,403);assert.equal(f.counts.sentSms,0);
 const stored=f.db.db.prepare('SELECT id FROM sessions').get() as {id:string};assert.notEqual(stored.id,f.cookie.split('=')[1]);assert.equal(stored.id,await hash(f.cookie.split('=')[1]));
});
test('the old booking host redirects GETs to the canonical host while dropping query data',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';
 for(const [path,expected] of [['/?phone=private','https://book.motionconcept.rest/'],['/ru/booking/?code=private','https://book.motionconcept.rest/'],['/api/booking/config?token=private','https://book.motionconcept.rest/api/booking/config']]){
  const response=await handle(new Request('https://booking.motionconcept.rest'+path),f.env,f.runtime);assert.equal(response.status,308);assert.equal(response.headers.get('location'),expected);assert.ok(!response.headers.get('location')!.includes('private'));
 }
});
test('canonical root serves booking HTML internally without changing the clean browser URL',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';
 const assetRequests:string[]=[];
 f.env.ASSETS={async fetch(request:Request){assetRequests.push(request.url);assert.equal(request.headers.get('Accept-Language'),'ru');return new Response('<html>booking form</html>',{headers:{'Content-Type':'text/html','X-Test-Asset':'preserved'}});}};
 const root=await handle(new Request('https://book.motionconcept.rest/?private=data',{headers:{'Accept-Language':'ru'}}),f.env,f.runtime);assert.equal(root.status,200);assert.equal(root.headers.get('location'),null);assert.equal(root.headers.get('x-test-asset'),'preserved');assert.equal(await root.text(),'<html>booking form</html>');assert.deepEqual(assetRequests,['https://book.motionconcept.rest/']);
 f.env.ASSETS=undefined;
 for(const host of ['www.motionconcept.rest','booking.example.com']){
  const response=await handle(new Request('https://'+host+'/'),f.env,f.runtime);assert.equal(response.status,404);assert.equal(response.headers.get('location'),null);
 }
 const post=await handle(new Request('https://booking.motionconcept.rest/api/booking/session',{method:'POST',headers:{Origin:'https://booking.motionconcept.rest','Content-Type':'application/json','CF-Connecting-IP':'192.0.2.1'},body:'{}'}),f.env,f.runtime);assert.notEqual(post.status,308);assert.equal(post.headers.get('location'),null);
});
test('canonical duplicate booking paths redirect to the clean root and marketing assets remain untouched',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';
 for(const path of ['/booking','/booking/','/ru/booking','/hy/booking/','/en/booking/']){
  const response=await handle(new Request('https://book.motionconcept.rest'+path+'?private=data'),f.env,f.runtime);assert.equal(response.status,308);assert.equal(response.headers.get('location'),'https://book.motionconcept.rest/');
 }
 const requests:string[]=[];f.env.ASSETS={async fetch(request:Request){requests.push(request.url);return new Response('asset');}};
 assert.equal((await handle(new Request('https://book.motionconcept.rest/assets/logo.png'),f.env,f.runtime)).status,200);
 assert.equal((await handle(new Request('https://www.motionconcept.rest/'),f.env,f.runtime)).status,200);
 assert.deepEqual(requests,['https://book.motionconcept.rest/assets/logo.png','https://www.motionconcept.rest/']);
});
test('the exact trusted legacy alias works only while its explicit flag is enabled',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';f.env.LEGACY_BOOKING_ACTIVE='true';
 const oldRequest=(method='GET',originHeader='https://booking.motionconcept.rest')=>new Request('https://booking.motionconcept.rest/api/booking/config',{method,headers:{Origin:originHeader,'CF-Connecting-IP':'192.0.2.1'}});
 assert.equal((await handle(oldRequest(),f.env,f.runtime)).status,200);
 assert.doesNotThrow(()=>origin(oldRequest('POST'),f.env,true));
 delete f.env.LEGACY_BOOKING_ACTIVE;assert.throws(()=>origin(oldRequest('POST'),f.env,true));assert.equal((await handle(oldRequest(),f.env,f.runtime)).status,308);
 f.env.LEGACY_BOOKING_ACTIVE='true';f.env.PUBLIC_ORIGIN='https://booking.example.com';assert.throws(()=>origin(oldRequest('POST'),f.env,true));
});

test('the studio short domains redirect to canonical booking without forwarding private queries or mutations',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';
 for(const host of ['e-motion.rest','www.e-motion.rest']){
  for(const method of ['GET','HEAD']){const result=await handle(new Request('https://'+host+'/?phone=private',{method}),f.env,f.runtime);assert.equal(result.status,308);assert.equal(result.headers.get('location'),'https://book.motionconcept.rest/');}
  const legal=await handle(new Request('https://'+host+'/hy/privacy/?code=private'),f.env,f.runtime);assert.equal(legal.headers.get('location'),'https://book.motionconcept.rest/hy/privacy/');
  const post=await handle(new Request('https://'+host+'/api/booking/appointments',{method:'POST',body:'{}'}),f.env,f.runtime);assert.equal(post.status,405);assert.equal(post.headers.get('location'),null);
 }
 assert.equal(f.counts.createCalls,0);assert.equal(f.counts.sentSms,0);
});
test('canonical and legacy origins cannot make cross-host credentialed mutations',()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';f.env.LEGACY_BOOKING_ACTIVE='true';
 for(const [incoming,sender] of [['https://booking.motionconcept.rest','https://book.motionconcept.rest'],['https://book.motionconcept.rest','https://booking.motionconcept.rest'],['https://book.motionconcept.rest','https://evil.example'],['https://booking.motionconcept.rest','https://evil.example']])assert.throws(()=>origin(new Request(incoming+'/api/booking/session',{method:'POST',headers:{Origin:sender}}),f.env,true));
 assert.throws(()=>origin(new Request('https://booking.motionconcept.rest:8443/api/booking/session',{method:'POST',headers:{Origin:'https://booking.motionconcept.rest:8443'}}),f.env,true));
});
test('Turnstile accepts only the active incoming hostname, including the temporary alias',async()=>{
 const f=fixture();f.env.PUBLIC_ORIGIN='https://book.motionconcept.rest';f.env.LEGACY_BOOKING_ACTIVE='true';
 const request=new Request('https://booking.motionconcept.rest/api/booking/verification/request',{method:'POST',headers:{Origin:'https://booking.motionconcept.rest','CF-Connecting-IP':'192.0.2.1'}});
 f.hostname='booking.motionconcept.rest';await assert.doesNotReject(verifyTurnstile('test-token-accepted',request,f.env,f.runtime));
 f.hostname='book.motionconcept.rest';await assert.rejects(verifyTurnstile('test-token-accepted',request,f.env,f.runtime));
 f.hostname='booking.motionconcept.rest';f.env.LEGACY_BOOKING_ACTIVE='false';await assert.rejects(verifyTurnstile('test-token-accepted',request,f.env,f.runtime));assert.equal(f.counts.sentSms,0);
});
test('missing configuration and absent human proof fail closed without SMS',async()=>{
 const f=fixture();await f.session();f.env.SMS_DAILY_CAP='0';
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,503);
 f.env.SMS_DAILY_CAP='10';f.hostname='evil.example';
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,400);assert.equal(f.counts.sentSms,0);
});
test('international routes are disabled by default and request phone is normalized',async()=>{
 assert.equal(normalizePhone('091 123456'),'+37491123456');
 const f=fixture();await f.session();const result=await f.request('/verification/request',{phone:'+14155552671',turnstileToken:'test-token-accepted'});
 assert.equal(result.value.error.code,'destination_unavailable');assert.equal(f.counts.sentSms,0);
});
test('worldwide mode accepts metadata-valid AM, RU, US, UK and FR numbers in canonical E164',async()=>{
 const f=fixture();f.env.SMS_ALLOWED_COUNTRY_CODES='*';
 for(const [input,expected] of [['091 123456','+37491123456'],['+7 (913) 733-39-31','+79137333931'],['+1 (415) 555-2671','+14155552671'],['0044 7911 123456','+447911123456'],['+33 6 12 34 56 78','+33612345678']]){
  const normalized=normalizePhone(input);assert.equal(normalized,expected);assert.doesNotThrow(()=>allowedDestination(normalized,f.env));
 }
 assert.deepEqual((await f.request('/config')).value.countryCodes,['*']);
});
test('strict global validation rejects unknown calling codes, invalid ranges, extraction and extensions',()=>{
 for(const invalid of ['+999123456789','+37400000000','+12223333333','+440000000000','Call me +14155552671','+14155552671 ext 123','+14155552671#123','+14155552671 x12','+14155552671/+33612345678'])assert.throws(()=>normalizePhone(invalid));
});
test('destination allowlists match complete shared calling codes and invalid config fails closed',async()=>{
 const f=fixture();f.env.SMS_ALLOWED_COUNTRY_CODES='1,44';
 assert.doesNotThrow(()=>allowedDestination(normalizePhone('+14155552671'),f.env));assert.doesNotThrow(()=>allowedDestination(normalizePhone('+14165552671'),f.env));assert.doesNotThrow(()=>allowedDestination(normalizePhone('+447911123456'),f.env));
 assert.throws(()=>allowedDestination(normalizePhone('+33612345678'),f.env));
 for(const invalid of ['', '*,374','999','3','374,','abc']){f.env.SMS_ALLOWED_COUNTRY_CODES=invalid;assert.throws(()=>destinationCodes(f.env));assert.equal((await f.request('/config')).response.status,503);}
 delete f.env.SMS_ALLOWED_COUNTRY_CODES;assert.deepEqual(destinationCodes(f.env),['374']);
});
test('OTP expiry and five wrong attempts survive subsequent requests',async()=>{
 const f=fixture();await f.session();await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'});
 for(let i=0;i<5;i++)assert.equal((await f.request('/verification/confirm',{code:f.otp==='000000'?'111111':'000000'})).response.status,400);
 assert.equal((await f.request('/verification/confirm',{code:f.otp})).value.error.code,'code_expired');
 const stored=f.db.db.prepare('SELECT otp_hash FROM sessions').get() as {otp_hash:string};assert.notEqual(stored.otp_hash,f.otp);
 const other=fixture();await other.session();await other.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'});other.advance(301);
 assert.equal((await other.request('/verification/confirm',{code:other.otp})).value.error.code,'code_expired');
});
test('SMS sends reserve caps before provider call and cannot be immediately repeated',async()=>{
 const f=fixture();await f.session();
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,200);
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,429);assert.equal(f.counts.sentSms,1);
});
test('global daily cap is atomic across distinct phone/session/IP identities',async()=>{
 const f=fixture(),store=new Store(f.env.DB),now=Math.floor(f.runtime.now()/1000);
 const reserve=(i:number)=>store.reserveSms({id:'id'+i,sessionId:'session'+i,phoneHash:'phone'+i,ipHash:'ip'+i,purpose:'otp',now,expiry:now+300,daily:1,monthly:10});
 // SQLite writes serialize; D1 executes each batch transaction atomically.
 await reserve(1);await assert.rejects(reserve(2));assert.equal((f.db.db.prepare('SELECT SUM(parts) as parts FROM sms_outbox').get() as {parts:number}).parts,1);
});
test('explicit unlimited skips business volume caps while all abuse limits remain',async()=>{
 assert.equal(smsCap('unlimited'),null);assert.equal(smsCap(''),0);assert.equal(smsCap('Unlimited'),0);assert.equal(smsCap('0'),0);
 const f=fixture();f.env.SMS_DAILY_CAP='unlimited';f.env.SMS_MONTHLY_CAP='unlimited';await f.session();
 assert.equal((await f.request('/config')).value.smsEnabled,true);
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,200);
 assert.equal((await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,429);assert.equal(f.counts.sentSms,1);
 const store=new Store(f.env.DB),now=Math.floor(f.runtime.now()/1000);
 for(let i=0;i<15;i++)await store.reserveSms({id:'unlimited'+i,sessionId:'distinct'+i,phoneHash:'distinct-phone'+i,ipHash:'distinct-ip'+i,purpose:'otp',now,expiry:now+300,daily:null,monthly:null});
 assert.equal((f.db.db.prepare('SELECT COUNT(*) as count FROM sms_outbox').get() as {count:number}).count,16);
});
test('public catalog sanitizes data and removes coordinated add-ons and unavailable staff',async()=>{
 const f=fixture(),result=await f.request('/services');assert.equal(result.value.services.length,1);assert.equal(result.value.services[0].name,'Motion Relax · 80 min');assert.equal(result.value.services[0].description,'Relax');
 const staff=await f.request('/staff?service_id=13827004');assert.equal(staff.value.staff.length,1);assert.equal(staff.value.staff[0].id,'99');
 assert.equal(staff.value.staff[0].avatarUrl,'https://assets.alteg.io/masters/origin/test.png');
 assert.equal(staff.value.staff[0].biography,'Care & comfort.');
 assert.equal(staffPhoto('javascript:alert(1)'),undefined);assert.equal(staffPhoto('https://evil.example/photo.png'),undefined);assert.equal(staffPhoto('https://assets.alteg.io@evil.example/masters/x.png'),undefined);
 assert.throws(()=>dateValue('2026-99-99'));assert.throws(()=>dateValue('2026-02-31'));
 assert.equal(result.value.services[0].durationMinutes,80);
});
test('OTP delivery status is private, paced and distinguishes queue acceptance from receipt',async()=>{
 const f=fixture();await f.session();await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'});
 const queued=await f.request('/verification/status');assert.equal(queued.value.status,'queued');assert.equal(f.counts.receiptReads,0);assert.ok(!JSON.stringify(queued.value).includes(f.otp));assert.ok(!JSON.stringify(queued.value).includes('+374'));
 f.advance(6);f.receiptState='Sent';assert.equal((await f.request('/verification/status')).value.status,'sent');assert.equal(f.counts.receiptReads,1);
 assert.equal((await f.request('/verification/status')).value.status,'sent');assert.equal(f.counts.receiptReads,1);
 f.advance(11);f.receiptState='Failed';assert.equal((await f.request('/verification/status')).value.status,'failed');assert.equal(f.counts.sentSms,1);
 const foreign=await f.request('/verification/status',undefined,{Cookie:''});assert.equal(foreign.response.status,401);
});
test('expired OTP and unconfirmed send status never claim delivery',async()=>{
 const f=fixture();await f.session();f.smsMode='timeout';await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'});
 assert.equal((await f.request('/verification/status')).value.status,'unconfirmed');f.advance(301);f.receiptState='Pending';assert.equal((await f.request('/verification/status')).value.status,'expired');assert.equal(f.counts.sentSms,1);assert.equal(f.counts.receiptReads,0);
 const verified=fixture();await verified.verified();verified.advance(11);const status=await verified.request('/verification/status');assert.equal(status.value.phoneVerified,true);assert.equal(status.value.expiresIn,0);assert.equal(verified.counts.receiptReads,0);
});
test('language context is non-PII and restricted to supported languages',async()=>{
 const f=fixture();assert.equal((await f.request('/config',undefined,{'X-Booking-Language':'hy'})).response.headers.get('content-language'),'hy');assert.equal((await f.request('/config',undefined,{'Accept-Language':'ru-RU,ru;q=0.9'})).response.headers.get('content-language'),'ru');assert.equal((await f.request('/config',undefined,{'X-Booking-Language':'<script>'})).response.headers.get('content-language'),'ru');assert.equal((await f.request('/config',undefined,{'Accept-Language':'en;q=0.3,hy-AM;q=0.9,ru;q=0.7'})).response.headers.get('content-language'),'hy');assert.equal((await f.request('/config',undefined,{'Accept-Language':'en;q=0,ru;q=0.7'})).response.headers.get('content-language'),'ru');
});
test('null catalog durations use minute labels and keep reservation padding private',async()=>{
 assert.equal(treatmentMinutes(null,'Face Motion 30 min'),30);assert.equal(treatmentMinutes(null,'Unknown 30'),0);assert.equal(treatmentMinutes(null,'20+40'),0);assert.equal(treatmentMinutes(3600,'80 min'),60);
 const f=fixture();f.slotSeconds=5700;await f.verified();const d=await f.draft();assert.equal(d.response.status,200);assert.equal(d.value.draft.durationMinutes,80);assert.equal(d.value.draft.reservedMinutes,undefined);
 const stored=f.db.db.prepare('SELECT summary FROM drafts WHERE id=?').get(d.value.draft.id) as {summary:string};assert.equal(JSON.parse(stored.summary).reservedMinutes,95);
 const result=await f.request('/appointments',{draftId:d.value.draft.id});assert.equal(result.value.status,'confirmed');assert.equal(result.value.appointment.durationMinutes,80);
});
test('a changed reservation length invalidates a draft without changing treatment duration',async()=>{
 const f=fixture();f.slotSeconds=5700;await f.verified();const d=await f.draft();f.slotSeconds=6000;
 assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.error.code,'selection_changed');assert.equal(f.counts.createCalls,0);
});
test('unverified draft, arbitrary price, wrong staff and unscheduled time cannot create',async()=>{
 const f=fixture();await f.session();assert.equal((await f.draft()).response.status,403);await f.verified();
 assert.equal((await f.request('/drafts',{serviceId:'13827004',staffId:'99',datetime:'2026-10-21T12:00:00+04:00',name:'Test Guest',consent:true,price:1})).response.status,400);
 assert.equal((await f.request('/drafts',{serviceId:'13827004',staffId:'100',datetime:'2026-10-21T12:00:00+04:00',name:'Test Guest',consent:true})).response.status,409);
 assert.equal(f.counts.createCalls,0);
});
test('booking confirms only after provider success and repeated confirmation sends one creation',async()=>{
 const f=fixture();await f.verified();const d=await f.draft();assert.equal(d.response.status,200);assert.equal(d.value.draft.durationMinutes,80);assert.equal(d.value.draft.price,'18000 AMD');
 const first=await f.request('/appointments',{draftId:d.value.draft.id});assert.equal(first.value.status,'confirmed');assert.equal(first.value.appointment.id,'6789');assert.ok(!JSON.stringify(first.value).includes('private-do-not-leak'));
 const again=await f.request('/appointments',{draftId:d.value.draft.id});assert.equal(again.value.status,'confirmed');assert.equal(f.counts.createCalls,1);assert.equal(f.counts.checkCalls,1);
 assert.equal((await f.request('/appointments/'+d.value.draft.id)).value.status,'confirmed');
});
test('optional consented confirmation sends once even within one minute after OTP',async()=>{
 const f=fixture();f.env.SMS_CONFIRMATIONS_ENABLED='true';await f.verified();const d=await f.draft();
 assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.status,'confirmed');
 assert.equal(f.counts.sentSms,2);
 assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.status,'confirmed');assert.equal(f.counts.sentSms,2);
});
test('concurrent confirmation is pending while one immutable create is in flight',async()=>{
 const f=fixture();await f.verified();const d=await f.draft();let release!:()=>void,entered!:()=>void;
 const enteredPromise=new Promise<void>(resolve=>entered=resolve),gate=new Promise<void>(resolve=>release=resolve);
 f.createHook=async()=>{entered();await gate;};const first=f.request('/appointments',{draftId:d.value.draft.id});await enteredPromise;
 const second=await f.request('/appointments',{draftId:d.value.draft.id});assert.equal(second.value.status,'pending');assert.equal(f.counts.createCalls,1);release();assert.equal((await first).value.status,'confirmed');
});
test('timeout and provider 500 are uncertain, never blindly retried',async()=>{
 for(const mode of ['timeout','500']){
  const f=fixture();await f.verified();const d=await f.draft();f.createMode=mode;
  assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.status,'uncertain');
  assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.status,'uncertain');assert.equal(f.counts.createCalls,1);
 }
});
test('uncertain result survives refresh and blocks a second draft on that phone',async()=>{
 const f=fixture();await f.verified();const d=await f.draft();f.createMode='timeout';
 assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.status,'uncertain');
 assert.equal((await f.session()).value.unresolvedDraftId,d.value.draft.id);
 assert.equal((await f.draft()).value.error.code,'booking_needs_review');assert.equal(f.counts.createCalls,1);
});
test('a fresh summary supersedes old ready drafts and cannot double book them',async()=>{
 const f=fixture();await f.verified();const first=await f.draft(),second=await f.draft();
 assert.notEqual(first.value.draft.id,second.value.draft.id);
 assert.equal((await f.request('/appointments',{draftId:first.value.draft.id})).response.status,409);assert.equal(f.counts.createCalls,0);
 assert.equal((await f.request('/appointments',{draftId:second.value.draft.id})).value.status,'confirmed');assert.equal(f.counts.createCalls,1);
});
test('changed live quote or expired draft cannot book stale details',async()=>{
 const f=fixture();await f.verified();const d=await f.draft();f.price=19000;
 assert.equal((await f.request('/appointments',{draftId:d.value.draft.id})).value.error.code,'selection_changed');assert.equal(f.counts.createCalls,0);
 const other=fixture();await other.verified();const draft=await other.draft();other.advance(901);
 assert.equal((await other.request('/appointments',{draftId:draft.value.draft.id})).value.error.code,'draft_expired');assert.equal(other.counts.createCalls,0);
});
test('draft status cannot be read with a different session or unverified phone',async()=>{
 const f=fixture();await f.verified();const d=await f.draft();
 const result=await f.request('/appointments/'+d.value.draft.id,undefined,{Cookie:''});assert.equal(result.response.status,401);
 const other=await f.env.DB.prepare('INSERT INTO sessions(id,created_at,expires_at) VALUES(?,?,?)').bind(await hash('a'.repeat(64)),Math.floor(f.runtime.now()/1000),Math.floor(f.runtime.now()/1000)+3600).run();assert.equal(other.meta.changes,1);
 const foreign=await f.request('/appointments/'+d.value.draft.id,undefined,{Cookie:'__Host-motion_booking='+'a'.repeat(64)});assert.equal(foreign.response.status,404);
});
test('Cloudflare default entrypoint ignores the real third ExecutionContext argument',async()=>{
 const f=fixture();
 const response=await (worker.fetch as Function)(new Request(f.env.PUBLIC_ORIGIN+'/api/booking/config',{headers:{'CF-Connecting-IP':'192.0.2.2'}}),f.env,{waitUntil(){},passThroughOnException(){}});
 assert.equal(response.status,200);const value=await response.json();assert.equal(value.currency,'AMD');assert.equal(value.timezone,'Asia/Yerevan');assert.equal(value.smsEnabled,true);
});
test('SMS receipt reconciliation records final failure without resending',async()=>{
 const f=fixture();await f.verified();f.receiptState='Failed';f.advance(301);await runNotifications(f.env,f.runtime);
 const row=f.db.db.prepare('SELECT status FROM sms_outbox').get() as {status:string};assert.equal(row.status,'Failed');assert.equal(f.counts.sentSms,1);
 f.advance(301);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,1);
});
test('one reminder re-reads the owned appointment and sends only once',async()=>{
 const f=fixture();f.env.SMS_REMINDERS_ENABLED='true';await f.verified();const draft=await f.draft();await f.request('/appointments',{draftId:draft.value.draft.id});
 f.advance(26*3600);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,2);
 f.advance(301);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,2);
 const reminder=f.db.db.prepare("SELECT status FROM sms_outbox WHERE purpose='reminder'").get() as {status:string};assert.equal(reminder.status,'Delivered');
});
test('bookings made close to the visit do not schedule a late two-hour reminder',async()=>{
 const f=fixture();f.env.SMS_REMINDERS_ENABLED='true';f.advance(27*3600);await f.verified();const draft=await f.draft();
 assert.equal((await f.request('/appointments',{draftId:draft.value.draft.id})).value.status,'confirmed');
 assert.equal((f.db.db.prepare('SELECT COUNT(*) as count FROM reminder_jobs').get() as {count:number}).count,0);assert.equal(f.counts.sentSms,1);
});
test('cancelled, rescheduled and unreadable appointments do not receive stale reminders',async()=>{
 for(const mode of ['deleted','changed','unreadable']){
  const f=fixture();f.env.SMS_REMINDERS_ENABLED='true';await f.verified();const draft=await f.draft();await f.request('/appointments',{draftId:draft.value.draft.id});
  if(mode==='deleted')f.liveDeleted=true;if(mode==='changed')f.liveStart='2026-10-21T13:00:00+04:00';if(mode==='unreadable')f.liveReadFails=true;
  f.advance(26*3600);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,1);
  f.advance(901);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,1);
 }
});
test('an uncertain reminder send is never automatically retried',async()=>{
 const f=fixture();f.env.SMS_REMINDERS_ENABLED='true';await f.verified();const draft=await f.draft();await f.request('/appointments',{draftId:draft.value.draft.id});
 f.smsMode='timeout';f.advance(26*3600);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,2);
 f.advance(301);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,2);
});
test('reminders fail closed when current private recipient changed or private auth is absent',async()=>{
 for(const mode of ['changed-phone','missing-auth']){
  const f=fixture();f.env.SMS_REMINDERS_ENABLED='true';await f.verified();const draft=await f.draft();await f.request('/appointments',{draftId:draft.value.draft.id});
  if(mode==='changed-phone')f.currentPhone='+37499123456';else delete f.env.ALTEGIO_USER_TOKEN;
  f.advance(26*3600);await runNotifications(f.env,f.runtime);assert.equal(f.counts.sentSms,1);
  const job=f.db.db.prepare('SELECT state FROM reminder_jobs').get() as {state:string};assert.equal(job.state,'skipped');
 }
});
test('cleanup retains long-lead future appointments and pending reminders',async()=>{
 const f=fixture(),store=new Store(f.env.DB),now=Math.floor(f.runtime.now()/1000),id='b'.repeat(32),future=new Date((now+90*86400)*1000+4*3600000).toISOString().slice(0,19)+'+04:00';
 await f.env.DB.prepare("INSERT INTO drafts(id,session_id,phone,summary,status,created_at,expires_at,updated_at,appointment) VALUES(?,?,?,?,'confirmed',?,?,?,?)")
  .bind(id,'old-session','+37491123456','{}',now-40*86400,now-40*86400+900,now-40*86400,JSON.stringify({id:'123',datetime:future})).run();
 await store.scheduleReminder(id,now+90*86400-7200,now-40*86400);
 await store.cleanup(now);
 assert.ok(f.db.db.prepare('SELECT id FROM drafts WHERE id=?').get(id));assert.ok(f.db.db.prepare('SELECT draft_id FROM reminder_jobs WHERE draft_id=?').get(id));
 await store.cleanup(now+91*86400);assert.equal(f.db.db.prepare('SELECT id FROM drafts WHERE id=?').get(id),undefined);
});
