// Display helpers mirroring internal/semver. The server computes the real
// next version; these only preview it and order versions on screen.
import type { SemverStep } from '../../types/taskAuthor'

function core(v: string): [number, number, number] {
  const [c = ''] = v.split('-', 1)
  const p = c.split('.').map((s) => Number.parseInt(s, 10))
  return [p[0] ?? 0, p[1] ?? 0, p[2] ?? 0]
}

/** semver.Bump: a pre-release suffix is dropped before increasing, so 1.3.0-rc.1 patches to 1.3.1. */
export function bumpVersion(v: string, step: SemverStep): string {
  const [major, minor, patch] = core(v)
  switch (step) {
    case 'major':
      return `${major + 1}.0.0`
    case 'minor':
      return `${major}.${minor + 1}.0`
    case 'patch':
      return `${major}.${minor}.${patch + 1}`
  }
}

/** Negative, zero or positive as a is lower than, equal to or higher than b. */
export function compareVersions(a: string, b: string): number {
  const ca = core(a)
  const cb = core(b)
  const d = ca[0] - cb[0] || ca[1] - cb[1] || ca[2] - cb[2]
  if (d !== 0) return d
  const pa = a.includes('-')
  const pb = b.includes('-')
  if (pa !== pb) return pa ? -1 : 1 // a pre-release is lower than its release
  return a < b ? -1 : a > b ? 1 : 0 // pre-release identifiers compared as text: close enough for display
}
