// lib/diff.ts — a small line-based diff. Good enough to show what changed
// between two task-version bodies; not meant to compete with a real diff
// algorithm, and no diff library is added for it (Global Constraints).
export interface DiffLine {
  kind: 'same' | 'added' | 'removed'
  text: string
}

export function diffLines(a: string, b: string): DiffLine[] {
  const linesA = a.split('\n')
  const linesB = b.split('\n')
  const n = linesA.length
  const m = linesB.length
  // lcs[i][j] = length of the longest common subsequence of linesA[i:] and linesB[j:]
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i][j] = linesA[i] === linesB[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < n && j < m) {
    if (linesA[i] === linesB[j]) {
      out.push({ kind: 'same', text: linesA[i] })
      i++
      j++
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: 'removed', text: linesA[i] })
      i++
    } else {
      out.push({ kind: 'added', text: linesB[j] })
      j++
    }
  }
  while (i < n) {
    out.push({ kind: 'removed', text: linesA[i] })
    i++
  }
  while (j < m) {
    out.push({ kind: 'added', text: linesB[j] })
    j++
  }
  return out
}
