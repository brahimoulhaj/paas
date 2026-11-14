import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Badge, cx, Empty, PageHeader, Panel, QueryState } from '../components/ui'
import { activeDeploymentsQuery, eventsQuery, meQuery, metaQuery, projectsQuery, type ActiveDeployment } from '../lib/api'
import { deploymentVersion, duration, timeAgo } from '../lib/format'
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

      <ActiveDeployments />
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

/**
 * An alert for each deployment queued or running across all apps, with a link to its log.
 * Shows nothing while no deployment is in progress.
 */
function ActiveDeployments() {
  const q = useQuery({ ...activeDeploymentsQuery, refetchInterval: 5_000 })
  if (!q.data?.length) return null
  return (
    <div role="status" aria-live="polite" className="space-y-2">
      {q.data.map((d) => (
        <ActiveDeploymentAlert key={d.id} d={d} />
      ))}
    </div>
  )
}

function ActiveDeploymentAlert({ d }: { d: ActiveDeployment }) {
  const t = useT()
  const running = d.status === 'running'
  const version = deploymentVersion(d).replace(/^—$/, '')
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border border-warn/60 bg-warn/10 px-4 py-3">
      <span
        aria-hidden
        className={cx('size-2 shrink-0 rounded-full', running ? 'animate-pulse bg-warn motion-reduce:animate-none' : 'bg-muted')}
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm">{running ? t.overview.deployingApp(d.app_name) : t.overview.queuedApp(d.app_name)}</span>
        <span className="block truncate text-xs text-muted">
          {[version, running ? t.overview.runningFor(duration(d.started_at, null)) : t.overview.queuedFor(timeAgo(d.created_at))]
            .filter(Boolean)
            .join(' · ')}
        </span>
      </span>
      <Link
        to="/projects/$projectId/apps/$appId"
        params={{ projectId: d.project_id, appId: d.app_id }}
        search={{ tab: 'deployments' }}
        className="inline-flex h-8 items-center border border-warn/60 px-3 text-sm hover:bg-warn hover:text-bg"
      >
        {t.overview.viewDeployment}
      </Link>
    </div>
  )
}
