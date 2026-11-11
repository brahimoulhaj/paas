import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { Button, ConfirmButton, Empty, ErrorNote, Field, HelpTip, Panel, QueryState } from '../components/ui'
import { api, tokensQuery, type ApiToken } from '../lib/api'
import { dateTime, timeAgo } from '../lib/format'
import { rich, useT } from '../lib/i18n'

/** API tokens: long-lived bearer credentials for scripts and CI. */
export function ApiTokens() {
  const queryClient = useQueryClient()
  const t = useT()
  const tokens = useQuery(tokensQuery)
  const [created, setCreated] = useState<ApiToken | null>(null)
  const closeSecret = () => {
    setCreated(null)
    queryClient.invalidateQueries({ queryKey: tokensQuery.queryKey }) // picks up "last used" if it was tried meanwhile
  }

  return (
    <Panel title={t.settingsTokens.title} actions={<HelpTip label={t.settingsTokens.title}>
      {rich(t.settingsTokens.intro, { header: () => <code dir="ltr">Authorization: Bearer &lt;token&gt;</code> })}
    </HelpTip>}>
      {created ? <NewSecret token={created} onDone={closeSecret} /> : <CreateToken onCreated={setCreated} />}
      {!tokens.data && (
        <div className="border-t border-line p-4">
          <QueryState loading={tokens.isPending} error={tokens.error} what={t.settingsTokens.what} />
        </div>
      )}
      {tokens.data &&
        (tokens.data.length === 0 ? (
          <div className="border-t border-line">
            <Empty title={t.settingsTokens.empty} />
          </div>
        ) : (
          <ul className="divide-y divide-line border-t border-line">
            {tokens.data.map((token) => (
              <TokenRow key={token.id} token={token} />
            ))}
          </ul>
        ))}
    </Panel>
  )
}

function CreateToken({ onCreated }: { onCreated: (t: ApiToken) => void }) {
  const queryClient = useQueryClient()
  const t = useT()
  const [name, setName] = useState('')
  const create = useMutation({
    mutationFn: () => api<ApiToken>('/tokens', { method: 'POST', body: { name } }),
    onSuccess: (token) => {
      setName('')
      queryClient.invalidateQueries({ queryKey: tokensQuery.queryKey })
      onCreated(token)
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    create.mutate()
  }

  return (
    <form onSubmit={submit} className="space-y-3 p-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end">
        <div className="flex-1">
          <Field
            label={t.common.name}
            name="token-name"
            placeholder="github-actions"
            maxLength={64}
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoComplete="off"
          />
        </div>
        <Button type="submit" disabled={!name.trim() || create.isPending}>
          {create.isPending ? t.common.creating : t.settingsTokens.createToken}
        </Button>
      </div>
      {create.error && <ErrorNote>{create.error.message}</ErrorNote>}
    </form>
  )
}

/** The secret of a token just created. It is shown here once and never again. */
function NewSecret({ token, onDone }: { token: ApiToken; onDone: () => void }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  const secret = token.token ?? ''

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret)
      setCopied(true)
    } catch {
      setCopied(false)
    }
  }

  return (
    <div className="space-y-3 border-b border-line bg-raised p-4">
      <p className="text-sm">
        {rich(t.settingsTokens.created, { name: () => <span className="text-accent">{token.name}</span> })}
      </p>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          readOnly
          value={secret}
          aria-label={t.settingsTokens.newTokenLabel}
          dir="ltr"
          onFocus={(e) => e.target.select()}
          className="block h-9 min-w-0 flex-1 border border-accent bg-bg px-3 text-sm text-fg focus:outline-none"
        />
        <Button type="button" variant="ghost" onClick={copy}>
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
      <pre dir="ltr" className="overflow-x-auto border border-line bg-bg p-3 text-xs text-muted">
        curl -H "Authorization: Bearer {secret}" {location.origin}/api/projects
      </pre>
      <Button type="button" onClick={onDone}>
        {t.common.done}
      </Button>
    </div>
  )
}

function TokenRow({ token }: { token: ApiToken }) {
  const queryClient = useQueryClient()
  const t = useT()
  const revoke = useMutation({
    mutationFn: () => api<void>(`/tokens/${token.id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: tokensQuery.queryKey }),
  })
  return (
    <li className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3">
      <div className="min-w-0">
        <p className="truncate text-sm">{token.name}</p>
        <p className="text-xs text-muted">
          <span title={dateTime(token.created_at)}>{t.settingsTokens.createdAgo(timeAgo(token.created_at))}</span>
          {' · '}
          <span title={dateTime(token.last_used_at)}>
            {token.last_used_at ? t.settingsTokens.lastUsed(timeAgo(token.last_used_at)) : t.settingsTokens.neverUsed}
          </span>
        </p>
        {revoke.error && <p className="text-xs text-danger">{revoke.error.message}</p>}
      </div>
      <ConfirmButton label={t.settingsTokens.revoke} confirm={t.settingsTokens.yesRevoke} pending={revoke.isPending} onConfirm={() => revoke.mutate()} />
    </li>
  )
}
