import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { Link, Outlet, useNavigate } from '@tanstack/react-router'
import { api, meQuery, metaQuery, updatesQuery } from '../lib/api'
import { Badge, Button, cx } from './ui'

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

export function Shell() {
  const { data: meta } = useSuspenseQuery(metaQuery)
  const { data: me } = useSuspenseQuery(meQuery)
  const updates = useQuery(updatesQuery)
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const logout = useMutation({
    mutationFn: () => api<void>('/auth/logout', { method: 'POST' }),
    onSettled: () => {
      queryClient.setQueryData(meQuery.queryKey, null)
      navigate({ to: '/login' })
    },
  })

  return (
    <div className="flex min-h-full flex-col md:flex-row">
      <aside className="flex shrink-0 flex-col border-b border-line bg-surface md:w-56 md:border-r md:border-b-0">
        <div className="flex items-baseline justify-between border-b border-line px-4 py-4">
          <span className="font-pixel text-lg text-accent">{meta.name}</span>
          {updates.data?.available ? (
            <Link to="/settings" title={`${updates.data.latest} is available`}>
              <Badge tone="warn">update</Badge>
            </Link>
          ) : (
            <span className="text-[10px] text-muted">{meta.version}</span>
          )}
        </div>
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
                className={cx('flex items-center justify-between gap-3 px-3 py-1.5 text-sm whitespace-nowrap text-muted/50')}
              >
                {item.label}
                <Badge tone="muted">soon</Badge>
              </span>
            ),
          )}
        </nav>
        <div className="mt-auto hidden border-t border-line p-3 md:block">
          <p className="truncate text-xs text-muted" title={me?.email}>
            {me?.email}
          </p>
          <Button variant="ghost" className="mt-2 h-8 w-full" onClick={() => logout.mutate()} disabled={logout.isPending}>
            Sign out
          </Button>
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center justify-end border-b border-line px-4 py-2 md:hidden">
          <Button variant="ghost" className="h-8" onClick={() => logout.mutate()} disabled={logout.isPending}>
            Sign out
          </Button>
        </header>
        <main className="flex-1 p-4 md:p-8">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
