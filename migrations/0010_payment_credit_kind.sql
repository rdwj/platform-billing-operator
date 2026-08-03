-- Add payment_credit to the adjustments kind constraint for PSP wallet credits.
ALTER TABLE adjustments DROP CONSTRAINT IF EXISTS adjustments_kind_check;
ALTER TABLE adjustments ADD CONSTRAINT adjustments_kind_check CHECK (kind IN
  ('reload','manual_credit','manual_debit','sla_credit','promo','volume_tier_rebate',
   'writeoff','unmetered_gap','dispute_hold','dispute_release','refund','balance_carry_in',
   'payment_credit'));
