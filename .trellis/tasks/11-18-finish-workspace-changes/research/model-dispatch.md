# Root coordination

Messages arrive encrypted in both directions. Read relay-repair.md: UpdateChannelLocked writes the key before Channel.Update; remap custom MultiKeyModels/MultiKeyUsers against the locked old key, retain the same index on credential rotation, and prevent stale maps being restored by Channel.Update. This is inside your model/channel.go ownership. Relay worker now owns DropPluginCredentials service logic and existing controller tests. Coordinate through model-to-relay.md if needed.

Go is D:/AppData/Local/Temp/new-api-build-tools/go/bin/go.exe. Do not run simultaneous builds: root owns constrained local compiler/test execution and CI. Run gofmt as needed.

Approved ownership extension: model/channel_plugin_credential.go and its existing test file, since UpdateChannelLocked is defined there. Root requests model implementation own old-key remapping, with a reusable model operation controller can call if needed. Coordinate controller changes with relay worker via research/model-to-relay.md.
