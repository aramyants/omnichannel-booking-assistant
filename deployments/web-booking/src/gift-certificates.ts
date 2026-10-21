// Studio-owned configuration, not native certificate types or issued balances.
// Creating a preview never issues a certificate or changes a client account.
export function giftCertificateCatalog() {
 return {
  source:'studio',version:2,currency:'AMD',validityMonths:6,
  multipleUse:true,checkoutEnabled:false,serviceCertificates:true,
  amount:{presets:[50000,100000,150000],min:1,max:Number.MAX_SAFE_INTEGER},
  // Cached older clients can render an empty catalog without showing native
  // deposits or mistaking a configuration option for an issued certificate.
  certificates:[],
 };
}
