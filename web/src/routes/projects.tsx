import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from '@tanstack/react-router'
import { useState, type FormEvent } from 'react'
import { Button, Empty, ErrorNote, Field, PageHeader, Panel, QueryState } from '../components/ui'
import { api, projectsQuery, type Project } from '../lib/api'
import { timeAgo } from '../lib/format'
import { useT } from '../lib/i18n'

export function ProjectsPage() {
  const t = useT()
  const projects = useQuery(projectsQuery)
  const [creating, setCreating] = useState(false)

  return (
    <div className="space-y-6">
      <PageHeader
        title={t.projects.title}
        subtitle={t.projects.subtitle}
        actions={!creating && <Button onClick={() => setCreating(true)}>{t.projects.newProject}</Button>}
      />
      {creating && <NewProject onDone={() => setCreating(false)} />}
      <QueryState loading={projects.isPending} error={projects.error} what={t.projects.what} />
      {projects.data && (
        <Panel>
          {projects.data.length === 0 ? (
            <Empty title={t.projects.emptyTitle}>{t.projects.emptyHint}</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {projects.data.map((p) => (
                <li key={p.id}>
                  <Link
                    to="/projects/$projectId"
                    params={{ projectId: p.id }}
                    className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1 px-4 py-3 hover:bg-raised"
                  >
                    <span className="min-w-0">
                      <span className="block truncate text-sm">{p.name}</span>
                      {p.description && <span className="block truncate text-xs text-muted">{p.description}</span>}
                    </span>
                    <span className="shrink-0 text-xs text-muted">
                      {t.projects.apps(p.app_count)} · {t.projects.created(timeAgo(p.created_at))}
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Panel>
      )}
    </div>
  )
}

function NewProject({ onDone }: { onDone: () => void }) {
  const t = useT()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  const create = useMutation({
    mutationFn: () => api<Project>('/projects', { method: 'POST', body: { name, description } }),
    onSuccess: (p) => {
      queryClient.invalidateQueries({ queryKey: projectsQuery.queryKey })
      navigate({ to: '/projects/$projectId', params: { projectId: p.id } })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate()
  }

  return (
    <Panel title={t.projects.newProject}>
      <form onSubmit={submit} className="space-y-4 p-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label={t.common.name} name="name" required autoFocus maxLength={64} value={name} onChange={(e) => setName(e.target.value)} />
          <Field label={`${t.common.description} (${t.common.optional})`} name="description" maxLength={500} value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        {create.error && <ErrorNote>{create.error.message}</ErrorNote>}
        <div className="flex gap-2">
          <Button type="submit" disabled={create.isPending}>
            {create.isPending ? t.common.creating : t.projects.createProject}
          </Button>
          <Button type="button" variant="ghost" onClick={onDone}>
            {t.common.cancel}
          </Button>
        </div>
      </form>
    </Panel>
  )
}
