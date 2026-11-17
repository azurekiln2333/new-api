// PI-3 no-checkin build step: bundles the fixture entry against the workspace
// sources so the browser runs the real components and the real Tailwind CSS.
import { build } from 'bun'
import { copyFileSync } from 'node:fs'
import path from 'node:path'

const webRoot = 'D:/My2KProjects/new-api/web'
const outDir = 'D:/AppData/Local/Temp/pi3-fixture/dist'

const result = await build({
  entrypoints: ['D:/AppData/Local/Temp/pi3-fixture/entry.tsx'],
  outdir: outDir,
  target: 'browser',
  format: 'iife',
  minify: false,
  sourcemap: 'none',
  define: { 'process.env.NODE_ENV': '"production"' },
  naming: { entry: 'bundle.js' },
  plugins: [
    {
      name: 'workspace-resolve',
      setup(build) {
        // Resolve bare package specifiers from the workspace node_modules.
        build.onResolve({ filter: /^[^.@/]/ }, (args) => {
          if (args.path.startsWith('node:')) return
          const candidate = path.join(webRoot, 'node_modules', args.path)
          const fs = require('node:fs')
          for (const ext of ['.js', '.mjs', '.cjs', '/index.js']) {
            if (fs.existsSync(candidate + ext)) return { path: candidate + ext }
          }
          if (fs.existsSync(path.join(candidate, 'package.json'))) {
            const pkg = JSON.parse(fs.readFileSync(path.join(candidate, 'package.json'), 'utf8'))
            const main = pkg.module || pkg.main || 'index.js'
            return { path: path.join(candidate, main) }
          }
          return
        })
        // Map the workspace alias the project uses onto the real source tree.
        build.onResolve({ filter: /^@\// }, (args) => {
          const base = path.join(webRoot, 'src', args.path.slice(2))
          for (const ext of ['.tsx', '.ts', '.jsx', '.js', '/index.tsx', '/index.ts']) {
            const candidate = base + ext
            if (require('node:fs').existsSync(candidate)) return { path: candidate }
          }
          return { path: base }
        })
      },
    },
  ],
})

if (!result.success) {
  console.error('BUILD FAILED')
  for (const log of result.logs) console.error(String(log))
  process.exit(1)
}

copyFileSync('D:/My2KProjects/new-api/web/dist/static/css/index.fec5a6e011.css', path.join(outDir, 'bundle.css'))
copyFileSync('D:/AppData/Local/Temp/pi3-fixture/index.html', path.join(outDir, 'index.html'))
console.log('BUILD OK ->', outDir)
