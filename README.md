# go-judgy

A distributed LLM-as-judge evaluation system built on Temporal for reliable, scalable evaluation workflows with comprehensive budget controls and multi-provider LLM support.

[![CI](https://github.com/ahrav/go-judgy/workflows/CI/badge.svg)](https://github.com/ahrav/go-judgy/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/ahrav/go-judgy)](https://goreportcard.com/report/github.com/ahrav/go-judgy)
[![Coverage](https://codecov.io/gh/ahrav/go-judgy/branch/main/graph/badge.svg)](https://codecov.io/gh/ahrav/go-judgy)
[![Go Reference](https://pkg.go.dev/badge/github.com/ahrav/go-judgy.svg)](https://pkg.go.dev/github.com/ahrav/go-judgy)

## What is go-judgy?

go-judgy implements the LLM-as-judge pattern at scale: given a question, it generates multiple candidate answers using LLMs, scores each answer with LLM judges, and produces a final verdict through configurable aggregation (mean, median, trimmed mean). The entire evaluation pipeline runs on [Temporal](https://temporal.io/) workflows for reliability, fault tolerance, and budget enforcement.

**Core workflow**: Question → Generate Multiple Answers → Score with LLM Judges → Aggregate Scores → Final Verdict

## Key Features

- **Multi-Provider LLM Support**: Works with OpenAI, Anthropic, Google, and other HTTP-based LLM providers
- **Budget Control**: Pre-authorization and reconciliation system prevents cost overruns with token/call/cost limits
- **Reliable Orchestration**: Temporal workflows handle retries, timeouts, and failure recovery automatically
- **Schema Enforcement**: Strict JSON validation for judge responses with repair-once policy
- **Comprehensive Observability**: Full tracing, metrics, and logging with request correlation
- **Deterministic Evaluation**: Reproducible results with seed control and idempotent operations
- **Cloud-Native**: Kubernetes-ready with full observability stack (LGTM)

## Quick Start

### Prerequisites

- [Go 1.24.5+](https://golang.org/doc/install), [Docker](https://docs.docker.com/get-docker/), [kind](https://kind.sigs.k8s.io/docs/user/quick-start/), [kubectl](https://kubernetes.io/docs/tasks/tools/)
- LLM provider API keys (OpenAI, Anthropic, or Google)

### Get Running

```bash
git clone https://github.com/ahrav/go-judgy.git
cd go-judgy
make cluster  # Creates local Kubernetes cluster with Temporal + observability stack
make build deploy  # Builds and deploys the evaluation system
```

### Run an Evaluation

```bash
# Via CLI
./bin/cli evaluate "What are the benefits of renewable energy?"

# Via kubectl job
kubectl create job evaluation --from=cronjob/evaluation-job --dry-run=client -o yaml > job.yaml
kubectl apply -f job.yaml
```

## Development

### Local Development

```bash
make test          # Run all tests with race detection
make lint          # Code linting and formatting
make pre-commit    # Full validation before commit
```

### Configuration

Copy `.env.example` to `.env` and configure your LLM provider API keys and endpoints. See [Configuration Guide](docs/configuration.md) for details.


## Observability

When running locally, access the monitoring stack at:

- **Grafana**: http://localhost:3000 (admin/admin) - Dashboards for evaluation metrics, LLM performance, cost tracking
- **Prometheus**: http://localhost:9090 - Metrics and alerting
- **Loki**: http://localhost:3100 - Centralized logging with structured JSON logs
- **Tempo**: http://localhost:3200 - Distributed tracing with request correlation
- **Temporal Web UI**: http://localhost:8080 - Workflow execution history and debugging


NOTE: This is all for fun, don't take any of it too seriously.


## License

MIT License - see [LICENSE](LICENSE) file for details.
