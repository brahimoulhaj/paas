import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { MAX_LINES, useLogStream, type LogLine } from '../lib/logstream'
import { cx } from './ui'

const time = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })

/**
 * Live log view over a server-sent stream.
 * Follow keeps the newest line in view (scrolling up turns it off), Pause freezes what is
 * shown while lines keep arriving, Search filters to matching lines, Wrap toggles long lines.
 */
export function LogViewer({ url, refused }: { url: string | null; refused?: string }) {
  const { lines, state, end, reconnect } = useLogStream(url)
  const [follow, setFollow] = useState(true)
  const [wrap, setWrap] = useState(true)
  const [query, setQuery] = useState('')
  const [frozen, setFrozen] = useState<LogLine[] | null>(null) // the snapshot shown while paused
  const box = useRef<HTMLDivElement>(null)

  const shown = frozen ?? lines
  const needle = query.trim().toLowerCase()
  const visible = useMemo(
    () => (needle ? shown.filter((l) => l.text.toLowerCase().includes(needle)) : shown),
    [shown, needle],
  )
  const missed = frozen ? (lines.at(-1)?.n ?? -1) - (frozen.at(-1)?.n ?? -1) : 0

  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight
  }, [visible, follow, wrap])

  // Scrolling away from the bottom stops following; scrolling back to it resumes.
  function onScroll() {
    const el = box.current
    if (!el) return
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24
    if (atBottom !== follow) setFollow(atBottom)
  }

  let status: ReactNode
  if (state === 'live') status = <span className="text-accent">● live</span>
  else if (state === 'connecting') status = <span className="text-warn">connecting…</span>
  else if (state === 'error') status = <span className="text-danger">{refused ?? 'cannot open the stream'}</span>
  else status = <span>{end?.reason ? `ended: ${end.reason}` : 'complete'}</span>

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2">
        <input
          type="search"
          aria-label="Search the log"
          placeholder="Search"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 min-w-0 flex-1 basis-40 border border-line bg-bg px-2 text-xs text-fg placeholder:text-muted/60 focus:border-accent focus:outline-none"
        />
        <Toggle on={follow} onClick={() => setFollow(!follow)} title="Keep the newest line in view">
          Follow
        </Toggle>
        <Toggle on={!!frozen} onClick={() => setFrozen(frozen ? null : lines)} title="Freeze the view; lines keep arriving in the background">
          {frozen ? 'Resume' : 'Pause'}
        </Toggle>
        <Toggle on={wrap} onClick={() => setWrap(!wrap)} title="Wrap long lines">
          Wrap
        </Toggle>
      </div>

      <div
        ref={box}
        onScroll={onScroll}
        tabIndex={0}
        role="log"
        aria-label="Log output"
        className={cx(
          'max-h-[min(28rem,70vh)] min-h-40 overflow-y-auto bg-bg px-4 py-3 text-xs leading-relaxed',
          wrap ? 'overflow-x-hidden' : 'overflow-x-auto',
        )}
      >
        {visible.length === 0 ? (
          <p className="text-muted">
            {needle && shown.length > 0
              ? 'No line matches the search.'
              : state === 'ended'
                ? 'No output.'
                : state === 'error'
                  ? ''
                  : 'Waiting for output…'}
          </p>
        ) : (
          visible.map((l) => (
            <div key={l.n} className={cx('flex gap-3', !wrap && 'w-max')}>
              {l.time && (
                <time className="shrink-0 text-muted/50 select-none" dateTime={l.time} title={new Date(l.time).toLocaleString()}>
                  {time.format(new Date(l.time))}
                </time>
              )}
              <span
                className={cx(
                  l.stream === 'stderr' ? 'text-warn/90' : 'text-fg/85',
                  wrap ? '[overflow-wrap:anywhere] whitespace-pre-wrap' : 'whitespace-pre',
                )}
                title={l.stream}
              >
                {needle ? <Marked text={l.text} needle={needle} /> : l.text}
              </span>
            </div>
          ))
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 border-t border-line px-4 py-2 text-xs text-muted">
        <span className="flex flex-wrap items-center gap-x-3">
          {status}
          {/* A finished deployment log is complete; only a stream that broke off can be reopened. */}
          {(state === 'error' || (state === 'ended' && end?.reason)) && (
            <button type="button" className="underline hover:text-fg" onClick={reconnect}>
              Reconnect
            </button>
          )}
          {frozen && <span className="text-warn">paused{missed > 0 && ` · ${missed} new`}</span>}
        </span>
        <span>
          {needle ? `${visible.length} of ${shown.length} lines` : `${shown.length} lines`}
          {lines.length >= MAX_LINES && ` (oldest dropped, last ${MAX_LINES} kept)`}
        </span>
      </div>
    </div>
  )
}

function Toggle(props: { on: boolean; onClick: () => void; title: string; children: ReactNode }) {
  return (
    <button
      type="button"
      aria-pressed={props.on}
      title={props.title}
      onClick={props.onClick}
      className={cx(
        'h-8 border px-2.5 text-xs transition-colors',
        props.on ? 'border-accent text-accent' : 'border-line text-muted hover:border-line-strong hover:text-fg',
      )}
    >
      {props.children}
    </button>
  )
}

/** The text with every occurrence of needle (already lowercase) highlighted. */
function Marked({ text, needle }: { text: string; needle: string }) {
  const parts: ReactNode[] = []
  const lower = text.toLowerCase()
  let from = 0
  for (let at = lower.indexOf(needle); at >= 0; at = lower.indexOf(needle, from)) {
    if (at > from) parts.push(text.slice(from, at))
    parts.push(
      <mark key={at} className="bg-accent text-accent-fg">
        {text.slice(at, at + needle.length)}
      </mark>,
    )
    from = at + needle.length
  }
  parts.push(text.slice(from))
  return <>{parts}</>
}
