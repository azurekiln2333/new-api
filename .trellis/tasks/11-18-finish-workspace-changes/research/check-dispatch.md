# Root to integration reviewer

Current full go test log: D:/AppData/Local/Temp/new-api-root-test.log. Controller only assertion failure so far is TestMultiKeyManageKeepsKeyScopesOnTheirKey (maps remained at old indexes after deletion). Model worker is fixing locked remap.

Approved ownership extension: pkg/jsplugin/routing.go (existing plugin runtime). TestReservedRouteNamespacesCoverConsolePages fails because custom /invitation-codes route is absent from reserved namespaces. Add this existing console path to the appropriate reserved list, read applicable plugin spec first. Existing test already protects behavior; no extra test file needed.

relay/channel test compile reported transient import artifact `open : The system cannot find the file specified` for pkg/requestpatch; focused test had passed before. Root will rerun after all edits.
