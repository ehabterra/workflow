-- Migration: Add metadata to transition history
ALTER TABLE transition_history ADD COLUMN duration_ms INTEGER;
ALTER TABLE transition_history ADD COLUMN metadata TEXT;

-- Create index for duration queries
CREATE INDEX IF NOT EXISTS idx_transition_history_duration ON transition_history(duration_ms);
