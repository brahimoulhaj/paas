import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Badge, Button, ConfirmButton, Empty, ErrorNote, Field, Panel, QueryState } from '../components/ui'
import { api, gitSourcesQuery, type GitHubManifest, type GitSource } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'

/** Git connections: a GitHub App or a GitLab token, for private repositories. */
export function GitConnections() {
  const sources = useQuery(gitSourcesQuery)
  const [adding, setAdding] = useState<'github' | 'gitlab' | null>(null)
  // GitHub sends the browser back here; a failed connection arrives with its reason.
  const [callbackError] = useState(() => new URLSearchParams(location.search).get('git_error'))

  return (
    <Panel
      title="Git connections"
      actions={
        !adding && (
          <span className="flex gap-2">
            <Button variant="ghost" className="h-7 px-2 text-xs" onClick={() => setAdding('github')}>
              Connect GitHub
            </Button>
            <Button variant="ghost" className="h-7 px-2 text-xs" onClick={() => setAdding('gitlab')}>
              Connect GitLab
            </Button>
          </span>
        )
      }
    >
      <p className="border-b border-line p-4 text-xs text-muted">
        Lets apps be built from your private repositories. GitHub is connected through a GitHub App that you own and that
        can only read the repositories you pick. GitLab is connected with an access token.
      </p>
      {callbackError && (
        <div className="border-b border-line p-4">
          <ErrorNote>GitHub was not connected: {callbackError}</ErrorNote>
        </div>
      )}
      {adding === 'github' && <ConnectGitHub onCancel={() => setAdding(null)} />}
      {adding === 'gitlab' && <ConnectGitLab onDone={() => setAdding(null)} />}
      {!sources.data && (
        <div className="p-4">
          <QueryState loading={sources.isPending} error={sources.error} what="Git connections" />
        </div>
      )}
      {sources.data &&
        (sources.data.length === 0 ? (
          <Empty title="No Git connections yet.">Public repositories work without one.</Empty>
        ) : (
          <ul className="divide-y divide-line">
            {sources.data.map((s) => (
              <SourceRow key={s.id} source={s} />
            ))}
          </ul>
        ))}
    </Panel>
  )
}

/**
 * Creating the app happens on GitHub: the panel prepares a manifest, the browser posts
 * it there, and GitHub returns to the panel, which then sends the user on to choose
 * repositories.
 */
function ConnectGitHub({ onCancel }: { onCancel: () => void }) {
  const [organization, setOrganization] = useState('')
  const start = useMutation({
    mutationFn: () =>
      api<GitHubManifest>('/git-sources/github', { method: 'POST', body: { origin: location.origin, organization: organization.trim() } }),
    onSuccess: (m) => {
      const form = document.createElement('form')
      form.method = 'post'
      form.action = m.action_url
      const input = document.createElement('input')
      input.type = 'hidden'
      input.name = 'manifest'
      input.value = m.manifest
      form.appendChild(input)
      document.body.appendChild(form)
      form.submit()
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    start.mutate()
  }

  return (
    <form onSubmit={submit} className="space-y-3 border-b border-line bg-raised p-4">
      <p className="text-sm">Connect GitHub</p>
      <ol className="list-decimal space-y-1 pl-5 text-xs text-muted">
        <li>GitHub asks you to create a private GitHub App for this panel. Confirm its name.</li>
        <li>GitHub then asks which repositories the app may read. Pick them and install.</li>
        <li>You come back here, and the repositories are available when you create an app.</li>
      </ol>
      <div className="max-w-sm">
        <Field
          label="Organization (optional)"
          name="github-organization"
          placeholder="my-org"
          hint="Leave empty to create the app under your own account."
          value={organization}
          onChange={(e) => setOrganization(e.target.value)}
          autoComplete="off"
        />
      </div>
      {start.error && <ErrorNote>{start.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={start.isPending || start.isSuccess}>
          {start.isPending || start.isSuccess ? 'Opening GitHub…' : 'Continue on GitHub'}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </form>
  )
}

function ConnectGitLab({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient()
  const [baseUrl, setBaseUrl] = useState('https://gitlab.com')
  const [token, setToken] = useState('')
  const add = useMutation({
    mutationFn: () => api<GitSource>('/git-sources/gitlab', { method: 'POST', body: { base_url: baseUrl.trim(), token: token.trim() } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: gitSourcesQuery.queryKey })
      onDone()
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    add.mutate()
  }

  return (
    <form onSubmit={submit} className="space-y-3 border-b border-line bg-raised p-4">
      <p className="text-sm">Connect GitLab</p>
      <p className="text-xs text-muted">
        Create an access token in GitLab (personal, group or project) with the scopes{' '}
        <code className="text-fg">read_api</code> and <code className="text-fg">read_repository</code>, and paste it here. It is
        stored encrypted and never shown again.
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          label="GitLab address"
          name="gitlab-url"
          type="url"
          required
          pattern="https://.+"
          title="An https URL"
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
        />
        <Field
          label="Access token"
          name="gitlab-token"
          type="password"
          required
          placeholder="glpat-…"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          autoComplete="off"
        />
      </div>
      {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={!token.trim() || add.isPending}>
          {add.isPending ? 'Checking the token…' : 'Connect'}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </form>
  )
}

function SourceRow({ source }: { source: GitSource }) {
  const queryClient = useQueryClient()
  const github = source.type === 'github_app'
  const remove = useMutation({
    mutationFn: () => api<void>(`/git-sources/${source.id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: gitSourcesQuery.queryKey }),
  })
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className="min-w-0">
        <p className="flex items-center gap-2 truncate text-sm">
          {source.name} <Badge tone="muted">{github ? 'GitHub App' : 'GitLab'}</Badge>
        </p>
        <p className="text-xs text-muted">
          {source.base_url.replace(/^https:\/\//, '')} · <span title={dateTime(source.created_at)}>connected {timeAgo(source.created_at)}</span>
        </p>
        {github && (
          <p className="mt-1 flex flex-wrap gap-x-4 text-xs">
            {source.install_url && (
              <a href={source.install_url} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                Choose repositories on GitHub
              </a>
            )}
            {source.app_url && (
              <a href={source.app_url} target="_blank" rel="noreferrer" className="text-muted hover:text-fg hover:underline">
                App page
              </a>
            )}
          </p>
        )}
        {remove.error && <p className="text-xs text-danger">{remove.error.message}</p>}
        {github && remove.isIdle && (
          <p className="mt-1 text-xs text-muted">Removing it here does not delete the app on GitHub; do that from its app page.</p>
        )}
      </div>
      <ConfirmButton label="Remove" confirm="Yes, remove" pending={remove.isPending} onConfirm={() => remove.mutate()} />
    </li>
  )
}
