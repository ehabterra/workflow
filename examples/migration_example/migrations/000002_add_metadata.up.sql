-- Migration: Add metadata columns to workflow_states table
-- This demonstrates how to evolve the schema while preserving existing data

-- Add new columns with default values for existing rows
-- Note: SQLite doesn't support DEFAULT CURRENT_TIMESTAMP in ALTER TABLE ADD COLUMN
-- So we use NULL as default and handle timestamps in the application layer
ALTER TABLE workflow_states ADD COLUMN created_at DATETIME;
ALTER TABLE workflow_states ADD COLUMN updated_at DATETIME;
ALTER TABLE workflow_states ADD COLUMN priority INTEGER DEFAULT 0;
ALTER TABLE workflow_states ADD COLUMN tags TEXT;

-- Create index for priority queries
CREATE INDEX IF NOT EXISTS idx_workflow_states_priority ON workflow_states(priority);
