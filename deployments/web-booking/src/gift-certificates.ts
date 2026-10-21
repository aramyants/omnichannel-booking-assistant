// Studio-owned configuration, not native certificate types or issued balances.
// Creating a preview never issues a certificate or changes a client account.
import { Failure } from './types.ts';
import type { Service } from './altegio.ts';
export function giftCertificateCatalog(services:Pick<Service,'kind'|'priceMin'>[]) {
 const prices=services.filter(service=>service.kind!=='extra'&&Number.isFinite(service.priceMin)&&service.priceMin>0).map(service=>Math.ceil(service.priceMin));
 const minimum=Math.min(...prices);
 if(!Number.isSafeInteger(minimum)||minimum<1)throw new Failure('booking_unavailable',503);
 return {
  source:'studio',version:2,currency:'AMD',validityMonths:6,
  multipleUse:true,checkoutEnabled:false,serviceCertificates:true,
  amount:{presets:[50000,100000,150000],min:minimum,max:Number.MAX_SAFE_INTEGER,minimumSource:'lowest-service-price'},
  // Cached older clients can render an empty catalog without showing native
  // deposits or mistaking a configuration option for an issued certificate.
  certificates:[],
 };
}
