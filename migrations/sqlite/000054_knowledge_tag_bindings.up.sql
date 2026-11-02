CREATE TABLE knowledge_tag_bindings (
    knowledge_id VARCHAR(36) NOT NULL REFERENCES knowledges(id) ON DELETE CASCADE,
    tag_id VARCHAR(36) NOT NULL REFERENCES knowledge_tags(id) ON DELETE CASCADE,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    confidence REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence TEXT NOT NULL,
    extractor_version VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);

CREATE INDEX idx_knowledge_tag_bindings_scope_tag
    ON knowledge_tag_bindings (tenant_id, knowledge_base_id, tag_id);
CREATE INDEX idx_knowledge_tag_bindings_scope_knowledge
    ON knowledge_tag_bindings (tenant_id, knowledge_base_id, knowledge_id);
