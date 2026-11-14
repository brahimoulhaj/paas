import { toast } from '@yusufceng1z/ding-react'

/**
 * Runs a slow action under a toast: loading while it runs, then success, or error with the
 * failure's message. The error is rethrown so the caller's own state still sees it. The
 * Toaster itself is mounted once in the shell (components/deploy-toasts.tsx).
 */
export async function withToast<T>(
  run: () => Promise<T>,
  m: { loading: string; success: string | ((value: T) => string); error: string; id?: string },
): Promise<T> {
  const id = toast.loading(m.loading, m.id ? { id: m.id } : undefined)
  try {
    const value = await run()
    toast.success(typeof m.success === 'function' ? m.success(value) : m.success, { id })
    return value
  } catch (e) {
    toast.error(m.error, { id, description: e instanceof Error ? e.message : String(e) })
    throw e
  }
}

const updatedKey = 'berth.updatedTo'

/** Remembers across the reload into the new panel that an update just succeeded. */
export function rememberUpdated(version: string) {
  try {
    sessionStorage.setItem(updatedKey, version)
  } catch {
    // No storage: the reload simply shows no toast.
  }
}

/** The version a just-finished update moved to, once; null otherwise. */
export function takeUpdated(): string | null {
  try {
    const v = sessionStorage.getItem(updatedKey)
    sessionStorage.removeItem(updatedKey)
    return v
  } catch {
    return null
  }
}
