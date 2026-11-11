import { Fragment, useSyncExternalStore, type ReactNode } from 'react'
import { messages, type Messages } from '../i18n'

// The interface language. Like the theme, the choice lives in localStorage and is applied
// to <html> (lang and dir) before the first render; components read their text with
// useT(), code outside components with tr().

export type Lang = 'en' | 'fr' | 'ar' | 'es'

export const languages: { code: Lang; name: string; english: string; dir: 'ltr' | 'rtl' }[] = [
  { code: 'en', name: 'English', english: 'English', dir: 'ltr' },
  { code: 'fr', name: 'Français', english: 'French', dir: 'ltr' },
  { code: 'ar', name: 'العربية', english: 'Arabic', dir: 'rtl' },
  { code: 'es', name: 'Español', english: 'Spanish', dir: 'ltr' },
]

const byCode = (code: string | null | undefined) => languages.find((l) => l.code === code)

function apply(lang: Lang) {
  const root = document.documentElement
  root.lang = lang
  root.dir = byCode(lang)!.dir
}

// No stored choice: the browser's first language that is supported, else English.
function initial(): Lang {
  let stored: string | null = null
  try {
    stored = localStorage.getItem('lang')
  } catch {
    // storage blocked
  }
  if (byCode(stored)) return stored as Lang
  for (const tag of navigator.languages ?? []) {
    const l = byCode(tag.toLowerCase().split('-')[0])
    if (l) return l.code
  }
  return 'en'
}

let current = initial()
apply(current)
const listeners = new Set<() => void>()

export function setLang(lang: Lang) {
  if (!byCode(lang) || lang === current) return
  current = lang
  apply(lang)
  try {
    localStorage.setItem('lang', lang)
  } catch {
    // the choice lasts until reload
  }
  listeners.forEach((l) => l())
}

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useLang(): Lang {
  return useSyncExternalStore(subscribe, () => current)
}

/** The texts in the current language. The component re-renders when the language changes. */
export function useT(): Messages {
  return messages[useLang()]
}

/** The texts in the current language, for code that is not a component. */
export function tr(): Messages {
  return messages[current]
}

/** The current language as a BCP 47 tag, for Intl formatters. */
export function locale(): string {
  return current
}

/**
 * Renders a text that contains markup, so a sentence is translated whole instead of in
 * pieces. "<a>Open settings</a> to change it" with tags={{ a: (c) => <Link>{c}</Link> }}
 * puts the link where each language wants it. "<x/>" marks a value with no text of its own.
 * Tags do not nest.
 */
export function rich(text: string, tags: Record<string, (children: string) => ReactNode>): ReactNode {
  const out: ReactNode[] = []
  const re = /<(\w+)>(.*?)<\/\1>|<(\w+)\/>/gs
  let last = 0
  let m: RegExpExecArray | null
  while ((m = re.exec(text))) {
    if (m.index > last) out.push(text.slice(last, m.index))
    const name = m[1] ?? m[3]
    const render = tags[name]
    out.push(<Fragment key={m.index}>{render ? render(m[2] ?? '') : (m[2] ?? '')}</Fragment>)
    last = m.index + m[0].length
  }
  if (last < text.length) out.push(text.slice(last))
  return out
}
