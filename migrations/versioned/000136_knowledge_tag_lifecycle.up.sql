DO $$ BEGIN RAISE NOTICE '[Migration 000136] Adding knowledge tag lifecycle fields'; END $$;

ALTER TABLE knowledge_tags
    ADD COLUMN IF NOT EXISTS normalized_name VARCHAR(256) NOT NULL DEFAULT '';
ALTER TABLE knowledge_tags
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';
ALTER TABLE knowledge_tags
    ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'active';
ALTER TABLE knowledge_tags
    ADD COLUMN IF NOT EXISTS confidence DOUBLE PRECISION NOT NULL DEFAULT 1.0;
ALTER TABLE knowledge_tags
    ADD COLUMN IF NOT EXISTS source VARCHAR(24) NOT NULL DEFAULT 'manual';

UPDATE knowledge_tags
SET normalized_name = lower(trim(name))
WHERE normalized_name = '';

ALTER TABLE knowledge_tags
    ADD CONSTRAINT chk_knowledge_tags_status
        CHECK (status IN ('pending', 'active', 'deprecated')),
    ADD CONSTRAINT chk_knowledge_tags_confidence
        CHECK (confidence >= 0 AND confidence <= 1),
    ADD CONSTRAINT chk_knowledge_tags_source
        CHECK (source IN ('manual', 'auto'));

CREATE INDEX IF NOT EXISTS idx_knowledge_tags_scope_name
    ON knowledge_tags (tenant_id, knowledge_base_id, normalized_name);
CREATE INDEX IF NOT EXISTS idx_knowledge_tags_scope_status
    ON knowledge_tags (tenant_id, knowledge_base_id, status);
