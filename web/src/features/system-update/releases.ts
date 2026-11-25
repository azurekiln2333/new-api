/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { z } from 'zod'

/**
 * Release channel used when the administrator has not selected another
 * repository. It is intentionally the upstream project so an unconfigured
 * deployment behaves exactly as before.
 */
export const DEFAULT_UPDATE_SOURCE_REPOSITORY = 'QuantumNous/new-api'

const repositoryPattern = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/

export function isUpdateSourceRepository(value: unknown): boolean {
  if (typeof value !== 'string') return false
  const trimmed = value.trim()
  if (!trimmed || trimmed.length > 140 || !repositoryPattern.test(trimmed)) {
    return false
  }
  return trimmed.split('/').every((part) => !part.endsWith('.'))
}

/** Resolve the effective repository, falling back to the upstream default. */
export function resolveUpdateSourceRepository(value: unknown): string {
  const trimmed = typeof value === 'string' ? value.trim() : ''
  return trimmed && isUpdateSourceRepository(trimmed)
    ? trimmed
    : DEFAULT_UPDATE_SOURCE_REPOSITORY
}

export function getSystemReleasesUrl(repository: string): string {
  return `https://api.github.com/repos/${resolveUpdateSourceRepository(
    repository
  )}/releases?per_page=100`
}

export const systemReleaseSchema = z.object({
  tag_name: z.string().trim().min(1),
  name: z.string().nullable().optional(),
  body: z.string().nullable().optional(),
  published_at: z.iso.datetime().nullable().optional(),
  prerelease: z.boolean(),
})

export type SystemRelease = z.infer<typeof systemReleaseSchema>

type SystemVersion = {
  core: bigint[]
  stage: number
  sequence: bigint
  revision: bigint
}

const releaseStages: Record<string, number> = {
  alpha: 0,
  beta: 1,
  rc: 2,
  stable: 3,
  patch: 4,
}

/** Fork builds are tagged `custom-YYYYMMDD-<shortsha>` by the release workflow. */
export function parseCustomBuildVersion(
  value: string | null | undefined
): { date: bigint; commit: string } | null {
  const match = value?.trim().match(/^custom-(\d{8})-([0-9a-f]{6,40})$/i)
  if (!match) return null
  const [, rawDate, commit] = match
  const year = Number(rawDate.slice(0, 4))
  const month = Number(rawDate.slice(4, 6))
  const day = Number(rawDate.slice(6, 8))
  if (year < 1000 || month < 1 || month > 12 || day < 1 || day > 31) {
    return null
  }
  const date = new Date(Date.UTC(year, month - 1, day))
  if (date.getUTCMonth() !== month - 1 || date.getUTCDate() !== day) return null
  return { date: BigInt(rawDate), commit: commit.toLowerCase() }
}

/** Project tags include post-release patches and historical numeric revisions. */
export function parseSystemVersion(
  value: string | null | undefined
): SystemVersion | null {
  const match = value
    ?.trim()
    .match(
      /^v?(\d+(?:\.\d+){2,})(?:-(alpha|beta|rc|patch)(?:\.(\d+))?(?:-i18nfix\.(\d+))?)?(?:\+[\da-zA-Z.-]+)?$/
    )
  if (!match || (match[4] && match[2] !== 'rc')) return null

  const core = match[1].split('.').map((part) => BigInt(part))
  if (core.every((part) => part === 0n)) return null

  return {
    core,
    stage: releaseStages[match[2] ?? 'stable'],
    sequence: BigInt(match[3] ?? '0'),
    revision: BigInt(match[4] ?? '0'),
  }
}

/** A running build counts as a prerelease when its stage precedes stable. */
export function isPrereleaseBuild(value: string | null | undefined): boolean {
  return (parseSystemVersion(value)?.stage ?? 3) < 3
}

export function compareSystemVersions(
  left: string | null | undefined,
  right: string | null | undefined
): -1 | 0 | 1 | null {
  const a = parseSystemVersion(left)
  const b = parseSystemVersion(right)
  if (!a || !b) return null

  for (let index = 0; index < Math.max(a.core.length, b.core.length); index++) {
    const leftPart = a.core[index] ?? 0n
    const rightPart = b.core[index] ?? 0n
    if (leftPart !== rightPart) return leftPart < rightPart ? -1 : 1
  }
  if (a.stage !== b.stage) return a.stage < b.stage ? -1 : 1
  if (a.sequence !== b.sequence) return a.sequence < b.sequence ? -1 : 1
  if (a.revision !== b.revision) return a.revision < b.revision ? -1 : 1
  return 0
}

/**
 * Instant a fork build was cut. The workflow stamps the commit date into the
 * tag, so the build is treated as the end of that UTC day: a release published
 * earlier on the same date must not look newer than the running binary.
 */
function customBuildInstant(date: bigint): number {
  const raw = date.toString()
  const iso = `${raw.slice(0, 4)}-${raw.slice(4, 6)}-${raw.slice(6, 8)}T23:59:59.999Z`
  const parsed = Date.parse(iso)
  return Number.isNaN(parsed) ? 0 : parsed
}

function releaseInstant(release: SystemRelease): number {
  if (release.published_at) {
    const parsed = Date.parse(release.published_at)
    if (!Number.isNaN(parsed)) return parsed
  }
  const custom = parseCustomBuildVersion(release.tag_name)
  if (custom) return customBuildInstant(custom.date)
  return 0
}

/**
 * Compare fork tags by their build date, not a later re-publication time.
 * A matching commit is already installed; distinct same-day commits cannot be
 * ordered from their hashes. Cross-family releases use the publication timeline
 * and must be published strictly after the build day to count as newer.
 */
export function compareCustomBuildRelease(
  currentVersion: string | null | undefined,
  release: SystemRelease | null | undefined
): -1 | 0 | 1 | null {
  const build = parseCustomBuildVersion(currentVersion)
  if (!build || !release) return null
  if (release.prerelease || isPrereleaseBuild(release.tag_name)) return 0

  const candidate = parseCustomBuildVersion(release.tag_name)
  if (candidate) {
    if (
      build.commit.startsWith(candidate.commit) ||
      candidate.commit.startsWith(build.commit) ||
      build.date === candidate.date
    ) {
      return 0
    }
    return build.date < candidate.date ? -1 : 1
  }

  const buildAt = customBuildInstant(build.date)
  const releaseAt = releaseInstant(release)
  if (!buildAt || !releaseAt) return null
  if (releaseAt > buildAt) return -1
  return releaseAt < buildAt ? 1 : 0
}

const publishedReleaseSchema = systemReleaseSchema.extend({
  draft: z.boolean(),
})

export function selectLatestRelease(payload: unknown): SystemRelease | null {
  if (!Array.isArray(payload)) throw new Error('Unexpected release payload')

  let latest: SystemRelease | null = null
  let validPayload = payload.length === 0
  for (const item of payload) {
    const parsed = publishedReleaseSchema.safeParse(item)
    if (!parsed.success) continue
    validPayload = true
    const release = parsed.data
    if (release.draft) continue
    if (
      !parseSystemVersion(release.tag_name) &&
      !parseCustomBuildVersion(release.tag_name)
    ) {
      continue
    }
    if (!latest || isNewerRelease(release, latest)) {
      latest = systemReleaseSchema.parse(release)
    }
  }
  if (!validPayload) throw new Error('Unexpected release payload')
  return latest
}

function isNewerRelease(candidate: SystemRelease, current: SystemRelease) {
  const candidateBuild = parseCustomBuildVersion(candidate.tag_name)
  const currentBuild = parseCustomBuildVersion(current.tag_name)
  if (candidateBuild && currentBuild && candidateBuild.date !== currentBuild.date) {
    return candidateBuild.date > currentBuild.date
  }
  const candidateVersion = parseSystemVersion(candidate.tag_name)
  const currentVersion = parseSystemVersion(current.tag_name)
  if (candidateVersion && currentVersion) {
    return compareSystemVersions(candidate.tag_name, current.tag_name) === 1
  }
  // Mixed families only compare on the release timeline.
  return releaseInstant(candidate) > releaseInstant(current)
}

export function getSystemReleaseUrl(
  release: SystemRelease,
  repository: string = DEFAULT_UPDATE_SOURCE_REPOSITORY
): string {
  return `https://github.com/${resolveUpdateSourceRepository(
    repository
  )}/releases/tag/${encodeURIComponent(release.tag_name)}`
}
