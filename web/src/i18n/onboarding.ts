// Texts of the setup guide shown after the owner account is created.

export const en = {
  skip: 'Skip setup',
  stepsLabel: 'Setup steps',
  domainTitle: 'Panel domain',
  domainIntro:
    'Serve this panel on its own hostname over HTTPS. Without one it stays on http://<server-ip>, which is fine to start with.',
  deployTitle: 'First deploy',
  deployIntro: 'That is all the setup there is for now. Next: a project, an app from any public image, and a domain for it.',
  projectLine: '<b>Project</b> — a folder for related apps.',
  appLine: '<b>App</b> — one container image run as one container, with CPU and memory limits.',
  domainLine: '<b>Domain</b> — attach one to an app and it is served over HTTPS with an automatic certificate.',
  back: 'Back',
  next: 'Next',
  goToOverview: 'Go to overview',
  createProject: 'Create a project',
}

export const fr: typeof en = {
  skip: 'Passer la configuration',
  stepsLabel: 'Étapes de configuration',
  domainTitle: 'Domaine du panneau',
  domainIntro:
    "Servez ce panneau sur son propre nom d'hôte en HTTPS. Sans domaine, il reste sur http://<server-ip>, ce qui convient pour commencer.",
  deployTitle: 'Premier déploiement',
  deployIntro:
    "C'est toute la configuration nécessaire pour l'instant. Ensuite : un projet, une application à partir de n'importe quelle image publique, et un domaine pour elle.",
  projectLine: '<b>Projet</b> — un dossier pour des applications liées.',
  appLine: '<b>Application</b> — une image de conteneur exécutée dans un seul conteneur, avec des limites de CPU et de mémoire.',
  domainLine: '<b>Domaine</b> — associez-en un à une application et elle est servie en HTTPS avec un certificat automatique.',
  back: 'Retour',
  next: 'Suivant',
  goToOverview: "Aller à la vue d'ensemble",
  createProject: 'Créer un projet',
}

export const ar: typeof en = {
  skip: 'تخطي الإعداد',
  stepsLabel: 'خطوات الإعداد',
  domainTitle: 'نطاق لوحة التحكم',
  domainIntro:
    'اجعل لوحة التحكم متاحة على اسم نطاق خاص بها عبر HTTPS. من دونه تبقى على ⁦http://<server-ip>⁩، وهذا كافٍ للبداية.',
  deployTitle: 'أول نشر',
  deployIntro: 'هذا كل ما يلزم من إعداد حالياً. التالي: مشروع، ثم تطبيق من أي صورة عامة، ثم نطاق له.',
  projectLine: '<b>مشروع</b> — مجلد لتطبيقات مترابطة.',
  appLine: '<b>تطبيق</b> — صورة حاوية واحدة تعمل كحاوية واحدة، مع حدود للمعالج والذاكرة.',
  domainLine: '<b>نطاق</b> — اربطه بتطبيق فيُتاح عبر HTTPS بشهادة تلقائية.',
  back: 'السابق',
  next: 'التالي',
  goToOverview: 'الانتقال إلى النظرة العامة',
  createProject: 'إنشاء مشروع',
}

export const es: typeof en = {
  skip: 'Omitir configuración',
  stepsLabel: 'Pasos de configuración',
  domainTitle: 'Dominio del panel',
  domainIntro:
    'Sirve este panel en su propio nombre de host por HTTPS. Sin él se queda en http://<server-ip>, que está bien para empezar.',
  deployTitle: 'Primer despliegue',
  deployIntro:
    'Esa es toda la configuración por ahora. Siguiente: un proyecto, una aplicación desde cualquier imagen pública y un dominio para ella.',
  projectLine: '<b>Proyecto</b> — una carpeta para aplicaciones relacionadas.',
  appLine: '<b>Aplicación</b> — una imagen de contenedor ejecutada como un solo contenedor, con límites de CPU y memoria.',
  domainLine: '<b>Dominio</b> — asocia uno a una aplicación y se sirve por HTTPS con un certificado automático.',
  back: 'Atrás',
  next: 'Siguiente',
  goToOverview: 'Ir al resumen',
  createProject: 'Crear un proyecto',
}
