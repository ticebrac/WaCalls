// Brazil is the default country: numbers typed without a country code get +55.
// Brazilian numbers without it are 10 (landline) or 11 (mobile with DDD) digits.
export const DEFAULT_COUNTRY_CODE = "55";

export const toWhatsAppPhone = (input: string): string => {
  const digits = input.replace(/\D/g, "");
  if (!digits) return "";
  return digits.length <= 11 ? `${DEFAULT_COUNTRY_CODE}${digits}` : digits;
};

// "5511999998888@s.whatsapp.net" -> "+55 11 99999-8888"
export const formatJidPhone = (jid: string): string => {
  const digits = jid.split("@")[0].replace(/\D/g, "");
  if (digits.startsWith("55") && (digits.length === 12 || digits.length === 13)) {
    const cc = digits.slice(0, 2);
    const ddd = digits.slice(2, 4);
    const rest = digits.slice(4);
    const head = rest.length === 9 ? rest.slice(0, 5) : rest.slice(0, 4);
    const tail = rest.slice(head.length);
    return `+${cc} ${ddd} ${head}-${tail}`;
  }
  return digits ? `+${digits}` : jid;
};
