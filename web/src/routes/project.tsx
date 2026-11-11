import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { SourceFields, sourceForm, sourceInput } from '../components/source'
import { Badge, Button, ConfirmButton, Empty, ErrorNote, Field, PageHeader, Panel, QueryState } from '../components/ui'
import { api, appQuery, appsQuery, projectQuery, projectsQuery, type App, type Project } from '../lib/api'
import { appSource, appStatus } from '../lib/format'

export function ProjectPage() {
  const { projectId } = useParams({ strict: false }) as { projectId: string }
  const project = useQuery(projectQuery(projectId))
  const apps = useQuery(appsQuery(projectId))
  const [adding, setAdding] = useState(false)

  if (!project.data) {
    return (
      <div className="space-y-4">
        <Crumbs />
        <QueryState loading={project.isPending} error={project.error} what="project" />
      </div>
    )
  }
  const p = project.data

  return (
    <div className="space-y-6">
      <PageHeader
        crumbs={<Crumbs />}
        title={p.name}
        subtitle={p.description || undefined}
        actions={!adding && <Button onClick={() => setAdding(true)}>New app</Button>}
      />

      {adding && <NewApp projectId={p.id} onDone={() => setAdding(false)} />}

      <Panel title="Apps">
        <QueryState loading={apps.isPending} error={apps.error} what="apps" />
        {apps.data &&
          (apps.data.length === 0 ? (
            <Empty title="No apps in this project yet.">
              An app runs one container, from a ready image or built from a Git repository. Add a domain to it and it is
              served over HTTPS.
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {apps.data.map((a) => (
                <AppRow key={a.id} app={a} />
              ))}
            </ul>
          ))}
      </Panel>

      <ProjectSettings project={p} />
    </div>
  )
}

function Crumbs() {
  return (
    <Link to="/projects" className="hover:text-fg">
      ← Projects
    </Link>
  )
}

/** One app in the list. It asks for the app on its own to learn what the container is doing. */
function AppRow({ app }: { app: App }) {
  const live = useQuery({ ...appQuery(app.id), refetchInterval: 10_000 })
  const status = appStatus(live.data ?? app, false)
  return (
    <li>
      <Link
        to="/projects/$projectId/apps/$appId"
        params={{ projectId: app.project_id, appId: app.id }}
        className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1 px-4 py-3 hover:bg-raised"
      >
        <span className="min-w-0">
          <span className="block truncate text-sm">{app.name}</span>
          <span className="block truncate text-xs text-muted">
            {appSource(app)} · port {app.port}
          </span>
        </span>
        <Badge tone={status.tone}>{status.label}</Badge>
      </Link>
    </li>
  )
}

function NewApp({ projectId, onDone }: { projectId: string; onDone: () => void }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [source, setSource] = useState(sourceForm())
  const [port, setPort] = useState('80')

  const create = useMutation({
    mutationFn: () =>
      api<App>(`/projects/${projectId}/apps`, { method: 'POST', body: { name, port: Number(port), ...sourceInput(source) } }),
    onSuccess: (a) => {
      queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey })
      navigate({ to: '/projects/$projectId/apps/$appId', params: { projectId, appId: a.id } })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate()
  }

  return (
    <Panel title="New app">
      <form onSubmit={submit} className="space-y-4 p-4">
        <div className="grid gap-4 sm:grid-cols-[2fr_1fr]">
          <Field
            label="Name"
            name="name"
            required
            autoFocus
            placeholder="web"
            pattern="[a-z0-9]([a-z0-9\-]{0,38}[a-z0-9])?"
            title="Lowercase letters, digits and dashes"
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
          />
          <Field label="Container port" name="port" type="number" required min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
        </div>
        <SourceFields form={source} onChange={setSource} />
        <p className="text-xs text-muted">
          The port is the one the container listens on; it is not published on the server. Nothing runs until you deploy.
        </p>
        {create.error && <ErrorNote>{create.error.message}</ErrorNote>}
        <div className="flex gap-2">
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? 'Creating…' : 'Create app'}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        </div>
      </form>
    </Panel>
  )
}

function ProjectSettings({ project }: { project: Project }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState(project.name)
  const [description, setDescription] = useState(project.description)
  const dirty = name !== project.name || description !== project.description

  const save = useMutation({
    mutationFn: () => api<Project>(`/projects/${project.id}`, { method: 'PATCH', body: { name, description } }),
    onSuccess: (p) => {
      queryClient.setQueryData(projectQuery(project.id).queryKey, p)
      queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey, exact: true })
    },
  })
  const remove = useMutation({
    mutationFn: () => api<void>(`/projects/${project.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: projectQuery(project.id).queryKey })
      queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey, exact: true })
      navigate({ to: '/projects' })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }

  return (
    <Panel title="Project settings">
      <form onSubmit={submit} className="space-y-4 p-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" name="project-name" required maxLength={64} value={name} onChange={(e) => setName(e.target.value)} />
          <Field label="Description" name="project-description" maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
        <Button type="submit" variant="ghost" disabled={!dirty || save.isPending}>
          {save.isPending ? 'Saving…' : 'Save'}
        </Button>
      </form>
      <div className="flex flex-wrap items-center justify-between gap-3 border-t border-line p-4">
        <p className="text-xs text-muted">
          {project.app_count > 0 ? 'Delete the apps in this project before deleting it.' : 'Deleting a project cannot be undone.'}
        </p>
        <ConfirmButton
          label="Delete project"
          confirm="Yes, delete it"
          disabled={project.app_count > 0}
          pending={remove.isPending}
          onConfirm={() => remove.mutate()}
        />
      </div>
      {remove.error && (
        <div className="px-4 pb-4">
          <ErrorNote>{remove.error.message}</ErrorNote>
        </div>
      )}
    </Panel>
  )
}
