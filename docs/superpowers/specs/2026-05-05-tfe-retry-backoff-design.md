# TFE API Retry & Poll Error Tolerance

**Date:** 2026-05-05  
**Status:** Approved

## Problem

TFE API calls in tfbuddy have no retry or backoff logic. A single transient failure (rate limit, 5xx, network blip) propagates as a hard error, aborting the entire plan/apply workflow. The VCS clients (GitHub, GitLab) already use `cenkalti/backoff/v4` for retries; the TFE client has none.

Additionally, the two polling loops that wait for long-running TFE operations crash or abort on transient read errors mid-poll.

## Scope

- Enable `RetryServerErrors` on the TFE client config
- Make `pollCVWhilePending` tolerate transient read errors
- Make `waitForRunCompletionOrFailure` tolerate transient read errors and guard against nil-run panic

Out of scope: exponential backoff on polling intervals, `cenkalti/backoff` wrappers on individual TFE calls.

## Design

### 1. TFE Client Config (`pkg/tfc_api/api_client.go`)

Change `NewTFCClient` to set `RetryServerErrors: true` on `tfe.Config`:

```go
config := &tfe.Config{
    Token:             token,
    RetryServerErrors: true,
    RetryLogHook: func(lvl retryablehttp.LogLevel, msg string, args ...interface{}) {
        log.Debug().Msgf("TFE retry: %s %v", msg, args)
    },
}
```

The `go-tfe` client wraps `go-retryablehttp` internally. With `RetryServerErrors: true`:
- **429 rate limits**: backed off using the `Retry-After` header (already active by default)
- **5xx server errors**: linear jitter backoff, 100ms–400ms wait, up to 30 retries

The `RetryLogHook` makes retries visible in zerolog output, consistent with the rest of the codebase.

### 2. CV Polling Error Tolerance (`pkg/tfc_api/api_driven_runs.go`)

`pollCVWhilePending` currently returns immediately on any read error. After `go-tfe` exhausts its own retries, a still-failing call should not abort the upload — it should log and continue to the next poll iteration, respecting the existing 30-iteration / 30-second total timeout.

```go
func (c *TFCClient) pollCVWhilePending(ctx context.Context, cv *tfe.ConfigurationVersion) (*tfe.ConfigurationVersion, error) {
    for i := 0; i < 30; i++ {
        cv, err := c.Client.ConfigurationVersions.Read(ctx, cv.ID)
        if err != nil {
            log.Warn().Err(err).Msg("transient error reading CV status, retrying")
            time.Sleep(1 * time.Second)
            continue
        }
        if cv.Status != tfe.ConfigurationPending {
            return cv, nil
        }
        time.Sleep(1 * time.Second)
    }
    return nil, fmt.Errorf("timed out waiting for CV to move from pending")
}
```

### 3. Run Status Polling Error Tolerance (`pkg/tfc_utils/ci_job_run_status.go`)

`waitForRunCompletionOrFailure` currently logs the error but proceeds to call `printRunInfo(run, ...)` with a potentially nil `run`, which will panic. Fix: skip the iteration on error.

```go
run, err := tfcClient.GetRun(ctx, runID)
if err != nil {
    log.Printf("transient error reading run %s, retrying: %v\n", runID, err)
    continue
}
```

## Files Changed

| File | Change |
|------|--------|
| `pkg/tfc_api/api_client.go` | Set `RetryServerErrors: true` + `RetryLogHook` on `tfe.Config` |
| `pkg/tfc_api/api_driven_runs.go` | `pollCVWhilePending`: continue on error instead of returning |
| `pkg/tfc_utils/ci_job_run_status.go` | `waitForRunCompletionOrFailure`: continue on error, guard nil run |

## Testing

- `RetryServerErrors` behavior is tested by `go-tfe` itself — no new unit tests needed for that.
- The polling loop changes eliminate a nil-run panic path — existing mock-based tests in `ci_job_runner_test.go` should continue to pass.
- Verify manually: run existing test suite (`go test ./...`).

## Risks

- **Double-retry on polling read errors**: `GetRun` inside the polling loop will itself retry up to 30 times via `go-retryablehttp` before returning an error. The loop then waits 10s and retries again. This is intentional — the outer loop is about polling state changes, not HTTP reliability.
- **RetryMax = 30 at 100–400ms**: worst case ~12s of retries per call. Acceptable for plan/apply operations which are already long-running.
