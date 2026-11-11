import type { UseQueryResult } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { DnsHint } from '../lib/api'

/** One line saying where a hostname points, from a DNS hint query. */
export function DnsHintText({ dns }: { dns: UseQueryResult<DnsHint> }): ReactNode {
  if (dns.error) return 'DNS check failed.'
  if (!dns.data) return 'Checking DNS…'
  const d = dns.data
  if (!d.resolves) return <span className="text-warn">Does not resolve yet. Create an A record pointing at this server.</span>
  if (d.points_here === true) return <span className="text-accent">Resolves to {d.addresses.join(', ')} — this server.</span>
  if (d.points_here === false)
    return (
      <span className="text-warn">
        Resolves to {d.addresses.join(', ')}, not to this server ({d.server_addresses.join(', ')}). Fine behind a CDN or proxy.
      </span>
    )
  return <>Resolves to {d.addresses.join(', ')}. The server cannot tell whether that is itself.</>
}
