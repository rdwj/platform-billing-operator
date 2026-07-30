# samples — config/samples seeds

Working CRs for `config/samples/` in the implementation repo (Session D/E), aligned three ways: API group `billing.opendatahub.io/v1alpha1` (Q17), field shapes from `operator-plan/crds.md`, and numbers from `contracts/rating-vectors.v1.json` — so `make demo-m0` applies these, the policy compiler emits Kuadrant CRs for the Plan, and the same PriceBook rates the golden-vector events to the same totals shown in the demo. All money values are decimal strings; a float in any of these files should fail webhook validation (that's a test case, not a footnote).
