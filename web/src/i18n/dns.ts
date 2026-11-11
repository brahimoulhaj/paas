// Texts of the DNS hint shown next to a hostname.

// Address lists are left-to-right; in Arabic they are isolated so a list keeps its order.
const ltr = (s: string) => `⁦${s}⁩`

export const en = {
  failed: 'DNS check failed.',
  checking: 'Checking DNS…',
  notResolving: 'Does not resolve yet. Create an A record pointing at this server.',
  pointsHere: (addresses: string) => `Resolves to ${addresses} — this server.`,
  pointsElsewhere: (addresses: string, server: string) =>
    `Resolves to ${addresses}, not to this server (${server}). Fine behind a CDN or proxy.`,
  unknown: (addresses: string) => `Resolves to ${addresses}. The server cannot tell whether that is itself.`,
}

export const fr: typeof en = {
  failed: 'La vérification DNS a échoué.',
  checking: 'Vérification du DNS…',
  notResolving: 'Ne se résout pas encore. Créez un enregistrement A pointant vers ce serveur.',
  pointsHere: (addresses) => `Se résout vers ${addresses} — ce serveur.`,
  pointsElsewhere: (addresses, server) =>
    `Se résout vers ${addresses}, pas vers ce serveur (${server}). Normal derrière un CDN ou un proxy.`,
  unknown: (addresses) => `Se résout vers ${addresses}. Le serveur ne peut pas déterminer s’il s’agit de lui-même.`,
}

export const ar: typeof en = {
  failed: 'فشل فحص DNS.',
  checking: 'جارٍ فحص DNS…',
  notResolving: 'لا يُحلّ بعد. أنشئ سجل A يشير إلى هذا الخادم.',
  pointsHere: (addresses) => `يُحلّ إلى ${ltr(addresses)} — هذا الخادم.`,
  pointsElsewhere: (addresses, server) =>
    `يُحلّ إلى ${ltr(addresses)}، وليس إلى هذا الخادم (${ltr(server)}). لا بأس بذلك خلف CDN أو وسيط.`,
  unknown: (addresses) => `يُحلّ إلى ${ltr(addresses)}. لا يستطيع الخادم معرفة ما إذا كان هذا عنوانه.`,
}

export const es: typeof en = {
  failed: 'La comprobación de DNS ha fallado.',
  checking: 'Comprobando DNS…',
  notResolving: 'Aún no se resuelve. Crea un registro A que apunte a este servidor.',
  pointsHere: (addresses) => `Se resuelve a ${addresses} — este servidor.`,
  pointsElsewhere: (addresses, server) =>
    `Se resuelve a ${addresses}, no a este servidor (${server}). Es normal detrás de un CDN o un proxy.`,
  unknown: (addresses) => `Se resuelve a ${addresses}. El servidor no puede saber si es él mismo.`,
}
