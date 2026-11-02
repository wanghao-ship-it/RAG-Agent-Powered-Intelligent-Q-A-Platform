ALTER TABLE knowledge_tags
    ADD COLUMN normalized_name VARCHAR(256) NOT NULL DEFAULT '';
ALTER TABLE knowledge_tags
    ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_tags
    ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'active'
        CHECK (status IN ('pending', 'active', 'deprecated'));
ALTER TABLE knowledge_tags
    ADD COLUMN confidence REAL NOT NULL DEFAULT 1.0
        CHECK (confidence >= 0 AND confidence <= 1);
ALTER TABLE knowledge_tags
    ADD COLUMN source VARCHAR(24) NOT NULL DEFAULT 'manual'
        CHECK (source IN ('manual', 'auto'));

UPDATE knowledge_tags
SET normalized_name = lower(trim(name))
WHERE normalized_name = '';

CREATE INDEX IF NOT EXISTS idx_knowledge_tags_scope_name
    ON knowledge_tags (tenant_id, knowledge_base_id, normalized_name);
CREATE INDEX IF NOT EXISTS idx_knowledge_tags_scope_status
    ON knowledge_tags (tenant_id, knowledge_base_id, status);
