// Texts of the projects list. `apps` and `count` are also used by the overview.

export const en = {
  title: 'Projects',
  subtitle: 'A project groups the apps that belong together.',
  newProject: 'New project',
  what: 'projects',
  emptyTitle: 'No projects yet.',
  emptyHint: 'Create one, then add an app to it: any public container image and the port it listens on.',
  apps: (n: number) => (n === 1 ? '1 app' : `${n} apps`),
  count: (n: number) => (n === 1 ? '1 project' : `${n} projects`),
  created: (ago: string) => `created ${ago}`,
  createProject: 'Create project',
}

export const fr: typeof en = {
  title: 'Projets',
  subtitle: 'Un projet regroupe les applications qui vont ensemble.',
  newProject: 'Nouveau projet',
  what: 'projets',
  emptyTitle: 'Aucun projet pour le moment.',
  emptyHint: "Créez-en un, puis ajoutez-y une application : n'importe quelle image de conteneur publique et le port sur lequel elle écoute.",
  apps: (n) => (n <= 1 ? `${n} application` : `${n} applications`),
  count: (n) => (n <= 1 ? `${n} projet` : `${n} projets`),
  created: (ago) => `créé ${ago}`,
  createProject: 'Créer le projet',
}

// Arabic counts: 0, 1, 2, 3–10 (plural), 11 and up (singular accusative).
const arCount = (n: number, zero: string, one: string, two: string, few: string, many: string) =>
  n === 0 ? zero : n === 1 ? one : n === 2 ? two : n % 100 >= 3 && n % 100 <= 10 ? `${n} ${few}` : `${n} ${many}`

export const ar: typeof en = {
  title: 'المشاريع',
  subtitle: 'المشروع يجمع التطبيقات المرتبطة ببعضها.',
  newProject: 'مشروع جديد',
  what: 'المشاريع',
  emptyTitle: 'لا توجد مشاريع بعد.',
  emptyHint: 'أنشئ مشروعًا ثم أضف إليه تطبيقًا: أي صورة حاوية عامة والمنفذ الذي تستمع عليه.',
  apps: (n) => arCount(n, 'لا تطبيقات', 'تطبيق واحد', 'تطبيقان', 'تطبيقات', 'تطبيقًا'),
  count: (n) => arCount(n, 'لا مشاريع', 'مشروع واحد', 'مشروعان', 'مشاريع', 'مشروعًا'),
  created: (ago) => `أُنشئ ${ago}`,
  createProject: 'إنشاء المشروع',
}

export const es: typeof en = {
  title: 'Proyectos',
  subtitle: 'Un proyecto agrupa las aplicaciones que van juntas.',
  newProject: 'Nuevo proyecto',
  what: 'proyectos',
  emptyTitle: 'Aún no hay proyectos.',
  emptyHint: 'Crea uno y añádele una aplicación: cualquier imagen de contenedor pública y el puerto en el que escucha.',
  apps: (n) => (n === 1 ? '1 aplicación' : `${n} aplicaciones`),
  count: (n) => (n === 1 ? '1 proyecto' : `${n} proyectos`),
  created: (ago) => `creado ${ago}`,
  createProject: 'Crear proyecto',
}
