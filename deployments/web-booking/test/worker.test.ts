import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSync } from 'node:sqlite';
import { readFileSync, readdirSync } from 'node:fs';
import worker, { handle } from '../src/worker.ts';
import { Store } from '../src/store.ts';
import { normalizePhone, hash, smsCap } from '../src/security.ts';
import { dateValue, treatmentMinutes } from '../src/altegio.ts';
import type { Database, D1Statement, Env, Runtime } from '../src/types.ts';
import { runNotifications } from '../src/notifications.ts';

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
function fixture(){
 const db=new SQLiteD1();
 const env:Env={DB:db,PUBLIC_ORIGIN:'https://booking.example.com',SESSION_SECRET:'test-only-secret-that-is-over-thirty-two-characters',ALTEGIO_PARTNER_TOKEN:'test-partner',ALTEGIO_USER_TOKEN:'test-private-user',ALTEGIO_COMPANY_ID:'1389810',TURNSTILE_SECRET:'test-turnstile',TURNSTILE_SITE_KEY:'test-sitekey',SMSGATE_CREDENTIALS_JSON:JSON.stringify({username:'test',password:'fake',device_id:'test-device',sim_number:1}),SMS_DAILY_CAP:'10',SMS_MONTHLY_CAP:'100',SMS_ALLOWED_COUNTRY_CODES:'374',BOOKING_ENABLED:'true'};
 let time=Date.parse('2026-10-20T08:00:00+04:00'),csrf='',cookie='',otp='',createCalls=0,checkCalls=0,turnstileHostname='booking.example.com',createMode='ok',createHook:undefined|(()=>Promise<void>);
 let sentSms=0,price=18000,slotSeconds=4800,receiptState='Delivered',smsMode='ok',liveDeleted=false,liveStart='2026-10-21T12:00:00+04:00',liveReadFails=false,currentPhone='+37491123456';
 const runtime:Runtime={now:()=>time,fetch:async(input:RequestInfo|URL,init?:RequestInit)=>{
  // workerd rejects redirect:'error' before sending; Node's native fetch does not.
  assert.equal(init?.redirect,'manual','all provider fetches must use Workers-supported manual redirects');
  const url=String(input);
  if(url.startsWith('https://api.alteg.io/')&&!url.includes('/record/'))assert.equal((init!.headers as Record<string,string>).Authorization,'Bearer test-partner','private credentials must not reach public catalog or booking endpoints');
  if(url.includes('siteverify'))return Response.json({success:true,hostname:turnstileHostname,action:'booking_otp'});
  if(url.includes('sms-gate.app')&&!init?.body)return Response.json({id:url.split('/').at(-1),state:receiptState});
  if(url.includes('sms-gate.app')){const data=JSON.parse(init!.body as string);sentSms++;otp=data.textMessage.text.match(/\b\d{6}\b/)?.[0]||'';if(smsMode==='timeout')throw new Error('simulated sms timeout');return Response.json({id:data.id,state:'Pending'},{status:202});}
  if(url.includes('/book_services/'))return Response.json({success:true,data:{category:[{id:1,title:'Motion Relax'},{id:2,title:'Add More Time'}],services:[{id:13827004,title:'80 min',active:1,category_id:1,price_min:price,price_max:price,seance_length:url.includes('staff_id')?4800:null,comment:'<p>Relax</p>'},{id:13827268,title:'20 min',active:1,category_id:2,price_min:5000,price_max:5000}]}});
  if(url.includes('/book_staff/'))return Response.json({success:true,data:[{id:99,name:'Test Specialist',specialization:'Massage',bookable:true,fired:0},{id:100,name:'Unavailable',bookable:false}]});
  if(url.includes('/book_dates/'))return Response.json({success:true,data:{booking_dates:['2026-10-21']}});
  if(url.includes('/book_times/'))return Response.json({success:true,data:[{datetime:'2026-10-21T12:00:00+04:00',seance_length:slotSeconds}]});
  if(url.includes('/book_check/')){checkCalls++;return Response.json({success:true,data:{}});}
  if(url.includes('/book_record/')&&(!init?.method||init.method==='GET')){if(liveReadFails)throw new Error('calendar unavailable');return Response.json({success:true,data:{id:6789,datetime:liveStart,length:slotSeconds,deleted:liveDeleted,staff:{id:99,name:'Test Specialist'},services:[{id:13827004,title:'80 min'}]}});}
  if(url.includes('/record/')){assert.equal((init!.headers as Record<string,string>).Authorization,'Bearer test-partner, User test-private-user');return Response.json({success:true,data:{id:6789,company_id:1389810,deleted:liveDeleted,client:{phone:currentPhone}}});}
  if(url.includes('/book_record/')){createCalls++;if(createHook)await createHook();if(createMode==='timeout')throw new Error('simulated timeout');if(createMode==='500')return new Response('bad',{status:500});return Response.json({success:true,data:[{record_id:6789,record_hash:'private-do-not-leak'}]});}
  throw new Error('unexpected fake endpoint '+url);
 }};
 async function request(path:string,data?:unknown,headers:Record<string,string>={}){
  const response=await handle(new Request(env.PUBLIC_ORIGIN+'/api/booking'+path,{method:data===undefined?'GET':'POST',headers:{Origin:env.PUBLIC_ORIGIN,'CF-Connecting-IP':'192.0.2.1',...(data===undefined?{}:{'Content-Type':'application/json'}),...(cookie?{Cookie:cookie}:{}),...(csrf?{'X-CSRF-Token':csrf}:{}),...headers},...(data===undefined?{}:{body:JSON.stringify(data)})}),env,runtime);
  const value=await response.json();return {response,value};
 }
 async function session(){const result=await request('/session',{});assert.equal(result.response.status,200);csrf=result.value.csrfToken;cookie=result.response.headers.get('set-cookie')!.split(';')[0];return result;}
 async function verified(){await session();assert.equal((await request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'})).response.status,200);assert.match(otp,/^\d{6}$/);assert.equal((await request('/verification/confirm',{code:otp})).response.status,200);}
 async function draft(){return request('/drafts',{serviceId:'13827004',staffId:'99',datetime:'2026-10-21T12:00:00+04:00',name:'Test Guest',consent:true});}
 return {db,env,runtime,request,session,verified,draft,get counts(){return {createCalls,checkCalls,sentSms};},get otp(){return otp;},get cookie(){return cookie;},get csrf(){return csrf;},advance(seconds:number){time+=seconds*1000;},set hostname(v:string){turnstileHostname=v;},set createMode(v:string){createMode=v;},set price(v:number){price=v;},set slotSeconds(v:number){slotSeconds=v;},set receiptState(v:string){receiptState=v;},set smsMode(v:string){smsMode=v;},set liveDeleted(v:boolean){liveDeleted=v;},set liveStart(v:string){liveStart=v;},set liveReadFails(v:boolean){liveReadFails=v;},set currentPhone(v:string){currentPhone=v;},set createHook(v:(()=>Promise<void>)|undefined){createHook=v;}};
}
test('sessions use secure cookies, exact origin and CSRF; no session ID in response',async()=>{
 const f=fixture(),s=await f.session();
 assert.match(s.response.headers.get('set-cookie')!,/HttpOnly; Secure; SameSite=Strict/);
 assert.equal(s.response.headers.get('cache-control'),'no-store');assert.equal(s.value.phoneVerified,false);
 const cross=await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'},{Origin:'https://evil.example'});assert.equal(cross.response.status,403);
 const csrf=await f.request('/verification/request',{phone:'+37491123456',turnstileToken:'test-token-accepted'},{'X-CSRF-Token':'bad'});assert.equal(csrf.response.status,403);assert.equal(f.counts.sentSms,0);
 const stored=f.db.db.prepare('SELECT id FROM sessions').get() as {id:string};assert.notEqual(stored.id,f.cookie.split('=')[1]);assert.equal(stored.id,await hash(f.cookie.split('=')[1]));
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
 assert.throws(()=>dateValue('2026-99-99'));assert.throws(()=>dateValue('2026-02-31'));
 assert.equal(result.value.services[0].durationMinutes,80);
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
