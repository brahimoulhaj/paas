import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { dockerMetricsQuery, type DockerUsage } from '../lib/api'
import { formatBytes as bytes, timeAgo } from '../lib/format'
import { locale, useT } from '../lib/i18n'
import { Badge, cx, HelpTip, Panel } from './ui'

const count = (n: number | null | undefined) => n == null || n < 0 ? '—' : new Intl.NumberFormat(locale()).format(n)

function Tile({ title, value, children }: { title: string; value: ReactNode; children: ReactNode }) {
  return <div className="min-w-0 space-y-2.5 bg-surface px-4 py-3">
    <h3 className="text-xs text-muted">{title}</h3>
    <p className="font-mono text-2xl leading-none tabular-nums" dir="ltr">{value}</p>
    <div className="text-xs text-muted">{children}</div>
  </div>
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return <div className="min-w-0"><dt className="text-muted">{label}</dt><dd className="mt-1 break-words text-fg" dir="ltr">{children || '—'}</dd></div>
}

export function DockerMetrics() {
  const t = useT().dockerMetrics
  const query = useQuery(dockerMetricsQuery)
  const m = query.data
  const engine = m?.engine
  const storage = m?.storage
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 15_000)
    return () => window.clearInterval(timer)
  }, [])
  const stale = query.isError || !!m && now - Date.parse(m.checked_at) > 45_000
  const rows: [string, DockerUsage | undefined][] = [
    [t.containers, storage?.containers], [t.images, storage?.images], [t.volumes, storage?.volumes], [t.buildCache, storage?.build_cache],
  ]

  return <Panel title={t.title} actions={<span className="flex items-center gap-2 text-xs text-muted">
    {m && <><span>{stale ? t.stale : t.live}</span><Badge tone={!m.online || stale ? 'warn' : 'accent'}>{!m.online ? t.offline : t.online}</Badge></>}
    <HelpTip label={t.title}>{t.help}</HelpTip>
  </span>}>
    {!m && <p role="status" className={cx('px-4 py-5 text-sm', query.isError ? 'text-danger' : 'text-muted')}>{query.isError ? t.failed : t.loading}</p>}
    {m && !m.online && <p role="status" className="border-b border-line px-4 py-3 text-xs text-warn">{t.disconnected} {engine ? t.lastKnown : ''}</p>}
    {m && engine && <>
      <div className="grid gap-px bg-line sm:grid-cols-2 lg:grid-cols-4">
        <Tile title={t.containers} value={count(m.containers?.total)}>
          <span className="text-accent">{count(m.containers?.running)} {t.running.toLocaleLowerCase(locale())}</span>
          <span> · {count(m.containers?.stopped)} {t.stopped.toLocaleLowerCase(locale())}</span>
          {!!m.containers?.paused && <span> · {count(m.containers.paused)} {t.paused.toLocaleLowerCase(locale())}</span>}
        </Tile>
        <Tile title={t.images} value={count(m.images)}>
          {t.used(bytes(storage?.images.size))}
        </Tile>
        <Tile title={t.volumes} value={count(storage?.volumes.count)}>
          {t.used(bytes(storage?.volumes.size))}
          {storage && storage.volumes.active >= 0 && <span> · {t.active(storage.volumes.active)}</span>}
        </Tile>
        <Tile title={t.buildCache} value={<span className="text-xl">{bytes(storage?.build_cache.size)}</span>}>
          {t.reclaimable(bytes(storage?.build_cache.reclaimable))}
        </Tile>
      </div>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-line px-4 py-3 text-xs sm:grid-cols-4">
        <Fact label={t.version}>{engine.version}</Fact>
        <Fact label={t.storageDriver}>{engine.storage_driver}</Fact>
        <Fact label={t.networks}>{count(m.networks)}</Fact>
        <Fact label={t.capacity}>{t.cpus(engine.cpus)} · {bytes(engine.memory)}</Fact>
      </dl>
      <details className="border-t border-line text-xs">
        <summary className="cursor-pointer px-4 py-2.5 text-muted hover:text-fg">{t.details}</summary>
        <div className="overflow-x-auto">
          <table className="w-full whitespace-nowrap text-start tabular-nums">
            <thead className="text-muted"><tr>{[t.category,t.count,t.size,t.reclaimableColumn].map((label) => <th key={label} className="px-4 py-2 text-start font-normal">{label}</th>)}</tr></thead>
            <tbody>{rows.map(([label, usage]) => <tr key={label} className="border-t border-line">
              <th scope="row" className="px-4 py-2 text-start font-normal">{label}</th>
              <td className="px-4 py-2" dir="ltr">{count(usage?.count)} / {count(usage?.active)}</td>
              <td className="px-4 py-2" dir="ltr">{bytes(usage?.size)}</td>
              <td className="px-4 py-2" dir="ltr">{bytes(usage?.reclaimable)}</td>
            </tr>)}</tbody>
          </table>
        </div>
        <dl className="grid grid-cols-1 gap-x-6 gap-y-3 border-t border-line px-4 py-3 sm:grid-cols-2 lg:grid-cols-3">
          <Fact label={t.name}>{engine.name}</Fact><Fact label={t.api}>{engine.api_version}</Fact>
          <Fact label={t.os}>{[engine.os,engine.architecture].filter(Boolean).join(' · ')}</Fact>
          <Fact label={t.kernel}>{engine.kernel}</Fact><Fact label={t.runtime}>{engine.runtime}</Fact>
          <Fact label={t.logging}>{engine.logging_driver}</Fact>
          <Fact label={t.cgroup}>{[engine.cgroup_driver,engine.cgroup_version].filter(Boolean).join(' · ')}</Fact>
          <Fact label={t.liveRestore}>{engine.live_restore ? t.enabled : t.disabled}</Fact>
          <Fact label={t.rootDir}>{engine.root_dir}</Fact>
        </dl>
      </details>
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-2 text-[11px] text-muted">
        <span>{t.storageRefresh}</span><span>{m.storage_at ? timeAgo(m.storage_at) : '—'}</span>
      </div>
      {m.online && m.unavailable.length > 0 && <p className="border-t border-line px-4 py-2 text-xs text-warn">{m.unavailable.includes('storage') ? t.storageUnavailable : t.partial}</p>}
    </>}
  </Panel>
}
