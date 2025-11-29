-- Rollback: Drop indexes first
DROP INDEX IF EXISTS idx_transition_history_created_at;
DROP INDEX IF EXISTS idx_transition_history_workflow_id;

-- Rollback: Drop tables
DROP TABLE IF EXISTS transition_history;
DROP TABLE IF EXISTS workflow_states;
