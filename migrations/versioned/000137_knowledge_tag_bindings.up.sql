CREATE TABLE IF NOT EXISTS knowledge_tag_bindings (
    knowledge_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    tag_id VARCHAR(36) NOT NULL REFERENCES knowledge_tags(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    confidence DOUBLE PRECISION NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence TEXT NOT NULL,
    extractor_version VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (knowledge_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_tag_bindings_scope_tag
    ON knowledge_tag_bindings (tenant_id, knowledge_base_id, tag_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_tag_bindings_scope_knowledge
    ON knowledge_tag_bindings (tenant_id, knowledge_base_id, knowledge_id);
