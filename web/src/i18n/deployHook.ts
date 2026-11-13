// Texts of the deploy webhook panel of an app's settings.

export const en = {
  title: 'Deploy webhook',
  what: 'deploy webhook',
  intro: 'A secret URL that deploys this app: paste it into your Git host to deploy on every push, or call it from CI.',
  off: 'Off. Turn it on to get the URL.',
  turnOn: 'Turn on',
  url: 'Webhook URL',
  gitHowTo: (branch: string) =>
    `GitHub: repository Settings → Webhooks → Add webhook, Payload URL above, Content type application/json, “Just the push event”. GitLab: Settings → Webhooks, Push events. Gitea: Settings → Webhooks. Pushes to ${branch} deploy; other branches are ignored.`,
  imageHowTo: 'Call it from CI once the image is pushed. With a tag, the app switches to that tag of its image first; without, it redeploys the current one.',
  queued: 'A push that arrives during a deployment runs right after it, once, with the latest commit.',
  unreachable: 'This browser reaches the panel on a local address; a Git host needs the panel’s public address or domain in the URL.',
  newUrl: 'New URL',
  newUrlConfirm: 'Yes, replace it',
  newUrlHint: 'The current URL stops working.',
  turnOff: 'Turn off',
  turnOffConfirm: 'Yes, turn off',
}

export const fr: typeof en = {
  title: 'Webhook de déploiement',
  what: 'webhook de déploiement',
  intro: 'Une URL secrète qui déploie cette app : collez-la chez votre hébergeur Git pour déployer à chaque push, ou appelez-la depuis la CI.',
  off: 'Désactivé. Activez-le pour obtenir l’URL.',
  turnOn: 'Activer',
  url: 'URL du webhook',
  gitHowTo: (branch) =>
    `GitHub : Settings du dépôt → Webhooks → Add webhook, Payload URL ci-dessus, Content type application/json, « Just the push event ». GitLab : Settings → Webhooks, Push events. Gitea : Settings → Webhooks. Les pushs sur ${branch} déploient ; les autres branches sont ignorées.`,
  imageHowTo: 'Appelez-la depuis la CI une fois l’image poussée. Avec un tag, l’app passe d’abord à ce tag de son image ; sans tag, elle redéploie l’image actuelle.',
  queued: 'Un push reçu pendant un déploiement est lancé juste après, une seule fois, avec le dernier commit.',
  unreachable: 'Ce navigateur joint le panneau par une adresse locale ; un hébergeur Git a besoin de l’adresse publique ou du domaine du panneau dans l’URL.',
  newUrl: 'Nouvelle URL',
  newUrlConfirm: 'Oui, la remplacer',
  newUrlHint: 'L’URL actuelle cesse de fonctionner.',
  turnOff: 'Désactiver',
  turnOffConfirm: 'Oui, désactiver',
}

export const ar: typeof en = {
  title: 'Webhook النشر',
  what: 'Webhook النشر',
  intro: 'رابط سري ينشر هذا التطبيق: الصقه لدى مستضيف Git للنشر عند كل push، أو استدعه من CI.',
  off: 'معطّل. فعّله للحصول على الرابط.',
  turnOn: 'تفعيل',
  url: 'رابط الـ Webhook',
  gitHowTo: (branch) =>
    `GitHub: إعدادات المستودع ← Webhooks ← Add webhook، الرابط أعلاه في Payload URL، ونوع المحتوى application/json، و«Just the push event». GitLab: ‏Settings ← Webhooks، ‏Push events. ‏Gitea: ‏Settings ← Webhooks. عمليات push إلى ${branch} تنشر؛ الفروع الأخرى تُتجاهل.`,
  imageHowTo: 'استدعه من CI بعد دفع الصورة. مع وسم (tag) ينتقل التطبيق أولًا إلى ذلك الوسم من صورته؛ ودونه يعيد نشر الصورة الحالية.',
  queued: 'الـ push الذي يصل أثناء نشر يُنفَّذ بعده مباشرة، مرة واحدة، بآخر commit.',
  unreachable: 'هذا المتصفح يصل إلى لوحة التحكم عبر عنوان محلي؛ يحتاج مستضيف Git إلى العنوان العام للوحة أو نطاقها في الرابط.',
  newUrl: 'رابط جديد',
  newUrlConfirm: 'نعم، استبدله',
  newUrlHint: 'يتوقف الرابط الحالي عن العمل.',
  turnOff: 'تعطيل',
  turnOffConfirm: 'نعم، عطّله',
}

export const es: typeof en = {
  title: 'Webhook de despliegue',
  what: 'webhook de despliegue',
  intro: 'Una URL secreta que despliega esta app: pégala en tu host de Git para desplegar en cada push, o llámala desde CI.',
  off: 'Desactivado. Actívalo para obtener la URL.',
  turnOn: 'Activar',
  url: 'URL del webhook',
  gitHowTo: (branch) =>
    `GitHub: Settings del repositorio → Webhooks → Add webhook, la Payload URL de arriba, Content type application/json, “Just the push event”. GitLab: Settings → Webhooks, Push events. Gitea: Settings → Webhooks. Los push a ${branch} despliegan; las demás ramas se ignoran.`,
  imageHowTo: 'Llámala desde CI cuando la imagen esté subida. Con un tag, la app cambia primero a ese tag de su imagen; sin él, vuelve a desplegar la actual.',
  queued: 'Un push que llega durante un despliegue se ejecuta justo después, una vez, con el último commit.',
  unreachable: 'Este navegador llega al panel por una dirección local; un host de Git necesita la dirección pública o el dominio del panel en la URL.',
  newUrl: 'Nueva URL',
  newUrlConfirm: 'Sí, reemplazarla',
  newUrlHint: 'La URL actual deja de funcionar.',
  turnOff: 'Desactivar',
  turnOffConfirm: 'Sí, desactivar',
}
