import { useCallback, useEffect, useState } from 'react'

export type LogLine = {
  /** Position in this stream; a stable key that survives trimming. */
  n: number
  stream?: 'stdout' | 'stderr'
  time?: string
  text: string
}

/** connecting: opening or re-opening · live: receiving · ended: the server finished · error: refused */
export type StreamState = 'connecting' | 'live' | 'ended' | 'error'

/** Lines kept in memory; older ones are dropped so a chatty container cannot exhaust the tab. */
export const MAX_LINES = 5000

/**
 * Follows a server-sent log stream. The browser reconnects by itself after a network
 * blip and the server resumes after the last line it delivered, so lines are neither
 * lost nor repeated. Lines are appended once per animation frame, not once per line.
 */
export function useLogStream(url: string | null) {
  const [lines, setLines] = useState<LogLine[]>([])
  const [state, setState] = useState<StreamState>('connecting')
  const [end, setEnd] = useState<Record<string, string> | null>(null)
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    setLines([])
    setEnd(null)
    if (!url) {
      setState('ended')
      return
    }
    setState('connecting')

    const source = new EventSource(url)
    let pending: LogLine[] = []
    let frame = 0
    let n = 0
    const flush = () => {
      frame = 0
      const batch = pending
      pending = []
      setLines((prev) => {
        const next = prev.concat(batch)
        return next.length > MAX_LINES ? next.slice(next.length - MAX_LINES) : next
      })
    }

    source.onopen = () => setState('live')
    source.addEventListener('log', (e) => {
      const data = JSON.parse((e as MessageEvent<string>).data) as Omit<LogLine, 'n'>
      pending.push({ ...data, n: n++ })
      // A hidden tab gets no animation frames; keep the backlog bounded until it is shown again.
      if (pending.length > MAX_LINES) pending = pending.slice(pending.length - MAX_LINES)
      if (!frame) frame = requestAnimationFrame(flush)
    })
    source.addEventListener('end', (e) => {
      setEnd(JSON.parse((e as MessageEvent<string>).data) as Record<string, string>)
      setState('ended')
      source.close() // otherwise the browser would reconnect and replay the stream
    })
    // CLOSED means the server refused (signed out, no container); otherwise it is retrying.
    source.onerror = () => setState(source.readyState === EventSource.CLOSED ? 'error' : 'connecting')

    return () => {
      source.close()
      if (frame) cancelAnimationFrame(frame)
    }
  }, [url, attempt])

  const reconnect = useCallback(() => setAttempt((a) => a + 1), [])
  return { lines, state, end, reconnect }
}
