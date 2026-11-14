import type { App, Deployment } from './api'
import { locale, tr } from './i18n'

/** IEC byte units shared by host and Docker metrics. */
export function formatBytes(value: number | null | undefined, rate = false): string {
  if (value == null || value < 0) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  const index = value > 0 ? Math.min(5, Math.max(0, Math.floor(Math.log(value) / Math.log(1024)))) : 0
  const number = new Intl.NumberFormat(locale(), { maximumFractionDigits: index ? 1 : 0 }).format(value / 1024 ** index)
  return `${number} ${units[index]}${rate ? '/s' : ''}`
}

/** "just now", "5 min ago", "3 h ago", "2 d ago", then a date. */
export function timeAgo(iso: string | null): string {
  if (!iso) return '—'
  const secs = Math.round((Date.now() - new Date(iso).getTime()) / 1000)
  const t = tr().format
  if (secs < 45) return t.justNow
  if (secs < 3600) return t.minAgo(Math.round(secs / 60))
  if (secs < 86_400) return t.hoursAgo(Math.round(secs / 3600))
  if (secs < 7 * 86_400) return t.daysAgo(Math.round(secs / 86_400))
  return new Date(iso).toLocaleDateString(locale())
}

export function dateTime(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString(locale()) : ''
}

/** Elapsed time between two timestamps, e.g. "42 s" or "3 min 5 s". */
export function duration(from: string | null, to: string | null): string {
  if (!from) return '—'
  const secs = Math.max(0, Math.round(((to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()) / 1000))
  const t = tr().format
  return secs < 60 ? t.seconds(secs) : t.minutesSeconds(Math.floor(secs / 60), secs % 60)
}

/** "github.com/org/app" from "https://github.com/org/app.git". */
export function repoName(url: string): string {
  return url
    .replace(/^https:\/\//, '')
    .replace(/^ssh:\/\/[^@/]+@/, '') // ssh://git@host:2222/org/app.git → host:2222/org/app
    .replace(/^[^@/:]+@([^:/]+):/, '$1/') // git@host:org/app.git → host/org/app
    .replace(/\.git$/, '')
    .replace(/\/$/, '')
}

/** Where an app comes from, in one line: its image, or its repository and branch. */
export function appSource(app: App): string {
  return app.source_type === 'git' ? `${repoName(app.git_repository)} · ${app.git_branch}` : app.image
}

/** What a deployment ran: the commit for a Git app, the image otherwise. */
/** The first line of a commit message, cut to max characters. */
export function commitSubject(message: string, max = 72): string {
  const line = message.split('\n', 1)[0].trim()
  return line.length > max ? line.slice(0, max - 1).trimEnd() + '…' : line
}

export function deploymentVersion(d: Deployment): string {
  if (d.commit_sha) return tr().format.commit(d.commit_sha.slice(0, 7))
  return d.image || '—'
}

export type Tone = 'accent' | 'muted' | 'warn' | 'danger'

/** One word for what an app is doing, from its settings, its container and any deploy in flight. */
export function appStatus(app: App, deploying: boolean): { label: string; tone: Tone } {
  const t = tr().format.status
  if (deploying) return { label: t.deploying, tone: 'warn' }
  if (!app.current_deployment_id) return { label: t.notDeployed, tone: 'muted' }
  if (app.desired_state === 'stopped') return { label: t.stopped, tone: 'muted' }
  switch (app.runtime_state) {
    case undefined: // list responses carry no container state
    case 'running':
      return { label: t.running, tone: 'accent' }
    case 'restarting':
      return { label: t.crashing, tone: 'danger' }
    case 'missing':
      return { label: t.containerMissing, tone: 'danger' }
    case 'unknown':
      return { label: t.unknown, tone: 'warn' }
    default: // exited, dead, paused, created: should be running and is not
      return { label: t.down, tone: 'danger' }
  }
}

/** What an activity event did, in words; the target's name follows it. */
export const actionLabel = (action: string) => (tr().format.actions as Record<string, string>)[action] ?? action
