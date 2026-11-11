import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState, type FormEvent } from 'react'
import { DnsHintText } from '../components/dns'
import { Link } from '@tanstack/react-router'
import { GitConnections } from './settings-git'
import { ApiTokens } from './settings-tokens'
import { PanelUpdates } from './settings-updates'
import { Button, CheckOption, ConfirmButton, ErrorNote, Field, HttpOnlyChoice, PageHeader, Panel, QueryState } from '../components/ui'
import { rich, useT } from '../lib/i18n'
import { api, ApiError, panelDnsQuery, panelSettingsQuery, type PanelSettings } from '../lib/api'

export function SettingsPage() {
  const t = useT()
  return (
    <div className="space-y-6">
      <PageHeader
        title={t.common.settings}
        actions={
          <Link to="/onboarding" search={{ step: 0 }} className="text-xs text-muted hover:text-fg">
            {t.settings.setupGuide}
          </Link>
        }
      />
      <PanelUpdates />
      <PanelDomain />
      <GitConnections />
      <ApiTokens />
    </div>
  )
}

/** The panel domain section; `bare` drops the section title where the page already names it. */
export function PanelDomain({ bare = false }: { bare?: boolean }) {
  const settings = useQuery(panelSettingsQuery)
  const t = useT()
  return (
    <Panel title={bare ? undefined : t.settings.panelDomain}>
      {!settings.data && (
        <div className="p-4">
          <QueryState loading={settings.isPending} error={settings.error} what={t.settings.what} />
        </div>
      )}
      {settings.data && <PanelDomainForm current={settings.data} />}
    </Panel>
  )
}

function PanelDomainForm({ current }: { current: PanelSettings }) {
  const queryClient = useQueryClient()
  const t = useT()
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
      setNotice(
        !s.domain
          ? t.settings.removedNotice
          : s.tls_mode === 'http_only'
            ? t.settings.savedHttpOnly(`http://${s.domain}`)
            : t.settings.savedTls(`https://${s.domain}`),
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
        {current.domain ? <CurrentDomain domain={current.domain} httpOnly={current.tls_mode === 'http_only'} /> : <p className="text-muted">{t.settings.noDomain}</p>}
        <p className="text-xs text-muted">
          {current.ip_access === false ? t.settings.ipOff : t.settings.ipOn}
        </p>
      </div>
      <form onSubmit={submit} className="space-y-4 p-4">
        <div className="grid gap-4 sm:grid-cols-2">
          <Field
            label={t.settings.domain}
            name="panel-domain"
            placeholder="panel.example.com"
            dir="ltr"
            value={domain}
            onChange={(e) => setDomain(e.target.value)}
            hint={t.settings.domainHint}
            autoComplete="off"
            spellCheck={false}
          />
          <Field
            label={t.settings.certEmail}
            name="acme-email"
            type="email"
            placeholder="ops@example.com"
            dir="ltr"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            hint={t.settings.certEmailHint}
          />
        </div>
        <div className="space-y-3">
          <HttpOnlyChoice checked={httpOnly} onChange={setHttpOnly} />
          <CheckOption
            checked={hasDomain && !ipAccess}
            disabled={!hasDomain}
            onChange={(off) => setIpAccess(!off)}
            label={t.settings.disableIp}
            hint={
              <>
                {t.settings.disableIpHint}
                {!hasDomain && ` ${t.settings.needsDomain}`}
                {hasDomain && !ipAccess && onIP && (
                  <span className="mt-1 block text-warn">
                    {t.settings.onIpWarning}
                  </span>
                )}
                {hasDomain && !ipAccess && (
                  <span className="mt-1 block">
                    {rich(t.settings.lockedOut, {
                      cmd: () => (
                        <code className="text-fg" dir="ltr">
                          ssh -L 8080:127.0.0.1:8080 root@server
                        </code>
                      ),
                    })}
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
            {save.isPending ? t.common.saving : t.common.save}
          </Button>
          {current.domain && (
            <ConfirmButton
              label={t.settings.removeDomain}
              confirm={t.common.yesRemove}
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
  const t = useT()
  const url = `${httpOnly ? 'http' : 'https'}://${domain}`
  return (
    <>
      <p>
        {rich(t.settings.servedOn, {
          url: () => (
            <a href={url} target="_blank" rel="noreferrer" dir="ltr" className="[overflow-wrap:anywhere] text-accent hover:underline">
              {url}
            </a>
          ),
        })}
        {httpOnly && <span className="text-xs text-muted"> {t.settings.httpOnlyNote}</span>}
      </p>
      <p className="text-xs text-muted">
        <DnsHintText dns={dns} />
      </p>
    </>
  )
}
