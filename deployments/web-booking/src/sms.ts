import { Failure } from './types.ts';
import type { Env, Runtime } from './types.ts';
import { getCountries, getCountryCallingCode, parsePhoneNumberWithError } from 'libphonenumber-js/max';
const knownCallingCodes=new Set<string>(getCountries().map(country=>getCountryCallingCode(country)));
interface Credentials { username:string; password:string; device_id:string; sim_number:number; api_base_url?:string }
export function credentials(env:Env):Credentials {
 try {
  const value=JSON.parse(env.SMSGATE_CREDENTIALS_JSON) as Credentials;
  if (!value.username || value.username.includes(':') || !value.password || !value.device_id || ![1,2,3].includes(value.sim_number)) throw new Error();
  // This deployment deliberately uses the existing official cloud transport only.
  if(value.api_base_url && value.api_base_url!=='https://api.sms-gate.app/3rdparty/v1')throw new Error();
  return value;
 } catch { throw new Failure('verification_unavailable',503,'Phone verification is temporarily unavailable.'); }
}
export function destinationCodes(env:Env):string[]{
 const raw=env.SMS_ALLOWED_COUNTRY_CODES===undefined?'374':env.SMS_ALLOWED_COUNTRY_CODES.trim();
 if(raw==='*')return ['*'];
 const codes=raw.split(',').map(x=>x.trim());
 if(!codes.length||codes.length>250||codes.some(x=>!/^[1-9]\d{0,2}$/.test(x)||!knownCallingCodes.has(x)))throw new Failure('verification_unavailable',503,'Phone verification is temporarily unavailable.');
 return [...new Set(codes)];
}
export function allowedDestination(phone:string,env:Env):void {
 const codes=destinationCodes(env);
 let callingCode:string;
 try{const parsed=parsePhoneNumberWithError(phone,{extract:false});if(parsed.ext||!parsed.isValid())throw new Error();callingCode=parsed.countryCallingCode;}catch{throw new Failure('invalid_phone',400,'Enter a valid phone number with its country code.');}
 if(codes[0]!=='*'&&!codes.includes(callingCode))throw new Failure('destination_unavailable',400,'Online verification is unavailable for this phone number. Please contact the studio.');
}
export async function sendSms(env:Env,runtime:Runtime,id:string,phone:string,text:string,expiry:number,priority=100):Promise<string> {
 const config=credentials(env);
 // Bounded localized Unicode text. Never truncate a verification code or URL.
 if(!text.trim()||/[\u0000-\u0008\u000b-\u001f\u007f]/.test(text)||text.length>201)throw new Failure('sms_unavailable',503);
 try {
  const response=await runtime.fetch('https://api.sms-gate.app/3rdparty/v1/messages',{
   method:'POST',headers:{'Authorization':'Basic '+btoa(config.username+':'+config.password),'Content-Type':'application/json','User-Agent':'E-Motion-Owned-Booking/1.0'},
   body:JSON.stringify({id,textMessage:{text},phoneNumbers:[phone],deviceId:config.device_id,simNumber:config.sim_number,validUntil:new Date(expiry*1000).toISOString(),priority,withDeliveryReport:true}),
   redirect:'manual',signal:AbortSignal.timeout(10000)
  });
  if(!response.ok)return [400,401,403,404,422].includes(response.status)?'rejected':'uncertain';
  const data=await response.json() as {id?:string;state?:string};
  if(data.id!==id || !['Pending','Processed','Sent','Delivered','Failed','Cancelling','Cancelled'].includes(data.state||''))return 'uncertain';
  return data.state!;
 } catch { return 'uncertain'; }
}
export async function smsReceipt(env:Env,runtime:Runtime,id:string):Promise<string> {
 if(!/^[a-f0-9]{32}$/.test(id))return 'unconfirmed';
 const config=credentials(env);
 try{
  const response=await runtime.fetch('https://api.sms-gate.app/3rdparty/v1/messages/'+id,{
   headers:{'Authorization':'Basic '+btoa(config.username+':'+config.password),'User-Agent':'E-Motion-Owned-Booking/1.0'},redirect:'manual',signal:AbortSignal.timeout(8000)
  });
  if(!response.ok)return 'unconfirmed';
  const value=await response.json() as {id?:string;state?:string};
  return value.id===id&&['Pending','Processed','Sent','Delivered','Failed','Cancelling','Cancelled'].includes(value.state||'')?value.state!:'unconfirmed';
 }catch{return 'unconfirmed';}
}
