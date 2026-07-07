-- Migration: Add the M4 timer due index
-- Since v0.8.0 the SQLite backend advertises workflow.DueStorage, so the
-- Manager writes a per-instance next-due timestamp on every save and
-- ListDue scans it. Pre-existing tables must add the column before
-- upgrading (or opt out with storage.WithDueColumn("")).
ALTER TABLE workflow_states ADD COLUMN due_at INTEGER;

-- Index for ListDue fleet scans
CREATE INDEX IF NOT EXISTS idx_workflow_states_due_at ON workflow_states(due_at);
