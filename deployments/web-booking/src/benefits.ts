import {Altegio,identifier,safeText} from './altegio.ts';
import {Failure} from './types.ts';
import type {Session,Summary} from './types.ts';
import {ecosystemContext} from './ecosystem.ts';

type Wire=Record<string,unknown>;
function rows(value:unknown):Wire[]{if(!Array.isArray(value)||value.length>500)throw new Failure('benefits_unavailable',503);return value.filter((v):v is Wire=>!!v&&typeof v==='object'&&!Array.isArray(v));}
function amount(value:unknown):number|undefined {const n=typeof value==='number'?value:typeof value==='string'&&/^\d+(?:\.\d+)?$/.test(value)?Number(value):NaN;return Number.isFinite(n)&&n>=0?n:undefined;}
function expiry(value:unknown):string|null {if(value===null||value===undefined||value===''||value===0)return null;return typeof value==='string'&&Number.isFinite(Date.parse(value))?value:'invalid';}
export async function certificateTypes(client:Altegio){
 const result=[];
 for(let page=1;page<=5;page++){
  const central=!!client.env.ECOSYSTEM_GATEWAY_ORIGIN;
  const list=central?rows((await ecosystemContext(client.env,client.runtime,{action:'certificate-types',locale:'ru'})).certificates):rows(await client.call('/locations/'+client.env.ALTEGIO_COMPANY_ID+'/loyalty/certificate_types/search?page='+page+'&page_size=100','GET',undefined,false,true));
  for(const row of list){
   if(row.is_archived===true||row.is_archived===1)continue;
   const balance=amount(row.value??row.balance),title=safeText(row.title,150);if(balance===undefined||!title)continue;
   const restriction=row.restriction??row.item_type_id;
   result.push({id:identifier(row.id),title,value:balance,multipleUse:(row.multipleUse??row.is_multi)===true,restriction:typeof restriction==='number'?restriction:null,expiryType:row.expiryType??row.expiration_type_id,expiryDate:expiry(row.expiryDate??row.expiration_date),expiryPeriod:row.expiryPeriod??row.expiration_timeout,expiryUnit:row.expiryUnit??row.expiration_timeout_unit_id,onlineSaleEnabled:(row.onlineSaleEnabled??row.online_sale_is_enabled)===true});
  }
  if(central||list.length<100)return {certificates:result};
 }
 throw new Failure('benefits_unavailable',503);
}
export async function clientCertificates(client:Altegio,session:Session){
 if(!session.phone)throw new Failure('verification_required',403);
 // The lookup phone comes exclusively from successful session ownership proof.
 // Never accept a phone or client id from a public query/body for this read.
 const list=client.env.ECOSYSTEM_GATEWAY_ORIGIN?rows((await ecosystemContext(client.env,client.runtime,{action:'client-certificates',phone:session.phone})).certificates):rows(await client.call('/loyalty/certificates?company_id='+client.env.ALTEGIO_COMPANY_ID+'&phone='+encodeURIComponent(session.phone.replace(/^\+/,'')),'GET',undefined,false,true));
 return list.flatMap(row=>{
  const type=row.type as Wire|undefined,status=row.status as Wire|undefined,balance=amount(row.balance),number=typeof row.number==='string'?row.number.trim():'';
  if(balance===undefined||!number||number.length>100||/[<>\u0000-\u001f]/.test(number))return [];
  return [{id:identifier(row.id),title:safeText(row.title??type?.title,150)||'Gift certificate',balance,number,maskedNumber:'•••• '+number.slice(-4),expiresAt:expiry(row.expiresAt??row.expiration_date),status:safeText(typeof row.status==='string'?row.status:status?.title,100)}];
 });
}
export async function selectedCertificate(client:Altegio,session:Session,id:string,datetime:string):Promise<NonNullable<Summary['certificate']>>{
 const card=(await clientCertificates(client,session)).find(card=>card.id===identifier(id));
 if(!card||card.balance<=0||card.expiresAt==='invalid'||(card.expiresAt&&card.expiresAt.slice(0,10)<datetime.slice(0,10)))throw new Failure('certificate_unavailable',409);
 // This is a request to use the client's own certificate at checkout, not a
 // debit, payment or promise that its service restrictions have been satisfied.
 return {id:card.id,title:card.title,number:card.number};
}
export function publicCertificate(card:Awaited<ReturnType<typeof clientCertificates>>[number]){const {number:_number,...view}=card;return view;}
