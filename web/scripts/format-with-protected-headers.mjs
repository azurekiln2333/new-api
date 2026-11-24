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
import { spawnSync } from 'node:child_process'
import {
  mkdtempSync,
  readdirSync,
  readFileSync,
  rmSync,
  rmdirSync,
  statSync,
  writeFileSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join, relative } from 'node:path'

const mode = process.argv[2]

if (mode !== '--check' && mode !== '--write') {
  console.error(
    'Usage: node scripts/format-with-protected-headers.mjs --check|--write'
  )
  process.exit(2)
}

const root = process.cwd()
const requestedPaths = process.argv.slice(3)
const excludedDirs = new Set([
  '.git',
  '.tanstack',
  'build',
  'coverage',
  'dist',
  'node_modules',
])
const headerExtensions = new Set([
  '.cjs',
  '.cts',
  '.js',
  '.jsx',
  '.mjs',
  '.mts',
  '.ts',
  '.tsx',
])
const protectedHeaderPattern =
  /^\/\*\nCopyright \(C\)[\s\S]*?QuantumNous[\s\S]*?\*\/\n+/

function extensionOf(path) {
  const index = path.lastIndexOf('.')
  return index === -1 ? '' : path.slice(index)
}

function walk(dir, files = []) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      if (!excludedDirs.has(entry.name)) {
        walk(join(dir, entry.name), files)
      }
      continue
    }

    if (entry.isFile()) {
      files.push(join(dir, entry.name))
    }
  }

  return files
}

function snapshotFiles(files) {
  const snapshot = new Map()
  for (const file of files) {
    snapshot.set(file, readFileSync(file))
  }
  return snapshot
}

function restoreSnapshot(snapshot) {
  for (const [file, content] of snapshot) {
    writeFileSync(file, content)
  }
}

function stripProtectedHeaders(files) {
  const headers = new Map()

  for (const file of files) {
    if (!headerExtensions.has(extensionOf(file))) {
      continue
    }

    const content = readFileSync(file, 'utf8')
    const match = content.match(protectedHeaderPattern)
    if (!match) {
      continue
    }

    headers.set(file, match[0])
    writeFileSync(file, content.slice(match[0].length))
  }

  return headers
}

function restoreProtectedHeaders(headers) {
  for (const [file, header] of headers) {
    const content = readFileSync(file, 'utf8').replace(/^\n+/, '')
    if (!content.startsWith(header)) {
      writeFileSync(file, header + content)
    }
  }
}

function listChangedFiles(before, files) {
  const changed = []

  for (const file of files) {
    const previous = before.get(file)
    const current = readFileSync(file)
    if (!previous || !previous.equals(current)) {
      changed.push(relative(root, file))
    }
  }

  return changed
}

function escapeAnnotation(value, property = false) {
  const escaped = String(value)
    .replaceAll('%', '%25')
    .replaceAll('\r', '%0D')
    .replaceAll('\n', '%0A')
  return property
    ? escaped.replaceAll(':', '%3A').replaceAll(',', '%2C')
    : escaped
}

function reportFormatterDiff(file, original) {
  let temporaryDirectory
  try {
    temporaryDirectory = mkdtempSync(join(tmpdir(), 'new-api-format-diff-'))
    const originalFile = join(temporaryDirectory, 'original')
    writeFileSync(originalFile, original)
    const result = spawnSync(
      'git',
      [
        'diff',
        '--no-index',
        '--no-color',
        '--no-ext-diff',
        '--unified=3',
        '--',
        originalFile,
        join(root, file),
      ],
      { cwd: root, encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }
    )
    if (result.error || (result.status !== 0 && result.status !== 1)) {
      throw new Error(
        result.error?.message ||
          result.stderr?.trim() ||
          `git diff exited with status ${result.status}`
      )
    }
    if (!result.stdout) {
      throw new Error('git diff produced no output for changed file bytes')
    }

    // Chunk after escaping so every annotation stays bounded. Iterating code
    // points also avoids cutting Unicode characters or workflow escape tokens.
    const parts = []
    let part = ''
    let size = 0
    let truncated = false
    for (const character of result.stdout) {
      const escaped = escapeAnnotation(character)
      if (size + escaped.length > 60_000) {
        truncated = true
        break
      }
      if (part.length + escaped.length > 6000) {
        parts.push(part)
        part = ''
      }
      part += escaped
      size += escaped.length
    }
    if (part) parts.push(part)
    const annotationFile = escapeAnnotation(file, true)
    for (const [index, content] of parts.entries()) {
      const title = `Formatter diff part ${index + 1} of ${parts.length}`
      console.log(`::notice file=${annotationFile},title=${title}::${content}`)
    }
    if (truncated) {
      console.log(
        `::notice file=${annotationFile}::Formatter diff truncated after 60000 escaped characters.`
      )
    }
  } catch (error) {
    const message = String(error.message ?? error).slice(0, 1200)
    console.log(
      `::warning file=${escapeAnnotation(file, true)}::${escapeAnnotation(
        `Could not report formatter diff: ${message}`
      )}`
    )
  } finally {
    if (temporaryDirectory) {
      try {
        rmSync(join(temporaryDirectory, 'original'), { force: true })
        rmdirSync(temporaryDirectory)
      } catch (error) {
        const message = String(error.message ?? error).slice(0, 1200)
        console.log(
          `::warning file=${escapeAnnotation(file, true)}::${escapeAnnotation(
            `Could not remove formatter diff temporary file: ${message}`
          )}`
        )
      }
    }
  }
}

const files = (
  requestedPaths.length > 0
    ? requestedPaths.map((path) => join(root, path))
    : walk(root)
).filter(
  (file) => statSync(file).isFile() && statSync(file).size < 10 * 1024 * 1024
)
const before = mode === '--check' ? snapshotFiles(files) : null
let headers = new Map()
let exitCode = 0

try {
  headers = stripProtectedHeaders(files)
  const result = spawnSync(
    'oxfmt',
    [
      '-c',
      '.oxfmtrc.json',
      '--ignore-path',
      '.gitignore',
      '--write',
      ...(requestedPaths.length > 0 ? requestedPaths : ['.']),
    ],
    {
      cwd: root,
      stdio: 'inherit',
    }
  )
  exitCode = result.status ?? 1
  restoreProtectedHeaders(headers)

  if (mode === '--check' && exitCode === 0) {
    const changed = listChangedFiles(before, files)
    if (changed.length > 0) {
      console.error('Format issues found in protected-header-safe check:')
      for (const file of changed) {
        console.error(file)
        if (process.env.GITHUB_ACTIONS === 'true') {
          console.log(
            `::error file=${escapeAnnotation(file, true)}::Frontend formatter would change this file`
          )
          reportFormatterDiff(file, before.get(join(root, file)))
        }
      }
      exitCode = 1
    }
  }
} finally {
  if (mode === '--check' && before) {
    restoreSnapshot(before)
  } else {
    restoreProtectedHeaders(headers)
  }
}

process.exit(exitCode)
