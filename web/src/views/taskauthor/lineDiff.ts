// A line diff by longest common subsequence: small, dependency-free
// (ruling 4.16), and plenty for task descriptions and catalog YAML files.

export type DiffOp = 'same' | 'add' | 'del'

export interface DiffLine {
  op: DiffOp
  text: string
}

/** Above this many table cells (lines before × lines after), show a full replace instead. */
const MAX_CELLS = 4_000_000

function lines(s: string): string[] {
  if (s === '') return []
  const ls = s.split('\n')
  if (ls[ls.length - 1] === '') ls.pop() // a final newline ends the last line; it does not start an empty one
  return ls
}

export function lineDiff(before: string, after: string): DiffLine[] {
  const a = lines(before)
  const b = lines(after)
  const n = a.length
  const m = b.length
  if (n * m > MAX_CELLS) {
    return [
      ...a.map((text): DiffLine => ({ op: 'del', text })),
      ...b.map((text): DiffLine => ({ op: 'add', text })),
    ]
  }
  // lcs[i][j] is the length of the longest common subsequence of a[i:] and b[j:].
  const lcs: Uint32Array[] = []
  for (let i = 0; i <= n; i++) lcs.push(new Uint32Array(m + 1))
  for (let i = n - 1; i >= 0; i--) {
    const row = lcs[i]!
    const next = lcs[i + 1]!
    for (let j = m - 1; j >= 0; j--) {
      row[j] = a[i] === b[j] ? next[j + 1]! + 1 : Math.max(next[j]!, row[j + 1]!)
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ op: 'same', text: a[i]! })
      i++
      j++
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      out.push({ op: 'del', text: a[i]! })
      i++
    } else {
      out.push({ op: 'add', text: b[j]! })
      j++
    }
  }
  while (i < n) out.push({ op: 'del', text: a[i++]! })
  while (j < m) out.push({ op: 'add', text: b[j++]! })
  return out
}
