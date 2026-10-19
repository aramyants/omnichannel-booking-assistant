import { Failure } from './types.ts';
import type { Env, Runtime } from './types.ts';
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
export function allowedDestination(phone:string,env:Env):void {
 const codes=(env.SMS_ALLOWED_COUNTRY_CODES || '374').split(',').map(x=>x.trim());
 if (!codes.length || codes.some(x=>!/^[1-9]\d{0,2}$/.test(x)) || !codes.some(x=>phone.startsWith('+'+x)))
  throw new Failure('destination_unavailable',400,'Online phone verification is currently available for Armenian numbers. Please contact the studio for assistance.');
}
export async function sendSms(env:Env,runtime:Runtime,id:string,phone:string,text:string,expiry:number,priority=100):Promise<string> {
 const config=credentials(env);
 // Every web SMS is deliberately a single GSM-7 ASCII segment. Never truncate codes.
 if(!/^[\x20-\x7e]+$/.test(text) || text.length>160)throw new Failure('sms_unavailable',503);
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
