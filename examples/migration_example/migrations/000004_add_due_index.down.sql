-- Rollback: remove the M4 timer due index
DROP INDEX IF EXISTS idx_workflow_states_due_at;
ALTER TABLE workflow_states DROP COLUMN due_at;
