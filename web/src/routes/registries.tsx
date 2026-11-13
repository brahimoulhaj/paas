import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Button, ConfirmButton, Empty, ErrorNote, Field, PageHeader, Panel, QueryState } from '../components/ui'
import { api, registriesQuery, type Registry } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'
import { useT } from '../lib/i18n'

/** The Registry page: private image registries apps pull from — add (checked by signing
 * in), list, remove. */
export function RegistryPage() {
  const registries = useQuery(registriesQuery)
  const [adding, setAdding] = useState(false)
  const t = useT()
  const tr = t.registries
  return (
    <div className="space-y-6">
      <PageHeader
        title={tr.title}
        subtitle={tr.intro}
        actions={
          !adding && (
            <Button variant="ghost" onClick={() => setAdding(true)}>
              {tr.add}
            </Button>
          )
        }
      />
      <QueryState loading={registries.isPending} error={registries.error} what={tr.what} />
      {(adding || registries.data) && (
        <Panel>
          {adding && <AddRegistry onDone={() => setAdding(false)} />}
          {registries.data &&
            (registries.data.length === 0 ? (
              !adding && <Empty title={tr.empty}>{tr.emptyHint}</Empty>
            ) : (
              <ul className="divide-y divide-line">
                {registries.data.map((r) => (
                  <RegistryRow key={r.id} registry={r} />
                ))}
              </ul>
            ))}
        </Panel>
      )}
    </div>
  )
}

function AddRegistry({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient()
  const t = useT()
  const tr = t.registries
  const [form, setForm] = useState({ server: '', username: '', password: '', name: '' })
  const set = (key: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [key]: e.target.value })
  const add = useMutation({
    mutationFn: () => api<Registry>('/registries', { method: 'POST', body: form }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: registriesQuery.queryKey })
      onDone()
    },
  })
  function submit(e: FormEvent) {
    e.preventDefault()
    add.mutate()
  }
  return (
    <form onSubmit={submit} className="space-y-3 border-b border-line bg-raised p-4 last:border-b-0">
      <p className="text-sm">{tr.add}</p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={tr.server} name="registry-server" required dir="ltr" placeholder="ghcr.io" hintTooltip hint={tr.serverHint}
          value={form.server} onChange={set('server')} autoComplete="off" spellCheck={false} />
        <Field label={tr.name} name="registry-name" maxLength={64} value={form.name} onChange={set('name')} autoComplete="off" />
        <Field label={tr.username} name="registry-username" required dir="ltr" value={form.username} onChange={set('username')}
          autoComplete="off" spellCheck={false} />
        <Field label={tr.password} name="registry-password" type="password" required dir="ltr" hintTooltip hint={tr.passwordHint}
          value={form.password} onChange={set('password')} autoComplete="new-password" />
      </div>
      {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={add.isPending || !form.server.trim() || !form.username.trim() || !form.password}>
          {add.isPending ? tr.checking : tr.save}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t.common.cancel}
        </Button>
      </div>
    </form>
  )
}

function RegistryRow({ registry }: { registry: Registry }) {
  const queryClient = useQueryClient()
  const t = useT()
  const remove = useMutation({
    mutationFn: () => api<void>(`/registries/${registry.id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: registriesQuery.queryKey }),
  })
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className="min-w-0">
        <p className="truncate text-sm">{registry.name}</p>
        <p className="text-xs text-muted">
          <span dir="ltr">
            {registry.username}@{registry.server}
          </span>{' '}
          · <span title={dateTime(registry.created_at)}>{t.registries.added(timeAgo(registry.created_at))}</span>
        </p>
        {remove.error && <p className="text-xs text-danger">{remove.error.message}</p>}
      </div>
      <ConfirmButton label={t.common.remove} confirm={t.common.yesRemove} pending={remove.isPending} onConfirm={() => remove.mutate()} />
    </li>
  )
}
