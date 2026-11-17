# PI-4 · CI 实际检查与本机对应命令

来源：`.github/workflows/ci.yml`（job `backend` / `frontend`，触发于 PR；`docker-fork.yml` 推送 `main` 后先 `workflow_call` 复用同一 CI）。

## CI 实际执行的检查

backend（ubuntu-latest，`env: GOWORK=off`，Go 版本取 `go.mod`）：

1. `mkdir -p web/dist && touch web/dist/index.html`（前端 embed 占位，否则 `go build` 失败）
2. `go vet ./...`（root）
3. `go vet ./...`（`relaykit/`，`working-directory`）
4. `go vet ./...`（`tokenkit/`）
5. `go build ./...`（root / `relaykit/` / `tokenkit/` 各一次）
6. `make test` = root 模块非根包 `GOWORK=off go test <pkgs>` + `relaykit` `go test ./...` + `tokenkit` `go test ./...`

frontend（`working-directory: web`，bun 1.4.4）：

1. `bun install --frozen-lockfile`
2. `bun check -b`（类型检查）
3. `bun run test`（vitest run）
4. `bun run build`（rsbuild）

CI 不执行 `bun run lint`、`format:check`、`i18n:sync`；这些是 `web/AGENTS.md` 的本地要求，属本次改动范围时仍要跑。

## 本机对应命令（Windows，PowerShell）

本机 PATH 里没有 `go` / `bun` / `make`，用下面的绝对路径（已实测可用）：

```powershell
$go  = "D:\AppData\Local\Temp\new-api-cx-a-tools\go\bin\go.exe"                        # go1.26.10，与 go.mod 一致
$bun = "D:\AppData\Local\Temp\new-api-cx-a-tools\bun144\bun-windows-x64\bun.exe"       # bun 1.4.4，与 CI 一致
cd D:\My2KProjects\new-api
$env:GOWORK = "off"          # 与 CI 一致；不设会用 go.work（若存在）
```

### 后端（等同 backend job）

```powershell
# 1. embed 占位（web/dist 已存在时跳过）
if (-not (Test-Path web\dist\index.html)) { New-Item -ItemType Directory -Force web\dist | Out-Null; New-Item -ItemType File web\dist\index.html | Out-Null }

# 2. vet
& $go vet ./...
Push-Location relaykit; & $go vet ./...; Pop-Location
Push-Location tokenkit; & $go vet ./...; Pop-Location

# 3. build
& $go build ./...
Push-Location relaykit; & $go build ./...; Pop-Location
Push-Location tokenkit; & $go build ./...; Pop-Location

# 4. test（等价 make test；本机没有 make）
$rootPkgs = (& $go list -e ./... | Where-Object { $_ -ne (& $go list -m) })
& $go test $rootPkgs
Push-Location relaykit; & $go test ./...; Pop-Location
Push-Location tokenkit; & $go test ./...; Pop-Location
```

只测自己改过的包时（各包自检用）：

```powershell
& $go vet ./model/... ./controller/... ./middleware/... ./service/... ./dto/...
& $go test ./model/... ./controller/... ./middleware/... ./service/... -run "相关用例"
```

注意：`gofmt` 是 `<GOROOT>\bin\gofmt.exe`，即 `D:\AppData\Local\Temp\new-api-cx-a-tools\go\bin\gofmt.exe -l <文件>`。

### 前端（等同 frontend job）

```powershell
cd D:\My2KProjects\new-api\web
& $bun install --frozen-lockfile     # node_modules 已存在时可跳过
& $bun check -b                      # ✓ No type errors in 1,462 files across 2 projects（改动前基线）
& $bun run test
& $bun run build
& $bun run lint                      # CI 不跑，但 web/AGENTS.md 要求改到的文件无 error
& $bun run i18n:sync                 # 改过文案时校验七语言
```

`bun check -b` 的 ✓ 行是绿色成功输出，PowerShell 可能整行染红，看文案而非颜色。

## 已实测（改动前基线）

- `& $go build ./...` → exit 0，约 26s。
- `& $bun check -b`（web/）→ `✓ No type errors in 1,462 files across 2 projects`。
- `git status --short` 干净；`.trellis/` 由 `.git/info/exclude:9` 排除，不会进提交。

## 数据库

本任务不改表结构 / GORM 标签 / 迁移 / 原生 SQL，按 `design.md` 第 10 节不做三库矩阵；若集成审查发现实际有此类改动，另按 AGENTS.md 处理并在 FINAL.md 记录。本机未见 MySQL / PostgreSQL 实例。
