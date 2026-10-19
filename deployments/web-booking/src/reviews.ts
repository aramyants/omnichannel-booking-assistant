import {Failure} from './types.ts';
import type {Env,Runtime,Session} from './types.ts';
import {ecosystemContext} from './ecosystem.ts';
import type {ReviewVisit,Locale} from './ecosystem.ts';
import {hmac,onlyKeys} from './security.ts';

function validVisit(value:unknown):value is ReviewVisit{
 const v=value as ReviewVisit|undefined;
 return !!v&&/^[1-9]\d{0,14}$/.test(v.id)&&/^[1-9]\d{0,14}$/.test(v.staffId)&&typeof v.staffName==='string'&&v.staffName.length<=120&&typeof v.serviceName==='string'&&v.serviceName.length<=1000&&typeof v.datetime==='string'&&Number.isFinite(Date.parse(v.datetime));
}
export async function publicReviews(env:Env,staffId:string|null){
 if(staffId!==null&&!/^[1-9]\d{0,14}$/.test(staffId))throw new Failure('invalid_selection');
 const filter=staffId?' AND staff_id=?':'';
 const stats=env.DB.prepare(`SELECT COUNT(*) AS count,AVG(rating) AS average FROM verified_reviews WHERE published=1${filter}`);
 const rows=env.DB.prepare(`SELECT staff_id AS staffId,staff_name AS staffName,rating,comment,display_name AS displayName,locale,created_at AS createdAt FROM verified_reviews WHERE published=1${filter} ORDER BY created_at DESC,record_id DESC LIMIT 30`);
 const summary=await (staffId?stats.bind(staffId):stats).first<{count:number;average:number|null}>();
 const result=await (staffId?rows.bind(staffId):rows).all();
 const specialists=await env.DB.prepare('SELECT staff_id AS staffId,staff_name AS staffName,COUNT(*) AS count,AVG(rating) AS average FROM verified_reviews WHERE published=1 GROUP BY staff_id,staff_name ORDER BY staff_name').all();
 return {source:'verified_visits',count:summary?.count||0,average:summary?.average||null,reviews:result.results,specialists:specialists.results};
}
export async function reviewVisits(env:Env,runtime:Runtime,session:Session){
 if(!session.phone)throw new Failure('verification_required',403);
 if(!env.ECOSYSTEM_GATEWAY_ORIGIN)throw new Failure('reviews_unavailable',503);
 const result=await ecosystemContext(env,runtime,{action:'review-visits',phone:session.phone});
 if(!Array.isArray(result.visits)||result.visits.length>100||result.visits.some(v=>!validVisit(v)))throw new Failure('reviews_unavailable',503);
 const digest=await hmac(env.SESSION_SECRET,'review-phone:'+session.phone);
 const existing=await env.DB.prepare('SELECT record_id FROM verified_reviews WHERE phone_hash=?').bind(digest).all<{record_id:string}>();
 const reviewed=new Set(existing.results.map(r=>r.record_id));
 return {visits:result.visits.map(v=>({...v,reviewed:reviewed.has(v.id)}))};
}
export async function submitReview(env:Env,runtime:Runtime,session:Session,data:Record<string,unknown>,locale:Locale){
 if(!session.phone)throw new Failure('verification_required',403);
 onlyKeys(data,['recordId','rating','comment','displayName','publish']);
 if(typeof data.recordId!=='string'||!/^[1-9]\d{0,14}$/.test(data.recordId)||typeof data.rating!=='number'||!Number.isInteger(data.rating)||data.rating<1||data.rating>5||typeof data.publish!=='boolean')throw new Failure('invalid_review');
 const comment=typeof data.comment==='string'?data.comment.trim():'';
 const name=typeof data.displayName==='string'?data.displayName.trim():'';
 if((data.comment!==undefined&&typeof data.comment!=='string')||(data.displayName!==undefined&&typeof data.displayName!=='string')||comment.length>1500||name.length>40||/[<>\u0000-\u0008\u000b-\u001f\u007f]/.test(comment+name)||/[\r\n]/.test(name)||/@|https?:\/\/|(?:\+?\d[\s()-]*){8,}/i.test(comment+' '+name)||(data.publish&&name.length<2))throw new Failure('invalid_review');
 const digest=await hmac(env.SESSION_SECRET,'review-phone:'+session.phone);
 const saved=await env.DB.prepare('SELECT phone_hash FROM verified_reviews WHERE record_id=?').bind(data.recordId).first<{phone_hash:string}>();
 // An idempotent repeat does not update a rating or disclose another client.
 if(saved){if(saved.phone_hash!==digest)throw new Failure('review_not_eligible',403);return {submitted:true,alreadySubmitted:true};}
 if(!env.ECOSYSTEM_GATEWAY_ORIGIN)throw new Failure('reviews_unavailable',503);
 const result=await ecosystemContext(env,runtime,{action:'review-check',phone:session.phone,recordId:data.recordId});
 if(!validVisit(result.visit)||result.visit.id!==data.recordId)throw new Failure('review_not_eligible',403);
 const v=result.visit;
 await env.DB.prepare('INSERT INTO verified_reviews(record_id,phone_hash,staff_id,staff_name,rating,comment,display_name,published,locale,created_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(record_id) DO NOTHING')
  .bind(v.id,digest,v.staffId,v.staffName,data.rating,comment,name,data.publish?1:0,locale,Math.floor(runtime.now()/1000)).run();
 const owner=await env.DB.prepare('SELECT phone_hash FROM verified_reviews WHERE record_id=?').bind(v.id).first<{phone_hash:string}>();if(owner?.phone_hash!==digest)throw new Failure('review_not_eligible',403);
 return {submitted:true};
}
