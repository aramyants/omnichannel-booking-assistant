import type {Env,Runtime} from './types.ts';
import {Failure} from './types.ts';
export type Locale='ru'|'en'|'hy';
export function locale(raw:unknown):Locale{return raw==='en'||raw==='hy'?raw:'ru';}
export interface ReviewVisit {id:string;staffId:string;staffName:string;serviceName:string;datetime:string}
export async function ecosystemContext(env:Env,runtime:Runtime,data:{action:'context'|'prepare'|'confirmed'|'record-context'|'review-visits'|'review-check'|'booking-settings'|'reminder';phone?:string;locale?:Locale;explicit?:boolean;consent?:boolean;recordId?:string;managementProof?:string;reminderMinutes?:number}):Promise<{locale:Locale;calendarUrl?:string;eligible?:boolean;anyStaff?:boolean;visits?:ReviewVisit[];visit?:ReviewVisit} >{
 if(!env.ECOSYSTEM_GATEWAY_ORIGIN)return {locale:locale(data.locale)};
 if(env.ECOSYSTEM_GATEWAY_ORIGIN!=='https://omnichannel-booking-assistant-q6l3x4lyuq-ew.a.run.app'||!env.ALTEGIO_USER_TOKEN)throw new Failure('booking_unavailable',503);
 const encoder=new TextEncoder(),body=JSON.stringify(data),timestamp=String(Math.floor(runtime.now()/1000));
 const key=await crypto.subtle.importKey('raw',encoder.encode(env.ALTEGIO_USER_TOKEN),{name:'HMAC',hash:'SHA-256'},false,['sign']);
 const derived=env.ECOSYSTEM_CONTEXT_KEY&&/^[a-f0-9]{64}$/.test(env.ECOSYSTEM_CONTEXT_KEY)?new Uint8Array(env.ECOSYSTEM_CONTEXT_KEY.match(/../g)!.map(part=>parseInt(part,16))):await crypto.subtle.sign('HMAC',key,encoder.encode('emotion-ecosystem-auth-v1'));
 const signing=await crypto.subtle.importKey('raw',derived,{name:'HMAC',hash:'SHA-256'},false,['sign']);
 const signature=Array.from(new Uint8Array(await crypto.subtle.sign('HMAC',signing,encoder.encode(timestamp+'\nPOST\n/internal/booking-context\n'+body))),n=>n.toString(16).padStart(2,'0')).join('');
 try{
  const response=await runtime.fetch(env.ECOSYSTEM_GATEWAY_ORIGIN+'/internal/booking-context',{method:'POST',headers:{'Content-Type':'application/json','X-Emotion-Time':timestamp,'X-Emotion-Signature':signature},body,redirect:'manual',signal:AbortSignal.timeout(12000)});
  if(!response.ok)throw new Error();
  const raw=await response.text();if(raw.length>(data.action==='review-visits'?131072:8192))throw new Error();const result=JSON.parse(raw);
  if(!['en','ru','hy'].includes(result.locale))throw new Error();
  if(result.calendarUrl&&(!result.calendarUrl.startsWith(env.PUBLIC_ORIGIN+'/calendar/')||result.calendarUrl.length>300))throw new Error();
  return result;
 }catch{throw new Failure('booking_unavailable',503);}
}
export function otpMessage(code:string,language:Locale):string{
 return {en:`E-Motion: ${code} is your booking code. Valid for 5 minutes. Do not share it.`,ru:`E-Motion: код ${code}. Действует 5 минут. Никому не сообщайте.`,hy:`E-Motion. Ձեր կոդը՝ ${code}։ Գործում է 5 րոպե։ Մի փոխանցեք այն։`}[language];
}
export function reminderMessage(datetime:string,language:Locale):string{
 const when=datetime.slice(8,10)+'.'+datetime.slice(5,7)+'.'+datetime.slice(0,4)+' '+datetime.slice(11,16);
 return {en:`E-Motion: a reminder of your visit ${when} (Yerevan). Myasnikyan 1/6. Questions: +37494768067.`,ru:`E-Motion: ждём вас ${when} (Ереван). Мясникяна 1/6. Вопросы: +37494768067.`,hy:`E-Motion. Սպասում ենք Ձեզ ${when} (Երևան)։ Մյասնիկյան 1/6։ Հեռ. +37494768067։`}[language];
}
