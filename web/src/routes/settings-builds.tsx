import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'
import { Button, ErrorNote, Field, Panel, QueryState } from '../components/ui'
import { api, buildSettingsQuery, type BuildLimits, type BuildSettings } from '../lib/api'
import { useT } from '../lib/i18n'

/** CPU and memory caps shared by Git builds. */
export function BuildSettingsSection() {
  const settings = useQuery(buildSettingsQuery)
  const t = useT()
  return (
    <Panel title={t.settingsBuilds.title}>
      {!settings.data ? (
        <div className="p-4">
          <QueryState loading={settings.isPending} error={settings.error} what={t.settingsBuilds.what} />
        </div>
      ) : (
        <BuildSettingsForm current={settings.data} />
      )}
    </Panel>
  )
}

function BuildSettingsForm({ current }: { current: BuildSettings }) {
  const queryClient = useQueryClient()
  const t = useT()
  const tb = t.settingsBuilds
  const [cpus, setCpus] = useState(String(current.cpus))
  const [memory, setMemory] = useState(String(current.memory_mb))
  const [notice, setNotice] = useState('')
  useEffect(() => {
    setCpus(String(current.cpus))
    setMemory(String(current.memory_mb))
  }, [current.cpus, current.memory_mb])

  const describe = (l: BuildLimits) =>
    [l.cpus > 0 ? tb.cpuValue(l.cpus) : tb.cpuNoCap, l.memory_mb > 0 ? tb.memoryValue(l.memory_mb) : tb.memoryNoCap] as const

  const onSuccess = (s: BuildSettings) => {
    queryClient.setQueryData(buildSettingsQuery.queryKey, s)
    setNotice(tb.saved)
  }
  const save = useMutation({
    mutationFn: (body: BuildLimits) => api<BuildSettings>('/settings/builds', { method: 'PUT', body }),
    onMutate: () => setNotice(''),
    onSuccess,
  })
  const reset = useMutation({
    mutationFn: () => api<BuildSettings>('/settings/builds', { method: 'DELETE' }),
    onMutate: () => setNotice(''),
    onSuccess,
  })

  const input: BuildLimits = { cpus: Number(cpus), memory_mb: Number(memory) }
  const dirty = input.cpus !== current.cpus || input.memory_mb !== current.memory_mb
  const atDefaults = current.cpus === current.defaults.cpus && current.memory_mb === current.defaults.memory_mb
  const error = save.error ?? reset.error

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate(input)
  }

  return (
    <form onSubmit={submit} className="space-y-4 p-4">
      <p className="text-sm text-muted">{tb.intro}</p>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          label={tb.cpus}
          name="build-cpus"
          dir="ltr"
          type="number"
          min={0}
          max={current.host_cpus}
          step="0.1"
          hintTooltip
          hint={tb.cpusHint(current.host_cpus)}
          value={cpus}
          onChange={(e) => setCpus(e.target.value)}
        />
        <Field
          label={tb.memory}
          name="build-memory"
          dir="ltr"
          type="number"
          min={0}
          step="1"
          hintTooltip
          hint={tb.memoryHint}
          value={memory}
          onChange={(e) => setMemory(e.target.value)}
        />
      </div>
      <div className="space-y-1 text-xs text-muted">
        <p>{tb.current(...describe(current))}</p>
        <p>{tb.defaults(...describe(current.defaults))}</p>
        <p>{tb.applies}</p>
      </div>
      {error && <ErrorNote>{error.message}</ErrorNote>}
      {notice && <p className="text-xs text-accent">{notice}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" disabled={!dirty || save.isPending}>
          {save.isPending ? t.common.saving : t.common.save}
        </Button>
        {!atDefaults && (
          <Button type="button" variant="ghost" disabled={reset.isPending} onClick={() => reset.mutate()}>
            {tb.reset}
          </Button>
        )}
      </div>
    </form>
  )
}
