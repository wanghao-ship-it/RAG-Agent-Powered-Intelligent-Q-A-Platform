DROP INDEX IF EXISTS idx_knowledge_tags_scope_status;
DROP INDEX IF EXISTS idx_knowledge_tags_scope_name;

ALTER TABLE knowledge_tags DROP COLUMN source;
ALTER TABLE knowledge_tags DROP COLUMN confidence;
ALTER TABLE knowledge_tags DROP COLUMN status;
ALTER TABLE knowledge_tags DROP COLUMN description;
ALTER TABLE knowledge_tags DROP COLUMN normalized_name;
