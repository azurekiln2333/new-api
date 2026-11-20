# Root-owned admission API (available now)

`admission := service.NewChannelDiagnosticAdmission(ctx, userID)`; background/management uses userID 0 (explicit trusted internal bucket), never fabricate a positive ID. `defer admission.Close()`.

`lease, apiErr := admission.Acquire(ctx, channelID, settings)` is fixed-channel, uses existing shared wait coordinator/backoff, bounded deadline and queue quotas. Reuse one admission owner across retries so deadline does not reset. Caller must revalidate latest channel/settings/key/model after grant and release/reacquire if cap changed. Set outbound request context to lease.Context(); defer Release until upstream body is fully handled/closed. Call MarkUpstreamAvailable only after real successful upstream evidence. No nested Acquire in callers already owning a lease.

Unsaved id0 returns an uncounted cancellable lease; no shared capacity identity. This API is for trusted server console/background callers, not API authorization. Ordinary NewChannelWaiter(ctx,0).Wait remains unauthorized.

Review correction: Acquire on unlimited channels calls existing best-effort telemetry admission, preserving availability on Redis failure. Limited channels queue. Explicitly recognized upstream-busy retry calls admission.Wait(ctx,channelID,settings), including for unlimited channels, after ReportChannelUpstreamBusy and release. Generic 429/quota/transport errors are not busy triggers.

Pi1 integrates in channel-test.go and handles snapshot/key revalidation there. Pi4 uses at HTTP helper body-lifetime boundary. Pi2 no longer owns service/channel_wait.go or service/channel_diagnostic_capacity.go; root owns these files and their existing service/channel_wait_test.go regressions.
