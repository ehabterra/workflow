-- Initial schema for workflow states table.
-- The `version` column backs optimistic-concurrency control (workflow.VersionedStorage);
-- it is part of the baseline schema the SQLite backend expects.
CREATE TABLE IF NOT EXISTS workflow_states (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 0,
    title TEXT,
    content TEXT
);

-- Initial schema for transition history table
CREATE TABLE IF NOT EXISTS transition_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workflow_id TEXT NOT NULL,
    from_state TEXT NOT NULL,
    to_state TEXT NOT NULL,
    transition TEXT NOT NULL,
    notes TEXT,
    actor TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Create index for faster history queries
CREATE INDEX IF NOT EXISTS idx_transition_history_workflow_id ON transition_history(workflow_id);
CREATE INDEX IF NOT EXISTS idx_transition_history_created_at ON transition_history(created_at);
