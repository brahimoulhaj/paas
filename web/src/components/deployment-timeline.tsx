import { useEffect, useState } from 'react'
import { deploymentPhases, isActive, type Deployment, type DeploymentPhase } from '../lib/api'
import { tr, useT } from '../lib/i18n'
import { cx } from './ui'

const isPhase = (p: string): p is DeploymentPhase => (deploymentPhases as readonly string[]).includes(p)

/** A step of the timeline: queued, then each phase, with its start and end in ms. */
type Step = { phase: DeploymentPhase | 'queued'; from: number; to: number; open: boolean }

/** steps turns a deployment's phases into timed steps. The last phase of a running
 * deployment is open: it lasts until now. */
export function steps(d: Deployment, now: number): Step[] {
  const out: Step[] = []
  const entries = d.phases ?? []
  // Deployments made before the timeline existed have no entries.
  if (entries.length === 0 && !isActive(d)) return out
  const created = new Date(d.created_at).getTime()
  const first = entries[0] ? new Date(entries[0].at).getTime() : d.started_at ? new Date(d.started_at).getTime() : null
  if (first !== null) out.push({ phase: 'queued', from: created, to: Math.max(created, first), open: false })
  else if (d.status === 'queued') out.push({ phase: 'queued', from: created, to: now, open: true })
  entries.forEach((e, i) => {
    if (!isPhase(e.phase)) return // the closing status entry
    const from = new Date(e.at).getTime()
    const next = entries[i + 1]
    const to = next ? new Date(next.at).getTime() : now
    out.push({ phase: e.phase, from, to: Math.max(from, to), open: !next })
  })
  return out
}

/** ms → "0.4 s", "12 s", "2 min 5 s". */
function span(ms: number): string {
  const f = tr().format
  if (ms < 10_000) return f.seconds(Math.round(ms / 100) / 10)
  const secs = Math.round(ms / 1000)
  return secs < 60 ? f.seconds(secs) : f.minutesSeconds(Math.floor(secs / 60), secs % 60)
}

/** DeploymentTimeline shows the steps of a deployment and how long each took: a bar per
 * step scaled to the whole, the step it failed or was cancelled in marked. */
export function DeploymentTimeline({ deployment }: { deployment: Deployment }) {
  const t = useT()
  const active = isActive(deployment)
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const id = setInterval(() => setNow(Date.now()), 500)
    return () => clearInterval(id)
  }, [active])

  const list = steps(deployment, now)
  if (list.length === 0) {
    return <p className="border-b border-line px-4 py-2 text-xs text-muted">{t.app.timelineNone}</p>
  }
  const start = list[0].from
  const end = Math.max(...list.map((s) => s.to))
  const total = Math.max(1, end - start)
  const ended = deployment.status === 'failed' || deployment.status === 'cancelled'
  const lastIndex = list.length - 1

  return (
    <div className="border-b border-line px-4 py-3">
      <div className="mb-2 flex items-baseline justify-between text-xs text-muted">
        <span className="font-medium text-fg">{t.app.timelineTitle}</span>
        <span className="tabular-nums">{t.app.timelineTotal(span(end - start))}</span>
      </div>
      <ol className="space-y-1">
        {list.map((s, i) => {
          const failedHere = ended && i === lastIndex && s.phase !== 'queued'
          const left = ((s.from - start) / total) * 100
          const width = Math.max(0.75, ((s.to - s.from) / total) * 100)
          return (
            <li key={s.phase + i} className="grid grid-cols-[minmax(8rem,12rem)_1fr_4.5rem] items-center gap-3 text-xs">
              <span className={cx('truncate', failedHere ? (deployment.status === 'failed' ? 'text-danger' : 'text-muted') : 'text-fg')}>
                {t.app.phase[s.phase]}
                {failedHere && (
                  <span className="ms-1.5 text-[0.7rem]">
                    · {deployment.status === 'failed' ? t.app.timelineFailedHere : t.app.timelineCancelledHere}
                  </span>
                )}
              </span>
              <span className="relative h-2 overflow-hidden rounded-full bg-raised" aria-hidden>
                <span
                  className={cx(
                    'absolute inset-y-0 rounded-full',
                    s.phase === 'queued'
                      ? 'bg-line-strong'
                      : failedHere
                        ? deployment.status === 'failed'
                          ? 'bg-danger'
                          : 'bg-line-strong'
                        : s.open && active
                          ? 'animate-pulse bg-warn'
                          : 'bg-accent',
                  )}
                  style={{ insetInlineStart: `${left}%`, width: `${Math.min(width, 100 - left)}%` }}
                />
              </span>
              <span className="text-end tabular-nums text-muted">{span(s.to - s.from)}</span>
            </li>
          )
        })}
      </ol>
    </div>
  )
}
