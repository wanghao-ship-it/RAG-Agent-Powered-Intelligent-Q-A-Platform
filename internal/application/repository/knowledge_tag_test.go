package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeTagTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    profile TEXT,
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL DEFAULT 'file',
    title VARCHAR(255) NOT NULL DEFAULT '',
    parse_status VARCHAR(50) NOT NULL DEFAULT 'completed',
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    seq_id INTEGER NOT NULL,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    normalized_name VARCHAR(256) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    confidence REAL NOT NULL DEFAULT 1,
    source VARCHAR(24) NOT NULL DEFAULT 'manual',
    color VARCHAR(32),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);
CREATE TABLE IF NOT EXISTS knowledge_tag_bindings (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    confidence REAL NOT NULL,
    evidence TEXT NOT NULL,
    extractor_version VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);
CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL DEFAULT '',
    chunk_type VARCHAR(20) NOT NULL DEFAULT 'text',
    content TEXT NOT NULL DEFAULT '',
    tag_id VARCHAR(36),
    deleted_at DATETIME
);
`

func setupKnowledgeTagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeTagTestDDL).Error)
	return db
}

func seedKnowledgeTagFixture(t *testing.T, db *gorm.DB) (kbID, knowledgeID, tagA, tagB string) {
	t.Helper()
	kbID = uuid.New().String()
	knowledgeID = uuid.New().String()
	tagA = uuid.New().String()
	tagB = uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'doc', 'completed')
	`, knowledgeID, kbID).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'alpha'), (?, 2, 1, ?, 'beta')
	`, tagA, kbID, tagB, kbID).Error)
	return kbID, knowledgeID, tagA, tagB
}

func TestSetKnowledgeTags_DedupesAndReplaces(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	err := repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagA, tagB})
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagB})
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestAddKnowledgeTagRelations_IsIncrementalIdempotentAndScoped(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA}))
	require.NoError(t, repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{tagA, tagB, tagB}))
	require.NoError(t, repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{tagB}))

	var relations []types.KnowledgeTagRelation
	require.NoError(t, db.Where("knowledge_id = ?", knowledgeID).Find(&relations).Error)
	assert.Len(t, relations, 2)

	otherKBTag := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 3, 1, ?, 'other')
	`, otherKBTag, uuid.New().String()).Error)
	err := repo.AddKnowledgeTagRelations(ctx, 1, kbID, knowledgeID, []string{otherKBTag})
	require.Error(t, err)
}

func TestAddKnowledgeTagRelations_RejectsCancelledKnowledge(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, _, tagB := seedKnowledgeTagFixture(t, db)
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("id = ?", knowledgeID).
		Update("parse_status", types.ParseStatusCancelled).Error)

	err := repo.AddKnowledgeTagRelations(context.Background(), 1, kbID, knowledgeID, []string{tagB})
	require.Error(t, err)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).
		Count(&count).Error)
	assert.Zero(t, count)
}

func TestPersistKnowledgeTagBindingsIsScopedAndIdempotent(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, tagA, _ := seedKnowledgeTagFixture(t, db)
	chunkID := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, chunk_type, content)
		VALUES (?, 1, ?, ?, 'text', ?)
	`, chunkID, kbID, knowledgeID, "HR employees must submit expenses by Friday.").Error)

	binding := types.KnowledgeTagBinding{
		TagID: tagA, ChunkID: chunkID, Confidence: 0.91,
		Evidence: "submit expenses by Friday", ExtractorVersion: "auto-tag-v1",
	}
	ctx := context.Background()
	require.NoError(t, repo.PersistKnowledgeTagClassification(ctx, 1, kbID, knowledgeID, []types.KnowledgeTagBinding{binding}, nil))
	require.NoError(t, repo.PersistKnowledgeTagClassification(ctx, 1, kbID, knowledgeID, []types.KnowledgeTagBinding{
		{TagID: tagA, ChunkID: chunkID, Confidence: 0.95, Evidence: "employees must submit expenses", ExtractorVersion: "auto-tag-v1"},
	}, nil))

	var relationCount int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ? AND tag_id = ?", knowledgeID, tagA).Count(&relationCount).Error)
	assert.Equal(t, int64(1), relationCount)
	var stored types.KnowledgeTagBinding
	require.NoError(t, db.Where("knowledge_id = ? AND tag_id = ?", knowledgeID, tagA).First(&stored).Error)
	assert.Equal(t, uint64(1), stored.TenantID)
	assert.Equal(t, kbID, stored.KnowledgeBaseID)
	assert.Equal(t, chunkID, stored.ChunkID)
	assert.Equal(t, 0.95, stored.Confidence)
	assert.Equal(t, "employees must submit expenses", stored.Evidence)

	otherKB := uuid.New().String()
	otherTag := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 3, 1, ?, 'out-of-scope')
	`, otherTag, otherKB).Error)
	err := repo.PersistKnowledgeTagClassification(ctx, 1, kbID, knowledgeID, []types.KnowledgeTagBinding{
		{TagID: otherTag, ChunkID: chunkID, Confidence: 0.9, Evidence: "submit expenses", ExtractorVersion: "auto-tag-v1"},
	}, nil)
	require.Error(t, err)
	var outOfScopeRelations int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ? AND tag_id = ?", knowledgeID, otherTag).Count(&outOfScopeRelations).Error)
	assert.Zero(t, outOfScopeRelations, "scope failure must roll back both relation and binding writes")
}

func TestPersistKnowledgeTagClassificationStoresPendingEvidenceWithoutAttaching(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, activeTagID, _ := seedKnowledgeTagFixture(t, db)
	pendingTagID := uuid.NewString()
	chunkID := uuid.NewString()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name, normalized_name, status, source)
		VALUES (?, 3, 1, ?, 'New Concept', 'new concept', 'pending', 'auto')
	`, pendingTagID, kbID).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (id, tenant_id, knowledge_base_id, knowledge_id, chunk_type, content)
		VALUES (?, 1, ?, ?, 'text', 'The quarterly safety review sets the standard.')
	`, chunkID, kbID, knowledgeID).Error)

	err := repo.PersistKnowledgeTagClassification(context.Background(), 1, kbID, knowledgeID,
		[]types.KnowledgeTagBinding{{
			TagID: activeTagID, ChunkID: chunkID, Confidence: 0.91,
			Evidence: "quarterly safety review", ExtractorVersion: "auto-tag-v1",
		}},
		[]types.KnowledgeTagBinding{{
			TagID: pendingTagID, ChunkID: chunkID, Confidence: 0.93,
			Evidence: "sets the standard", ExtractorVersion: "auto-tag-v1",
		}},
	)
	require.NoError(t, err)

	var relationCount int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ? AND tag_id = ?", knowledgeID, activeTagID).Count(&relationCount).Error)
	assert.Equal(t, int64(1), relationCount)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ? AND tag_id = ?", knowledgeID, pendingTagID).Count(&relationCount).Error)
	assert.Zero(t, relationCount, "pending candidate evidence must not make the tag visible on the document")

	var activeEvidence, candidateEvidence types.KnowledgeTagBinding
	require.NoError(t, db.Where("knowledge_id = ? AND tag_id = ?", knowledgeID, activeTagID).First(&activeEvidence).Error)
	require.NoError(t, db.Where("knowledge_id = ? AND tag_id = ?", knowledgeID, pendingTagID).First(&candidateEvidence).Error)
	assert.Equal(t, "quarterly safety review", activeEvidence.Evidence)
	assert.Equal(t, "sets the standard", candidateEvidence.Evidence)
}

func TestGetKnowledgeTags_ReturnsTagDetails(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagB}))

	tagMap, err := repo.GetKnowledgeTags(ctx, []string{knowledgeID})
	require.NoError(t, err)
	require.Len(t, tagMap[knowledgeID], 2)

	names := map[string]bool{}
	for _, tag := range tagMap[knowledgeID] {
		names[tag.Name] = true
	}
	assert.True(t, names["alpha"])
	assert.True(t, names["beta"])
}

func TestDeleteKnowledgeTagRelations(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID, knowledgeID, tagA, _ := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA}))
	require.NoError(t, db.Create(&types.KnowledgeTagBinding{
		KnowledgeID: knowledgeID, TagID: tagA, TenantID: 1,
		KnowledgeBaseID: kbID, ChunkID: "chunk",
		Confidence: 0.9, Evidence: "supporting text", ExtractorVersion: "auto-tag-v1",
	}).Error)
	require.NoError(t, repo.DeleteKnowledgeTagRelations(ctx, knowledgeID))

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
	require.NoError(t, db.Model(&types.KnowledgeTagBinding{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestApplyKnowledgeListFilter_TagIDsOrSemantics(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	ctx := context.Background()

	kbID := uuid.New().String()
	docA := uuid.New().String()
	docB := uuid.New().String()
	docC := uuid.New().String()
	tagA := uuid.New().String()
	tagB := uuid.New().String()

	for _, row := range []struct{ id, title string }{
		{docA, "a"}, {docB, "b"}, {docC, "c"},
	} {
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
			VALUES (?, 1, ?, 'file', ?, 'completed')
		`, row.id, kbID, row.title).Error)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 't-a'), (?, 2, 1, ?, 't-b')
	`, tagA, kbID, tagB, kbID).Error)
	require.NoError(t, repo.SetKnowledgeTags(ctx, docA, []string{tagA}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docB, []string{tagB}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docC, []string{tagA, tagB}))

	query := db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", uint64(1), kbID)
	query = applyKnowledgeListFilter(query, types.KnowledgeListFilter{
		TagIDs: []string{tagA, tagB},
	})

	var ids []string
	require.NoError(t, query.Pluck("id", &ids).Error)
	assert.ElementsMatch(t, []string{docA, docB, docC}, ids)
}

func TestBatchCountReferences_ScopedToKnowledgeBase(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	knowledgeRepo := &knowledgeRepository{db: db}
	tagRepo := &knowledgeTagRepository{db: db}
	ctx := context.Background()

	kb1 := uuid.New().String()
	kb2 := uuid.New().String()
	doc1 := uuid.New().String()
	doc2 := uuid.New().String()
	tag1 := uuid.New().String()
	tag2 := uuid.New().String()

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'kb1-doc', 'completed'), (?, 1, ?, 'file', 'kb2-doc', 'completed')
	`, doc1, kb1, doc2, kb2).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'tag-kb1'), (?, 2, 1, ?, 'tag-kb2')
	`, tag1, kb1, tag2, kb2).Error)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc1, []string{tag1}))
	// Stale relation: doc in kb2 still linked to tag1 (simulates pre-fix move bug)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc2, []string{tag1}))

	counts, err := tagRepo.BatchCountReferences(ctx, 1, kb1, []string{tag1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[tag1].KnowledgeCount)
}
