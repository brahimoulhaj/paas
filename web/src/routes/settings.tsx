import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'
import { DnsHintText } from '../components/dns'
import { Link } from '@tanstack/react-router'
import { ApiTokens } from './settings-tokens'
import { PanelUpdates } from './settings-updates'
import { Button, ConfirmButton, ErrorNote, Field, PageHeader, Panel, QueryState } from '../components/ui'
import { api, ApiError, panelDnsQuery, panelSettingsQuery, type PanelSettings } from '../lib/api'

export function SettingsPage() {
  return (
    <div className="space-y-6">
      <PageHeader
        title="Settings"
        actions={
          <Link to="/onboarding" search={{ step: 0 }} className="text-xs text-muted hover:text-fg">
            Setup guide →
          </Link>
        }
      />
      <PanelUpdates />
      <PanelDomain />
      <ApiTokens />
    </div>
  )
}

/** The panel domain section; `bare` drops the section title where the page already names it. */
export function PanelDomain({ bare = false }: { bare?: boolean }) {
  const settings = useQuery(panelSettingsQuery)
  return (
    <Panel title={bare ? undefined : 'Panel domain'}>
      {!settings.data && (
        <div className="p-4">
          <QueryState loading={settings.isPending} error={settings.error} what="settings" />
        </div>
      )}
      {settings.data && <PanelDomainForm current={settings.data} />}
    </Panel>
  )
}

function PanelDomainForm({ current }: { current: PanelSettings }) {
  const queryClient = useQueryClient()
  const [domain, setDomain] = useState(current.domain)
  const [email, setEmail] = useState(current.acme_email)
  const [notice, setNotice] = useState('')
  useEffect(() => {
    setDomain(current.domain)
    setEmail(current.acme_email)
  }, [current.domain, current.acme_email])

  const save = useMutation({
    mutationFn: (body: PanelSettings) => api<PanelSettings>('/settings/panel', { method: 'PUT', body }),
    onMutate: () => setNotice(''),
    onSuccess: (s) => {
      queryClient.setQueryData(panelSettingsQuery.queryKey, s)
      setNotice(s.domain ? `Saved. The panel is served on https://${s.domain} once its certificate is issued.` : 'Domain removed.')
    },
    onError: (e) => {
      // 503: saved, but the proxy is down; it picks the setting up when it is back.
      if (e instanceof ApiError && e.code === 'proxy_unavailable') queryClient.invalidateQueries({ queryKey: panelSettingsQuery.queryKey })
    },
  })

  const dirty = domain.trim().toLowerCase() !== current.domain || email.trim() !== current.acme_email

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate({ domain, acme_email: email })
  }

  return (
    <>
      <div className="space-y-1 border-b border-line p-4 text-sm">
        {current.domain ? <CurrentDomain domain={current.domain} /> : <p className="text-muted">No domain set.</p>}
        <p className="text-xs text-muted">The panel always stays reachable over plain HTTP on the server's IP address.</p>
      </div>
      <form onSubmit={submit} className="space-y-4 p-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label="Domain"
            name="panel-domain"
            placeholder="panel.example.com"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            hint="Point an A record at this server first. A certificate is issued automatically."
            autoComplete="off"
            spellCheck={false}
          />
          <Field
            label="Certificate email (optional)"
            name="acme-email"
            type="email"
            placeholder="ops@example.com"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            hint="Given to Let's Encrypt for expiry notices. Used for app domains too."
          />
        </div>
        {save.error && <ErrorNote>{save.error.message}</ErrorNote>}
        {notice && <p className="text-xs text-accent">{notice}</p>}
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={!dirty || save.isPending}>
            {save.isPending ? 'Saving…' : 'Save'}
          </Button>
          {current.domain && (
            <ConfirmButton
              label="Remove domain"
              confirm="Yes, remove"
              pending={save.isPending}
              onConfirm={() => save.mutate({ domain: '', acme_email: current.acme_email })}
            />
          )}
        </div>
      </form>
    </>
  )
}

function CurrentDomain({ domain }: { domain: string }) {
  const dns = useQuery(panelDnsQuery(domain))
  return (
    <>
      <p>
        Served on{' '}
        <a href={`https://${domain}`} target="_blank" rel="noreferrer" className="[overflow-wrap:anywhere] text-accent hover:underline">
          https://{domain}
        </a>
      </p>
      <p className="text-xs text-muted">
        <DnsHintText dns={dns} />
      </p>
    </>
  )
}
