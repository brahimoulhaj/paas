import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Badge, Button, ConfirmButton, Empty, ErrorNote, Field, HelpTip, Panel, QueryState } from '../components/ui'
import { api, gitSourcesQuery, type GitHubManifest, type GitSource } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'
import { rich, useT } from '../lib/i18n'

/** Git connections: a GitHub App or a GitLab token, for private repositories. */
export function GitConnections() {
  const sources = useQuery(gitSourcesQuery)
  const [adding, setAdding] = useState<'github' | 'gitlab' | 'deploy_key' | null>(null)
  // GitHub sends the browser back here; a failed connection arrives with its reason.
  const [callbackError] = useState(() => new URLSearchParams(location.search).get('git_error'))
  const t = useT()

  return (
    <Panel
      title={<span className="inline-flex items-center gap-2">{t.settingsGit.title}<HelpTip label={t.settingsGit.title}>{t.settingsGit.intro}</HelpTip></span>}
      actions={
        !adding && (
          <span className="flex gap-2">
            <Button variant="ghost" className="h-7 px-2 text-xs" onClick={() => setAdding('github')}>
              {t.settingsGit.connectGitHub}
            </Button>
            <Button variant="ghost" className="h-7 px-2 text-xs" onClick={() => setAdding('gitlab')}>
              {t.settingsGit.connectGitLab}
            </Button>
            <Button variant="ghost" className="h-7 px-2 text-xs" onClick={() => setAdding('deploy_key')}>
              {t.settingsGit.addDeployKey}
            </Button>
          </span>
        )
      }
    >
      {callbackError && (
        <div className="border-b border-line p-4">
          <ErrorNote>{t.settingsGit.notConnected(callbackError)}</ErrorNote>
        </div>
      )}
      {adding === 'github' && <ConnectGitHub onCancel={() => setAdding(null)} />}
      {adding === 'gitlab' && <ConnectGitLab onDone={() => setAdding(null)} />}
      {adding === 'deploy_key' && <AddDeployKey onDone={() => setAdding(null)} />}
      {!sources.data && (
        <div className="p-4">
          <QueryState loading={sources.isPending} error={sources.error} what={t.settingsGit.what} />
        </div>
      )}
      {sources.data &&
        (sources.data.length === 0 ? (
          <Empty title={t.settingsGit.empty}>{t.settingsGit.emptyHint}</Empty>
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
  const t = useT()
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
      <p className="text-sm">{t.settingsGit.connectGitHub}</p>
      <ol className="list-decimal space-y-1 ps-5 text-xs text-muted">
        <li>{t.settingsGit.step1}</li>
        <li>{t.settingsGit.step2}</li>
        <li>{t.settingsGit.step3}</li>
      </ol>
      <div className="max-w-sm">
        <Field
          label={t.settingsGit.organization}
          name="github-organization"
          placeholder="my-org"
          dir="ltr"
          hint={t.settingsGit.organizationHint}
          value={organization}
          onChange={(e) => setOrganization(e.target.value)}
          autoComplete="off"
        />
      </div>
      {start.error && <ErrorNote>{start.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={start.isPending || start.isSuccess}>
          {start.isPending || start.isSuccess ? t.settingsGit.openingGitHub : t.settingsGit.continueOnGitHub}
        </Button>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t.common.cancel}
        </Button>
      </div>
    </form>
  )
}

function ConnectGitLab({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient()
  const t = useT()
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
      <p className="text-sm">{t.settingsGit.connectGitLab}</p>
      <p className="text-xs text-muted">
        {rich(t.settingsGit.gitlabIntro, {
          c: (scope) => (
            <code className="text-fg" dir="ltr">
              {scope}
            </code>
          ),
        })}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          label={t.settingsGit.gitlabAddress}
          name="gitlab-url"
          type="url"
          required
          pattern="https://.+"
          title={t.settingsGit.httpsUrl}
          dir="ltr"
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
        />
        <Field
          label={t.settingsGit.accessToken}
          name="gitlab-token"
          type="password"
          required
          placeholder="glpat-…"
          dir="ltr"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          autoComplete="off"
        />
      </div>
      {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={!token.trim() || add.isPending}>
          {add.isPending ? t.settingsGit.checkingToken : t.settingsGit.connect}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t.common.cancel}
        </Button>
      </div>
    </form>
  )
}

/** A new deploy key: only a name; the key pair is made by the panel. */
function AddDeployKey({ onDone }: { onDone: () => void }) {
  const queryClient = useQueryClient()
  const t = useT()
  const [name, setName] = useState('')
  const add = useMutation({
    mutationFn: () => api<GitSource>('/git-sources/deploy-key', { method: 'POST', body: { name } }),
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
      <p className="text-sm">{t.settingsGit.addDeployKey}</p>
      <p className="text-xs text-muted">{t.settingsGit.deployKeyIntro}</p>
      <div className="max-w-sm">
        <Field
          label={t.settingsGit.deployKeyName}
          name="deploy-key-name"
          placeholder="shop-api"
          maxLength={64}
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
        />
      </div>
      {add.error && <ErrorNote>{add.error.message}</ErrorNote>}
      <div className="flex gap-2">
        <Button type="submit" disabled={add.isPending}>
          {add.isPending ? t.settingsGit.creating : t.settingsGit.create}
        </Button>
        <Button type="button" variant="ghost" onClick={onDone}>
          {t.common.cancel}
        </Button>
      </div>
    </form>
  )
}

/** A deploy key's public half, to copy into a repository's settings. */
function PublicKey({ value }: { value: string }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }
  return (
    <div className="mt-2 space-y-1.5">
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          readOnly
          value={value}
          aria-label={t.settingsGit.publicKey}
          dir="ltr"
          onFocus={(e) => e.target.select()}
          className="block h-8 min-w-0 flex-1 border border-line bg-bg px-2 font-mono text-xs text-fg focus:border-accent focus:outline-none"
        />
        <Button type="button" variant="ghost" className="h-8 px-3 text-xs" onClick={copy}>
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
      <p className="text-xs text-muted">{t.settingsGit.deployKeyWhere}</p>
    </div>
  )
}

function SourceRow({ source }: { source: GitSource }) {
  const queryClient = useQueryClient()
  const t = useT()
  const github = source.type === 'github_app'
  const deployKey = source.type === 'deploy_key'
  const remove = useMutation({
    mutationFn: () => api<void>(`/git-sources/${source.id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: gitSourcesQuery.queryKey }),
  })
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className={deployKey ? 'min-w-0 flex-1' : 'min-w-0'}>
        <p className="flex items-center gap-2 truncate text-sm">
          {source.name} <Badge tone="muted">{github ? 'GitHub App' : deployKey ? t.settingsGit.deployKeyBadge : 'GitLab'}</Badge>
        </p>
        <p className="text-xs text-muted">
          {deployKey ? t.settingsGit.anyHost : <span dir="ltr">{source.base_url.replace(/^https:\/\//, '')}</span>} ·{' '}
          <span title={dateTime(source.created_at)}>{t.settingsGit.connected(timeAgo(source.created_at))}</span>
        </p>
        {github && (
          <p className="mt-1 flex flex-wrap gap-x-4 text-xs">
            {source.install_url && (
              <a href={source.install_url} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                {t.settingsGit.chooseRepos}
              </a>
            )}
            {source.app_url && (
              <a href={source.app_url} target="_blank" rel="noreferrer" className="text-muted hover:text-fg hover:underline">
                {t.settingsGit.appPage}
              </a>
            )}
          </p>
        )}
        {deployKey && source.public_key && <PublicKey value={source.public_key} />}
        {remove.error && <p className="text-xs text-danger">{remove.error.message}</p>}
        {github && remove.isIdle && (
          <p className="mt-1 text-xs text-muted">{t.settingsGit.removeNote}</p>
        )}
      </div>
      <ConfirmButton label={t.common.remove} confirm={t.common.yesRemove} pending={remove.isPending} onConfirm={() => remove.mutate()} />
    </li>
  )
}
