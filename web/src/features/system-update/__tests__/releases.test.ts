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
import { describe, expect, test } from 'vitest'

import {
  compareCustomBuildRelease,
  compareSystemVersions,
  DEFAULT_UPDATE_SOURCE_REPOSITORY,
  getSystemReleaseUrl,
  getSystemReleasesUrl,
  parseCustomBuildVersion,
  resolveUpdateSourceRepository,
  selectLatestRelease,
} from '../releases'

describe('system release ordering', () => {
  test.each([
    ['v0.13.2', 'v1.0.0-alpha.1', -1],
    ['v1.0.0-alpha.2', 'v1.0.0-beta.1', -1],
    ['v1.0.0-beta.1', 'v1.0.0-rc.1', -1],
    ['v1.0.0-rc.9', 'v1.0.0-rc.10', -1],
    ['v1.0.0-rc.19', 'v1.0.0-rc.19-i18nfix.1', -1],
    ['v1.0.0-rc.19-i18nfix.2', 'v1.0.0-rc.20', -1],
    ['v1.0.0-rc.36', 'v1.0.0', -1],
    ['v0.13.1', 'v0.13.1-patch.1', -1],
    ['v0.13.1-patch.9', 'v0.13.1-patch.10', -1],
    ['v0.13.1-patch.10', 'v0.13.2', -1],
    ['v0.8.8.3.2', 'v0.8.8.3.3', -1],
    ['v0.9.3.0', 'v0.9.3', 0],
    ['v1.0.0+build.1', '1.0.0+build.2', 0],
    ['v1.0.0-rc.37', 'v1.0.0-rc.36', 1],
    ['v1.0.0', 'v1.0.0', 0],
    ['v0.0.0', 'v1.0.0', null],
    ['dev', 'v1.0.0', null],
    ['', 'v1.0.0', null],
    ['v1.0.0-custom.1', 'v1.0.0', null],
    ['v1.0.0-rc.36-2-gabcdef', 'v1.0.0', null],
  ])('compares %s against %s as %s', (current, latest, expected) => {
    expect(compareSystemVersions(current, latest)).toBe(expected)
  })

  test('selects the highest published version including pre-releases, regardless of order', () => {
    const releases = [
      { tag_name: 'v1.0.0-rc.19-i18nfix.2', draft: false, prerelease: true },
      { tag_name: 'v0.13.2', draft: false, prerelease: false },
      { tag_name: 'v2.0.0', draft: true, prerelease: false },
      { tag_name: 'v1.0.0-rc.36', draft: false, prerelease: true },
      { tag_name: 'nightly', draft: false, prerelease: true },
    ]
    expect(selectLatestRelease(releases)?.tag_name).toBe('v1.0.0-rc.36')
  })

  test('returns no release for an empty list or a list containing only drafts', () => {
    expect(selectLatestRelease([])).toBeNull()
    expect(
      selectLatestRelease([
        { tag_name: 'v2.0.0', draft: true, prerelease: false },
      ])
    ).toBeNull()
  })

  test('rejects malformed payloads instead of reporting that the system is current', () => {
    expect(() => selectLatestRelease({ message: 'bad response' })).toThrow()
    expect(() => selectLatestRelease([{ tag_name: 42 }])).toThrow()
  })
})

describe('update source repository', () => {
  test('defaults to the upstream project when nothing is configured', () => {
    expect(resolveUpdateSourceRepository('')).toBe(
      DEFAULT_UPDATE_SOURCE_REPOSITORY
    )
    expect(resolveUpdateSourceRepository(undefined)).toBe(
      DEFAULT_UPDATE_SOURCE_REPOSITORY
    )
    expect(getSystemReleasesUrl(DEFAULT_UPDATE_SOURCE_REPOSITORY)).toBe(
      'https://api.github.com/repos/QuantumNous/new-api/releases?per_page=100'
    )
  })

  test('uses a configured fork repository', () => {
    expect(resolveUpdateSourceRepository('azurekiln2333/new-api')).toBe(
      'azurekiln2333/new-api'
    )
    expect(getSystemReleasesUrl('azurekiln2333/new-api')).toBe(
      'https://api.github.com/repos/azurekiln2333/new-api/releases?per_page=100'
    )
    expect(
      getSystemReleaseUrl(
        { tag_name: 'custom-20261124-53a71f8', prerelease: false },
        'azurekiln2333/new-api'
      )
    ).toBe(
      'https://github.com/azurekiln2333/new-api/releases/tag/custom-20261124-53a71f8'
    )
  })

  test('rejects values that are not a bare owner/repo pair', () => {
    for (const invalid of [
      'https://github.com/QuantumNous/new-api',
      'QuantumNous',
      'QuantumNous/new-api/extra',
      'QuantumNous new-api',
      'user:pass@owner/repo',
      'owner/repo?x=1',
      './repo',
      '../repo',
      'owner/.',
      'owner/..',
      'owner./repo',
      'owner/repo.',
      `owner/${'x'.repeat(135)}`,
      42,
    ]) {
      expect(resolveUpdateSourceRepository(invalid)).toBe(
        DEFAULT_UPDATE_SOURCE_REPOSITORY
      )
    }
  })
})

describe('fork build ordering', () => {
  test('parses custom-YYYYMMDD-<sha> tags', () => {
    expect(parseCustomBuildVersion('custom-20261124-53a71f8')).toEqual({
      date: 20261124n,
      commit: '53a71f8',
    })
    expect(parseCustomBuildVersion('custom-20240229-ABCDEF1')).toEqual({
      date: 20240229n,
      commit: 'abcdef1',
    })
    expect(parseCustomBuildVersion('custom-20261324-53a71f8')).toBeNull()
    expect(parseCustomBuildVersion('custom-20260229-53a71f8')).toBeNull()
    expect(parseCustomBuildVersion('custom-20260431-53a71f8')).toBeNull()
    expect(parseCustomBuildVersion('custom-00001124-53a71f8')).toBeNull()
    expect(parseCustomBuildVersion('v1.0.0-rc.36')).toBeNull()
    expect(parseCustomBuildVersion('custom-20261124')).toBeNull()
  })

  test('does not flag a release published before the build day', () => {
    const release = {
      tag_name: 'v1.0.0',
      prerelease: false,
      published_at: '2026-11-20T10:00:00Z',
    }
    expect(compareCustomBuildRelease('custom-20261124-53a71f8', release)).toBe(
      1
    )
  })

  test('flags a newer fork release published after the build day', () => {
    const release = {
      tag_name: 'custom-20261126-abcdef1',
      prerelease: false,
      published_at: '2026-11-26T09:00:00Z',
    }
    expect(compareCustomBuildRelease('custom-20261124-53a71f8', release)).toBe(
      -1
    )
  })

  test.each([
    ['custom-20261124-53a71f8', '2026-11-28T10:00:00Z', 0],
    ['custom-20261125-53a71f8abcdef', '2026-11-28T10:00:00Z', 0],
    ['custom-20261124-abcdef1', '2026-11-28T10:00:00Z', 0],
    ['custom-20261123-abcdef1', '2026-11-28T10:00:00Z', 1],
    ['custom-20261126-abcdef1', '2026-11-20T10:00:00Z', -1],
  ] as const)(
    'compares fork tag %s by build identity/date despite publication %s',
    (tag_name, published_at, expected) => {
      expect(
        compareCustomBuildRelease('custom-20261124-53a71f8', {
          tag_name,
          published_at,
          prerelease: false,
        })
      ).toBe(expected)
    }
  )

  test('selects newer fork build dates instead of republished older tags', () => {
    expect(
      selectLatestRelease([
        {
          tag_name: 'custom-20261123-abcdef1',
          published_at: '2026-11-28T10:00:00Z',
          prerelease: false,
          draft: false,
        },
        {
          tag_name: 'custom-20261124-53a71f8',
          published_at: '2026-11-24T10:00:00Z',
          prerelease: false,
          draft: false,
        },
      ])?.tag_name
    ).toBe('custom-20261124-53a71f8')
  })

  test('treats a same-day release as not newer', () => {
    const release = {
      tag_name: 'v1.0.0',
      prerelease: false,
      published_at: '2026-11-24T08:00:00Z',
    }
    expect(compareCustomBuildRelease('custom-20261124-53a71f8', release)).toBe(
      1
    )
  })

  test.each([
    ['v1.0.0-rc.52', true],
    ['v1.0.0-rc.52', false],
    ['custom-20261126-abcdef1', true],
  ] as const)(
    'ignores prerelease %s with flag %s for a stable fork build',
    (tag_name, prerelease) => {
      expect(
        compareCustomBuildRelease('custom-20261124-53a71f8', {
          tag_name,
          prerelease,
          published_at: '2026-12-01T00:00:00Z',
        })
      ).toBe(0)
    }
  )
})
