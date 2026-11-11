export type EnvironmentRow = { key: string; value: string }

export function parseEnvironment(text: string): EnvironmentRow[] {
  return text.split(/\r?\n/).flatMap((line, index) => {
    if (!line.trim() || line.trimStart().startsWith('#')) return []
    const match = line.match(/^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$/)
    if (!match) throw new Error(String(index + 1))
    let value = match[2]
    if (value.startsWith('"')) {
      try { value = JSON.parse(value) as string } catch { throw new Error(String(index + 1)) }
    } else if (value.startsWith("'")) {
      if (!value.endsWith("'") || value.length < 2) throw new Error(String(index + 1))
      value = value.slice(1, -1)
    }
    return [{ key: match[1], value }]
  })
}

export function serializeEnvironment(rows: EnvironmentRow[]): string {
  return rows.map(({ key, value }) => `${key}=${JSON.stringify(value)}`).join('\n')
}

export function environmentMap(rows: EnvironmentRow[]): Record<string, string> {
  const result: Record<string, string> = Object.create(null)
  for (const { key, value } of rows) {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) || key.length > 256 || key in result || value.includes('\0')) throw new Error(key)
    result[key] = value
  }
  return result
}
