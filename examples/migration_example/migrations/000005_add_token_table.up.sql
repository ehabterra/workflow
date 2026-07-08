-- Migration: Add the normalized token table
-- The SQL backends now persist the marking as one row per token (child
-- table) instead of a JSON blob in workflow_states.state, which makes
-- tokens queryable ACROSS instances (workflow.TokenQueryStorage /
-- Manager.ListPlaceTokens). Existing rows keep their blob and stay
-- loadable — each instance is normalized on its next save, or eagerly via
-- SQLiteStorage.BackfillTokenStates. Hosts not ready to migrate can opt
-- out with storage.WithTokenTable("").
CREATE TABLE IF NOT EXISTS workflow_states_tokens (
    seq INTEGER PRIMARY KEY,
    workflow_id TEXT NOT NULL,
    place TEXT NOT NULL,
    token_id TEXT NOT NULL DEFAULT '',
    token TEXT NOT NULL
);

-- The instance index serves load/save; the place index is the
-- cross-instance read-model (ListPlaceTokens).
CREATE INDEX IF NOT EXISTS workflow_states_tokens_wf_idx ON workflow_states_tokens (workflow_id);
CREATE INDEX IF NOT EXISTS workflow_states_tokens_place_idx ON workflow_states_tokens (place);
