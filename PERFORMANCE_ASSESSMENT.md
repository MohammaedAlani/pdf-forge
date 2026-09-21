# Performance assessment

Original assessment: 2026-09-21 against commit `722edf2`. The initial review did not change application code; see the implementation update below for subsequent changes and measurements.

The service has useful concurrency controls and metrics, but its current rendering lifecycle, admission control, and memory handling need work before claiming high-throughput readiness. No production capacity or large-document guarantee can be established from this assessment.

## Measured baseline

Local Go 1.25.1, macOS ARM64, 11 logical CPUs, installed Google Chrome, `MAX_WORKERS=4`. Server bound to loopback. Each request posted the same small, single-page HTML document without external resources to `/html`. The client read the complete response and checked HTTP 200 and a PDF header. No post-processing was requested.

| Client concurrency | Requests | Successful | Throughput (requests/s) | Median latency | Maximum latency |
|---|---:|---:|---:|---:|---:|
| 1 | 4 | 4 | 0.425 | 2.051 s | 3.279 s |
| 4 | 8 | 8 | 1.422 | 2.758 s | 2.825 s |
| 8 | 8 | 8 | 1.141 | 5.267 s | 7.005 s |

These are short, sequentially executed test rounds, without a separate request warm-up. The first round includes first-request effects. Throughput is total completions divided by round wall time. The sample is too small for reliable tail percentiles or sustained-capacity estimates. The concurrency-eight result demonstrates queueing under this workload; it does not establish a universal throughput regression. This host has no container resource limits matching production. CPU, peak memory, and temporary disk usage were not profiled.

Validation: `go test -race ./...` and `go vet ./...` passed. The existing health test is skipped and handler tests mainly cover validation; these results do not establish rendering or post-processing correctness. The local linker emitted duplicate-library and macOS library-version warnings. No existing Go benchmarks were found. `qpdf` and `gs` were not available on PATH, so their execution paths were assessed from code only.

## Findings, in priority order

### 1. High: each conversion starts a new browser

Evidence: `internal/converters/chrome.go:56-67`, `:186`, `:243`.

Startup creates a browser and immediately cancels it. Each subsequent conversion creates a context from the allocator, which has no live browser to inherit. The pinned chromedp v0.11.2 implementation confirms that the first `Run` allocates a browser whenever the context has no browser. Sharing an allocator is not browser reuse.

Impact: repeated process startup, browser initialization, temporary profiles, and teardown add CPU, memory churn, and latency to every document.

Change: maintain a bounded pool of live browser contexts, create isolated job contexts within that pool, and handle browser crashes and recycling. Preserve request isolation and propagate cancellation without cancelling the shared browser owner.

### 2. High: every successful render waits at least 1.5 seconds

Evidence: `internal/converters/chrome.go:28-29`, `:212`, `:252`.

HTML and URL conversions always sleep while holding a worker slot. Markdown, images, templates, and tables also use the HTML path. With four workers, this delay alone imposes a theoretical ceiling of about `4 / 1.5 = 2.67` successful renders per second, even before startup, rendering, and cleanup. This is a code-derived upper bound, not measured capacity.

Change: use bounded readiness checks for fonts, images, and an optional application-ready condition. Avoid an unconditional delay for self-contained documents. Validate visual correctness before removing the wait.

### 3. High: waiting requests are unbounded and cancellation is ignored

Evidence: `internal/converters/chrome.go:80-83`, `:182-189`, `:235-245`; `internal/handlers/handlers.go:57`; `internal/handlers/extended.go:323-340`.

The semaphore bounds active Chrome work, but acquisition is an unconditional channel send. The supplied request context is not connected to the rendering context, and the render timeout starts only after admission. Synchronous requests can accumulate indefinitely behind the semaphore and keep decoded bodies alive. Client disconnects do not stop queued or active Chrome work. Async admission is capped at 32, but the body is decoded before checking capacity; its five-minute context is also ignored by Chrome.

Change: introduce bounded admission before expensive body decoding, cancelable acquisition, explicit overload responses, and an end-to-end deadline covering queueing and execution. Connect request cancellation to the individual rendering job. Bound aggregate in-flight bytes as well as job count.

### 4. High: large payloads create multiple full-size memory representations

Evidence: `cmd/server/main.go` (`loadConfig`); `internal/handlers/handlers.go:62-69`, `:129-135`, `:203-204`; `internal/converters/chrome.go` (`detectImageMIME`, `ConvertImages`); `internal/handlers/extended.go:506-510`; `docker-compose.prod.yml:70-92`.

The default body limit is 500 MiB per request. JSON decoding retains complete strings; Base64 decoding adds binary buffers; raw HTML is read completely, formatted into JSON, and decoded again. Image MIME detection decodes the entire image. Batch responses retain raw PDFs even when non-merged results already hold their Base64 representations. Outputs are buffered completely.

Four near-limit requests can account for roughly 2 GiB of payload data alone, before extra copies and Chrome. The production Compose defaults specify 2G memory and a shared 500M `/tmp` tmpfs. Temporary input/output files, browser profiles, and image expansion compete for that space. Actual peaks depend on workload and object lifetimes; they were not measured. The deployed Kubernetes limits were not available in this repository.

Change: select endpoint limits from measured document sizes, enforce aggregate byte budgets, eliminate the raw-HTML JSON round trip, inspect only an image prefix for MIME detection, retain raw batch outputs only when merging, and use file-backed/streamed transfers where practical. Budget temporary storage explicitly.

### 5. High: PDF processing bypasses the Chrome concurrency limit

Evidence: `internal/handlers/extended.go:120-301`; `internal/handlers/handlers.go:110-119`; `internal/converters/processor.go:110` and other `exec.Command` calls.

Manipulation and merge endpoints have no equivalent worker bound. Conversion releases the Chrome slot before watermarking, metadata, or encryption. These stages can therefore accumulate independently. Processor subprocesses use `exec.Command` without a context or execution deadline. Manipulator subprocesses do use `CommandContext`, but the handler adds no explicit processing deadline. PDF-to-image work accepts caller-specified positive DPI without a maximum and retains all image outputs.

Change: introduce bounded processing concurrency and per-job resource limits, propagate deadlines to processor subprocesses, and cap pages, DPI, and output expansion. Do not assume HTTP write timeouts cancel computation.

### 6. Medium: batches are serial and retain all results

Evidence: `internal/handlers/extended.go:462-515`.

A single batch uses one renderer at a time even when other workers are idle. Ten items incur at least 15 seconds of fixed sleeps before other work. There is no item-count limit beyond the overall body limit, and response memory grows with accumulated output.

Change: process items with bounded parallelism through the shared admission mechanism, preserve ordering, and cap batch size and aggregate output. Avoid letting one batch occupy every worker indefinitely. Fix memory admission before adding parallelism.

### 7. Medium: slow webhook delivery occupies conversion job capacity

Evidence: `internal/handlers/extended.go:343-348`, `:399-427`; `internal/services/webhook.go` (`Send`).

An async slot remains occupied through storage upload and webhook retries. Slow destinations can fill all 32 slots even after rendering finishes. Default webhook delivery allows four attempts with 30-second HTTP timeouts plus retry delays, subject to the shared job context. The recorded async duration is captured before post-processing, upload, and webhook delivery, so it understates full job latency.

Change: separate bounded delivery capacity from conversion capacity, use durable delivery state if callbacks must survive restarts, and record each stage plus total completion time.

### 8. Medium: rate limiting and observability do not establish overload protection

Evidence: `internal/middleware/middleware.go` (`RateLimiter.Limit`); `internal/metrics/metrics.go`; `internal/handlers/extended.go` (`Batch`, `TableToPDF`); `internal/handlers/handlers.go:40-47`.

The rate limiter keys buckets by `RemoteAddr`, including the source port; opening new connections obtains new buckets. Its global mutex also covers timestamp filtering and full-map cleanup. It is disabled by default and is not an in-flight resource bound when enabled. Existing metrics omit queue wait, browser startup, and stage durations; batch/table paths do not call `metrics.Record`. Health reports Chrome as running without a live check.

Change: use a stable client identity with an explicit trusted-proxy policy, keep resource admission independent of rate limiting, instrument all conversion paths, and monitor the whole process tree/container because Go memory metrics omit Chrome and PDF subprocess memory.

## Verification plan after changes

1. Establish document classes: simple invoice, long report, image-heavy document, font-heavy document, URL with controlled assets, and representative merge/compress/rasterization inputs. Define required throughput, latency, document limits, and deployment resources.
2. Benchmark in the deployment image with fixed CPU/memory limits. Sweep worker counts 1, 2, 4, and 8 and client concurrency below, at, and above capacity. Use warm-up followed by sustained runs; compare the same fixtures before and after each change.
3. Capture p50/p95/p99 end-to-end latency, successful documents/second, queue time, overload responses, errors, CPU throttling, total container memory, temporary-space peaks, browser launches/restarts, and PDF validity. Add an arrival-rate test to expose overload rather than only closed-loop concurrency tests.
4. Exercise cancellation while queued and rendering, browser failure, slow callbacks, large bodies, and mixed manipulation traffic. Verify bounded memory/queues, prompt cancellation, and recovery after overload.
5. Validate visual output for fonts, images, and page breaks after changing readiness logic. Run a sustained soak test to detect resource accumulation.

Recommended implementation order: admission/cancellation and memory bounds; browser reuse and readiness; bounded PDF processing; batch/delivery improvements. Add stage metrics while implementing these changes so each gain can be measured. Raising `MAX_WORKERS` alone does not address these constraints.

## Implementation update

Implemented reusable Chrome with isolated job contexts and crash recovery;
resource readiness plus an optional application predicate; cancelable rendering;
request count/input byte admission before decoding; shared processing concurrency;
bounded PDF output and subprocess scratch growth; parallel ordered batches;
separate webhook delivery slots; background-job shutdown tracking; stable-IP rate
limiting; and expanded metrics. Removed unnecessary raw-HTML and image MIME copies.
The README and environment/Compose examples document new limits and compatibility
changes. Container builds now cache pinned dependencies and use the target
architecture; the deployment workflow runs tests and vet before publishing.

Repeating the original local workload (same host, four workers, 20 total requests,
no separate request warm-up) produced:

| Client concurrency | Successful | Throughput (PDFs/s) | Median latency | Maximum latency |
|---|---:|---:|---:|---:|
| 1 | 4/4 | 5.044 | 0.186 s | 0.252 s |
| 4 | 8/8 | 6.825 | 0.578 s | 0.679 s |
| 8 | 8/8 | 7.114 | 0.927 s | 1.123 s |

At concurrency four, observed throughput increased about 4.8x and median latency
fell about 79%. This remains a short local comparison, not a production capacity
or sustained tail-latency guarantee. Reproduce with `python3 scripts/performance.py`
against a disposable server on loopback port 18089.

The implementation intentionally retains some boundaries: outputs still reside
in bounded buffers; the input budget does not bound all Go/Chrome memory; pure-Go
PDF routines cannot be interrupted within a library call; subprocess scratch
checks can overshoot between polls; and async work is not durable. Resource limits
must be tuned and sustained load/visual regression tests run with real documents
before establishing a production service-level objective.

Validation completed:

- Race-enabled unit and browser integration tests, plus `go vet ./...`.
- Browser reuse, cookie isolation, delayed/lazy image readiness, request
  cancellation, browser crash recovery, batch ordering, and invalid Base64.
- Admission before body reads, retained async reservations through callbacks,
  per-IP limits across source ports, and subprocess deadline/scratch guards.
- A 24-request overload test returned 16 successful PDFs and eight HTTP 503
  responses, then reported all four workers available and healthy.
- Linux/amd64 compilation and an ARM64 Docker runtime build. The runtime smoke
  test used two CPUs, 2 GiB memory, 256 MiB shared memory, and a 500 MiB tmpfs.
  Rendering, split, merge, compression, rasterization at 72 DPI, watermarking,
  metadata, encryption, and batch merge passed using the bundled tools.
- The pinned DevTools bindings can log unknown-event enum warnings with Chrome
  153 during loopback tests; the tested operations pass. This does not establish
  compatibility with every browser event or future Chrome release.

The smoke suite is reproducible with `python3 scripts/smoke.py --url URL` against
a disposable server with Chrome, qpdf, Ghostscript, and Poppler installed. These
checks verify PDF signatures, page counts, image dimensions, operation success,
and lifecycle behavior; they are not a full visual regression or soak suite.
