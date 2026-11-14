import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { api, type App } from '../lib/api'
import { useT } from '../lib/i18n'
import { Button, ErrorNote, Field, HelpTip, Panel, QueryState } from './ui'

type CheckType = 'tcp' | 'http' | 'running'
type HealthCheck = {
  type: CheckType
  path: string
  expected_status: number
  interval: number
  timeout: number
  retries: number
  start_period: number
}
type Saved = HealthCheck & { saved: boolean }

const checkQuery = (appId: string) => ({
  queryKey: ['apps', appId, 'health-check'] as const,
  queryFn: () => api<Saved>(`/apps/${appId}/health-check`),
})

/** The check a deploy waits for before switching traffic to the new container. */
export function HealthCheckPanel({ app }: { app: App }) {
  const t = useT()
  const check = useQuery(checkQuery(app.id))
  return (
    <Panel title={<span className="inline-flex items-center gap-2">{t.healthCheck.title}<HelpTip label={t.healthCheck.title}>{t.healthCheck.intro}</HelpTip></span>}>
      {check.data ? (
        <CheckForm key={app.id} app={app} saved={check.data} />
      ) : (
        <div className="p-4">
          <QueryState loading={check.isPending} error={check.error} what={t.healthCheck.what} />
        </div>
      )}
    </Panel>
  )
}

/** Form values are strings while editing, so a field can be emptied. */
const toForm = (h: HealthCheck) => ({
  type: h.type,
  path: h.path || '/',
  expected: h.expected_status ? String(h.expected_status) : '',
  interval: String(h.interval),
  timeout: String(h.timeout),
  retries: String(h.retries),
  start: String(h.start_period),
})

function CheckForm({ app, saved }: { app: App; saved: Saved }) {
  const t = useT()
  const th = t.healthCheck
  const queryClient = useQueryClient()
  // An app with nothing saved runs the legacy 3-second wait: offer TCP as the starting point.
  const [form, setForm] = useState(() => toForm(saved.saved ? saved : { ...saved, type: 'tcp', start_period: 30 }))
  const set = (key: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [key]: e.target.value })
  const body: HealthCheck = {
    type: form.type,
    path: form.path.trim(),
    expected_status: Number(form.expected) || 0,
    interval: Number(form.interval),
    timeout: Number(form.timeout),
    retries: Number(form.retries),
    start_period: Number(form.start),
  }
  const dirty = !saved.saved || JSON.stringify(toForm(body)) !== JSON.stringify(toForm(saved))
  const save = useMutation({
    mutationFn: () => api<Saved>(`/apps/${app.id}/health-check`, { method: 'PUT', body }),
    onSuccess: (h) => queryClient.setQueryData(checkQuery(app.id).queryKey, h),
  })
  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate()
  }
  const target = form.type === 'http' ? `http://<container>:${app.port}${form.path.trim()}` : `<container>:${app.port}`

  return (
    <form onSubmit={submit} className="space-y-4 p-4">
      {!saved.saved && <p className="border border-warn/60 px-3 py-2 text-xs text-warn">{th.legacy}</p>}
      <label className="block max-w-sm space-y-1.5 text-xs text-muted">
        {th.type}
        <select
          aria-label={th.type}
          value={form.type}
          onChange={(e) => setForm({ ...form, type: e.target.value as CheckType, start: e.target.value === 'running' ? '10' : form.start })}
          className="block h-9 w-full border border-line bg-bg px-2 text-sm text-fg"
        >
          <option value="tcp">{th.tcp}</option>
          <option value="http">{th.http}</option>
          <option value="running">{th.running}</option>
        </select>
      </label>

      {form.type === 'running' ? (
        <div className="max-w-48">
          <Field label={th.stayUp} name="stay" dir="ltr" type="number" required min={1} max={900} hint={th.stayUpHint} value={form.start} onChange={set('start')} />
        </div>
      ) : (
        <>
          {form.type === 'http' && (
            <div className="grid gap-4 sm:grid-cols-[1fr_12rem]">
              <Field label={th.path} name="path" dir="ltr" className="font-mono" required pattern="/\S*" maxLength={512} value={form.path} onChange={set('path')} />
              <Field label={th.expectedStatus} name="expected" dir="ltr" type="number" min={100} max={599} placeholder={th.anyStatus} value={form.expected} onChange={set('expected')} />
            </div>
          )}
          <p className="font-mono text-xs text-muted" dir="ltr">{th.target(target)}</p>
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Field label={th.startPeriod} name="start" dir="ltr" type="number" required min={0} max={900} hintTooltip hint={th.startPeriodHint} value={form.start} onChange={set('start')} />
            <Field label={th.interval} name="interval" dir="ltr" type="number" required min={1} max={300} value={form.interval} onChange={set('interval')} />
            <Field label={th.timeout} name="timeout" dir="ltr" type="number" required min={1} max={60} value={form.timeout} onChange={set('timeout')} />
            <Field label={th.retries} name="retries" dir="ltr" type="number" required min={1} max={20} hintTooltip hint={th.retriesHint} value={form.retries} onChange={set('retries')} />
          </div>
        </>
      )}

      {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
      {save.isSuccess && !dirty && <p className="text-xs text-accent">{t.common.saved}</p>}
      <div className="flex flex-wrap items-center gap-3">
        <Button type="submit" disabled={!dirty || save.isPending}>
          {save.isPending ? t.common.saving : t.app.saveSettings}
        </Button>
        <span className="text-xs text-muted">{th.nextDeploy}</span>
      </div>
    </form>
  )
}
