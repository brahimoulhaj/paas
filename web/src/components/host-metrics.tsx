import { useQuery } from '@tanstack/react-query'
import { useEffect, useState, type ReactNode } from 'react'
import { hostMetricsQuery, type HostMetrics as Metrics } from '../lib/api'
import { locale, useT } from '../lib/i18n'
import { formatBytes as bytes } from '../lib/format'
import { cx, HelpTip, Panel } from './ui'

const percent = (value: number | null | undefined) => value == null ? '—' : `${new Intl.NumberFormat(locale(), { maximumFractionDigits: 1 }).format(value)}%`

function Meter({ value, label }: { value: number | null | undefined; label: string }) {
  return <div role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={value ?? undefined} aria-valuetext={value == null ? '—' : percent(value)} className="h-1 overflow-hidden bg-line">
    <div className={cx('h-full transition-[width] duration-500 motion-reduce:transition-none', value != null && value >= 90 ? 'bg-danger' : value != null && value >= 75 ? 'bg-warn' : 'bg-accent')} style={{ width: `${Math.max(0, Math.min(100, value ?? 0))}%` }} />
  </div>
}

function Sparkline({ history, field, label, second }: { history: Metrics['history']; field: 'cpu' | 'memory' | 'receive_rate'; label: string; second?: 'send_rate' }) {
  const series = history.map((p) => p[field])
  const peak = field === 'receive_rate' ? Math.max(1, ...series.map((n) => n ?? 0), ...history.map((p) => p.send_rate ?? 0)) : 100
  const newest = history.length ? Date.parse(history[history.length - 1].at) : 0
  const path = (key: typeof field | 'send_rate') => {
    let drawing = false
    return history.map((p) => {
      const value = p[key]
      if (value == null) { drawing = false; return '' }
      const x = Math.max(0, 200 - (newest - Date.parse(p.at)) / 600_000 * 200)
      const y = 29 - Math.min(1, Math.max(0, value / peak)) * 26
      const instruction = `${drawing ? 'L' : 'M'}${x.toFixed(1)},${y.toFixed(1)}`
      drawing = true
      return instruction
    }).join(' ')
  }
  return <svg viewBox="0 0 200 32" preserveAspectRatio="none" role="img" aria-label={label} className="h-8 w-full overflow-visible">
    <path d="M0,29 H200" className="stroke-line" fill="none" />
    {second && <path d={path(second)} className="stroke-muted" strokeWidth="1.5" fill="none" vectorEffect="non-scaling-stroke" />}
    <path d={path(field)} className="stroke-accent" strokeWidth="1.5" fill="none" vectorEffect="non-scaling-stroke" />
  </svg>
}

function Metric({ label, value, children }: { label: string; value: ReactNode; children: ReactNode }) {
  return <div className="min-w-0 space-y-3 bg-surface px-4 py-4">
    <h3 className="text-xs text-muted">{label}</h3>
    <p className="font-mono text-2xl leading-none tabular-nums" dir="ltr">{value}</p>
    {children}
  </div>
}

export function HostMetrics() {
  const t = useT().metrics
  const query = useQuery(hostMetricsQuery)
  const m = query.data
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 5_000)
    return () => window.clearInterval(timer)
  }, [])
  const stale = query.isError || !!m && now - Date.parse(m.collected_at) > 20_000
  const duration = (seconds: number | null) => {
    if (seconds == null) return '—'
    const days = Math.floor(seconds / 86400), hours = Math.floor(seconds % 86400 / 3600), minutes = Math.floor(seconds % 3600 / 60)
    return [days ? t.days(days) : '', t.hours(hours), t.minutes(minutes)].filter(Boolean).join(' ')
  }
  const history = m?.history ?? []
  const cpuHint = m?.cpu.percent == null ? m?.unavailable.includes('cpu') ? t.unavailable : t.sampling : t.cores(m.cpu.cores)

  return <Panel title={t.title} actions={<span className="flex items-center gap-2 text-xs text-muted">
    {m && <><span className={cx('size-1.5 rounded-full', stale ? 'bg-warn' : 'bg-accent')} />{stale ? t.stale : t.live}</>}
    <HelpTip label={t.title}>{t.help}</HelpTip>
  </span>}>
    {!m && <p className={cx('px-4 py-6 text-sm', query.isError ? 'text-danger' : 'text-muted')} role="status">{query.isError ? t.failed : t.loading}</p>}
    {m && <>
      <div className="grid gap-px bg-line sm:grid-cols-2 lg:grid-cols-4">
        <Metric label={t.cpu} value={percent(m.cpu.percent)}>
          <Meter label={t.cpu} value={m.cpu.percent} />
          <Sparkline history={history} field="cpu" label={`${t.cpu} · ${t.history}`} />
          <p className="truncate text-xs text-muted" title={m.cpu.model}>{cpuHint}</p>
        </Metric>
        <Metric label={t.ram} value={percent(m.memory?.percent)}>
          <Meter label={t.ram} value={m.memory?.percent} />
          <Sparkline history={history} field="memory" label={`${t.ram} · ${t.history}`} />
          <p className="text-xs text-muted" dir="ltr">{m.memory ? t.usedOf(bytes(m.memory.used), bytes(m.memory.total)) : t.unavailable}</p>
        </Metric>
        <Metric label={t.disk} value={percent(m.disk?.percent)}>
          <Meter label={t.disk} value={m.disk?.percent} />
          <div className="flex h-8 items-center text-xs text-fg">{m.disk ? t.available(bytes(m.disk.available)) : t.unavailable}</div>
          <p className="text-xs text-muted" dir="ltr" title={m.disk?.path}>{m.disk ? t.usedOf(bytes(m.disk.used), bytes(m.disk.total)) : '—'}</p>
        </Metric>
        <Metric label={t.network} value={<span className="text-xl"><span className="text-accent" aria-hidden="true">↓ </span>{bytes(m.network?.receive_rate, true)}</span>}>
          <p className="text-xs text-muted" aria-label={t.send} dir="ltr">↑ {bytes(m.network?.send_rate, true)}</p>
          <Sparkline history={history} field="receive_rate" second="send_rate" label={`${t.network} · ${t.history}`} />
          <p className="text-xs text-muted">{m.network ? m.network.receive_rate == null ? t.sampling : t.history : t.unavailable}</p>
        </Metric>
      </div>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-line px-4 py-3 text-xs sm:grid-cols-4">
        <div><dt className="text-muted">{t.uptime}</dt><dd className="mt-1 tabular-nums">{duration(m.uptime_seconds)}</dd></div>
        <div><dt className="text-muted">{t.load}</dt><dd className="mt-1 tabular-nums" dir="ltr">{m.cpu.load?.map((n) => new Intl.NumberFormat(locale(), {maximumFractionDigits: 2}).format(n)).join(' / ') ?? '—'}</dd></div>
        <div><dt className="text-muted">{t.swap}</dt><dd className={cx('mt-1 tabular-nums', m.swap && m.swap.percent >= 75 && 'text-warn')}>{m.swap ? m.swap.total ? t.usedOf(bytes(m.swap.used), bytes(m.swap.total)) : t.noSwap : '—'}</dd></div>
        <div><dt className="text-muted">{t.inodes}</dt><dd className="mt-1 tabular-nums">{percent(m.disk?.inodes_percent)}</dd></div>
      </dl>
      {m.network && m.network.interfaces.length > 0 && <details className="border-t border-line text-xs">
        <summary className="cursor-pointer px-4 py-2.5 text-muted hover:text-fg">{t.interfaces} <span className="ms-1 tabular-nums">({m.network.interfaces.length})</span></summary>
        <div className="overflow-x-auto"><table className="w-full whitespace-nowrap text-start tabular-nums">
          <thead className="text-muted"><tr>{[t.interface,t.receive,t.send,t.received,t.sent,t.errors].map((label) => <th key={label} className="px-4 py-2 text-start font-normal">{label}</th>)}</tr></thead>
          <tbody>{m.network.interfaces.map((nic) => <tr key={nic.name} className="border-t border-line" dir="ltr">
            <td className="px-4 py-2 font-mono">{nic.name}</td><td className="px-4 py-2">{bytes(nic.receive_rate, true)}</td><td className="px-4 py-2">{bytes(nic.send_rate, true)}</td><td className="px-4 py-2">{bytes(nic.received)}</td><td className="px-4 py-2">{bytes(nic.sent)}</td><td className={cx('px-4 py-2', (nic.errors > 0 || nic.drops > 0) && 'text-warn')}>{nic.errors} / {nic.drops}</td>
          </tr>)}</tbody>
        </table></div>
      </details>}
      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-line px-4 py-2 text-[11px] text-muted">
        <span className="min-w-0 truncate" dir="ltr">{[m.hostname,m.os,m.arch].filter(Boolean).join(' · ')}</span>
        <span>{t.history}</span>
      </div>
      {m.unavailable.length > 0 && <p className="border-t border-line px-4 py-2 text-xs text-warn">{m.unavailable.includes('host_network_mount') ? t.networkMount : t.partial}</p>}
    </>}
  </Panel>
}
