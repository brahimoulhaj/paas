import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import { Badge, Button, CheckOption, HelpTip, ErrorNote, Panel, QueryState } from '../components/ui'
import { api, updatesQuery, type Updates } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'
import { rich, useT } from '../lib/i18n'

/** Panel self-update: what runs, what is available, update now, automatic updates. */
export function PanelUpdates() {
  const updates = useQuery(updatesQuery)
  const t = useT()
  return (
    <Panel title={t.settingsUpdates.title}>
      {!updates.data ? (
        <div className="p-4">
          <QueryState loading={updates.isPending} error={updates.error} what={t.settingsUpdates.what} />
        </div>
      ) : (
        <UpdatesBody u={updates.data} />
      )}
    </Panel>
  )
}

function UpdatesBody({ u }: { u: Updates }) {
  const queryClient = useQueryClient()
  const t = useT()
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
      <div className="flex items-center gap-2 p-4 text-sm">
        <p>{rich(t.settingsUpdates.version, { v: () => <span className="text-fg" dir="ltr">{u.version}</span> })}</p>
        <span className="text-xs text-muted">{t.settingsUpdates.unavailableLabel}</span>
        <HelpTip label={t.settingsUpdates.title}>{t.settingsUpdates.unavailable(u.reason ?? '')}</HelpTip>
      </div>
    )
  }

  const busy = u.status !== 'idle'
  return (
    <>
      <div className="flex flex-wrap items-start justify-between gap-4 border-b border-line p-4">
        <div className="min-w-0 space-y-1 text-sm">
          <p className="flex flex-wrap items-center gap-2">
            <span>{rich(t.settingsUpdates.version, { v: () => <span className="text-fg" dir="ltr">{u.version}</span> })}</span>
            {u.available ? (
              <Badge tone="warn">{t.settingsUpdates.updateAvailable}</Badge>
            ) : (
              u.checked_at && !u.error && <Badge tone="muted">{t.settingsUpdates.upToDate}</Badge>
            )}
          </p>
          <p className="text-xs text-muted" title={dateTime(u.checked_at ?? null)}>
            {u.checked_at ? t.settingsUpdates.checked(timeAgo(u.checked_at)) : t.settingsUpdates.notChecked}

          </p>
        </div>
        <Button variant="ghost" type="button" disabled={busy || check.isPending} onClick={() => check.mutate()}>
          {check.isPending || u.status === 'checking' ? t.settingsUpdates.checking : t.settingsUpdates.checkNow}
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
              {rich(t.settingsUpdates.available, {
                v: () => (
                  <span className="text-accent" dir="ltr">
                    {u.latest}
                  </span>
                ),
              })}
            </p>
            <p className="text-xs [overflow-wrap:anywhere] text-muted">
              {t.settingsUpdates.pullsNote(u.latest_image ?? '')}
            </p>
          </div>
          <Button type="button" disabled={busy || apply.isPending} onClick={() => apply.mutate()}>
            {apply.isPending ? t.settingsUpdates.pulling : t.settingsUpdates.updateNow}
          </Button>
        </div>
      )}
      {apply.error && (
        <div className="border-b border-line p-4">
          <ErrorNote>{apply.error.message}</ErrorNote>
        </div>
      )}

      <div className="p-4"><CheckOption checked={u.auto} disabled={auto.isPending} onChange={(on) => auto.mutate(on)} label={t.settingsUpdates.auto} hint={t.settingsUpdates.autoHint} /></div>
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
  const t = useT()
  return (
    <p className="border-t border-line px-4 py-3 text-xs text-muted" title={dateTime(r.finished_at)}>
      {t.settingsUpdates.lastUpdate(timeAgo(r.finished_at))}{' '}
      {r.ok ? (
        <span className="text-accent">{t.settingsUpdates.nowOn(r.to)}</span>
      ) : (
        <span className="text-danger">{t.settingsUpdates.failedKept(r.to, r.error ?? '')}</span>
      )}
    </p>
  )
}

/** Waits for the helper to finish (a new result appears), then reloads into the new panel. */
function Restarting({ before, target }: { before: string; target: string }) {
  const t = useT()
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
        <ErrorNote>{t.settingsUpdates.updateFailed(result.to, result.error ?? '')}</ErrorNote>
        <Button type="button" variant="ghost" onClick={() => location.reload()}>
          {t.settingsUpdates.reload}
        </Button>
      </div>
    )
  }
  return (
    <div className="space-y-1 p-4 text-sm">
      <p>
        {rich(t.settingsUpdates.updatingTo, {
          v: () => (
            <span className="text-accent" dir="ltr">
              {target}
            </span>
          ),
        })}{' '}
        <span className="text-muted">{t.settingsUpdates.seconds(seconds)}</span>
      </p>
      <p className="text-xs text-muted">
        {t.settingsUpdates.restartNote}
        {seconds > 120 && ` ${t.settingsUpdates.slowNote}`}
      </p>
    </div>
  )
}
