-- +migrate Down
ALTER TABLE bridge ADD COLUMN tx_hash VARCHAR;
UPDATE bridge SET tx_hash = bridge_tx_hash WHERE bridge_tx_hash IS NOT NULL;
ALTER TABLE bridge DROP COLUMN bridge_tx_hash;

ALTER TABLE claim ADD COLUMN tx_hash VARCHAR;
UPDATE claim SET tx_hash = COALESCE(claim_tx_hash, bridge_tx_hash) WHERE claim_tx_hash IS NOT NULL OR bridge_tx_hash IS NOT NULL;
ALTER TABLE claim DROP COLUMN bridge_tx_hash;
ALTER TABLE claim DROP COLUMN claim_tx_hash;

-- +migrate Up
-- Add new specific tx_hash columns
ALTER TABLE bridge ADD COLUMN bridge_tx_hash VARCHAR;
ALTER TABLE claim ADD COLUMN bridge_tx_hash VARCHAR;
ALTER TABLE claim ADD COLUMN claim_tx_hash VARCHAR;

-- Migrate existing data from old tx_hash column
-- For bridge table, tx_hash always represents bridge transaction
UPDATE bridge SET bridge_tx_hash = tx_hash WHERE tx_hash IS NOT NULL;

-- For claim table, tx_hash currently represents bridge_tx_hash for pending claims
-- and claim_tx_hash for completed claims. Since we can't easily distinguish here,
-- we'll copy to bridge_tx_hash as the default and let the application logic handle it
UPDATE claim SET bridge_tx_hash = tx_hash WHERE tx_hash IS NOT NULL;

-- Remove the old confusing tx_hash columns
ALTER TABLE bridge DROP COLUMN tx_hash;
ALTER TABLE claim DROP COLUMN tx_hash;