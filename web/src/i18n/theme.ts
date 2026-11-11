// The theme picker. The miniature panel inside each card is a drawing and stays as it is.

export const en = {
  choose: 'Choose a theme',
  use: (name: string) => `Use ${name}`,
  show: (name: string) => `Show ${name}`,
  light: 'light',
  dark: 'dark',
  current: 'current',
  keys: '← → browse · Enter or click to apply · Esc to close',
  previous: 'Previous theme',
  next: 'Next theme',
}

export const fr: typeof en = {
  choose: 'Choisir un thème',
  use: (name) => `Utiliser ${name}`,
  show: (name) => `Afficher ${name}`,
  light: 'clair',
  dark: 'sombre',
  current: 'actuel',
  keys: '← → parcourir · Entrée ou clic pour appliquer · Échap pour fermer',
  previous: 'Thème précédent',
  next: 'Thème suivant',
}

export const ar: typeof en = {
  choose: 'اختر سمة',
  use: (name) => `استخدام ${name}`,
  show: (name) => `عرض ${name}`,
  light: 'فاتحة',
  dark: 'داكنة',
  current: 'الحالية',
  keys: '← → للتصفح · Enter أو نقرة للتطبيق · Esc للإغلاق',
  previous: 'السمة السابقة',
  next: 'السمة التالية',
}

export const es: typeof en = {
  choose: 'Elegir un tema',
  use: (name) => `Usar ${name}`,
  show: (name) => `Mostrar ${name}`,
  light: 'claro',
  dark: 'oscuro',
  current: 'actual',
  keys: '← → explorar · Enter o clic para aplicar · Esc para cerrar',
  previous: 'Tema anterior',
  next: 'Tema siguiente',
}
