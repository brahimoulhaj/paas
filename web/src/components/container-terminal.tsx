import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { useT } from '../lib/i18n'
import type { App } from '../lib/api'
import { useTheme } from '../lib/theme'
import { Button, cx } from './ui'

type State = 'ready' | 'connecting' | 'connected' | 'disconnected'
const TAB_IDLE_TIMEOUT = 2 * 60 * 1000

export default function ContainerTerminal({ app, visible }: { app: App; visible: boolean }) {
  const t = useT().terminal
  const theme = useTheme()
  const host = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const socket = useRef<WebSocket | null>(null)
  const isVisible = useRef(visible)
  isVisible.current = visible
  const hiddenDeadline = useRef<number | null>(null)
  const [state, setState] = useState<State>('ready')
  const [shell, setShell] = useState('/bin/sh')
  const [expanded, setExpanded] = useState(false)
  const [size, setSize] = useState({ cols: 80, rows: 24 })
  const [message, setMessage] = useState('')
  const text = useRef(t)
  text.current = t
  const available = !!app.current_deployment_id && app.runtime_state === 'running'
  const active = state === 'connecting' || state === 'connected'

  function send(value: object) {
    if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify(value))
  }
  function disconnect() {
    const ws = socket.current
    socket.current = null
    ws?.close()
    if (term.current) term.current.options.disableStdin = true
    setState('disconnected')
  }
  function connect() {
    if (!available || !term.current) return
    disconnect()
    const terminal = term.current
    terminal.reset()
    fit.current?.fit()
    setMessage('')
    setState('connecting')
    const url = new URL(`/api/apps/${app.id}/terminal`, window.location.href)
    url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    url.search = new URLSearchParams({ shell, cols: String(terminal.cols), rows: String(terminal.rows) }).toString()
    const ws = new WebSocket(url)
    ws.binaryType = 'arraybuffer'
    socket.current = ws
    const timeout = window.setTimeout(() => {
      if (socket.current === ws && ws.readyState !== WebSocket.CLOSED) {
        setMessage(text.current.failed); disconnect()
      }
    }, 15000)
    ws.onmessage = (event) => {
      if (socket.current !== ws) return
      if (event.data instanceof ArrayBuffer) {
        terminal.write(new Uint8Array(event.data), () => {
          if (socket.current === ws) send({ type: 'ack' })
        })
        return
      }
      const msg = JSON.parse(event.data) as { type: string; message?: string }
      if (msg.type === 'ready') {
        window.clearTimeout(timeout)
        terminal.options.disableStdin = false
        setState('connected')
        if (isVisible.current) terminal.focus()
        send({ type: 'resize', cols: terminal.cols, rows: terminal.rows })
      } else if (msg.type === 'error') {
        setMessage(msg.message ?? text.current.failed)
      }
    }
    ws.onerror = () => { if (socket.current === ws) setMessage(text.current.failed) }
    ws.onclose = () => {
      window.clearTimeout(timeout)
      if (socket.current !== ws) return
      socket.current = null
      terminal.options.disableStdin = true
      setState('disconnected')
    }
  }

  useEffect(() => {
    if (!host.current) return
    const terminal = new Terminal({
      fontFamily: '"JetBrains Mono", monospace', fontSize: 13, lineHeight: 1.25,
      cursorBlink: true, cursorStyle: 'bar', scrollback: 5000, smoothScrollDuration: 80,
      disableStdin: true, allowProposedApi: false, screenReaderMode: true,
    })
    const addon = new FitAddon()
    terminal.loadAddon(addon)
    terminal.open(host.current)
    terminal.textarea?.setAttribute('aria-label', text.current.output)
    term.current = terminal
    fit.current = addon
    let frame = 0
    const resize = () => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => { if (host.current?.clientWidth) addon.fit() })
    }
    const observer = new ResizeObserver(resize)
    observer.observe(host.current)
    const input = terminal.onData((data) => {
      const bytes = new TextEncoder().encode(data)
      const ws = socket.current
      if (ws?.readyState !== WebSocket.OPEN) return
      if (ws.bufferedAmount + bytes.byteLength > 1024 * 1024) {
        setMessage(text.current.failed); disconnect(); return
      }
      for (let offset = 0; offset < bytes.length; offset += 16384) ws.send(bytes.subarray(offset, offset + 16384))
    })
    const resized = terminal.onResize(({ cols, rows }) => { setSize({ cols, rows }); send({ type: 'resize', cols, rows }) })
    const ping = window.setInterval(() => send({ type: 'ping' }), 20000)
    let disposed = false
    void document.fonts.ready.then(() => { if (!disposed) resize() })
    resize()
    return () => {
      disposed = true
      const ws = socket.current; socket.current = null; ws?.close()
      window.clearInterval(ping); cancelAnimationFrame(frame)
      observer.disconnect(); input.dispose(); resized.dispose(); terminal.dispose()
      term.current = null; fit.current = null
    }
  }, [app.id])

  // Keep the PTY and scrollback mounted while browsing the other app tabs.
  // Polling app data must not extend the grace period; only returning here resets it.
  useEffect(() => {
    if (visible) {
      if (hiddenDeadline.current !== null && Date.now() >= hiddenDeadline.current && socket.current) {
        disconnect()
        setMessage(text.current.idleClosed)
      }
      hiddenDeadline.current = null
      const frame = requestAnimationFrame(() => {
        fit.current?.fit()
        if (socket.current?.readyState === WebSocket.OPEN) term.current?.focus()
      })
      return () => cancelAnimationFrame(frame)
    }
    setExpanded(false)
    if (!active) { hiddenDeadline.current = null; return }
    hiddenDeadline.current ??= Date.now() + TAB_IDLE_TIMEOUT
    const timer = window.setTimeout(() => {
      disconnect()
      setMessage(text.current.idleClosed)
      hiddenDeadline.current = null
    }, Math.max(0, hiddenDeadline.current - Date.now()))
    return () => window.clearTimeout(timer)
  }, [visible, active])

  useEffect(() => {
    if (!term.current) return
    term.current.options.theme = {
      background: theme.tokens.bg, foreground: theme.tokens.fg, cursor: theme.tokens.accent,
      selectionBackground: theme.tokens.raised, black: '#1b1f27', red: '#f7768e', green: '#9ece6a',
      yellow: '#e0af68', blue: '#7aa2f7', magenta: '#bb9af7', cyan: '#7dcfff', white: '#d5d9e2',
    }
  }, [theme])

  const deployment = useRef(app.current_deployment_id)
  useEffect(() => {
    if (deployment.current !== app.current_deployment_id || !available) {
      if (socket.current) { disconnect(); setMessage(text.current.changed) }
      deployment.current = app.current_deployment_id
    }
  }, [app.current_deployment_id, available])

  useEffect(() => {
    if (!expanded || !visible) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !host.current?.contains(event.target as Node)) setExpanded(false) }
    window.addEventListener('keydown', escape)
    return () => { document.body.style.overflow = previous; window.removeEventListener('keydown', escape) }
  }, [expanded, visible])

  return <section aria-label={t.title} className={cx('flex min-w-0 flex-col overflow-hidden border border-line bg-bg', expanded && 'fixed inset-3 z-40 shadow-xl sm:inset-6')}>
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line bg-surface px-3 py-2">
      <div className="flex min-w-0 items-center gap-3">
        <span aria-hidden className="font-mono text-base text-accent">&gt;_</span>
        <span className="truncate text-xs font-medium" dir="ltr">{app.name}</span>
        <span className="flex items-center gap-1.5 text-xs text-muted" role="status"><span className={cx('size-1.5 rounded-full', state === 'connected' ? 'bg-accent' : state === 'connecting' ? 'animate-pulse bg-warn' : 'bg-muted')} />{t[state]}</span>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <select aria-label={t.shell} value={shell} disabled={active} onChange={(e) => setShell(e.target.value)} className="h-8 border border-line bg-bg px-2 font-mono text-xs text-fg disabled:opacity-60">
          <option>/bin/sh</option><option>/bin/bash</option><option>/bin/ash</option>
        </select>
        <Button variant="ghost" className="h-8 px-2 text-xs" onClick={() => { term.current?.clear(); term.current?.focus() }}>{t.clear}</Button>
        <Button variant="ghost" className="h-8 px-2 text-xs" aria-pressed={expanded} onClick={() => setExpanded(!expanded)}>{expanded ? t.collapse : t.expand}</Button>
        <Button className="h-8 px-3 text-xs" disabled={!available && !active} onClick={active ? disconnect : connect}>{active ? t.disconnect : state === 'ready' ? t.connect : t.reconnect}</Button>
      </div>
    </div>
    {message && <p role="alert" className="border-b border-line px-3 py-2 text-xs text-warn">{message}</p>}
    <div className={cx('relative min-h-0', expanded ? 'flex-1' : 'h-[min(32rem,65vh)] min-h-64')}>
      <div ref={host} dir="ltr" className="absolute inset-3" />
      {state === 'ready' && <div className="absolute inset-0 flex flex-col items-center justify-center gap-4 bg-bg text-center">
        <span aria-hidden className="font-mono text-4xl text-muted/40">&gt;_</span>
        <p className="px-4 text-sm text-muted">{available ? t.intro : t.unavailable}</p>
        {available && <Button onClick={connect}>{t.connect}</Button>}
      </div>}
    </div>
    <footer className="flex items-center justify-between gap-3 border-t border-line px-3 py-2 text-[11px] text-muted">
      <span>{state === 'disconnected' ? t.ended : t.shortcut}</span>
      <span dir="ltr" className="font-mono tabular-nums">{size.cols} × {size.rows} · UTF-8</span>
    </footer>
  </section>
}
