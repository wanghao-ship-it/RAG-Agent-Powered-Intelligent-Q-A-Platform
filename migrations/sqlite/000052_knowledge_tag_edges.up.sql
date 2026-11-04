CREATE TABLE IF NOT EXISTS knowledge_tag_edges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    from_tag_id VARCHAR(36) NOT NULL REFERENCES knowledge_tags(id) ON DELETE CASCADE,
    to_tag_id VARCHAR(36) NOT NULL REFERENCES knowledge_tags(id) ON DELETE CASCADE,
    relation_type VARCHAR(24) NOT NULL CHECK (relation_type IN ('IS_A', 'SYNONYM_OF', 'RELATED_TO')),
    confidence REAL NOT NULL DEFAULT 1.0 CHECK (confidence >= 0 AND confidence <= 1),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (from_tag_id <> to_tag_id),
    CONSTRAINT uq_knowledge_tag_edges UNIQUE (knowledge_base_id, from_tag_id, to_tag_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_tag_edges_scope
    ON knowledge_tag_edges (tenant_id, knowledge_base_id, relation_type);
CREATE INDEX IF NOT EXISTS idx_knowledge_tag_edges_from
    ON knowledge_tag_edges (knowledge_base_id, relation_type, from_tag_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_tag_edges_to
    ON knowledge_tag_edges (knowledge_base_id, relation_type, to_tag_id);
