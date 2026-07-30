# platform-billing-operator

A Kubernetes operator that compiles billing Plans into Kuadrant enforcement policies (TokenRateLimitPolicy, RateLimitPolicy, AuthPolicy) on a Gateway API gateway. Part of the "X as a Business" billing and metering layer for OpenShift AI.

See [docs/getting-started.md](docs/getting-started.md) for prerequisites, installation, and usage.

## Quick start (local development)

```bash
# Create a kind cluster with the full Kuadrant stack.
make dev-cluster

# Build and deploy the operator.
make docker-build IMG=controller:latest
make deploy IMG=controller:latest

# Run the M0 end-to-end demo.
make demo-m0
```

## CRDs

| CRD | Purpose |
|-----|---------|
| `PriceBook` | SKU pricing catalog with immutability after effectiveFrom |
| `Plan` | Rate limits and model-scope access, compiled to Kuadrant CRs |

## Contributing

Run `make help` for available targets. Run `make test && make lint && make money-safety` before committing.

## License

Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

