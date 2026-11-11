import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Badge, Button, ErrorNote, Panel, QueryState } from '../components/ui'
import { api, updatesQuery, type Updates } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'

/** Panel self-update: what runs, what is available, update now, automatic updates. */
export function PanelUpdates() {
  const updates = useQuery(updatesQuery)
  return (
    <Panel title="Updates">
      {!updates.data ? (
        <div className="p-4">
          <QueryState loading={updates.isPending} error={updates.error} what="update status" />
        </div>
      ) : (
        <UpdatesBody u={updates.data} />
      )}
    </Panel>
  )
}

function UpdatesBody({ u }: { u: Updates }) {
  const queryClient = useQueryClient()
  const set = (next: Updates) => queryClient.setQueryData(updatesQuery.queryKey, next)
  const [restarting, setRestarting] = useState<string | null>(null) // finished_at of the result before the update

  const check = useMutation({ mutationFn: () => api<Updates>('/updates/check', { method: 'POST' }), onSuccess: set })
  const auto = useMutation({
    mutationFn: (on: boolean) => api<Updates>('/updates/settings', { method: 'PUT', body: { auto: on } }),
    onSuccess: set,
  })
  const apply = useMutation({
    mutationFn: () => api<Updates>('/updates/apply', { method: 'POST' }),
    onSuccess: (next) => {
      set(next)
      setRestarting(u.last_result?.finished_at ?? '')
    },
  })

  if (restarting !== null) return <Restarting before={restarting} target={u.latest ?? ''} />

  if (!u.supported) {
    return (
      <div className="space-y-1 p-4 text-sm">
        <p>
          Version <span className="text-fg">{u.version}</span>
        </p>
        <p className="text-xs text-muted">Updates are unavailable here: {u.reason}.</p>
      </div>
    )
  }

  const busy = u.status !== 'idle'
  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-4 border-b border-line p-4">
        <div className="min-w-0 space-y-1 text-sm">
          <p className="flex flex-wrap items-center gap-2">
            Version <span className="text-fg">{u.version}</span>
            {u.available ? <Badge tone="warn">update available</Badge> : u.checked_at && !u.error && <Badge tone="muted">up to date</Badge>}
          </p>
          <p className="text-xs [overflow-wrap:anywhere] text-muted">{u.image}</p>
          <p className="text-xs text-muted" title={dateTime(u.checked_at ?? null)}>
            {u.checked_at ? `Checked ${timeAgo(u.checked_at)}` : 'Not checked yet'}
            {' · '}
            {u.mode === 'digest' ? 'follows new pushes of this tag' : 'follows new version tags'}
          </p>
        </div>
        <Button variant="ghost" type="button" disabled={busy || check.isPending} onClick={() => check.mutate()}>
          {check.isPending || u.status === 'checking' ? 'Checking…' : 'Check now'}
        </Button>
      </div>

      {u.error && (
        <div className="border-b border-line p-4">
          <ErrorNote>{u.error}</ErrorNote>
        </div>
      )}

      {u.available && (
        <div className="flex flex-wrap items-center justify-between gap-4 border-b border-line bg-raised p-4">
          <div className="min-w-0 text-sm">
            <p>
              <span className="text-accent">{u.latest}</span> is available.
            </p>
            <p className="text-xs [overflow-wrap:anywhere] text-muted">
              Pulls {u.latest_image} and restarts the panel; apps keep running. If the new version does not start, the current one comes back.
            </p>
          </div>
          <Button type="button" disabled={busy || apply.isPending} onClick={() => apply.mutate()}>
            {apply.isPending ? 'Pulling…' : 'Update now'}
          </Button>
        </div>
      )}
      {apply.error && (
        <div className="border-b border-line p-4">
          <ErrorNote>{apply.error.message}</ErrorNote>
        </div>
      )}

      <label className="flex cursor-pointer items-start gap-3 p-4 text-sm">
        <input
          type="checkbox"
          className="mt-0.5 accent-[var(--color-accent)]"
          checked={u.auto}
          disabled={auto.isPending}
          onChange={(e) => auto.mutate(e.target.checked)}
        />
        <span>
          Update automatically
          <span className="block text-xs text-muted">Checked every hour. An update waits while a deployment is running.</span>
        </span>
      </label>
      {auto.error && (
        <div className="px-4 pb-4">
          <ErrorNote>{auto.error.message}</ErrorNote>
        </div>
      )}

      {u.last_result && <LastResult r={u.last_result} />}
    </>
  )
}

function LastResult({ r }: { r: NonNullable<Updates['last_result']> }) {
  return (
    <p className="border-t border-line px-4 py-3 text-xs text-muted" title={dateTime(r.finished_at)}>
      Last update {timeAgo(r.finished_at)}:{' '}
      {r.ok ? (
        <span className="text-accent">now on {r.to}</span>
      ) : (
        <span className="text-danger">
          {r.to} failed and the previous version was kept — {r.error}
        </span>
      )}
    </p>
  )
}

/** Waits for the helper to finish (a new result appears), then reloads into the new panel. */
function Restarting({ before, target }: { before: string; target: string }) {
  const [seconds, setSeconds] = useState(0)
  const [result, setResult] = useState<Updates['last_result']>()
  const timer = useRef<number | undefined>(undefined)

  useEffect(() => {
    let stopped = false
    const started = Date.now()
    const poll = async () => {
      if (stopped) return
      setSeconds(Math.round((Date.now() - started) / 1000))
      try {
        const u = await api<Updates>('/updates')
        if (u.last_result && u.last_result.finished_at !== before) {
          setResult(u.last_result)
          if (u.last_result.ok) location.reload()
          return
        }
      } catch {
        // The panel is restarting; keep waiting.
      }
      timer.current = window.setTimeout(poll, 2000)
    }
    timer.current = window.setTimeout(poll, 2000)
    return () => {
      stopped = true
      window.clearTimeout(timer.current)
    }
  }, [before])

  if (result && !result.ok) {
    return (
      <div className="space-y-2 p-4">
        <ErrorNote>
          The update to {result.to} failed and the previous version is running again: {result.error}
        </ErrorNote>
        <Button type="button" variant="ghost" onClick={() => location.reload()}>
          Reload
        </Button>
      </div>
    )
  }
  return (
    <div className="space-y-1 p-4 text-sm">
      <p>
        Updating to <span className="text-accent">{target}</span>… <span className="text-muted">{seconds}s</span>
      </p>
      <p className="text-xs text-muted">
        The panel restarts on the new version and this page reloads by itself. Apps keep serving traffic meanwhile.
        {seconds > 120 && ' This is taking longer than usual; check `docker logs berth-updater` on the server.'}
      </p>
    </div>
  )
}
