import { useQuery } from '@tanstack/react-query'
import { Empty, PageHeader, Panel, QueryState } from '../components/ui'
import { eventsQuery, type PanelEvent } from '../lib/api'
import { actionLabel, dateTime, timeAgo } from '../lib/format'
import { useT } from '../lib/i18n'

export function ActivityPage() {
  const t = useT()
  const events = useQuery({ ...eventsQuery(200), refetchInterval: 15_000 })
  return (
    <div className="space-y-6">
      <PageHeader title={t.activity.title} subtitle={t.activity.subtitle} />
      <QueryState loading={events.isPending} error={events.error} what={t.activity.what} />
      {events.data && (
        <Panel>
          <EventList events={events.data} />
        </Panel>
      )}
    </div>
  )
}

/** Shared by the activity page and the overview. */
export function EventList({ events }: { events: PanelEvent[] }) {
  const t = useT()
  if (events.length === 0) return <Empty title={t.activity.empty} />
  return (
    <ul className="divide-y divide-line">
      {events.map((e) => (
        <li key={e.id} className="flex flex-wrap items-baseline justify-between gap-x-6 gap-y-0.5 px-4 py-2.5">
          <p className="min-w-0 text-sm break-words">
            <span className="text-muted">{e.actor || t.activity.system}</span> {actionLabel(e.action)}{' '}
            {e.target_name && <span className={e.action.endsWith('failed') ? 'text-danger' : 'text-fg'}>{e.target_name}</span>}
            {detail(e) && (
              <span className="text-muted">
                {' '}
                — <span dir="ltr">{detail(e)}</span>
              </span>
            )}
          </p>
          <time className="shrink-0 text-xs text-muted" dateTime={e.created_at} title={dateTime(e.created_at)}>
            {timeAgo(e.created_at)}
          </time>
        </li>
      ))}
    </ul>
  )
}

// The detail column holds ids for some actions; only show what a person can read.
function detail(e: PanelEvent): string {
  switch (e.action) {
    case 'app.create':
    case 'domain.add':
    case 'domain.remove':
    case 'domain.primary':
    case 'deployment.failed':
      return e.detail
    default:
      return ''
  }
}
