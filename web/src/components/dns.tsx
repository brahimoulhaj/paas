import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { DnsHint } from '../lib/api'
import { useT } from '../lib/i18n'

/** One line saying where a hostname points, from a DNS hint query, with a way to look
 * again (after creating a record) unless it already points at this server. */
export function DnsHintText({ dns }: { dns: UseQueryResult<DnsHint> }): ReactNode {
  const t = useT()
  if (!dns.data && !dns.error) return t.dns.checking
  const d = dns.data
  const again = (
    <button
      type="button"
      onClick={() => dns.refetch()}
      disabled={dns.isFetching}
      className="ms-2 whitespace-nowrap text-muted underline decoration-dotted underline-offset-2 hover:text-fg disabled:no-underline disabled:opacity-60"
    >
      {dns.isFetching ? t.dns.checking : t.dns.checkAgain}
    </button>
  )
  let text: ReactNode
  if (dns.error || !d) text = t.dns.failed
  else if (!d.resolves) text = <span className="text-warn">{t.dns.notResolving}</span>
  else if (d.points_here === true) return <span className="text-accent">{t.dns.pointsHere(d.addresses.join(', '))}</span>
  else if (d.points_here === false)
    text = <span className="text-warn">{t.dns.pointsElsewhere(d.addresses.join(', '), d.server_addresses.join(', '))}</span>
  else text = t.dns.unknown(d.addresses.join(', '))
  return (
    <>
      {text}
      {again}
    </>
  )
}
