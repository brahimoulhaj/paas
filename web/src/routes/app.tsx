import { MountSettings } from '../components/mounts'
import { EnvironmentSettings } from '../components/environment'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams, useSearch } from '@tanstack/react-router'
import { lazy, Suspense, useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { DnsHintText } from '../components/dns'
import { LogViewer } from '../components/logviewer'
import { Badge, Button, ConfirmButton, cx, Empty, ErrorNote, Field, HelpTip, HttpOnlyChoice, PageHeader, Panel, QueryState } from '../components/ui'
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
import { SourceFields, sourceChanged, sourceForm, sourceInput } from '../components/source'
import { rich, useT } from '../lib/i18n'
import { appSource, appStatus, dateTime, deploymentVersion, duration, repoName, timeAgo, type Tone } from '../lib/format'

const ContainerTerminal = lazy(() => import('../components/container-terminal'))
export const appTabs = ['overview', 'deployments', 'logs', 'terminal', 'domains', 'settings'] as const
export type AppTab = (typeof appTabs)[number]

export function AppPage() {
  const t = useT()
  const { projectId, appId } = useParams({ strict: false }) as { projectId: string; appId: string }
  const raw = (useSearch({ strict: false }) as { tab?: string }).tab
  const tab: AppTab = appTabs.includes(raw as AppTab) ? (raw as AppTab) : 'overview'
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const project = useQuery(projectQuery(projectId))
  const domains = useQuery(domainsQuery(appId))
  const primaryDomain = domains.data?.find((d) => d.is_primary)
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
  const [terminalAppId, setTerminalAppId] = useState<string | null>(null)
  useEffect(() => {
    if (tab === 'terminal') setTerminalAppId(appId)
  }, [tab, appId])

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
      {t.app.back(project.data?.name ?? t.app.project)}
    </Link>
  )

  if (!app.data) {
    return (
      <div className="space-y-4">
        <p className="text-xs text-muted">{crumbs}</p>
        <QueryState loading={app.isPending} error={app.error} what={t.app.whatApp} />
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
        subtitle={
          <>
            <span className="block">{appSource(a)}</span>
            {primaryDomain && (
              <a
                href={`https://${primaryDomain.hostname}`}
                target="_blank"
                rel="noopener noreferrer"
                title={t.app.primaryDomain}
                className="mt-1 inline-flex max-w-full items-center gap-1.5 text-accent hover:underline"
                dir="ltr"
              >
                <span className="[overflow-wrap:anywhere]">{primaryDomain.hostname}</span>
                <span aria-hidden="true">↗</span>
              </a>
            )}
          </>
        }
        actions={
          <>
            {deployed && a.desired_state === 'running' && (
              <>
                <Button variant="ghost" disabled={busy} onClick={() => action.mutate('restart')}>
                  {t.app.restart}
                </Button>
                <Button variant="ghost" disabled={busy} onClick={() => action.mutate('stop')}>
                  {t.app.stop}
                </Button>
              </>
            )}
            {deployed && a.desired_state === 'stopped' && (
              <Button variant="ghost" disabled={busy} onClick={() => action.mutate('start')}>
                {t.app.start}
              </Button>
            )}
            <Button disabled={busy} onClick={() => deploy.mutate()}>
              {deploying ? t.app.deploying : deployed ? t.app.redeploy : t.app.deploy}
            </Button>
          </>
        }
      />

      {actionError && <ErrorNote>{actionError.message}</ErrorNote>}

      <nav className="flex gap-1 overflow-x-auto overflow-y-hidden border-b border-line" aria-label={t.app.sections}>
        {appTabs.map((name) => (
          <Link
            key={name}
            to="/projects/$projectId/apps/$appId"
            params={{ projectId, appId }}
            search={{ tab: name }}
            className={cx(
              'border-b-2 px-3 py-1.5 text-sm whitespace-nowrap capitalize',
              name === tab ? 'border-accent text-fg' : 'border-transparent text-muted hover:text-fg',
            )}
            aria-current={name === tab ? 'page' : undefined}
          >
            {name === 'terminal' ? t.terminal.title : t.app.tabs[name]}
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
      {(tab === 'terminal' || terminalAppId === a.id) && (
        <div hidden={tab !== 'terminal'}>
          <Suspense fallback={<p className="p-4 text-sm text-muted">{t.terminal.connecting}</p>}>
            <ContainerTerminal key={a.id} app={a} visible={tab === 'terminal'} />
          </Suspense>
        </div>
      )}
      {tab === 'domains' && <Domains app={a} />}
      {tab === 'settings' && <Settings app={a} />}
    </div>
  )
}

// ---- overview ----

function Overview({ app, latest, onOpenDeployments }: { app: App; latest?: Deployment; onOpenDeployments: () => void }) {
  const t = useT()
  const domains = useQuery(domainsQuery(app.id))
  const deployments = useQuery(deploymentsQuery(app.id))
  const current = deployments.data?.find((d) => d.id === app.current_deployment_id)
  // A Git deployment does not record the settings it was built with; only the port is compared.
  const pending = current && ((app.source_type === 'image' && current.image !== app.image) || current.port !== app.port)

  return (
    <div className="space-y-6">
      {!app.current_deployment_id && !(latest && isActive(latest)) && (
        <Panel>
          <Empty title={t.app.neverDeployed}>
            {app.source_type === 'git'
              ? rich(t.app.pressDeployGit, {
                  repo: () => (
                    <span className="text-fg" dir="ltr">
                      {repoName(app.git_repository)}
                    </span>
                  ),
                  branch: () => (
                    <span className="text-fg" dir="ltr">
                      {app.git_branch}
                    </span>
                  ),
                })
              : rich(t.app.pressDeployImage, {
                  image: () => (
                    <span className="text-fg" dir="ltr">
                      {app.image}
                    </span>
                  ),
                })}
            {latest?.status === 'failed' && ` ${t.app.lastAttemptFailed}`}
          </Empty>
        </Panel>
      )}
      {pending && (
        <p className="border border-warn/60 px-3 py-2 text-xs text-warn">
          {t.app.settingsChanged(deploymentVersion(current), current.port)}
        </p>
      )}
      {latest?.status === 'failed' && app.current_deployment_id && (
        <p className="border border-danger/60 px-3 py-2 text-xs text-danger">
          {rich(t.app.deployFailed, {
            err: () => latest.error,
            a: (c) => (
              <button className="underline" onClick={onOpenDeployments}>
                {c}
              </button>
            ),
          })}
        </p>
      )}
      <Panel title={t.app.summary}>
        <dl className="grid gap-px bg-line sm:grid-cols-2">
          {app.source_type === 'git' ? (
            <>
              <Fact label={t.app.repository}>
                <a href={app.git_repository} target="_blank" rel="noreferrer" className="text-accent hover:underline" dir="ltr">
                  {repoName(app.git_repository)}
                </a>{' '}
                · <span dir="ltr">{app.git_branch}</span>
              </Fact>
              <Fact label={t.source.builder}>
                {app.builder === 'railpack' ? 'Railpack' : app.builder === 'auto' ? t.source.builderAuto : 'Dockerfile'}
                {app.builder !== 'railpack' && (
                  <>
                    {' · '}
                    <span dir="ltr">{[app.build_context, app.dockerfile_path].filter(Boolean).join('/')}</span>
                  </>
                )}
              </Fact>
            </>
          ) : (
            <Fact label={t.app.image}>
              <span dir="ltr">{app.image}</span>
            </Fact>
          )}
          {app.start_command && (
            <Fact label={t.app.startCommand}>
              <code dir="ltr" className="[overflow-wrap:anywhere]">{app.start_command}</code>
            </Fact>
          )}
          <Fact label={t.app.containerPort}>{app.port}</Fact>
          <Fact label={t.app.limits}>
            {app.cpu_limit > 0 ? t.app.cpuValue(app.cpu_limit) : t.app.cpuUnlimited} ·{' '}
            {app.memory_limit_mb > 0 ? t.app.memoryValue(app.memory_limit_mb) : t.app.memoryUnlimited}
          </Fact>
          <Fact label={t.app.container}>{app.runtime_state === 'none' ? '—' : app.runtime_state}</Fact>
          <Fact label={t.app.runningVersion}>
            {current ? (
              <span title={dateTime(current.finished_at)}>
                {t.app.deployedAgo(deploymentVersion(current), timeAgo(current.finished_at))}
              </span>
            ) : (
              '—'
            )}
          </Fact>
          <Fact label={t.app.tabs.domains}>
            {domains.data?.length ? (
              <span className="flex flex-col items-start">
                {domains.data.map((d) => (
                  <span key={d.id} className="flex flex-wrap items-center gap-2">
                    <a href={`https://${d.hostname}`} target="_blank" rel="noopener noreferrer" className="text-accent hover:underline" dir="ltr">
                      {d.hostname}
                    </a>
                    {d.is_primary && <Badge tone="muted">{t.app.primary}</Badge>}
                  </span>
                ))}
              </span>
            ) : (
              <span className="text-muted">{t.app.noDomains}</span>
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
  cancelled: 'muted',
}

function Deployments(props: {
  list?: Deployment[]
  loading: boolean
  error: Error | null
  currentId: string
  selected: string | null
  onSelect: (id: string) => void
}) {
  const t = useT()
  const selected = props.list?.find((d) => d.id === props.selected)
  return (
    <div className="space-y-6">
      <Panel title={t.app.tabs.deployments}>
        <QueryState loading={props.loading} error={props.error} what={t.app.whatDeployments} />
        {props.list &&
          (props.list.length === 0 ? (
            <Empty title={t.app.noDeployments}>{t.app.noDeploymentsHint}</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {props.list.map((d) => (
                <li key={d.id}>
                  <button
                    type="button"
                    onClick={() => props.onSelect(d.id)}
                    aria-pressed={d.id === props.selected}
                    className={cx(
                      'flex w-full flex-wrap items-center gap-x-4 gap-y-1 px-4 py-2.5 text-start hover:bg-raised',
                      d.id === props.selected && 'bg-raised',
                    )}
                  >
                    <span className="w-24 shrink-0">
                      <Badge tone={deployTone[d.status]}>{t.app.deployStatus[d.status]}</Badge>
                    </span>
                    <span className="min-w-0 flex-1 truncate text-sm">
                      {deploymentVersion(d)}
                      {d.id === props.currentId && <span className="ms-2 text-xs text-accent">{t.app.live}</span>}
                    </span>
                    <span className="w-full shrink-0 text-xs text-muted sm:w-auto" title={dateTime(d.created_at)}>
                      {t.app.took(timeAgo(d.created_at), duration(d.started_at, isActive(d) ? null : d.finished_at))}
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
  const t = useT()
  const queryClient = useQueryClient()
  const cancel = useMutation({
    mutationFn: () => api<Deployment>(`/deployments/${deployment.id}/cancel`, { method: 'POST' }),
    onSettled: () => queryClient.invalidateQueries({ queryKey: deploymentsQuery(deployment.app_id).queryKey }),
  })
  return (
    <Panel
      title={t.app.logTitle(dateTime(deployment.created_at))}
      actions={
        <div className="flex items-center gap-2">
          {isActive(deployment) && (
            <Button
              variant="ghost"
              type="button"
              title={t.app.cancelDeploymentHint}
              disabled={cancel.isPending}
              onClick={() => cancel.mutate()}
            >
              {t.app.cancelDeployment}
            </Button>
          )}
          <Badge tone={deployTone[deployment.status]}>{t.app.deployStatus[deployment.status]}</Badge>
        </div>
      }
    >
      {cancel.error && <p className="border-b border-line px-4 py-2 text-xs text-danger">{cancel.error.message}</p>}
      {deployment.error && (
        <p className={cx('border-b border-line px-4 py-2 text-xs', deployment.status === 'cancelled' ? 'text-muted' : 'text-danger')}>
          {deployment.error}
        </p>
      )}
      {/* The key reopens the stream when another deployment is selected. */}
      <LogViewer key={deployment.id} url={`/api/deployments/${deployment.id}/log/stream`} />
    </Panel>
  )
}

// ---- runtime logs ----

function RuntimeLogs({ app }: { app: App }) {
  const t = useT()
  if (!app.current_deployment_id) {
    return (
      <Panel>
        <Empty title={t.app.noContainer}>{t.app.noContainerHint}</Empty>
      </Panel>
    )
  }
  // A new deployment, or a stopped container starting again, is a new stream.
  const run = `${app.current_deployment_id}:${app.runtime_state === 'running'}`
  return (
    <Panel title={t.app.containerOutput}>
      <LogViewer
        key={run}
        url={`/api/apps/${app.id}/logs/stream`}
        refused={t.app.containerGone}
      />
    </Panel>
  )
}

// ---- domains ----

function Domains({ app }: { app: App }) {
  const t = useT()
  const queryClient = useQueryClient()
  const domains = useQuery(domainsQuery(app.id))
  const [hostname, setHostname] = useState('')
  const [httpOnly, setHttpOnly] = useState(false)
  const refresh = () => queryClient.invalidateQueries({ queryKey: domainsQuery(app.id).queryKey })

  const add = useMutation({
    mutationFn: () =>
      api<Domain>(`/apps/${app.id}/domains`, { method: 'POST', body: { hostname, tls_mode: httpOnly ? 'http_only' : 'auto' } }),
    onSuccess: () => {
      setHostname('')
      setHttpOnly(false)
    },
    onSettled: refresh, // a 503 "saved, proxy not updated" still added the domain
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    add.mutate()
  }

  return (
    <div className="space-y-6">
      <Panel title={t.app.tabs.domains}>
        <QueryState loading={domains.isPending} error={domains.error} what={t.app.whatDomains} />
        {domains.data &&
          (domains.data.length === 0 ? (
            <Empty title={t.app.noDomainsYet}>{t.app.noDomainsYetHint}</Empty>
          ) : (
            <ul className="divide-y divide-line">
              {domains.data.map((d) => (
                <DomainRow key={d.id} domain={d} onChanged={refresh} />
              ))}
            </ul>
          ))}
      </Panel>
      <Panel title={t.app.addDomainTitle}>
        <form onSubmit={submit} className="space-y-4 p-4">
          <Field
            label={t.app.hostname}
            name="hostname"
            required
            placeholder="app.example.com"
            dir="ltr"
            hint={httpOnly ? t.app.hostnameHintHttp : t.app.hostnameHintHttps}
            value={hostname}
            onChange={(e) => setHostname(e.target.value.trim().toLowerCase())}
          />
          <HttpOnlyChoice checked={httpOnly} onChange={setHttpOnly} />
          {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
          <Button type="submit" disabled={add.isPending}>
            {add.isPending ? t.app.adding : t.app.addDomain}
          </Button>
        </form>
      </Panel>
    </div>
  )
}

function DomainRow({ domain, onChanged }: { domain: Domain; onChanged: () => void }) {
  const t = useT()
  const dns = useQuery(dnsQuery(domain.id))
  const remove = useMutation({
    mutationFn: () => api<void>(`/domains/${domain.id}`, { method: 'DELETE' }),
    onSuccess: onChanged,
  })
  const httpOnly = domain.tls_mode === 'http_only'
  const setMode = useMutation({
    mutationFn: (tls_mode: string) => api<Domain>(`/domains/${domain.id}`, { method: 'PATCH', body: { tls_mode } }),
    onSettled: onChanged, // refreshes the list
  })
  const setPrimary = useMutation({
    mutationFn: () => api<Domain>(`/domains/${domain.id}`, { method: 'PATCH', body: { is_primary: true } }),
    onSuccess: onChanged,
  })
  const busy = remove.isPending || setMode.isPending || setPrimary.isPending

  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className="min-w-0">
        <p className="flex flex-wrap items-center gap-2">
          <a href={`https://${domain.hostname}`} target="_blank" rel="noreferrer" className="text-sm [overflow-wrap:anywhere] text-accent hover:underline" dir="ltr">
            {domain.hostname}
          </a>
          {domain.is_primary && <Badge tone="accent">{t.app.primary}</Badge>}
          {httpOnly && <Badge tone="muted">{t.common.httpOnly}</Badge>}
        </p>
        <p className="text-xs text-muted">
          <DnsHintText dns={dns} />
        </p>
        {remove.error && <p className="text-xs text-danger">{remove.error.message}</p>}
        {setMode.error && <p className="text-xs text-danger">{setMode.error.message}</p>}
        {setPrimary.error && <p className="text-xs text-danger">{setPrimary.error.message}</p>}
      </div>
      <span className="flex flex-wrap items-center gap-2">
        {!domain.is_primary && (
          <Button variant="ghost" type="button" disabled={busy} onClick={() => setPrimary.mutate()}>
            {setPrimary.isPending ? t.common.saving : t.app.makePrimary}
          </Button>
        )}
        <Button
          variant="ghost"
          type="button"
          disabled={busy}
          title={httpOnly ? t.app.toHttpsTitle : t.app.toHttpTitle}
          onClick={() => setMode.mutate(httpOnly ? 'auto' : 'http_only')}
        >
          {setMode.isPending ? t.common.saving : httpOnly ? t.app.useHttps : t.common.httpOnly}
        </Button>
        <ConfirmButton label={t.common.remove} confirm={t.common.yesRemove} disabled={busy} pending={remove.isPending} onConfirm={() => remove.mutate()} />
      </span>
    </li>
  )
}

// ---- settings ----

function Settings({ app }: { app: App }) {
  const t = useT()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const [source, setSource] = useState(sourceForm(app))
  const [form, setForm] = useState({
    name: app.name,
    port: String(app.port),
    cpu: String(app.cpu_limit),
    memory: String(app.memory_limit_mb),
    start: app.start_command,
  })
  const set = (key: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [key]: e.target.value })
  const input: AppInput = {
    ...sourceInput(source),
    name: form.name,
    port: Number(form.port),
    cpu_limit: Number(form.cpu),
    memory_limit_mb: Number(form.memory),
    start_command: form.start.trim(),
  }
  const dirty =
    input.name !== app.name ||
    sourceChanged(source, app) ||
    input.port !== app.port ||
    input.cpu_limit !== app.cpu_limit ||
    input.memory_limit_mb !== app.memory_limit_mb ||
    input.start_command !== app.start_command

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
      <Panel title={t.app.tabs.settings} actions={<HelpTip label={t.app.tabs.settings}>{t.app.appliesNextDeploy}</HelpTip>}>
        <form onSubmit={submit} className="space-y-4 p-4">
          <Field
            label={t.common.name}
            name="name"
            required
            pattern="[a-z0-9]([a-z0-9\-]{0,38}[a-z0-9])?"
            title={t.app.nameRule}
            dir="ltr"
            value={form.name}
            onChange={set('name')}
          />
          <SourceFields form={source} onChange={setSource} />
          <Field
            label={t.app.startCommand}
            name="start_command"
            dir="ltr"
            className="font-mono"
            maxLength={4096}
            placeholder={t.app.startCommandDefault}
            hint={t.app.startCommandHint}
            value={form.start}
            onChange={set('start')}
          />
          <div className="grid gap-4 sm:grid-cols-3">
            <Field label={t.app.containerPort} name="port" dir="ltr" type="number" required min={1} max={65535} value={form.port} onChange={set('port')} />
            <Field label={t.app.cpuLimit} name="cpu" dir="ltr" type="number" min={0} max={256} step="0.05" hintTooltip hint={t.app.zeroUnlimited} value={form.cpu} onChange={set('cpu')} />
            <Field label={t.app.memoryLimit} name="memory" dir="ltr" type="number" min={0} step="1" hintTooltip hint={t.app.zeroUnlimited} value={form.memory} onChange={set('memory')} />
          </div>
          {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
          {save.isSuccess && !dirty && <p className="text-xs text-accent">{t.common.saved}</p>}
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? t.common.saving : t.app.saveSettings}
          </Button>
        </form>
      </Panel>
      <EnvironmentSettings key={app.id} app={app} />
      <MountSettings key={`mounts-${app.id}`} app={app} />
      <Panel title={t.app.deleteApp}>
        <div className="flex flex-wrap items-center justify-between gap-3 p-4">
          <p className="max-w-md text-xs text-muted">
            {t.app.deleteAppHint}
          </p>
          <ConfirmButton label={t.app.deleteApp} confirm={t.common.yesDelete} pending={remove.isPending} onConfirm={() => remove.mutate()} />
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
