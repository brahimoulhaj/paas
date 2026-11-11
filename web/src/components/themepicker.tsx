import { useEffect, useRef, useState } from 'react'
import { useT } from '../lib/i18n'
import { setTheme, themes, themeStyle, useTheme, type Theme } from '../lib/theme'
import { cx } from './ui'

// A full-screen carousel. Browsing only moves the cards; Enter or a click on the middle
// card applies the theme, Escape or a click on the backdrop leaves it unchanged.
export function ThemePicker({ brand, onClose }: { brand: string; onClose: () => void }) {
  const active = useTheme()
  const t = useT()
  const [index, setIndex] = useState(() => Math.max(0, themes.indexOf(active)))
  const dialog = useRef<HTMLDivElement>(null)
  const n = themes.length
  const go = (d: number) => setIndex((i) => (i + d + n) % n)
  const choose = (t: Theme) => {
    setTheme(t.id)
    onClose()
  }

  useEffect(() => {
    dialog.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
      else if (e.key === 'ArrowLeft') go(-1)
      else if (e.key === 'ArrowRight') go(1)
      else if (e.key === 'Enter' && e.target === dialog.current) choose(themes[index])
      else return
      e.preventDefault()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  })

  const shown = themes[index]

  return (
    <div
      ref={dialog}
      role="dialog"
      aria-modal="true"
      aria-label={t.theme.choose}
      // The carousel is laid out by position, so it keeps one direction in every language.
      dir="ltr"
      tabIndex={-1}
      onClick={(e) => e.target === e.currentTarget && onClose()}
      className="fixed inset-0 z-50 flex flex-col items-center justify-center gap-6 bg-black/80 backdrop-blur-sm outline-none"
    >
      {/* perspective (without preserve-3d) gives each tilted card its depth while z-index
          still decides which card sits on top. */}
      <div className="pointer-events-none relative h-[min(28.75rem,48.75vw)] w-full [perspective:1600px]">
        {themes.map((th, i) => {
          let o = i - index
          if (o > n / 2) o -= n
          if (o < -n / 2) o += n
          const d = Math.abs(o)
          return (
            <button
              key={th.id}
              type="button"
              tabIndex={o === 0 ? 0 : -1}
              aria-label={o === 0 ? t.theme.use(th.name) : t.theme.show(th.name)}
              aria-hidden={d > 2}
              onClick={() => (o === 0 ? choose(th) : go(o))}
              style={{
                // Side cards turn away from the middle one, their inner edge receding behind it.
                transform: `translate(-50%, -50%) translateX(${o * 34}%) rotateY(${-Math.sign(o) * 38}deg) scale(${o === 0 ? 1 : 0.82 - (d - 1) * 0.06})`,
                zIndex: 10 - d,
                opacity: d > 2 ? 0 : o === 0 ? 1 : 0.5 - (d - 1) * 0.15,
              }}
              className={cx(
                'absolute top-1/2 left-1/2 w-[min(46rem,78vw)] border-2 shadow-2xl motion-safe:transition-[transform,opacity] motion-safe:duration-300',
                d > 2 ? 'pointer-events-none' : 'pointer-events-auto',
                o === 0 ? 'border-white/70' : 'border-transparent',
              )}
            >
              <Preview theme={th} brand={brand} />
            </button>
          )
        })}
      </div>

      <div className="text-center text-white">
        <p className="text-2xl font-medium">{shown.name}</p>
        <p className="mt-1 text-xs text-white/60">
          {t.theme[shown.mode]} · {index + 1}/{n}
          {shown.id === active.id && ` · ${t.theme.current}`}
        </p>
        <p className="mt-3 hidden text-xs text-white/40 sm:block">{t.theme.keys}</p>
      </div>

      <Arrow side="left" onClick={() => go(-1)} />
      <Arrow side="right" onClick={() => go(1)} />
    </div>
  )
}

function Arrow({ side, onClick }: { side: 'left' | 'right'; onClick: () => void }) {
  const t = useT()
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={side === 'left' ? t.theme.previous : t.theme.next}
      className={cx(
        'absolute top-1/2 z-20 flex size-11 -translate-y-1/2 items-center justify-center border border-white/15 bg-white/5 text-white/80 hover:bg-white/15 hover:text-white',
        side === 'left' ? 'left-3 sm:left-6' : 'right-3 sm:right-6',
      )}
    >
      <svg viewBox="0 0 16 16" className="size-4" fill="none" stroke="currentColor" strokeWidth="1.5" aria-hidden>
        <path d={side === 'left' ? 'M10 3 5 8l5 5' : 'm6 3 5 5-5 5'} />
      </svg>
    </button>
  )
}

// A miniature of the panel, drawn with the theme's own tokens. Sizes are in em off a
// container-relative font size, so it scales with the card.
function Preview({ theme, brand }: { theme: Theme; brand: string }) {
  return (
    <div style={themeStyle(theme)} className="@container aspect-[16/10] w-full overflow-hidden bg-bg text-left text-fg">
      <div className="flex h-full flex-col font-mono text-[1.55cqw] leading-tight">
        <div className="flex h-[2.8em] shrink-0 items-center justify-between border-b border-line bg-surface px-[1.2em]">
          <span className="font-pixel text-[1.25em] text-accent">{brand}</span>
          <span className="flex size-[1.8em] items-center justify-center border border-line bg-raised font-pixel text-accent">a</span>
        </div>
        <div className="flex min-h-0 flex-1">
          <div className="w-[22%] space-y-[0.3em] border-r border-line bg-surface p-[0.7em]">
            {['Overview', 'Projects', 'Activity', 'Databases', 'Settings'].map((l, i) => (
              <div
                key={l}
                className={cx(
                  'border px-[0.7em] py-[0.3em]',
                  i === 1 ? 'border-line bg-raised text-fg' : 'border-transparent text-muted',
                  i === 3 && 'opacity-50',
                )}
              >
                {l}
              </div>
            ))}
          </div>
          <div className="min-w-0 flex-1 space-y-[1.1em] p-[1.4em]">
            <div className="flex items-start justify-between">
              <div>
                <p className="font-pixel text-[1.2em] uppercase">Projects</p>
                <p className="text-muted">A project groups the apps that belong together.</p>
              </div>
              <span className="bg-accent px-[0.9em] py-[0.45em] font-medium text-accent-fg">New project</span>
            </div>
            <div className="border border-line bg-surface">
              <p className="border-b border-line px-[1em] py-[0.5em] font-pixel text-[0.85em] text-muted uppercase">Apps</p>
              {[
                ['web', 'caddy · port 80', 'running', 'accent'],
                ['api', 'ghcr.io/acme/api', 'deploying', 'warn'],
                ['worker', 'acme/worker:2', 'failed', 'danger'],
              ].map(([name, sub, label, tone]) => (
                <div key={name} className="flex items-center justify-between border-b border-line px-[1em] py-[0.55em] last:border-b-0">
                  <span>
                    {name} <span className="text-muted">{sub}</span>
                  </span>
                  <span
                    className={cx(
                      'border px-[0.4em] py-[0.15em] font-pixel text-[0.75em] uppercase',
                      tone === 'accent' && 'border-accent text-accent',
                      tone === 'warn' && 'border-warn text-warn',
                      tone === 'danger' && 'border-danger text-danger',
                    )}
                  >
                    {label}
                  </span>
                </div>
              ))}
            </div>
            <div className="space-y-[0.2em] border border-line bg-bg px-[1em] py-[0.7em] text-[0.9em]">
              <p className="text-muted">pulling caddy:2 …</p>
              <p>
                <span className="text-accent">✓</span> container started
              </p>
              <p className="text-danger">health check failed: connection refused</p>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
