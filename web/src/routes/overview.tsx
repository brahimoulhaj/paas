import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Badge, Empty, PageHeader, Panel, QueryState } from '../components/ui'
import { eventsQuery, meQuery, metaQuery, projectsQuery } from '../lib/api'
import { useT } from '../lib/i18n'
import { EventList } from './activity'
import { HostMetrics } from '../components/host-metrics'
import { DockerMetrics } from '../components/docker-metrics'

export function OverviewPage() {
  const t = useT()
  const { data: meta } = useSuspenseQuery(metaQuery)
  const { data: me } = useSuspenseQuery(meQuery)
  const projects = useQuery(projectsQuery)
  const events = useQuery({ ...eventsQuery(8), refetchInterval: 15_000 })
  const apps = projects.data?.reduce((n, p) => n + p.app_count, 0) ?? 0

  return (
    <div className="space-y-6">
      <PageHeader title={t.overview.title} subtitle={t.overview.signedInAs(me?.email ?? '')} />

      <HostMetrics />
      <DockerMetrics />

      <QueryState loading={projects.isPending} error={projects.error} what={t.projects.what} />
      {projects.data &&
        (projects.data.length === 0 ? (
          <Panel>
            <Empty title={t.overview.ready(meta.name)}>
              <p>{t.overview.readyHint}</p>
              <Link
                to="/projects"
                className="mt-4 inline-flex h-9 items-center border border-accent bg-accent px-4 text-sm font-medium text-accent-fg hover:bg-transparent hover:text-accent"
              >
                {t.overview.createFirst}
              </Link>
            </Empty>
          </Panel>
        ) : (
          <Panel
            title={t.projects.title}
            actions={
              <span className="text-xs text-muted">
                {t.projects.count(projects.data.length)} · {t.projects.apps(apps)}
              </span>
            }
          >
            <ul className="divide-y divide-line">
              {projects.data.map((p) => (
                <li key={p.id}>
                  <Link to="/projects/$projectId" params={{ projectId: p.id }} className="flex items-center justify-between gap-4 px-4 py-2.5 hover:bg-raised">
                    <span className="truncate text-sm">{p.name}</span>
                    <Badge tone="muted">{t.projects.apps(p.app_count)}</Badge>
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        ))}

      <Panel
        title={t.overview.recentActivity}
        actions={
          <Link to="/activity" className="text-xs text-muted hover:text-fg">
            {t.overview.allActivity}
          </Link>
        }
      >
        <QueryState loading={events.isPending} error={events.error} what={t.activity.what} />
        {events.data && <EventList events={events.data} />}
      </Panel>
    </div>
  )
}
