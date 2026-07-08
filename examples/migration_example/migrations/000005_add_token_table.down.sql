-- Rollback: remove the normalized token table. Only safe if every instance
-- still has (or is re-given) its marking blob in workflow_states.state —
-- rows saved after the upgrade store the marking in token rows ONLY.
DROP INDEX IF EXISTS workflow_states_tokens_place_idx;
DROP INDEX IF EXISTS workflow_states_tokens_wf_idx;
DROP TABLE IF EXISTS workflow_states_tokens;
