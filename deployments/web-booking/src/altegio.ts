import { Failure } from './types.ts';
import type { Env, Runtime, Summary } from './types.ts';
import { normalizePhone } from './security.ts';

export interface Service { id: string; name: string; description: string; durationMinutes: number; priceMin: number; priceMax: number; category: string }
export interface Staff { id: string; name: string; specialization: string; avatarUrl?:string }
export interface Slot { datetime: string; label: string; durationMinutes: number }
type Wire = Record<string, unknown>;
const titles: Record<string, [string, string]> = {
 '13827004':['Motion Relax','Motion Relax · 80 min'], '13827160':['Motion Relax','Motion Relax · 110 min'],
 '13827161':['Motion Relax','Motion Relax · 140 min'], '13827167':['Motion Sport','Motion Sport · 90 min'],
 '13779299':['Motion Sport','Motion Sport · 120 min'], '13827163':['Motion Sport','Motion Sport · 150 min'],
 '13827186':['Motion Sculpt','Motion Sculpt · 60 min'], '13827188':['Motion Sculpt','Motion Sculpt · 90 min'],
 '13815928':['Face Motion','Face Motion Classic · 60 min'], '13827244':['Face Motion','Face Motion Gua Sha · 60 min'],
 '13827261':['Local','Back Motion · 50 min'], '13827262':['Local','Body Boost · 50 min'], '13827264':['Local','Head Motion · 30 min']
};
export function identifier(raw: unknown): string {
 const value = String(raw ?? '');
 if (!/^[1-9]\d{0,14}$/.test(value) || !Number.isSafeInteger(Number(value))) throw new Failure('invalid_selection');
 return value;
}
export function safeText(raw: unknown, max = 2000): string {
 if (typeof raw !== 'string') return '';
 return raw.replace(/<[^>]*>/g, ' ').replace(/&nbsp;/g, ' ').replace(/&amp;/g, '&').replace(/[\u0000-\u001f\u007f]/g, ' ').replace(/\s+/g, ' ').trim().slice(0,max);
}
export function staffPhoto(raw:unknown):string|undefined {
 if(typeof raw!=='string'||raw.length>2048)return;
 try{const url=new URL(raw);if(url.protocol==='https:'&&url.hostname==='assets.alteg.io'&&!url.username&&!url.password&&url.pathname.startsWith('/masters/'))return url.href;}catch{}
}
export function requiresCoordination(category: string, id: string): boolean {
 return ['add more time','motion four hands'].includes(category.trim().toLowerCase()) || ['13827172','13827180','13827268','13827269'].includes(id);
}
function array(value: unknown): Wire[] {
 if (!Array.isArray(value) || value.length > 500) throw new Failure('booking_unavailable', 503, 'The booking calendar is temporarily unavailable.');
 return value.filter((x): x is Wire => x !== null && typeof x === 'object' && !Array.isArray(x));
}
function number(raw: unknown): number { return typeof raw === 'number' && Number.isFinite(raw) && raw >= 0 ? raw : 0; }
export function treatmentMinutes(seconds:unknown,rawTitle:string,curatedTitle?:string):number {
 const explicit=number(seconds)/60;
 if(explicit>0&&explicit<=480)return explicit;
 // Duration labels are maintained by the studio in the authoritative catalog.
 // A number without an explicit minute unit is not enough to infer duration.
 for(const title of [rawTitle,curatedTitle||'']){
  const match=title.match(/(?:^|[\s·])([1-9]\d{0,2})\s*(?:min(?:ute)?s?|мин(?:ут)?|րոպե)(?=$|[\s.,])/i);
  if(match&&Number(match[1])<=480)return Number(match[1]);
 }
 return 0;
}
export function dateValue(raw: unknown): string {
 if (typeof raw !== 'string' || !/^\d{4}-\d{2}-\d{2}$/.test(raw)) throw new Failure('invalid_date');
 const date = new Date(raw+'T00:00:00Z');
 if (!Number.isFinite(date.getTime()) || date.toISOString().slice(0,10) !== raw) throw new Failure('invalid_date');
 return raw;
}
export function localDate(instant: number): string { return new Date(instant + 4*3600000).toISOString().slice(0,10); }
export function localDatetime(raw: unknown, now: number): string {
 if (typeof raw !== 'string' || raw.length > 35 || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(:00)?\+04:00$/.test(raw)) throw new Failure('invalid_selection');
 const parsed = new Date(raw).getTime();
 if (!Number.isFinite(parsed) || parsed <= now || parsed > now + 183*86400000) throw new Failure('invalid_selection', 400, 'Choose an available future time.');
 dateValue(raw.slice(0,10));
 if (new Date(parsed + 4*3600000).toISOString().slice(0,16) !== raw.slice(0,16)) throw new Failure('invalid_selection');
 return raw.slice(0,16)+':00+04:00';
}
export class Altegio {
 env: Env; runtime: Runtime;
 constructor(env: Env, runtime: Runtime) { this.env=env; this.runtime=runtime; }
 async call(path: string, method='GET', data?: unknown, creation=false,privateRead=false): Promise<unknown> {
  if(privateRead&&!this.env.ALTEGIO_USER_TOKEN)throw new Failure('booking_unavailable',503);
  let response: Response;
  try {
   response = await this.runtime.fetch('https://api.alteg.io/api/v1'+path, {
    // Private CRM auth is restricted to the explicit current-recipient read.
    // Catalog, availability, creation and owned-hash reads use partner auth only.
    method, headers:{'Accept':'application/vnd.api.v2+json','User-Agent':'E-Motion-Owned-Booking/1.0','Authorization':'Bearer '+this.env.ALTEGIO_PARTNER_TOKEN+(privateRead?', User '+this.env.ALTEGIO_USER_TOKEN:''), ...(data ? {'Content-Type':'application/json'} : {})},
    body: data ? JSON.stringify(data): undefined, redirect:'manual', signal:AbortSignal.timeout(12000)
   });
  } catch {
   throw new Failure(creation ? 'outcome_unknown':'booking_unavailable',503,'The booking calendar is temporarily unavailable.');
  }
  if (response.status===401 || response.status===403) {
   throw new Failure('booking_unavailable',503,'The booking calendar is temporarily unavailable.');
  }
  if (response.status===429 || response.status>=500) throw new Failure(creation ? 'outcome_unknown':'booking_unavailable',503);
  if (!response.ok) throw new Failure('selection_rejected',409,'That selection could not be booked. Please choose another time.');
  let envelope: {success?:boolean;data?:unknown};
  try {
   const content = await response.text(); if (content.length > 1024*1024) throw new Error();
   envelope=JSON.parse(content);
  } catch { throw new Failure(creation ? 'outcome_unknown':'booking_unavailable',503); }
  if (envelope.success !== true) throw new Failure(creation ? 'outcome_unknown':'selection_rejected',409);
  return envelope.data;
 }
 async services(staffId?: string): Promise<Service[]> {
  const data = await this.call('/book_services/'+this.env.ALTEGIO_COMPANY_ID+(staffId?'?staff_id='+identifier(staffId):'')) as Wire;
  if (!data || typeof data !== 'object') throw new Failure('booking_unavailable',503);
  const categories = new Map(array(data.category).map(x=>[String(x.id),safeText(x.title,100)]));
  return array(data.services).filter(x=>Number(x.active)===1).flatMap(x=>{
   const id=identifier(x.id), category=categories.get(String(x.category_id)) || '';
   if (requiresCoordination(category,id)) return [];
   const copy=titles[id];
   const name=copy && copy[0].toLowerCase()===category.toLowerCase() ? copy[1] : safeText(x.title,150);
   if (!name || x.price_min===null || typeof x.price_min!=='number' || !Number.isFinite(x.price_min) || x.price_min<0) return [];
   return [{id,name,description:safeText(x.comment || x.description),durationMinutes:treatmentMinutes(x.seance_length,safeText(x.title,150),copy&&copy[0].toLowerCase()===category.toLowerCase()?copy[1]:undefined),priceMin:number(x.price_min),priceMax:number(x.price_max ?? x.price_min),category}];
  });
 }
 async staff(serviceId: string): Promise<Staff[]> {
  return array(await this.call('/book_staff/'+this.env.ALTEGIO_COMPANY_ID+'?service_ids%5B%5D='+identifier(serviceId)))
   .filter(x=>!Number(x.fired) && x.bookable!==false && x.bookable!==0)
   .map(x=>({id:identifier(x.id),name:safeText(x.name,150),specialization:safeText(x.specialization,250),...(staffPhoto(x.avatar_big)||staffPhoto(x.avatar)?{avatarUrl:staffPhoto(x.avatar_big)||staffPhoto(x.avatar)}:{})}));
 }
 async validateService(serviceId: string): Promise<Service> {
  const service=(await this.services()).find(x=>x.id===identifier(serviceId));
  if (!service) throw new Failure('service_unavailable',409,'This treatment requires our team or is no longer available online.');
  return service;
 }
 async validatePair(serviceId: string,staffId: string): Promise<{service:Service;staff:Staff}> {
  await this.validateService(serviceId);
  const staff=(await this.staff(serviceId)).find(x=>x.id===identifier(staffId));
  const service=(await this.services(staffId)).find(x=>x.id===serviceId);
  if (!staff || !service) throw new Failure('selection_rejected',409,'Please choose a specialist for this treatment.');
  return {service,staff};
 }
 async dates(serviceId: string,staffId: string): Promise<string[]> {
  const data=await this.call('/book_dates/'+this.env.ALTEGIO_COMPANY_ID+'?staff_id='+identifier(staffId)+'&service_ids%5B%5D='+identifier(serviceId)) as Wire;
  if (!data || !Array.isArray(data.booking_dates)) throw new Failure('booking_unavailable',503);
  const today=localDate(this.runtime.now());
  return [...new Set(data.booking_dates.filter((x):x is string=>typeof x==='string' && /^\d{4}-\d{2}-\d{2}$/.test(x) && x>=today))].sort().slice(0,183);
 }
 async slots(serviceId: string,staffId: string,date: string): Promise<Slot[]> {
  dateValue(date);
  const data=array(await this.call('/book_times/'+this.env.ALTEGIO_COMPANY_ID+'/'+identifier(staffId)+'/'+date+'?service_ids%5B%5D='+identifier(serviceId)));
  return data.flatMap(x=>{
   const raw=typeof x.datetime==='string' && x.datetime ? x.datetime : date+'T'+String(x.time)+':00+04:00';
   try { const datetime=localDatetime(raw,this.runtime.now()); if (datetime.slice(0,10)!==date) return [];
    return [{datetime,label:datetime.slice(11,16),durationMinutes:number(x.seance_length)/60}];
   } catch { return []; }
  });
 }
 async summary(serviceId:string,staffId:string,datetime:string,name:string,id:string):Promise<Summary> {
  const pair=await this.validatePair(serviceId,staffId);
  const slot=(await this.slots(serviceId,staffId,datetime.slice(0,10))).find(x=>x.datetime===datetime);
  if (!slot) throw new Failure('slot_unavailable',409,'That time is no longer available. Choose another time.');
  const durationMinutes=pair.service.durationMinutes;
  const reservedMinutes=slot.durationMinutes || durationMinutes;
  if (durationMinutes<=0 || durationMinutes>480 || reservedMinutes<durationMinutes || reservedMinutes>480) throw new Failure('selection_rejected',409);
  const currency=this.env.ALTEGIO_CURRENCY || 'AMD';
  const price=pair.service.priceMin===pair.service.priceMax ? `${pair.service.priceMin} ${currency}` : `${pair.service.priceMin}–${pair.service.priceMax} ${currency}`;
  return {id,serviceId,serviceName:pair.service.name,staffId,staffName:pair.staff.name,datetime,name,durationMinutes,reservedMinutes,price,priceMin:pair.service.priceMin,priceMax:pair.service.priceMax,currency,consent:true};
 }
 appointment(summary:Summary):Wire {return {id:1,services:[Number(summary.serviceId)],staff_id:Number(summary.staffId),datetime:summary.datetime};}
 async check(summary:Summary):Promise<void> { await this.call('/book_check/'+this.env.ALTEGIO_COMPANY_ID,'POST',{appointments:[this.appointment(summary)]}); }
 async create(summary:Summary,phone:string):Promise<{id:string;managementHash:string}> {
  const result=await this.call('/book_record/'+this.env.ALTEGIO_COMPANY_ID,'POST',{
   phone,fullname:summary.name,email:'',comment:'Booked through the E-Motion Concept owned booking page. Name supplied: '+summary.name,
   appointments:[this.appointment(summary)],api_id:'omnichannel-web-'+summary.id,notify_by_sms:0,notify_by_email:0
  },true);
  if (!Array.isArray(result) || result.length!==1 || !result[0] || !Number.isSafeInteger(Number(result[0].record_id)) || Number(result[0].record_id)<=0) throw new Failure('outcome_unknown',503);
  return {id:String(result[0].record_id),managementHash:typeof result[0].record_hash==='string'?result[0].record_hash:''};
 }
 async readOwned(id:string,managementHash:string):Promise<{deleted:boolean;datetime:string;staffId:string;serviceIds:string[];reservedMinutes:number;phone?:string}> {
  identifier(id);
  if(!managementHash||managementHash.length>256)throw new Failure('booking_unavailable',503);
  const value=await this.call('/book_record/'+this.env.ALTEGIO_COMPANY_ID+'/'+id+'/'+encodeURIComponent(managementHash)) as Wire;
  if(!value||typeof value!=='object'||String(value.id)!==id)throw new Failure('booking_unavailable',503);
  if(value.deleted===true||value.deleted===1)return {deleted:true,datetime:'',staffId:'',serviceIds:[],reservedMinutes:0};
  const staff=value.staff as Wire|undefined,services=array(value.services);
  const datetime=typeof value.datetime==='string'?value.datetime.replace(/([+-]\d{2})(\d{2})$/,'$1:$2'):'';
  if(!Number.isFinite(Date.parse(datetime))||!staff||!services.length||number(value.length)<=0)throw new Failure('booking_unavailable',503);
  const client=value.client as Wire|undefined;
  return {deleted:false,datetime:new Date(Date.parse(datetime)+4*3600000).toISOString().slice(0,19)+'+04:00',staffId:identifier(staff.id),serviceIds:services.map(s=>identifier(s.id)),reservedMinutes:number(value.length)/60,
   ...(typeof client?.phone==='string'?{phone:client.phone}:{})};
 }
 async currentPhone(id:string):Promise<string>{
  identifier(id);
  const value=await this.call('/record/'+this.env.ALTEGIO_COMPANY_ID+'/'+id,'GET',undefined,false,true) as Wire;
  const client=value?.client as Wire|undefined;
  if(!value||String(value.id)!==id||String(value.company_id)!==this.env.ALTEGIO_COMPANY_ID||value.deleted===true||value.deleted===1||typeof client?.phone!=='string')throw new Failure('booking_unavailable',503);
  const raw=client.phone.trim();
  try{return normalizePhone(raw);}catch{
   if(!/^\d+$/.test(raw))throw new Failure('booking_unavailable',503);
   try{return normalizePhone('+'+raw);}catch{throw new Failure('booking_unavailable',503);}
  }
 }
}
