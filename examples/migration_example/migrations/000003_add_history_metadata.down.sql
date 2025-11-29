-- Rollback: Remove history metadata
DROP INDEX IF EXISTS idx_transition_history_duration;

-- Note: SQLite doesn't support DROP COLUMN directly
-- See migration 000002 for details on handling this in production
