// Texts of a project's page: its apps, the new app form and the project settings.

export const en = {
  what: 'project',
  back: '← Projects',
  newApp: 'New app',
  apps: 'Apps',
  appsWhat: 'apps',
  emptyTitle: 'No apps in this project yet.',
  emptyHint:
    'An app runs one container, from a ready image or built from a Git repository. Add a domain to it and it is served over HTTPS.',
  port: (port: number) => `port ${port}`,
  containerPort: 'Container port',
  nameTitle: 'Lowercase letters, digits and dashes',
  portHint: 'The port is the one the container listens on; it is not published on the server. Nothing runs until you deploy.',
  createApp: 'Create app',
  settingsTitle: 'Project settings',
  deleteBlocked: 'Delete the apps in this project before deleting it.',
  deleteWarning: 'Deleting a project cannot be undone.',
  deleteProject: 'Delete project',
}

export const fr: typeof en = {
  what: 'projet',
  back: '← Projets',
  newApp: 'Nouvelle application',
  apps: 'Applications',
  appsWhat: 'applications',
  emptyTitle: 'Aucune application dans ce projet pour le moment.',
  emptyHint:
    "Une application exécute un conteneur, à partir d'une image prête ou construite depuis un dépôt Git. Ajoutez-lui un domaine et elle est servie en HTTPS.",
  port: (port) => `port ${port}`,
  containerPort: 'Port du conteneur',
  nameTitle: 'Lettres minuscules, chiffres et tirets',
  portHint: "Le port est celui sur lequel le conteneur écoute ; il n'est pas publié sur le serveur. Rien ne s'exécute avant le déploiement.",
  createApp: "Créer l'application",
  settingsTitle: 'Paramètres du projet',
  deleteBlocked: 'Supprimez les applications de ce projet avant de le supprimer.',
  deleteWarning: "La suppression d'un projet est irréversible.",
  deleteProject: 'Supprimer le projet',
}

export const ar: typeof en = {
  what: 'المشروع',
  back: '→ المشاريع',
  newApp: 'تطبيق جديد',
  apps: 'التطبيقات',
  appsWhat: 'التطبيقات',
  emptyTitle: 'لا توجد تطبيقات في هذا المشروع بعد.',
  emptyHint: 'التطبيق يشغّل حاوية واحدة، من صورة جاهزة أو مبنية من مستودع Git. أضف إليه نطاقًا فيُقدَّم عبر HTTPS.',
  port: (port) => `المنفذ ${port}`,
  containerPort: 'منفذ الحاوية',
  nameTitle: 'أحرف لاتينية صغيرة وأرقام وشرطات',
  portHint: 'المنفذ هو الذي تستمع عليه الحاوية؛ ولا يُفتح على الخادم. لا شيء يعمل قبل أن تنشر.',
  createApp: 'إنشاء التطبيق',
  settingsTitle: 'إعدادات المشروع',
  deleteBlocked: 'احذف تطبيقات هذا المشروع قبل حذفه.',
  deleteWarning: 'لا يمكن التراجع عن حذف المشروع.',
  deleteProject: 'حذف المشروع',
}

export const es: typeof en = {
  what: 'proyecto',
  back: '← Proyectos',
  newApp: 'Nueva aplicación',
  apps: 'Aplicaciones',
  appsWhat: 'aplicaciones',
  emptyTitle: 'Aún no hay aplicaciones en este proyecto.',
  emptyHint:
    'Una aplicación ejecuta un contenedor, a partir de una imagen lista o compilada desde un repositorio Git. Añádele un dominio y se sirve por HTTPS.',
  port: (port) => `puerto ${port}`,
  containerPort: 'Puerto del contenedor',
  nameTitle: 'Letras minúsculas, dígitos y guiones',
  portHint: 'El puerto es aquel en el que escucha el contenedor; no se publica en el servidor. Nada se ejecuta hasta que despliegues.',
  createApp: 'Crear aplicación',
  settingsTitle: 'Ajustes del proyecto',
  deleteBlocked: 'Elimina las aplicaciones de este proyecto antes de eliminarlo.',
  deleteWarning: 'Eliminar un proyecto no se puede deshacer.',
  deleteProject: 'Eliminar proyecto',
}
