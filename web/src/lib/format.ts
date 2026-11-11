import type { App, Deployment } from './api'

/** "just now", "5 min ago", "3 h ago", "2 d ago", then a date. */
export function timeAgo(iso: string | null): string {
  if (!iso) return '—'
  const secs = Math.round((Date.now() - new Date(iso).getTime()) / 1000)
  if (secs < 45) return 'just now'
  if (secs < 3600) return `${Math.round(secs / 60)} min ago`
  if (secs < 86_400) return `${Math.round(secs / 3600)} h ago`
  if (secs < 7 * 86_400) return `${Math.round(secs / 86_400)} d ago`
  return new Date(iso).toLocaleDateString()
}

export function dateTime(iso: string | null): string {
  return iso ? new Date(iso).toLocaleString() : ''
}

/** Elapsed time between two timestamps, e.g. "42 s" or "3 min 5 s". */
export function duration(from: string | null, to: string | null): string {
  if (!from) return '—'
  const secs = Math.max(0, Math.round(((to ? new Date(to).getTime() : Date.now()) - new Date(from).getTime()) / 1000))
  return secs < 60 ? `${secs} s` : `${Math.floor(secs / 60)} min ${secs % 60} s`
}

/** "github.com/org/app" from "https://github.com/org/app.git". */
export function repoName(url: string): string {
  return url.replace(/^https:\/\//, '').replace(/\.git$/, '').replace(/\/$/, '')
}

/** Where an app comes from, in one line: its image, or its repository and branch. */
export function appSource(app: App): string {
  return app.source_type === 'git' ? `${repoName(app.git_repository)} · ${app.git_branch}` : app.image
}

/** What a deployment ran: the commit for a Git app, the image otherwise. */
export function deploymentVersion(d: Deployment): string {
  if (d.commit_sha) return `commit ${d.commit_sha.slice(0, 7)}`
  return d.image || '—'
}

export type Tone = 'accent' | 'muted' | 'warn' | 'danger'

/** One word for what an app is doing, from its settings, its container and any deploy in flight. */
export function appStatus(app: App, deploying: boolean): { label: string; tone: Tone } {
  if (deploying) return { label: 'deploying', tone: 'warn' }
  if (!app.current_deployment_id) return { label: 'not deployed', tone: 'muted' }
  if (app.desired_state === 'stopped') return { label: 'stopped', tone: 'muted' }
  switch (app.runtime_state) {
    case undefined: // list responses carry no container state
    case 'running':
      return { label: 'running', tone: 'accent' }
    case 'restarting':
      return { label: 'crashing', tone: 'danger' }
    case 'missing':
      return { label: 'container missing', tone: 'danger' }
    case 'unknown':
      return { label: 'unknown', tone: 'warn' }
    default: // exited, dead, paused, created: should be running and is not
      return { label: 'down', tone: 'danger' }
  }
}

const actions: Record<string, string> = {
  'project.create': 'created project',
  'project.update': 'updated project',
  'project.delete': 'deleted project',
  'app.create': 'created app',
  'app.update': 'changed settings of',
  'app.delete': 'deleted app',
  'app.deploy': 'started a deploy of',
  'app.start': 'started',
  'app.stop': 'stopped',
  'app.restart': 'restarted',
  'domain.add': 'added a domain to',
  'domain.remove': 'removed a domain from',
  'domain.tls': 'changed how a domain is served for',
  'deployment.succeeded': 'deployed',
  'deployment.failed': 'failed to deploy',
  'settings.panel': 'changed the panel domain',
  'panel.update': 'started a panel update to',
  'panel.updated': 'updated the panel to',
  'panel.update_failed': 'could not update the panel to',
  'settings.updates': 'changed the update settings',
  'git_source.add': 'connected the Git host',
  'git_source.remove': 'removed the Git connection',
  'token.create': 'created the API token',
  'token.revoke': 'revoked the API token',
}

export const actionLabel = (action: string) => actions[action] ?? action
