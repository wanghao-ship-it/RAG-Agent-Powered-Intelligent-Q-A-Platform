DROP INDEX IF EXISTS idx_knowledge_tags_scope_status;
DROP INDEX IF EXISTS idx_knowledge_tags_scope_name;

ALTER TABLE knowledge_tags
    DROP CONSTRAINT IF EXISTS chk_knowledge_tags_source,
    DROP CONSTRAINT IF EXISTS chk_knowledge_tags_confidence,
    DROP CONSTRAINT IF EXISTS chk_knowledge_tags_status,
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS confidence,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS normalized_name;
