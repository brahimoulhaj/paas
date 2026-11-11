import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { DnsHintText } from '../components/dns'
import { LogViewer } from '../components/logviewer'
import { Badge, Button, ConfirmButton, cx, Empty, ErrorNote, Field, PageHeader, Panel, QueryState } from '../components/ui'
import {
  api,
  appQuery,
  appsQuery,
  deploymentsQuery,
  dnsQuery,
  domainsQuery,
  isActive,
  projectQuery,
  type App,
  type AppInput,
  type Deployment,
  type Domain,
} from '../lib/api'
import { appStatus, dateTime, duration, timeAgo, type Tone } from '../lib/format'

export const appTabs = ['overview', 'deployments', 'logs', 'domains', 'settings'] as const
export type AppTab = (typeof appTabs)[number]

export function AppPage() {
  const { projectId, appId } = useParams({ strict: false }) as { projectId: string; appId: string }
  const tab = (useSearch({ strict: false }) as { tab?: AppTab }).tab ?? 'overview'
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const project = useQuery(projectQuery(projectId))
  const deployments = useQuery({
    ...deploymentsQuery(appId),
    refetchInterval: (q) => (q.state.data?.some(isActive) ? 1500 : false),
  })
  const latest = deployments.data?.[0]
  const deploying = !!latest && isActive(latest)
  const app = useQuery({ ...appQuery(appId), refetchInterval: deploying ? 2000 : 10_000 })

  // A deployment that just finished changes the app (current deployment, state).
  useEffect(() => {
    queryClient.invalidateQueries({ queryKey: appQuery(appId).queryKey, exact: true })
  }, [latest?.id, latest?.status, appId, queryClient])

  const [selected, setSelected] = useState<string | null>(null)

  const goto = (t: AppTab) =>
    navigate({ to: '/projects/$projectId/apps/$appId', params: { projectId, appId }, search: { tab: t } })

  const deploy = useMutation({
    mutationFn: () => api<Deployment>(`/apps/${appId}/deploy`, { method: 'POST' }),
    onSuccess: (d) => {
      queryClient.setQueryData<Deployment[]>(deploymentsQuery(appId).queryKey, (old) => [d, ...(old ?? [])])
      setSelected(d.id)
      goto('deployments')
    },
  })
  const action = useMutation({
    mutationFn: (name: 'start' | 'stop' | 'restart') => api<App>(`/apps/${appId}/${name}`, { method: 'POST' }),
    onSuccess: (a) => queryClient.setQueryData(appQuery(appId).queryKey, a),
  })

  const crumbs = (
    <Link to="/projects/$projectId" params={{ projectId }} className="hover:text-fg">
      ← {project.data?.name ?? 'Project'}
    </Link>
  )

  if (!app.data) {
    return (
      <div className="space-y-4">
        <p className="text-xs text-muted">{crumbs}</p>
        <QueryState loading={app.isPending} error={app.error} what="app" />
      </div>
    )
  }
  const a = app.data
  const status = appStatus(a, deploying)
  const deployed = !!a.current_deployment_id
  const busy = deploying || deploy.isPending || action.isPending
  const actionError = deploy.error ?? action.error

  return (
    <div className="space-y-6">
      <PageHeader
        crumbs={crumbs}
        title={
          <>
            {a.name} <Badge tone={status.tone}>{status.label}</Badge>
          </>
        }
        subtitle={a.image}
        actions={
          <>
            {deployed && a.desired_state === 'running' && (
              <>
                <Button variant="ghost" disabled={busy} onClick={() => action.mutate('restart')}>
                  Restart
                </Button>
                <Button variant="ghost" disabled={busy} onClick={() => action.mutate('stop')}>
                  Stop
                </Button>
              </>
            )}
            {deployed && a.desired_state === 'stopped' && (
              <Button variant="ghost" disabled={busy} onClick={() => action.mutate('start')}>
                Start
              </Button>
            )}
            <Button disabled={busy} onClick={() => deploy.mutate()}>
              {deploying ? 'Deploying…' : deployed ? 'Redeploy' : 'Deploy'}
            </Button>
          </>
        }
      />

      {actionError && <ErrorNote>{actionError.message}</ErrorNote>}

      <nav className="flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line" aria-label="App sections">
        {appTabs.map((t) => (
          <Link
            key={t}
            to="/projects/$projectId/apps/$appId"
            params={{ projectId, appId }}
            search={{ tab: t }}
            className={cx(
              'border-b-2 px-3 py-1.5 text-sm whitespace-nowrap capitalize',
              t === tab ? 'border-accent text-fg' : 'border-transparent text-muted hover:text-fg',
            )}
            aria-current={t === tab ? 'page' : undefined}
          >
            {t}
          </Link>
        ))}
      </nav>

      {tab === 'overview' && <Overview app={a} latest={latest} onOpenDeployments={() => goto('deployments')} />}
      {tab === 'deployments' && (
        <Deployments
          list={deployments.data}
          loading={deployments.isPending}
          error={deployments.error}
          currentId={a.current_deployment_id}
          selected={selected ?? latest?.id ?? null}
          onSelect={setSelected}
        />
      )}
      {tab === 'logs' && <RuntimeLogs app={a} />}
      {tab === 'domains' && <Domains app={a} />}
      {tab === 'settings' && <Settings app={a} />}
    </div>
  )
}

// ---- overview ----

function Overview({ app, latest, onOpenDeployments }: { app: App; latest?: Deployment; onOpenDeployments: () => void }) {
  const domains = useQuery(domainsQuery(app.id))
  const deployments = useQuery(deploymentsQuery(app.id))
  const current = deployments.data?.find((d) => d.id === app.current_deployment_id)
  const pending = current && (current.image !== app.image || current.port !== app.port)

  return (
    <div className="space-y-6">
      {!app.current_deployment_id && !(latest && isActive(latest)) && (
        <Panel>
          <Empty title="This app has never been deployed.">
            Press Deploy to pull <span className="text-fg">{app.image}</span> and start it.
            {latest?.status === 'failed' && ' The last attempt failed; its log is under Deployments.'}
          </Empty>
        </Panel>
      )}
      {pending && (
        <p className="border border-warn/60 px-3 py-2 text-xs text-warn">
          Settings changed since the last deploy (deployed: {current.image}, port {current.port}). Redeploy to apply them.
        </p>
      )}
      {latest?.status === 'failed' && app.current_deployment_id && (
        <p className="border border-danger/60 px-3 py-2 text-xs text-danger">
          The last deploy failed: {latest.error}.{' '}
          <button className="underline" onClick={onOpenDeployments}>
            Open its log
          </button>
          . The previous version was kept.
        </p>
      )}
      <Panel title="Summary">
        <dl className="grid gap-px bg-line sm:grid-cols-2">
          <Fact label="Image">{app.image}</Fact>
          <Fact label="Container port">{app.port}</Fact>
          <Fact label="Limits">
            {app.cpu_limit > 0 ? `${app.cpu_limit} CPU` : 'CPU unlimited'} ·{' '}
            {app.memory_limit_mb > 0 ? `${app.memory_limit_mb} MB` : 'memory unlimited'}
          </Fact>
          <Fact label="Container">{app.runtime_state === 'none' ? '—' : app.runtime_state}</Fact>
          <Fact label="Running version">
            {current ? (
              <span title={dateTime(current.finished_at)}>
                {current.image} · deployed {timeAgo(current.finished_at)}
              </span>
            ) : (
              '—'
            )}
          </Fact>
          <Fact label="Domains">
            {domains.data?.length ? (
              <span className="flex flex-col">
                {domains.data.map((d) => (
                  <a key={d.id} href={`https://${d.hostname}`} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                    {d.hostname}
                  </a>
                ))}
              </span>
            ) : (
              <span className="text-muted">None. Without a domain the app is not reachable from outside.</span>
            )}
          </Fact>
        </dl>
      </Panel>
    </div>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="bg-surface px-4 py-3">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="mt-0.5 text-sm [overflow-wrap:anywhere]">{children}</dd>
    </div>
  )
}

// ---- deployments ----

const deployTone: Record<Deployment['status'], Tone> = {
  queued: 'muted',
  running: 'warn',
  succeeded: 'accent',
  failed: 'danger',
}

function Deployments(props: {
  list?: Deployment[]
  loading: boolean
  error: Error | null
  currentId: string
  selected: string | null
  onSelect: (id: string) => void
}) {
  const selected = props.list?.find((d) => d.id === props.selected)
  return (
    <div className="space-y-6">
      <Panel title="Deployments">
        <QueryState loading={props.loading} error={props.error} what="deployments" />
        {props.list &&
          (props.list.length === 0 ? (
            <Empty title="No deployments yet.">Each deploy is recorded here with its log.</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {props.list.map((d) => (
                <li key={d.id}>
                  <button
                    type="button"
                    onClick={() => props.onSelect(d.id)}
                    aria-pressed={d.id === props.selected}
                    className={cx(
                      'flex w-full flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 text-left hover:bg-raised',
                      d.id === props.selected && 'bg-raised',
                    )}
                  >
                    <span className="w-24 shrink-0">
                      <Badge tone={deployTone[d.status]}>{d.status}</Badge>
                    </span>
                    <span className="min-w-0 flex-1 truncate text-sm">
                      {d.image}
                      {d.id === props.currentId && <span className="ml-2 text-xs text-accent">live</span>}
                    </span>
                    <span className="w-full shrink-0 text-xs text-muted sm:w-auto" title={dateTime(d.created_at)}>
                      {timeAgo(d.created_at)} · took {duration(d.started_at, isActive(d) ? null : d.finished_at)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          ))}
      </Panel>
      {selected && <DeploymentLog deployment={selected} />}
    </div>
  )
}

function DeploymentLog({ deployment }: { deployment: Deployment }) {
  return (
    <Panel
      title={`Log · ${dateTime(deployment.created_at)}`}
      actions={<Badge tone={deployTone[deployment.status]}>{deployment.status}</Badge>}
    >
      {deployment.error && <p className="border-b border-line px-4 py-2 text-xs text-danger">{deployment.error}</p>}
      {/* The key reopens the stream when another deployment is selected. */}
      <LogViewer key={deployment.id} url={`/api/deployments/${deployment.id}/log/stream`} />
    </Panel>
  )
}

// ---- runtime logs ----

function RuntimeLogs({ app }: { app: App }) {
  if (!app.current_deployment_id) {
    return (
      <Panel>
        <Empty title="No container yet.">Logs appear here once the app has been deployed.</Empty>
      </Panel>
    )
  }
  // A new deployment, or a stopped container starting again, is a new stream.
  const run = `${app.current_deployment_id}:${app.runtime_state === 'running'}`
  return (
    <Panel title="Container output">
      <LogViewer
        key={run}
        url={`/api/apps/${app.id}/logs/stream?tail=500`}
        refused="The container is gone. Redeploy the app."
      />
    </Panel>
  )
}

// ---- domains ----

function Domains({ app }: { app: App }) {
  const queryClient = useQueryClient()
  const domains = useQuery(domainsQuery(app.id))
  const [hostname, setHostname] = useState('')
  const refresh = () => queryClient.invalidateQueries({ queryKey: domainsQuery(app.id).queryKey })

  const add = useMutation({
    mutationFn: () => api<Domain>(`/apps/${app.id}/domains`, { method: 'POST', body: { hostname } }),
    onSuccess: () => setHostname(''),
    onSettled: refresh, // a 503 "saved, proxy not updated" still added the domain
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    add.mutate()
  }

  return (
    <div className="space-y-6">
      <Panel title="Domains">
        <QueryState loading={domains.isPending} error={domains.error} what="domains" />
        {domains.data &&
          (domains.data.length === 0 ? (
            <Empty title="No domains yet.">
              Add a hostname and point its DNS record at this server. The certificate is issued automatically.
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {domains.data.map((d) => (
                <DomainRow key={d.id} domain={d} onRemoved={refresh} />
              ))}
            </ul>
          ))}
      </Panel>
      <Panel title="Add a domain">
        <form onSubmit={submit} className="space-y-4 p-4">
          <Field
            label="Hostname"
            name="hostname"
            required
            placeholder="app.example.com"
            hint="No scheme, port or path. Plain HTTP requests are redirected to HTTPS."
            value={hostname}
            onChange={(e) => setHostname(e.target.value.trim().toLowerCase())}
          />
          {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
          <Button type="submit" disabled={add.isPending}>
            {add.isPending ? 'Adding…' : 'Add domain'}
          </Button>
        </form>
      </Panel>
    </div>
  )
}

function DomainRow({ domain, onRemoved }: { domain: Domain; onRemoved: () => void }) {
  const dns = useQuery(dnsQuery(domain.id))
  const remove = useMutation({
    mutationFn: () => api<void>(`/domains/${domain.id}`, { method: 'DELETE' }),
    onSuccess: onRemoved,
  })

  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className="min-w-0">
        <a href={`https://${domain.hostname}`} target="_blank" rel="noreferrer" className="text-sm [overflow-wrap:anywhere] text-accent hover:underline">
          {domain.hostname}
        </a>
        <p className="text-xs text-muted">
          <DnsHintText dns={dns} />
        </p>
        {remove.error && <p className="text-xs text-danger">{remove.error.message}</p>}
      </div>
      <ConfirmButton label="Remove" confirm="Yes, remove" pending={remove.isPending} onConfirm={() => remove.mutate()} />
    </li>
  )
}

// ---- settings ----

function Settings({ app }: { app: App }) {
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [form, setForm] = useState({
    name: app.name,
    image: app.image,
    port: String(app.port),
    cpu: String(app.cpu_limit),
    memory: String(app.memory_limit_mb),
  })
  const set = (key: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [key]: e.target.value })
  const input: AppInput = {
    name: form.name,
    image: form.image.trim(),
    port: Number(form.port),
    cpu_limit: Number(form.cpu),
    memory_limit_mb: Number(form.memory),
  }
  const dirty =
    input.name !== app.name ||
    input.image !== app.image ||
    input.port !== app.port ||
    input.cpu_limit !== app.cpu_limit ||
    input.memory_limit_mb !== app.memory_limit_mb

  const save = useMutation({
    mutationFn: () => api<App>(`/apps/${app.id}`, { method: 'PATCH', body: input }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: appQuery(app.id).queryKey, exact: true })
      queryClient.invalidateQueries({ queryKey: appsQuery(app.project_id).queryKey })
    },
  })
  const remove = useMutation({
    mutationFn: () => api<void>(`/apps/${app.id}`, { method: 'DELETE' }),
    onSuccess: () => {
      queryClient.removeQueries({ queryKey: appQuery(app.id).queryKey })
      queryClient.invalidateQueries({ queryKey: projectQuery(app.project_id).queryKey })
      navigate({ to: '/projects/$projectId', params: { projectId: app.project_id } })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }

  return (
    <div className="space-y-6">
      <Panel title="Settings">
        <form onSubmit={submit} className="space-y-4 p-4">
          <div className="grid gap-4 sm:grid-cols-[1fr_2fr]">
            <Field
              label="Name"
              name="name"
              required
              pattern="[a-z0-9]([a-z0-9\-]{0,38}[a-z0-9])?"
              title="Lowercase letters, digits and dashes"
              value={form.name}
              onChange={set('name')}
            />
            <Field label="Image" name="image" required value={form.image} onChange={set('image')} />
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label="Container port" name="port" type="number" required min={1} max={65535} value={form.port} onChange={set('port')} />
            <Field label="CPU limit (cores)" name="cpu" type="number" min={0} max={256} step="0.05" hint="0 = unlimited" value={form.cpu} onChange={set('cpu')} />
            <Field label="Memory limit (MB)" name="memory" type="number" min={0} step="1" hint="0 = unlimited" value={form.memory} onChange={set('memory')} />
          </div>
          <p className="text-xs text-muted">Image, port and limits apply with the next deploy. The running container is not touched.</p>
          {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
          {save.isSuccess && !dirty && <p className="text-xs text-accent">Saved.</p>}
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? 'Saving…' : 'Save settings'}
          </Button>
        </form>
      </Panel>
      <Panel title="Delete app">
        <div className="flex flex-wrap items-center justify-between gap-3 p-4">
          <p className="max-w-md text-xs text-muted">
            Stops and removes the container, the domains, and every deployment record and log of this app. This cannot be
            undone.
          </p>
          <ConfirmButton label="Delete app" confirm="Yes, delete it" pending={remove.isPending} onConfirm={() => remove.mutate()} />
        </div>
        {remove.error && (
          <div className="px-4 pb-4">
            <ErrorNote>{remove.error.message}</ErrorNote>
          </div>
        )}
      </Panel>
    </div>
  )
}
