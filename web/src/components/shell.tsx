import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet, useLocation, useNavigate } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { api, meQuery, metaQuery, updatesQuery, type User } from '../lib/api'
import { useTheme } from '../lib/theme'
import { ThemePicker } from './themepicker'
import { Badge, cx } from './ui'

// Sections light up as their milestone lands; until then they show as "soon".
const nav = [
  { label: 'Overview', to: '/' as const, exact: true },
  { label: 'Projects', to: '/projects' as const },
  { label: 'Activity', to: '/activity' as const },
  { label: 'Databases' },
  { label: 'Storage' },
  { label: 'Registry' },
  { label: 'Settings', to: '/settings' as const },
]

// The page itself never scrolls: the navbar and the sidebar stay put and only <main> scrolls.
export function Shell() {
  const { data: meta } = useSuspenseQuery(metaQuery)
  const { data: me } = useSuspenseQuery(meQuery)
  const updates = useQuery(updatesQuery)
  const main = useRef<HTMLElement>(null)
  const { pathname } = useLocation()
  const [picking, setPicking] = useState(false)

  // The router restores window scroll, which no longer moves; a new page starts at the top.
  useEffect(() => {
    main.current?.scrollTo(0, 0)
  }, [pathname])

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between border-b border-line bg-surface">
        <div className="flex h-full items-center gap-3 px-4 md:w-56 md:justify-between md:border-r md:border-line">
          <Link to="/" className="font-pixel text-lg text-accent">
            {meta.name}
          </Link>
          {updates.data?.available ? (
            <Link to="/settings" title={`${updates.data.latest} is available`}>
              <Badge tone="warn">update</Badge>
            </Link>
          ) : (
            <span className="text-[10px] text-muted">{meta.version}</span>
          )}
        </div>
        <div className="flex items-center gap-2 px-4">
          <button
            type="button"
            onClick={() => setPicking(true)}
            aria-label="Choose a theme"
            title="Theme"
            className="flex size-8 items-center justify-center border border-transparent text-muted hover:border-line hover:text-fg"
          >
            <PaletteIcon />
          </button>
          {me && <UserMenu user={me} onPickTheme={() => setPicking(true)} />}
        </div>
      </header>

      <div className="flex min-h-0 flex-1 flex-col md:flex-row">
        <aside className="shrink-0 border-b border-line bg-surface md:w-56 md:overflow-y-auto md:border-r md:border-b-0">
          <nav className="flex gap-1 overflow-x-auto p-2 md:flex-col md:overflow-visible">
            {nav.map((item) =>
              item.to ? (
                <Link
                  key={item.label}
                  to={item.to}
                  activeOptions={{ exact: 'exact' in item && item.exact }}
                  className="border border-transparent px-3 py-1.5 text-sm whitespace-nowrap text-muted hover:text-fg"
                  activeProps={{ className: 'border-line! bg-raised text-fg!' }}
                >
                  {item.label}
                </Link>
              ) : (
                <span
                  key={item.label}
                  className="flex items-center justify-between gap-3 px-3 py-1.5 text-sm whitespace-nowrap text-muted/50"
                >
                  {item.label}
                  <Badge tone="muted">soon</Badge>
                </span>
              ),
            )}
          </nav>
        </aside>
        <main ref={main} className="min-h-0 min-w-0 flex-1 overflow-y-auto p-4 md:p-8">
          <Outlet />
        </main>
      </div>
      {picking && <ThemePicker brand={meta.name} onClose={() => setPicking(false)} />}
    </div>
  )
}

function PaletteIcon() {
  return (
    <svg viewBox="0 0 16 16" className="size-4" fill="currentColor" aria-hidden>
      <path d="M8 1.5a6.5 6.5 0 0 0 0 13c.9 0 1.4-.6 1.4-1.3 0-.4-.2-.7-.4-1-.2-.2-.3-.5-.3-.8 0-.7.6-1.2 1.3-1.2h1.5a3 3 0 0 0 3-3C14.5 4 11.6 1.5 8 1.5Zm-3.5 7a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm1.5-3a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm4 0a1 1 0 1 1 0-2 1 1 0 0 1 0 2Zm2 3a1 1 0 1 1 0-2 1 1 0 0 1 0 2Z" />
    </svg>
  )
}

function UserMenu({ user, onPickTheme }: { user: User; onPickTheme: () => void }) {
  const [open, setOpen] = useState(false)
  const theme = useTheme()
  const root = useRef<HTMLDivElement>(null)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const logout = useMutation({
    mutationFn: () => api<void>('/auth/logout', { method: 'POST' }),
    onSettled: () => {
      queryClient.setQueryData(meQuery.queryKey, null)
      navigate({ to: '/login' })
    },
  })

  // Close on a click outside or on Escape.
  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  const initial = user.email.charAt(0).toUpperCase() || '?'

  return (
    <div ref={root} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
        title={user.email}
        className={cx(
          'flex size-8 items-center justify-center border bg-raised font-pixel text-sm text-accent',
          open ? 'border-accent' : 'border-line hover:border-line-strong',
        )}
      >
        {initial}
      </button>
      {open && (
        <div role="menu" className="absolute top-full right-0 z-20 mt-2 w-64 border border-line bg-surface shadow-lg">
          <div className="border-b border-line px-4 py-3">
            <p className="truncate text-sm" title={user.email}>
              {user.email}
            </p>
            <p className="text-xs text-muted capitalize">{user.role}</p>
          </div>
          <div className="p-1">
            <button
              type="button"
              role="menuitem"
              onClick={() => {
                setOpen(false)
                onPickTheme()
              }}
              className="flex w-full items-center justify-between px-3 py-1.5 text-left text-sm text-muted hover:bg-raised hover:text-fg"
            >
              Theme
              <span className="text-xs">{theme.name}</span>
            </button>
            <Link
              to="/settings"
              role="menuitem"
              onClick={() => setOpen(false)}
              className="block px-3 py-1.5 text-sm text-muted hover:bg-raised hover:text-fg"
            >
              Settings
            </Link>
            <button
              type="button"
              role="menuitem"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
              className="block w-full px-3 py-1.5 text-left text-sm text-muted hover:bg-raised hover:text-danger disabled:opacity-50"
            >
              {logout.isPending ? 'Signing out…' : 'Sign out'}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
