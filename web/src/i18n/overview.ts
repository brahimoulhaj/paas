// Texts of the overview page.

export const en = {
  title: 'Overview',
  signedInAs: (email: string) => `Signed in as ${email}.`,
  ready: (name: string) => `${name} is ready. Nothing is deployed yet.`,
  readyHint: 'Create a project, add an app from any public container image, and deploy it.',
  createFirst: 'Create your first project',
  recentActivity: 'Recent activity',
  allActivity: 'All activity →',
}

export const fr: typeof en = {
  title: "Vue d'ensemble",
  signedInAs: (email) => `Connecté en tant que ${email}.`,
  ready: (name) => `${name} est prêt. Rien n'est encore déployé.`,
  readyHint: "Créez un projet, ajoutez une application à partir de n'importe quelle image de conteneur publique, puis déployez-la.",
  createFirst: 'Créer votre premier projet',
  recentActivity: 'Activité récente',
  allActivity: "Toute l'activité →",
}

export const ar: typeof en = {
  title: 'نظرة عامة',
  signedInAs: (email) => `تم تسجيل الدخول باسم ${email}.`,
  ready: (name) => `${name} جاهز. لم يُنشر شيء بعد.`,
  readyHint: 'أنشئ مشروعًا، وأضف تطبيقًا من أي صورة حاوية عامة، ثم انشره.',
  createFirst: 'أنشئ مشروعك الأول',
  recentActivity: 'النشاط الأخير',
  allActivity: 'كل النشاط ←',
}

export const es: typeof en = {
  title: 'Resumen',
  signedInAs: (email) => `Sesión iniciada como ${email}.`,
  ready: (name) => `${name} está listo. Aún no hay nada desplegado.`,
  readyHint: 'Crea un proyecto, añade una aplicación a partir de cualquier imagen de contenedor pública y despliégala.',
  createFirst: 'Crea tu primer proyecto',
  recentActivity: 'Actividad reciente',
  allActivity: 'Toda la actividad →',
}
