import { useState, type FormEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api, appQuery, type App, type AppMount } from '../lib/api'
import { useT } from '../lib/i18n'
import { Button, ErrorNote, Field, HelpTip, Panel } from './ui'

export function MountSettings({ app }: { app: App }) {
  const t = useT().app
  const common = useT().common
  const client = useQueryClient()
  const [mounts, setMounts] = useState<AppMount[]>(app.mounts ?? [])
  const [dirty, setDirty] = useState(false)
  const save = useMutation({
    mutationFn: () => api<App>(`/apps/${app.id}`, { method: 'PATCH', body: { mounts } }),
    onSuccess: () => { setDirty(false); client.invalidateQueries({ queryKey: appQuery(app.id).queryKey, exact: true }) },
  })
  function update(index: number, patch: Partial<AppMount>) {
    setMounts(mounts.map((m, i) => i === index ? { ...m, ...patch } : m)); setDirty(true)
  }
  function submit(e: FormEvent) { e.preventDefault(); save.mutate() }
  return <Panel title={t.mountsTitle} actions={<HelpTip label={t.mountsTitle}>{t.mountsHint}</HelpTip>}>
    <form onSubmit={submit} className="space-y-4 p-4">
      <fieldset disabled={save.isPending} className="min-w-0 space-y-3">
        {mounts.length === 0 && <p className="text-xs text-muted">{t.mountsEmpty}</p>}
        {mounts.map((m, i) => <div key={i} className="space-y-3 border border-line p-3">
          <div className="grid items-end gap-3 sm:grid-cols-2">
            <label className="space-y-1.5 text-xs text-muted">{t.mountType}
              <select aria-label={t.mountType} value={m.type} onChange={(e) => update(i, { type: e.target.value as AppMount['type'], source: '', content: '', read_only: e.target.value === 'file' })} className="block h-9 w-full border border-line bg-bg px-2 text-sm text-fg">
                <option value="volume">{t.mountVolume}</option><option value="bind">{t.mountBind}</option><option value="file">{t.mountFile}</option>
              </select>
            </label>
            <Field label={t.mountTarget} name={`mount-target-${i}`} value={m.target} onChange={(e) => update(i, { target: e.target.value })} placeholder="/app/data" dir="ltr" required />
            {m.type !== 'file' && <Field label={m.type === 'bind' ? t.mountHostPath : t.mountVolumeName} name={`mount-source-${i}`} value={m.source} onChange={(e) => update(i, { source: e.target.value })} placeholder={m.type === 'bind' ? '/srv/my-app/data' : 'my-app-data'} dir="ltr" required />}
          </div>
          {m.type === 'file' && <label className="block space-y-1.5 text-xs text-muted">{t.mountContent}
            <textarea aria-label={t.mountContent} value={m.content} onChange={(e) => update(i, { content: e.target.value })} dir="ltr" spellCheck={false} className="min-h-40 w-full resize-y border border-line bg-bg p-3 font-mono text-xs text-fg focus:border-accent focus:outline-none" />
          </label>}
          <div className="flex items-center justify-between gap-3">
            <label className="flex items-center gap-2 text-xs text-muted"><input type="checkbox" checked={m.read_only} onChange={(e) => update(i, { read_only: e.target.checked })} />{t.mountReadOnly}</label>
            <Button type="button" variant="ghost" onClick={() => { setMounts(mounts.filter((_, index) => index !== i)); setDirty(true) }}>{t.environmentRemove}</Button>
          </div>
        </div>)}
        <Button type="button" variant="ghost" disabled={mounts.length >= 50} onClick={() => { setMounts([...mounts, { type: 'volume', source: '', target: '', read_only: false, content: '' }]); setDirty(true) }}>{t.mountAdd}</Button>
      </fieldset>
      {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
      {save.isSuccess && !dirty && <p className="text-xs text-accent">{common.saved}</p>}
      <Button type="submit" disabled={!dirty || save.isPending}>{save.isPending ? common.saving : t.saveSettings}</Button>
    </form>
  </Panel>
}
