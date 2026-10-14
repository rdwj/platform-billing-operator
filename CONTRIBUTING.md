# Contributing

Thanks for your interest in the platform billing operator. This document covers how to get a
development environment running, the rules this codebase enforces, and what a reviewable change
looks like.

The project is pre-1.0. CRDs are at `v1alpha1` and metric names are not yet stable, so expect
breaking changes between releases.

## Licensing

The project is Apache-2.0. Contributions are accepted under the same license. By opening a pull
request you confirm you have the right to submit the work under those terms.

## Development environment

You need Go (see `go` directive in `go.mod` for the current version), Podman or Docker for
testcontainers, and `kind` for cluster-backed tests. Rust with the `wasm32-wasip1` target is only
needed if you are working on the capture filter.

```bash
make dev-cluster     # kind + Gateway API + Kuadrant + CNPG + fake-openai
make deploy-dev      # build and deploy the operator into it
make demo-m0         # end-to-end walkthrough, demo-m0 through demo-m6 exist
```

`make help` lists every target.

One environment trap worth knowing: the dev gateway must be Istio-based. Production runs
`data-science-gateway-class` with controller `openshift.io/gateway-controller/v1`. An Envoy
Gateway dev environment reports filter attachment as working when it is not, so tests pass
locally and fail on a real cluster.

## Rules the codebase enforces

`CLAUDE.md` holds the full list of nine non-negotiable rules. Four of them are enforced
mechanically and will fail your build:

**Money is never a float.** Money is `shopspring/decimal` in Go, decimal strings with pattern
validation in CRDs, and `NUMERIC(18,6)` in SQL. `make money-safety` greps for `float32` and
`float64` near money-related identifiers and fails the build on a match. A float money field is
treated as a defect, not a style preference.

**`TODO(money)` means unshippable.** The same target fails on any `TODO(money)` in Go source. Use
it freely while working, but it cannot survive into a merge.

**The rater is a pure function.** Identical ledger state and PriceBook revision must produce
byte-identical charges. `make rater-determinism` runs the determinism tests ten times to catch map
iteration order and unstable sorts. If you touch `internal/rater/`, run it before you push.

**Fail degraded, never free.** No error path may remove or loosen a rate limit. An allowance
failure holds the last known grant or degrades to plan defaults. Falling through to unlimited is a
correctness bug. Cover it with a test rather than a comment.

The remaining rules are review-enforced: money is never authoritative in etcd, enforcement writes
Kuadrant CRs rather than reimplementing Limitador, `contracts/usage.v1.schema.json` and
`api/openapi.yaml` are interfaces that do not break, ingest is at-least-once and idempotent on
`record_id`, only `ledger-svc` holds database credentials and only `psp-connector` holds payment
provider credentials, and metric labels stay bounded to `tenant`, `model`/`sku`, and `geo`.

## Before you open a pull request

```bash
make test && make lint && make money-safety
```

CI runs seven gates on every pull request: lint, unit tests with per-package coverage checks,
money safety, rater determinism, generated manifest verification, ledger integration tests, and a
container image build. Running the three above locally catches most failures in seconds.

Coverage thresholds live in `hack/coverage-thresholds.txt` as per-package minimums. Raise them
when coverage improves. Do not lower one to make a build pass. If a change genuinely needs a lower
threshold, say why in the pull request and expect it to be discussed.

If you change API types, run `make manifests generate` and commit the result. The
`verify-manifests` gate fails when generated output differs from what is committed.

## Commits and pull requests

Use conventional commit format in the imperative mood, for example
`rater: add proration for mid-cycle plan changes`. The body explains why the change is needed and
gives context a reviewer would not get from the diff alone. Skip detail that is obvious from the
diff itself.

Keep commits logically separate. A refactor that enables a fix reviews better as its own commit
than folded into the fix.

Open pull requests against `main`. Describe what changed and why, note anything a reviewer should
look at closely, and say how you tested it. Draft pull requests are welcome for work in progress.

## Architecture decisions

Significant decisions are recorded as ADRs in `docs/adr/`. If a change resolves an open decision,
update the ADR in the same pull request rather than settling it implicitly in code. Reviewers will
ask for this.

Design documentation beyond `docs/` is maintained in a separate internal repository. If you need
context that is not in this repo to evaluate a change, open an issue and ask. The gap is ours to
close, not yours to work around.

## Reporting bugs

Open an issue with the operator version or commit, the OpenShift or Kubernetes version, the
Connectivity Link or Kuadrant version, what you expected, and what happened. Relevant CR YAML and
controller logs help. Redact credentials, customer identifiers, and internal hostnames before
pasting.

For anything with a security impact, do not open a public issue. Contact the maintainer listed in
the repository metadata directly.
