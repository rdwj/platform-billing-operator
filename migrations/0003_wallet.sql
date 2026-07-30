-- +goose Up
-- wallet: derived + materialized; reconstructible. Only the ledger writer
-- updates it, transactionally with the charge/adjustment insert.
CREATE TABLE wallet (
  tenant_id     text PRIMARY KEY,
  balance       numeric(18,6) NOT NULL,
  as_of         timestamptz NOT NULL,
  version       bigint NOT NULL DEFAULT 1
);
-- Reference recompute (nightly job diffs this against wallet.balance):
CREATE VIEW wallet_recomputed AS
  SELECT t.tenant_id,
         COALESCE(a.total, 0) - COALESCE(c.total, 0) AS balance
  FROM (SELECT tenant_id FROM adjustments UNION SELECT tenant_id FROM charges) t
  LEFT JOIN (SELECT tenant_id, sum(amount) total FROM adjustments GROUP BY 1) a USING (tenant_id)
  LEFT JOIN (SELECT tenant_id, sum(amount) total FROM charges     GROUP BY 1) c USING (tenant_id);

-- +goose Down
DROP VIEW wallet_recomputed;
DROP TABLE wallet;
