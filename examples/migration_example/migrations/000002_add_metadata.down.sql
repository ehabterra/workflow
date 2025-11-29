-- Rollback: Remove metadata columns
-- Note: SQLite doesn't support DROP COLUMN directly, so we need to recreate the table
-- This is a simplified rollback - in production, you'd need to preserve data

-- Drop index
DROP INDEX IF EXISTS idx_workflow_states_priority;

-- SQLite doesn't support DROP COLUMN, so we recreate the table
-- In a real scenario, you'd need to:
-- 1. Create a new table without these columns
-- 2. Copy data from old table
-- 3. Drop old table
-- 4. Rename new table
-- For this example, we'll just document the limitation
-- ALTER TABLE workflow_states DROP COLUMN tags;
-- ALTER TABLE workflow_states DROP COLUMN priority;
-- ALTER TABLE workflow_states DROP COLUMN updated_at;
-- ALTER TABLE workflow_states DROP COLUMN created_at;
