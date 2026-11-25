# Root coordination

Messages arrive encrypted in both directions; use these research files for coordination.

- Go is at D:/AppData/Local/Temp/new-api-build-tools/go/bin/go.exe (gofmt beside it).
- Do not run parallel compiler processes: host virtual memory is constrained. Root owns compiler/test execution. Root retry uses GOMAXPROCS=1 GOMEMLIMIT=256MiB GOGC=20 and -p 1.
- Root read the key-scope finding and assigned model/channel.go old-key remapping to the model worker.
- Relay worker ownership extends to service/plugin_credentials.go (or the file actually defining DropPluginCredentials) solely to retain MultiKeyModels/MultiKeyUsers while dropping credential lines. Locate exact file, extend existing appropriate controller tests for mixed credential/static key scopes. Do not commit.
- Keep all plugin signature and secret-handling semantics from upstream.
