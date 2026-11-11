export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message)
  }
}

export async function api<T>(path: string, init?: { method?: string; body?: unknown }): Promise<T> {
  let res: Response
  try {
    res = await fetch('/api' + path, {
      method: init?.method ?? 'GET',
      credentials: 'same-origin',
      headers: init?.body === undefined ? undefined : { 'Content-Type': 'application/json' },
      body: init?.body === undefined ? undefined : JSON.stringify(init.body),
    })
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the server.')
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    throw new ApiError(
      res.status,
      data?.error?.code ?? 'error',
      data?.error?.message ?? `Request failed (${res.status}).`,
    )
  }
  return data as T
}

export type Meta = { name: string; slug: string; version: string; setup_required: boolean }
export type User = { id: string; email: string; role: string; created_at: string }

export const metaQuery = {
  queryKey: ['meta'] as const,
  queryFn: () => api<Meta>('/meta'),
  staleTime: Infinity,
}

// Resolves to null when signed out, so "no session" is data rather than an error.
export const meQuery = {
  queryKey: ['me'] as const,
  queryFn: async (): Promise<User | null> => {
    try {
      return await api<User>('/auth/me')
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return null
      throw e
    }
  },
  staleTime: 60_000,
}

export type Project = {
  id: string
  name: string
  description: string
  app_count: number
  created_at: string
  updated_at: string
}

export type App = {
  id: string
  project_id: string
  name: string
  source_type: string
  image: string
  port: number
  cpu_limit: number
  memory_limit_mb: number
  desired_state: 'running' | 'stopped'
  current_deployment_id: string
  /** What the container is doing right now; only on single-app responses. */
  runtime_state?: string
  created_at: string
  updated_at: string
}

export type Deployment = {
  id: string
  app_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  trigger: string
  image: string
  port: number
  container_name: string
  error: string
  started_at: string | null
  finished_at: string | null
  created_at: string
}

export type Domain = { id: string; app_id: string; hostname: string; tls_mode: string; created_at: string }

export type DnsHint = {
  hostname: string
  resolves: boolean
  addresses: string[]
  server_addresses: string[]
  /** null when the server cannot tell (no public address of its own, e.g. behind NAT). */
  points_here: boolean | null
}

export type ApiToken = {
  id: string
  name: string
  last_used_at: string | null
  created_at: string
  /** The secret; only in the response that creates the token. */
  token?: string
}

export type PanelSettings = { domain: string; acme_email: string; tls_mode?: 'auto' | 'http_only'; ip_access?: boolean }

export type PanelEvent = {
  id: string
  actor: string
  action: string
  target_type: string
  target_id: string
  target_name: string
  detail: string
  created_at: string
}

export type AppInput = { name: string; image: string; port: number; cpu_limit: number; memory_limit_mb: number }

export const isActive = (d: Deployment) => d.status === 'queued' || d.status === 'running'

export const projectsQuery = { queryKey: ['projects'] as const, queryFn: () => api<Project[]>('/projects') }

export const projectQuery = (id: string) => ({
  queryKey: ['projects', id] as const,
  queryFn: () => api<Project>(`/projects/${id}`),
})

export const appsQuery = (projectId: string) => ({
  queryKey: ['projects', projectId, 'apps'] as const,
  queryFn: () => api<App[]>(`/projects/${projectId}/apps`),
})

export const appQuery = (id: string) => ({
  queryKey: ['apps', id] as const,
  queryFn: () => api<App>(`/apps/${id}`),
})

export const deploymentsQuery = (appId: string) => ({
  queryKey: ['apps', appId, 'deployments'] as const,
  queryFn: () => api<Deployment[]>(`/apps/${appId}/deployments`),
})

export const domainsQuery = (appId: string) => ({
  queryKey: ['apps', appId, 'domains'] as const,
  queryFn: () => api<Domain[]>(`/apps/${appId}/domains`),
})

export const dnsQuery = (domainId: string) => ({
  queryKey: ['domains', domainId, 'dns'] as const,
  queryFn: () => api<DnsHint>(`/domains/${domainId}/dns`),
  staleTime: 30_000,
})

export const eventsQuery = (limit: number) => ({
  queryKey: ['events', limit] as const,
  queryFn: () => api<PanelEvent[]>(`/events?limit=${limit}`),
})

export const panelSettingsQuery = {
  queryKey: ['settings', 'panel'] as const,
  queryFn: () => api<PanelSettings>('/settings/panel'),
}

export const panelDnsQuery = (domain: string) => ({
  queryKey: ['settings', 'panel', 'dns', domain] as const,
  queryFn: () => api<DnsHint>('/settings/panel/dns'),
})

export const tokensQuery = { queryKey: ['tokens'] as const, queryFn: () => api<ApiToken[]>('/tokens') }

export const onboardingQuery = {
  queryKey: ['onboarding'] as const,
  queryFn: () => api<{ completed: boolean }>('/onboarding'),
  staleTime: Infinity,
}

export type UpdateResult = { from: string; to: string; ok: boolean; error?: string; finished_at: string }

export type Updates = {
  supported: boolean
  reason?: string
  version: string
  image?: string
  mode?: 'semver' | 'digest'
  available: boolean
  latest?: string
  latest_image?: string
  checked_at?: string
  error?: string
  status: 'idle' | 'checking' | 'pulling' | 'applying'
  last_result?: UpdateResult
  auto: boolean
}

export const updatesQuery = {
  queryKey: ['updates'] as const,
  queryFn: () => api<Updates>('/updates'),
  refetchInterval: 5 * 60_000,
}
