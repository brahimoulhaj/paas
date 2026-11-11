import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'
import { DnsHintText } from '../components/dns'
import { Link } from '@tanstack/react-router'
import { ApiTokens } from './settings-tokens'
import { PanelUpdates } from './settings-updates'
import { Button, CheckOption, ConfirmButton, ErrorNote, Field, HttpOnlyChoice, PageHeader, Panel, QueryState } from '../components/ui'
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
  const [httpOnly, setHttpOnly] = useState(current.tls_mode === 'http_only')
  const [ipAccess, setIpAccess] = useState(current.ip_access !== false)
  const [notice, setNotice] = useState('')
  useEffect(() => {
    setDomain(current.domain)
    setEmail(current.acme_email)
    setHttpOnly(current.tls_mode === 'http_only')
    setIpAccess(current.ip_access !== false)
  }, [current.domain, current.acme_email, current.tls_mode, current.ip_access])
  const hasDomain = domain.trim() !== ''
  const onIP = /^[\d.]+$|^\[?[0-9a-f:]+\]?$/i.test(location.hostname)

  const save = useMutation({
    mutationFn: (body: PanelSettings) => api<PanelSettings>('/settings/panel', { method: 'PUT', body }),
    onMutate: () => setNotice(''),
    onSuccess: (s) => {
      queryClient.setQueryData(panelSettingsQuery.queryKey, s)
      const scheme = s.tls_mode === 'http_only' ? 'http' : 'https'
      setNotice(
        !s.domain
          ? 'Domain removed. The panel answers on the server IP again.'
          : s.tls_mode === 'http_only'
            ? `Saved. The panel is served on http://${s.domain} for your CDN to put HTTPS in front.`
            : `Saved. The panel is served on ${scheme}://${s.domain} once its certificate is issued.`,
      )
    },
    onError: (e) => {
      // 503: saved, but the proxy is down; it picks the setting up when it is back.
      if (e instanceof ApiError && e.code === 'proxy_unavailable') queryClient.invalidateQueries({ queryKey: panelSettingsQuery.queryKey })
    },
  })

  const dirty =
    domain.trim().toLowerCase() !== current.domain ||
    email.trim() !== current.acme_email ||
    httpOnly !== (current.tls_mode === 'http_only') ||
    (hasDomain && ipAccess !== (current.ip_access !== false))

  function submit(e: FormEvent) {
    e.preventDefault()
    save.mutate({ domain, acme_email: email, tls_mode: httpOnly ? 'http_only' : 'auto', ip_access: !hasDomain || ipAccess })
  }

  return (
    <>
      <div className="space-y-1 border-b border-line p-4 text-sm">
        {current.domain ? <CurrentDomain domain={current.domain} httpOnly={current.tls_mode === 'http_only'} /> : <p className="text-muted">No domain set.</p>}
        <p className="text-xs text-muted">
          {current.ip_access === false
            ? "Access by IP address is off: only the domain opens the panel."
            : "The panel is also reachable over plain HTTP on the server's IP address."}
        </p>
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
        <div className="space-y-3">
          <HttpOnlyChoice checked={httpOnly} onChange={setHttpOnly} />
          <CheckOption
            checked={hasDomain && !ipAccess}
            disabled={!hasDomain}
            onChange={(off) => setIpAccess(!off)}
            label="Disable access by IP address"
            hint={
              <>
                Only the domain opens the panel; requests to http://&lt;server-ip&gt; are dropped.
                {!hasDomain && ' Needs a domain first.'}
                {hasDomain && !ipAccess && onIP && (
                  <span className="mt-1 block text-warn">
                    You are on the IP address now. After saving, open the domain instead; check it works first.
                  </span>
                )}
                {hasDomain && !ipAccess && (
                  <span className="mt-1 block">
                    Locked out? On the server: <code className="text-fg">ssh -L 8080:127.0.0.1:8080 root@server</code>, then open
                    http://localhost:8080. Removing the domain turns IP access back on.
                  </span>
                )}
              </>
            }
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
              onConfirm={() => save.mutate({ domain: '', acme_email: current.acme_email, tls_mode: 'auto', ip_access: true })}
            />
          )}
        </div>
      </form>
    </>
  )
}

function CurrentDomain({ domain, httpOnly }: { domain: string; httpOnly: boolean }) {
  const dns = useQuery(panelDnsQuery(domain))
  const url = `${httpOnly ? 'http' : 'https'}://${domain}`
  return (
    <>
      <p>
        Served on{' '}
        <a href={url} target="_blank" rel="noreferrer" className="[overflow-wrap:anywhere] text-accent hover:underline">
          {url}
        </a>
        {httpOnly && <span className="text-xs text-muted"> (HTTP only, HTTPS from your CDN)</span>}
      </p>
      <p className="text-xs text-muted">
        <DnsHintText dns={dns} />
      </p>
    </>
  )
}
