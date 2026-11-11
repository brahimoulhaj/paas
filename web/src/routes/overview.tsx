import { useQuery, useSuspenseQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { Badge, Empty, PageHeader, Panel, QueryState } from '../components/ui'
import { eventsQuery, meQuery, metaQuery, projectsQuery } from '../lib/api'
import { EventList } from './activity'

export function OverviewPage() {
  const { data: meta } = useSuspenseQuery(metaQuery)
  const { data: me } = useSuspenseQuery(meQuery)
  const projects = useQuery(projectsQuery)
  const events = useQuery({ ...eventsQuery(8), refetchInterval: 15_000 })
  const apps = projects.data?.reduce((n, p) => n + p.app_count, 0) ?? 0

  return (
    <div className="space-y-6">
      <PageHeader title="Overview" subtitle={`Signed in as ${me?.email}.`} />

      <QueryState loading={projects.isPending} error={projects.error} what="projects" />
      {projects.data &&
        (projects.data.length === 0 ? (
          <Panel>
            <Empty title={`${meta.name} is ready. Nothing is deployed yet.`}>
              <p>Create a project, add an app from any public container image, and deploy it.</p>
              <Link
                to="/projects"
                className="mt-4 inline-flex h-9 items-center border border-accent bg-accent px-4 text-sm font-medium text-accent-fg hover:bg-transparent hover:text-accent"
              >
                Create your first project
              </Link>
            </Empty>
          </Panel>
        ) : (
          <Panel
            title="Projects"
            actions={
              <span className="text-xs text-muted">
                {projects.data.length} {projects.data.length === 1 ? 'project' : 'projects'} · {apps} {apps === 1 ? 'app' : 'apps'}
              </span>
            }
          >
            <ul className="divide-y divide-line">
              {projects.data.map((p) => (
                <li key={p.id}>
                  <Link to="/projects/$projectId" params={{ projectId: p.id }} className="flex items-center justify-between gap-4 px-4 py-2.5 hover:bg-raised">
                    <span className="truncate text-sm">{p.name}</span>
                    <Badge tone="muted">
                      {p.app_count} {p.app_count === 1 ? 'app' : 'apps'}
                    </Badge>
                  </Link>
                </li>
              ))}
            </ul>
          </Panel>
        ))}

      <Panel
        title="Recent activity"
        actions={
          <Link to="/activity" className="text-xs text-muted hover:text-fg">
            All activity →
          </Link>
        }
      >
        <QueryState loading={events.isPending} error={events.error} what="activity" />
        {events.data && <EventList events={events.data} />}
      </Panel>
    </div>
  )
}
