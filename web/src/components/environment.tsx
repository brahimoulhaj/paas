import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, appQuery, appsQuery, type App } from '../lib/api'
import { environmentMap, parseEnvironment, serializeEnvironment, type EnvironmentRow } from '../lib/environment'
import { useT } from '../lib/i18n'
import { Button, ErrorNote, HelpTip, Panel } from './ui'

const inputClass = 'block h-9 w-full border border-line bg-bg px-3 text-sm text-fg placeholder:text-muted/60 focus:border-accent focus:outline-none'

export function EnvironmentSettings({ app }: { app: App }) {
  const t = useT().app
  const common = useT().common
  const client = useQueryClient()
  const [rows, setRows] = useState<EnvironmentRow[]>(() => Object.entries(app.environment ?? {}).map(([key, value]) => ({ key, value })))
  const [bulk, setBulk] = useState(false)
  const [text, setText] = useState('')
  const [error, setError] = useState('')
  const [changed, setChanged] = useState(false)
  const save = useMutation({
    mutationFn: (environment: Record<string, string>) => api<App>(`/apps/${app.id}`, { method: 'PATCH', body: { environment } }),
    onSuccess: () => {
      setChanged(false)
      client.invalidateQueries({ queryKey: appQuery(app.id).queryKey, exact: true })
      client.invalidateQueries({ queryKey: appsQuery(app.project_id).queryKey })
    },
  })
  function switchMode() {
    try {
      if (bulk) { const next = parseEnvironment(text); environmentMap(next); setRows(next) }
      else { environmentMap(rows); setText(serializeEnvironment(rows)) }
      setBulk(!bulk); setError('')
    } catch { setError(t.environmentInvalid) }
  }
  function submit(e: FormEvent) {
    e.preventDefault()
    try { const next = bulk ? parseEnvironment(text) : rows; const values = environmentMap(next); setRows(next); setError(''); save.mutate(values) }
    catch { setError(t.environmentInvalid) }
  }
  return <Panel title={t.environment} actions={<HelpTip label={t.environment}>{t.environmentHint}</HelpTip>}>
    <form onSubmit={submit} className="space-y-4 p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <Button type="button" variant="ghost" disabled={save.isPending} onClick={switchMode}>{bulk ? t.environmentFields : t.environmentBulk}</Button>
      </div>
      <fieldset disabled={save.isPending} className="min-w-0 space-y-3">
        {bulk ? <>
          <div className="flex items-center gap-1.5 text-xs text-muted">{t.environmentBulk}<HelpTip label={t.environmentBulk}>{t.environmentFormat}</HelpTip></div>
          <textarea aria-label={t.environmentBulk} dir="ltr" spellCheck={false} value={text} onChange={(e) => { setText(e.target.value); setChanged(true) }} className="min-h-64 w-full resize-y border border-line bg-bg p-3 font-mono text-xs text-fg focus:border-accent focus:outline-none" placeholder={'APP_ENV=production\nPORT=8080'} />
        </> : <>
          {rows.length === 0 && <p className="text-xs text-muted">{t.environmentEmpty}</p>}
          {rows.length > 0 && <div aria-hidden className="hidden gap-2 text-xs text-muted sm:grid sm:grid-cols-[1fr_2fr_auto]">
            <span>{t.environmentKey}</span>
            <span>{t.environmentValue}</span>
            <span className="invisible px-4 text-sm">{t.environmentRemove}</span>
          </div>}
          {rows.map((row, index) => <div key={index} className="grid items-center gap-2 sm:grid-cols-[1fr_2fr_auto]">
            <input aria-label={`${t.environmentKey} ${index + 1}`} placeholder={t.environmentKey} name={`env-key-${index}`} dir="ltr" autoComplete="off" spellCheck={false} className={inputClass} value={row.key} onChange={(e) => { setRows(rows.map((r, i) => i === index ? { ...r, key: e.target.value } : r)); setChanged(true) }} />
            <input aria-label={`${t.environmentValue} ${index + 1}`} placeholder={t.environmentValue} name={`env-value-${index}`} dir="ltr" autoComplete="off" spellCheck={false} className={inputClass} value={row.value} onChange={(e) => { setRows(rows.map((r, i) => i === index ? { ...r, value: e.target.value } : r)); setChanged(true) }} />
            <Button type="button" variant="ghost" onClick={() => { setRows(rows.filter((_, i) => i !== index)); setChanged(true) }}>{t.environmentRemove}</Button>
          </div>)}
          <Button type="button" variant="ghost" onClick={() => { setRows([...rows, { key: '', value: '' }]); setChanged(true) }}>{t.environmentAdd}</Button>
        </>}
      </fieldset>
      {error && <ErrorNote>{error}</ErrorNote>}
      {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
      {save.isSuccess && !changed && <p className="text-xs text-accent">{common.saved}</p>}
      <Button type="submit" disabled={!changed || save.isPending}>{save.isPending ? common.saving : t.saveSettings}</Button>
    </form>
  </Panel>
}
