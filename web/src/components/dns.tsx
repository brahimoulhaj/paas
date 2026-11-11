import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { DnsHint } from '../lib/api'
import { useT } from '../lib/i18n'

/** One line saying where a hostname points, from a DNS hint query. */
export function DnsHintText({ dns }: { dns: UseQueryResult<DnsHint> }): ReactNode {
  const t = useT()
  if (dns.error) return t.dns.failed
  if (!dns.data) return t.dns.checking
  const d = dns.data
  if (!d.resolves) return <span className="text-warn">{t.dns.notResolving}</span>
  if (d.points_here === true) return <span className="text-accent">{t.dns.pointsHere(d.addresses.join(', '))}</span>
  if (d.points_here === false)
    return (
      <span className="text-warn">
        {t.dns.pointsElsewhere(d.addresses.join(', '), d.server_addresses.join(', '))}
      </span>
    )
  return <>{t.dns.unknown(d.addresses.join(', '))}</>
}
