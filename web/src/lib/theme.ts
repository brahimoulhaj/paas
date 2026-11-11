import { useSyncExternalStore, type CSSProperties } from 'react'

// Every theme fills the same tokens that styles.css declares in @theme, so components never
// know which theme is on. Applying one sets the variables inline on <html>; a preview sets
// them on its own box, and everything inside it re-themes through the cascade.
type Tokens = {
  bg: string
  surface: string
  raised: string
  line: string
  'line-strong': string
  fg: string
  muted: string
  accent: string
  'accent-fg': string
  danger: string
  warn: string
}

export type Theme = { id: string; name: string; mode: 'light' | 'dark'; tokens: Tokens }

export const themes: Theme[] = [
  {
    id: 'midnight',
    name: 'Midnight',
    mode: 'dark',
    tokens: {
      bg: '#0c0e12', surface: '#12151b', raised: '#181c24', line: '#262b36', 'line-strong': '#3a4150',
      fg: '#d5d9e2', muted: '#7f8798', accent: '#9ece6a', 'accent-fg': '#0c0e12', danger: '#f7768e', warn: '#e0af68',
    },
  },
  {
    id: 'paper',
    name: 'Paper',
    mode: 'light',
    tokens: {
      bg: '#f3f4f6', surface: '#ffffff', raised: '#eaecf0', line: '#d8dbe2', 'line-strong': '#b5bac6',
      fg: '#1b1f27', muted: '#5d6474', accent: '#4a7c15', 'accent-fg': '#ffffff', danger: '#c42b48', warn: '#9a6200',
    },
  },
  {
    id: 'white',
    name: 'White',
    mode: 'light',
    tokens: {
      bg: '#f4f4f4', surface: '#ffffff', raised: '#ebebeb', line: '#dcdcdc', 'line-strong': '#a8a8a8',
      fg: '#111111', muted: '#5c5c5c', accent: '#111111', 'accent-fg': '#ffffff', danger: '#c4182b', warn: '#8a5a00',
    },
  },
  {
    id: 'tokyo-night',
    name: 'Tokyo Night',
    mode: 'dark',
    tokens: {
      bg: '#16161e', surface: '#1a1b26', raised: '#24283b', line: '#292e42', 'line-strong': '#414868',
      fg: '#c0caf5', muted: '#737aa2', accent: '#7aa2f7', 'accent-fg': '#16161e', danger: '#f7768e', warn: '#e0af68',
    },
  },
  {
    id: 'catppuccin',
    name: 'Catppuccin',
    mode: 'dark',
    tokens: {
      bg: '#181825', surface: '#1e1e2e', raised: '#282839', line: '#313244', 'line-strong': '#45475a',
      fg: '#cdd6f4', muted: '#9399b2', accent: '#cba6f7', 'accent-fg': '#1e1e2e', danger: '#f38ba8', warn: '#f9e2af',
    },
  },
  {
    id: 'catppuccin-latte',
    name: 'Catppuccin Latte',
    mode: 'light',
    tokens: {
      bg: '#e6e9ef', surface: '#eff1f5', raised: '#dce0e8', line: '#ccd0da', 'line-strong': '#acb0be',
      fg: '#4c4f69', muted: '#6c6f85', accent: '#8839ef', 'accent-fg': '#eff1f5', danger: '#d20f39', warn: '#b86e00',
    },
  },
  {
    id: 'everforest',
    name: 'Everforest',
    mode: 'dark',
    tokens: {
      bg: '#232a2e', surface: '#2d353b', raised: '#343f44', line: '#3d484d', 'line-strong': '#475258',
      fg: '#d3c6aa', muted: '#859289', accent: '#a7c080', 'accent-fg': '#2d353b', danger: '#e67e80', warn: '#dbbc7f',
    },
  },
  {
    id: 'gruvbox',
    name: 'Gruvbox',
    mode: 'dark',
    tokens: {
      bg: '#1d2021', surface: '#282828', raised: '#32302f', line: '#3c3836', 'line-strong': '#504945',
      fg: '#ebdbb2', muted: '#a89984', accent: '#b8bb26', 'accent-fg': '#1d2021', danger: '#fb4934', warn: '#fabd2f',
    },
  },
  {
    id: 'nord',
    name: 'Nord',
    mode: 'dark',
    tokens: {
      bg: '#242933', surface: '#2e3440', raised: '#353c4a', line: '#3b4252', 'line-strong': '#4c566a',
      fg: '#eceff4', muted: '#8f9bb3', accent: '#88c0d0', 'accent-fg': '#2e3440', danger: '#bf616a', warn: '#ebcb8b',
    },
  },
  {
    id: 'rose-pine',
    name: 'Rosé Pine',
    mode: 'dark',
    tokens: {
      bg: '#191724', surface: '#1f1d2e', raised: '#26233a', line: '#403d52', 'line-strong': '#524f67',
      fg: '#e0def4', muted: '#908caa', accent: '#ebbcba', 'accent-fg': '#191724', danger: '#eb6f92', warn: '#f6c177',
    },
  },
  {
    id: 'kanagawa',
    name: 'Kanagawa',
    mode: 'dark',
    tokens: {
      bg: '#16161d', surface: '#1f1f28', raised: '#2a2a37', line: '#363646', 'line-strong': '#54546d',
      fg: '#dcd7ba', muted: '#727169', accent: '#7e9cd8', 'accent-fg': '#1f1f28', danger: '#e46876', warn: '#ff9e3b',
    },
  },
  {
    id: 'flexoki-light',
    name: 'Flexoki Light',
    mode: 'light',
    tokens: {
      bg: '#f2f0e5', surface: '#fffcf0', raised: '#e6e4d9', line: '#dad8ce', 'line-strong': '#b7b5ac',
      fg: '#100f0f', muted: '#6f6e69', accent: '#24837b', 'accent-fg': '#fffcf0', danger: '#af3029', warn: '#ad8301',
    },
  },
]

const byId = (id: string | null) => themes.find((t) => t.id === id)

/** CSS variables for a theme, to put on any element (or <html>). */
export function themeStyle(t: Theme): CSSProperties {
  const style: Record<string, string> = { colorScheme: t.mode }
  for (const [k, v] of Object.entries(t.tokens)) style[`--color-${k}`] = v
  return style as CSSProperties
}

function apply(t: Theme) {
  const root = document.documentElement
  root.dataset.theme = t.id
  root.style.colorScheme = t.mode
  for (const [k, v] of Object.entries(t.tokens)) root.style.setProperty(`--color-${k}`, v)
}

// No stored choice: follow the system. 'light'/'dark' are what the first version stored.
function initial(): Theme {
  let stored: string | null = null
  try {
    stored = localStorage.getItem('theme')
  } catch {
    // storage blocked
  }
  if (stored === 'light') stored = 'paper'
  if (stored === 'dark') stored = 'midnight'
  return byId(stored) ?? (matchMedia('(prefers-color-scheme: light)').matches ? byId('paper')! : themes[0])
}

let current = initial()
apply(current)
const listeners = new Set<() => void>()

export function setTheme(id: string) {
  const t = byId(id)
  if (!t) return
  current = t
  apply(t)
  try {
    localStorage.setItem('theme', t.id)
    // index.html reads this before the first paint, so a reload never flashes the default.
    localStorage.setItem('theme-vars', JSON.stringify({ mode: t.mode, tokens: t.tokens }))
  } catch {
    // the choice lasts until reload
  }
  listeners.forEach((l) => l())
}

export function useTheme(): Theme {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => current,
  )
}
