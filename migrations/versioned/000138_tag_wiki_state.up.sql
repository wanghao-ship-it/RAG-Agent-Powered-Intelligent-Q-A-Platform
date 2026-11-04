CREATE TABLE IF NOT EXISTS tag_wiki_states (
    tag_id VARCHAR(36) PRIMARY KEY REFERENCES knowledge_tags(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    wiki_page_id VARCHAR(36) NOT NULL DEFAULT '',
    source_hash VARCHAR(64) NOT NULL DEFAULT '',
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    last_error TEXT NOT NULL DEFAULT '',
    lock_version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tag_wiki_state_scope_status
    ON tag_wiki_states (tenant_id, knowledge_base_id, status);
