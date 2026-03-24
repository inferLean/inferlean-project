# InferLean

This repository contains a Go CLI with a deterministic analyzer and a separate corpus-driven recommender.

## Install CLI

```bash
curl -fsSL https://raw.githubusercontent.com/inferLean/inferlean-project/main/scripts/install_inferlean.sh | sh
```

## Build

```bash
go build ./cmd/infer-lean
```

The dashboard frontend lives in the [`frontend`](./frontend) git submodule.
If the submodule is missing locally, initialize it with:

```bash
git submodule update --init --recursive
```

## Backend API

Run an HTTP backend service:

```bash
go run ./backend \
  --listen :8080 \
  --database-url "postgres://postgres:postgres@localhost:5432/inferlean?sslmode=disable" \
  --dex-issuer-url "https://dex.example.com" \
  --dex-client-id "inferlean-backend" \
  --job-run-interval 10s \
  --job-batch-size 5 \
  --corpus-file ./corpus/qwen35_08b_rtx_pro_6000_seed.json
```

Endpoints:

- `GET /healthz`
- `POST /api/v1/trigger-job`
- `POST /api/v1/jobs/{job_uuid}/claim` (requires login; claims anonymous job)
- `GET /api/v1/jobs/{job_id}/collector`
- `GET /api/v1/jobs/{job_id}/analyze` (alias: `/analysis`)
- `GET /api/v1/jobs/{job_id}/recommend` (alias: `/recommendation`, requires login)
- `GET /swagger/index.html` (Swagger UI)

`POST /api/v1/trigger-job` accepts multipart upload with a `collector` file (the output JSON from `collect`) and optional form fields:

- `objective`: `balanced`, `throughput_first`, or `latency_first`
- `corpus_file`: local corpus file override for this request
- `set`: repeatable what-if override in `key=value` format
- `llm_enhance`: `true` or `false`

Example:

```bash
curl -X POST http://127.0.0.1:8080/api/v1/trigger-job \
  -F "collector=@./collector-report.json" \
  -F "objective=throughput_first" \
  -F "set=max_num_seqs=16" \
  -F "set=max_num_batched_tokens=16384"
```

Response JSON includes `job_id`, `job_uuid`, and `status=queued`.

The backend runs analyze/recommend asynchronously in a background loop:

- every `--job-run-interval`, it scans incomplete jobs
- it processes up to `--job-batch-size` jobs per loop
- a job is incomplete when collector input exists but analyze/recommend output is still null

Until processing completes:

- `GET /api/v1/jobs/{job_id}/analyze` returns `202` with `{"status":"pending", ...}`
- `GET /api/v1/jobs/{job_id}/recommend` returns `202` with `{"status":"pending", ...}` (and still requires login)

The backend persists every successful trigger into PostgreSQL table `jobs` with:

- nullable job owner (`owner`) from Dex user identity (email/username/sub)
- collector input JSON (`collector_input`)
- analyzer output JSON (`analyzer_output`)
- recommender output JSON (`recommender_output`)

Authentication behavior:

- If no bearer token is sent, request is treated as anonymous and `owner` stays `NULL`.
- If a valid Dex bearer token is sent, `owner` is persisted for that job.
- If a bearer token is sent but invalid, the endpoint returns `401`.
- Recommendation retrieval endpoint requires login and returns `401` when unauthenticated.
- Claim endpoint requires login and only claims jobs where `owner` is currently `NULL`.

Swagger docs are generated from API annotations in `backend/server.go`.

Regenerate docs:

```bash
make swagger
```

## Docker Compose

Provision Postgres, Dex, and backend together:

```bash
docker compose up --build
```

Services started:

- `nginx` on `localhost:3000` (routes `/api/*` to backend and all other paths to frontend)
- `postgres` (internal only; not exposed to host)
- `dex` (internal only; reachable through `http://localhost:3000/dex`)

The compose gateway proxies `/api/*` to backend, `/dex/*` to Dex, and all non-API routes to frontend, so browser calls stay same-origin in local compose.

Dex demo user from [`deploy/dex/config.yaml`](./deploy/dex/config.yaml):

- username: `demo`
- email: `demo@example.com`
- password: `password`
- client_id: `inferlean-backend`
- client_secret: `inferlean-backend-secret`

Frontend OAuth client:

- client_id: `inferlean-frontend`
- redirect_uri: `http://localhost:3000/callback`

Get an access token from Dex:

```bash
TOKEN=$(curl -sS -X POST http://localhost:3000/dex/token \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "grant_type=password&scope=openid+email+profile&username=demo&password=password&client_id=inferlean-backend&client_secret=inferlean-backend-secret" \
  | jq -r '.id_token')
```

Call trigger endpoint with authenticated owner:

```bash
curl -X POST http://localhost:3000/api/v1/trigger-job \
  -H "Authorization: Bearer $TOKEN" \
  -F "collector=@./collector-report.json"
```

Call trigger endpoint anonymously (owner stays `NULL`):

```bash
curl -X POST http://localhost:3000/api/v1/trigger-job \
  -F "collector=@./collector-report.json"
```

## Source-Run Dev Stack

If you want to run frontend and backend from local source code, while keeping only support services in Docker, use:

```bash
./scripts/dev_stack.sh start
```

This starts:

- frontend from source on `http://127.0.0.1:3000`
- backend from source on `http://127.0.0.1:8080`
- `postgres` in Docker on `127.0.0.1:5432`
- `dex` in Docker on `http://127.0.0.1:5556/dex`

Useful commands:

```bash
./scripts/dev_stack.sh status
./scripts/dev_stack.sh logs
./scripts/dev_stack.sh stop
```

The script does not modify or depend on `docker-compose.yml`; it is a separate local development path.

Dex demo login for the source-run stack:

- username: `dev`
- password: `admin123`

Sample collector file to upload in the dashboard:

- [`corpus/observed/qwen35_08b_rtx_pro_6000/latency_sensitive_queue_16_512.collector.json`](./corpus/observed/qwen35_08b_rtx_pro_6000/latency_sensitive_queue_16_512.collector.json)

## Collect

## Run

Run full flow in one command:

1. collect locally
2. trigger backend job upload
3. open dashboard in browser

```bash
inferlean run
```

`run` accepts the same flags as `collect`.

Backend/dashboard base URL is read from `INFERLEAN_BASE_URL` and defaults to `https://app.inferlean.com`:

```bash
INFERLEAN_BASE_URL=http://localhost:3000 inferlean run
```

Run with zero arguments (same as `run`):

```bash
./inferLean
```

This runs the full end-to-end workflow and auto-discovers vLLM on the host for the collection step.
If vLLM is not found, it exits with an error asking for `--config-file <path>`.
The CLI also auto-discovers vLLM version by calling the `vllm` binary itself. If the binary is not found, provide `--vllm-bin <path>`.

Run local collection and write a diagnosis-only JSON report file:

```bash
go run ./cmd/infer-lean collect \
  --output analysis-report.json \
  --vllm-version 0.17.1 \
  --deployment-type host \
  --config-file ./config.json \
  --workload-profile-file ./workload-profile.json
```

If `--metrics-file` is not provided, the CLI will:

1. start `node_exporter`
2. start `dcgm-exporter`
3. scrape vLLM metrics endpoint (default `127.0.0.1:8000/metrics`)
4. start `prometheus` with scrape targets for node exporter, dcgm-exporter, and vLLM
5. run advanced profilers (`bcc`, `py-spy`, `nsys`) by default during the same collection window
6. wait for `--duration-minutes` (default `10`)
7. query Prometheus range data and write `collected_metrics` into the final report JSON

If `dcgm-exporter` is unavailable (for example, missing `libdcgm.so`), collection continues with node exporter + vLLM metrics and records a warning in `metric_collection_outputs`.

Advanced profiling (enabled by default) includes:

- `bcc` profile capture for kernel-level/scheduler visibility
- `py-spy` stack dump for Python bottlenecks
- `nsys` profile capture for low-level GPU timeline data

These are written under `advanced_profiling_information` in the report JSON.

Tool auto-install behavior:

- if a tool path flag is empty, the CLI auto-detects and auto-installs the tool
- this applies to Prometheus, exporters, and profiling tools
- if profiling is disabled via flags, profiling tool installation is skipped

All profiling outputs and metric collection outputs are embedded in the final JSON (`advanced_profiling_information` and `metric_collection_outputs`) so no extra artifact file is required for reporting.

Flags:

- `--output`: report path, defaults to `analysis-report.json`
- `--vllm-version`: optional version override (otherwise auto-discovered from `vllm` binary)
- `--vllm-bin`: vLLM binary path (required only when auto-discovery cannot find `vllm`)
- `--vllm-version-timeout-seconds`: timeout for each version probe command (default: `150`)
- `--deployment-type`: `host`, `docker`, or `k8s`
- `--metrics-file`: optional JSON file that includes `collected_metrics` (time-labeled metrics). When set, Prometheus auto-collection is skipped.
- `--config-file`: optional JSON or YAML file with current vLLM config
- `--workload-profile-file`: optional JSON or YAML file with user-provided workload intent and reuse expectations
- `--collect-prometheus`: run exporter + Prometheus collection when `--metrics-file` is empty (default: `true`)
- `--duration-minutes`: collection window for metrics and profiling (default: `10`)
- `--prometheus-step-seconds`: Prometheus query step seconds (default: `30`)
- `--prometheus-bin`: Prometheus binary path (empty means auto-install/auto-detect)
- `--node-exporter-bin`: node_exporter binary path (empty means auto-install/auto-detect)
- `--dcgm-exporter-bin`: dcgm-exporter binary path (empty means auto-install/auto-detect)
- `--vllm-metrics-target`: vLLM Prometheus target host:port (default: `127.0.0.1:8000`)
- `--vllm-metrics-path`: vLLM metrics path (default: `/metrics`)
- `--prometheus-workdir`: working directory for temporary Prometheus files (default: temp dir)
- `--debug`: enable verbose debug logs for discovery/installation/collection
- `--enable-profiling`: enable advanced profiling collection (default: `true`)
- `--collect-bcc`: collect bcc profile output (default: `true`)
- `--collect-py-spy`: collect py-spy stack dump (default: `true`)
- `--collect-nsys`: collect Nsight Systems profile output (default: `true`)
- `--profiling-workdir`: directory for profiling artifacts/logs (default: `<prometheus-workdir>/profiling`)
- `--vllm-pid`: explicit vLLM process PID (default: auto-detect)
- `--bcc-bin`: bcc profile binary path (empty means auto-install/auto-detect)
- `--py-spy-bin`: py-spy binary path (empty means auto-install/auto-detect)
- `--nsys-bin`: nsys binary path (empty means auto-install/auto-detect)

The `collect` command prints the absolute path to the report JSON on success.

Analyzer output is diagnosis-only. It contains:

- environment and deployment details
- normalized `workload_profile`
- inferred `observed_workload_profile`
- `workload_profile_alignment` when user input was provided
- collected metrics
- `feature_summary`
- `current_load_summary` with current saturation, GPU load, queue pressure, and bottleneck tag
- current effective vLLM config
- `analysis_summary.data_quality`
- `analysis_summary.total_heuristic_improvement_pct`
- `analysis_summary.findings`
- optional `llm_enhanced`

It does not emit exact tuning recommendations.

Findings are ranked by heuristic importance. Present findings include per-finding `importance_score` and `heuristic_improvement_pct`.

## Analyze

Run the recommender against a collected report plus an optional local benchmark corpus:

```bash
go run ./cmd/infer-lean analyze \
  --corpus-file ./benchmark-corpus.json \
  --output recommendation-report.json
```

When `--objective` is omitted, the recommender uses `analysis.workload_profile.objective` and falls back to `balanced`.

Add explicit what-if changes to get an immediate scenario prediction:

```bash
go run ./cmd/infer-lean analyze \
  --corpus-file ./benchmark-corpus.json \
  --set max_num_seqs=16 \
  --set max_num_batched_tokens=16384
```

The recommender output contains:

- `matched_corpus_profile`
- `baseline_prediction`
- optional `capacity_opportunity`
- exact `recommendations[].changes`
- `predicted_effect` with metric deltas
- optional `scenario_prediction`
- optional `llm_enhanced`

The recommender consumes a local corpus file. A profile in the corpus should describe:

- `model_name`
- `model_family`
- `gpu_count`
- `hardware_class`
- `workload_class`
- `measurements[]`

Each measurement should include benchmarked parameters and measured metrics:

```json
{
  "version": "2026-03-21",
  "profiles": [
    {
      "id": "qwen3-30b-h100x4-throughput",
      "model_name": "Qwen 3 30B A3B",
      "model_family": "qwen3",
      "gpu_count": 4,
      "hardware_class": "h100",
      "workload_class": "throughput_headroom",
      "measurements": [
        {
          "parameters": {
            "max_num_seqs": 8,
            "max_num_batched_tokens": 8192
          },
          "metrics": {
            "throughput_tokens_per_second": 4200,
            "ttft_ms": 620,
            "latency_p50_ms": 1450,
            "latency_p95_ms": 2100,
            "gpu_utilization_pct": 24
          }
        }
      ]
    }
  ]
}
```

## Optional LLM Enhancement

Both `collect` and `analyze` support `--llm-enhance`.

When enabled, the command reads these env vars:

- `INFERLEAN_LLM_BASE_URL`
- `INFERLEAN_LLM_API_KEY`
- `INFERLEAN_LLM_MODEL`

The enhancer only writes a separate `llm_enhanced` block. It does not change canonical findings, parameter values, or predicted metrics.

## Input file examples

Example `metrics.json`:

```json
{
  "vllm_version": "0.17.1",
  "deployment_type": "docker",
  "collected_metrics": [
    {
      "time_label": "2026-03-20T10:00:00Z",
      "metrics": {
        "request_tps": 6,
        "latency_ms": 420,
        "gpu_utilization": 38
      }
    },
    {
      "time_label": "2026-03-20T10:01:00Z",
      "metrics": {
        "request_tps": 7,
        "latency_ms": 390,
        "gpu_utilization": 42
      }
    }
  ]
}
```

Example `config.json`:

```json
{
  "model_name": "qwen-3.5",
  "gpu_memory_utilization": 0.7,
  "max_num_batched_tokens": 8192,
  "max_num_seqs": 8,
  "tensor_parallel_size": 1
}
```

Example `workload-profile.json`:

```json
{
  "schema_version": "workload-profile/v1",
  "preset": "chatbot",
  "serving_pattern": "realtime_chat",
  "task_pattern": "multi_task",
  "objective": "latency_first",
  "prefix_reuse": "high",
  "media_reuse": "unknown",
  "notes": "Customer-facing chat workload with repeated system prompts."
}
```

Supported workload profile fields:

- `preset`: `chatbot`, `batch_single_task`, `batch_multi_task`, `mixed`, or `custom`
- `serving_pattern`: `realtime_chat`, `offline_batch`, `mixed`, or `unknown`
- `task_pattern`: `single_task`, `multi_task`, `mixed`, or `unknown`
- `objective`: `balanced`, `latency_first`, or `throughput_first`
- `prefix_reuse`: `high`, `low`, or `unknown`
- `media_reuse`: `high`, `low`, or `unknown`

If `preset` is provided, it initializes the other fields, and any explicit canonical fields in the file override the preset-derived values.
