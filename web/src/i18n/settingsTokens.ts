// Texts of the API tokens section of Settings.

export const en = {
  title: 'API tokens',
  intro:
    'For scripts and CI. Send one as <header/>. A token acts as you on every API endpoint except this one: tokens cannot create or revoke tokens.',
  what: 'tokens',
  empty: 'No API tokens yet.',
  createToken: 'Create token',
  created: 'Token <name/> created. Copy it now: it is not shown again.',
  newTokenLabel: 'New API token',
  createdAgo: (ago: string) => `created ${ago}`,
  lastUsed: (ago: string) => `last used ${ago}`,
  neverUsed: 'never used',
  revoke: 'Revoke',
  yesRevoke: 'Yes, revoke',
}

export const fr: typeof en = {
  title: "Jetons d'API",
  intro:
    "Pour les scripts et la CI. Envoyez-en un sous la forme <header/>. Un jeton agit en votre nom sur tous les points d'accès de l'API sauf celui-ci : les jetons ne peuvent ni créer ni révoquer de jetons.",
  what: 'jetons',
  empty: "Aucun jeton d'API pour le moment.",
  createToken: 'Créer un jeton',
  created: "Jeton <name/> créé. Copiez-le maintenant : il ne sera plus affiché.",
  newTokenLabel: "Nouveau jeton d'API",
  createdAgo: (ago) => `créé ${ago}`,
  lastUsed: (ago) => `dernière utilisation ${ago}`,
  neverUsed: 'jamais utilisé',
  revoke: 'Révoquer',
  yesRevoke: 'Oui, révoquer',
}

export const ar: typeof en = {
  title: 'رموز API',
  intro:
    'للسكربتات و CI. أرسل الرمز بالصيغة <header/>. يتصرف الرمز باسمك على جميع نقاط API عدا هذه: لا تستطيع الرموز إنشاء رموز أو إبطالها.',
  what: 'الرموز',
  empty: 'لا توجد رموز API بعد.',
  createToken: 'إنشاء رمز',
  created: 'تم إنشاء الرمز <name/>. انسخه الآن: لن يُعرض مرة أخرى.',
  newTokenLabel: 'رمز API الجديد',
  createdAgo: (ago) => `أُنشئ ${ago}`,
  lastUsed: (ago) => `آخر استخدام ${ago}`,
  neverUsed: 'لم يُستخدم قط',
  revoke: 'إبطال',
  yesRevoke: 'نعم، أبطِل',
}

export const es: typeof en = {
  title: 'Tokens de API',
  intro:
    'Para scripts y CI. Envía uno como <header/>. Un token actúa en tu nombre en todos los endpoints de la API salvo este: los tokens no pueden crear ni revocar tokens.',
  what: 'tokens',
  empty: 'Aún no hay tokens de API.',
  createToken: 'Crear token',
  created: 'Token <name/> creado. Cópialo ahora: no se volverá a mostrar.',
  newTokenLabel: 'Nuevo token de API',
  createdAgo: (ago) => `creado ${ago}`,
  lastUsed: (ago) => `último uso ${ago}`,
  neverUsed: 'nunca usado',
  revoke: 'Revocar',
  yesRevoke: 'Sí, revocar',
}
