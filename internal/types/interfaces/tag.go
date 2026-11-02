package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// KnowledgeTagService defines operations on knowledge base scoped tags.
type KnowledgeTagService interface {
	// ListTags lists all tags under a knowledge base with associated statistics.
	ListTags(ctx context.Context, kbID string, page *types.Pagination, keyword string) (*types.PageResult, error)
	// CreateTag creates a new tag under a knowledge base.
	CreateTag(ctx context.Context, kbID string, name string, color string, sortOrder int) (*types.KnowledgeTag, error)
	// UpdateTag updates tag basic information.
	UpdateTag(ctx context.Context, id string, name *string, color *string, sortOrder *int) (*types.KnowledgeTag, error)
	// DeleteTag deletes a tag.
	// When contentOnly=true, only deletes the content under the tag but keeps the tag itself.
	// excludeIDs: IDs of chunks to exclude from deletion (only valid when deleting chunks)
	DeleteTag(ctx context.Context, id string, force bool, contentOnly bool, excludeIDs []string) error
	// FindOrCreateTagByName finds a tag by name or creates it if not exists.
	FindOrCreateTagByName(ctx context.Context, kbID string, name string) (*types.KnowledgeTag, error)
	// ProcessIndexDelete handles async index deletion task
	ProcessIndexDelete(ctx context.Context, t *asynq.Task) error
}

// KnowledgeTagRepository defines persistence operations for tags.
type KnowledgeTagRepository interface {
	Create(ctx context.Context, tag *types.KnowledgeTag) error
	Update(ctx context.Context, tag *types.KnowledgeTag) error
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.KnowledgeTag, error)
	// GetBySeqID retrieves a tag by its seq_id.
	GetBySeqID(ctx context.Context, tenantID uint64, seqID int64) (*types.KnowledgeTag, error)
	// GetByIDs retrieves multiple tags by their IDs in a single query.
	GetByIDs(ctx context.Context, tenantID uint64, ids []string) ([]*types.KnowledgeTag, error)
	// GetBySeqIDs retrieves multiple tags by their seq_ids in a single query.
	GetBySeqIDs(ctx context.Context, tenantID uint64, seqIDs []int64) ([]*types.KnowledgeTag, error)
	GetByName(ctx context.Context, tenantID uint64, kbID string, name string) (*types.KnowledgeTag, error)
	ListByKB(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		page *types.Pagination,
		keyword string,
	) ([]*types.KnowledgeTag, int64, error)
	Delete(ctx context.Context, tenantID uint64, id string) error
	// CountReferences returns number of knowledges and chunks that reference the tag.
	CountReferences(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		tagID string,
	) (knowledgeCount int64, chunkCount int64, err error)
	// BatchCountReferences returns number of knowledges and chunks for multiple tags in a single query.
	// Returns a map of tagID -> {knowledgeCount, chunkCount}
	BatchCountReferences(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		tagIDs []string,
	) (map[string]types.TagReferenceCounts, error)
	// DeleteUnusedTags deletes tags that are not referenced by any knowledge or chunk.
	DeleteUnusedTags(ctx context.Context, tenantID uint64, kbID string) (int64, error)
}

// KnowledgeTagActiveRepository exposes only active tags to automatic classification.
type KnowledgeTagActiveRepository interface {
	ListActiveByKB(ctx context.Context, tenantID uint64, kbID string, page *types.Pagination) ([]*types.KnowledgeTag, int64, error)
}

// KnowledgeTagEdgeService manages typed relationships between tags.
type KnowledgeTagEdgeService interface {
	ListRelations(ctx context.Context, kbID string, tagID string) ([]*types.KnowledgeTagEdge, error)
	CreateRelation(ctx context.Context, kbID string, fromTagID string, toTagID string, relationType string, confidence float64) (*types.KnowledgeTagEdge, error)
	DeleteRelation(ctx context.Context, kbID string, relationID string) error
}

// KnowledgeTagEdgeRepository persists tag relationships in their owning KB.
type KnowledgeTagEdgeRepository interface {
	Create(ctx context.Context, edge *types.KnowledgeTagEdge) error
	ListByKB(ctx context.Context, tenantID uint64, kbID string, tagID string) ([]*types.KnowledgeTagEdge, error)
	GetByID(ctx context.Context, tenantID uint64, kbID string, id string) (*types.KnowledgeTagEdge, error)
	Delete(ctx context.Context, tenantID uint64, kbID string, id string) error
}

// KnowledgeTagLifecycleService creates, lists, and reviews automatic tag candidates.
type KnowledgeTagLifecycleService interface {
	CreateCandidate(ctx context.Context, kbID string, name string, description string, confidence float64) (*types.KnowledgeTag, error)
	ListPending(ctx context.Context, kbID string) ([]*types.KnowledgeTag, error)
	ListCandidateEvidence(ctx context.Context, kbID string, tagID string) ([]*types.KnowledgeTagBinding, error)
	ReviewCandidate(ctx context.Context, kbID string, tagID string, action string) (*types.KnowledgeTag, error)
}

// KnowledgeTagLifecycleRepository stores automatic-tag state transitions.
type KnowledgeTagLifecycleRepository interface {
	CreateCandidate(ctx context.Context, candidate *types.KnowledgeTag) error
	ListByStatus(ctx context.Context, tenantID uint64, kbID string, status string) ([]*types.KnowledgeTag, error)
	ListBindingsByTag(ctx context.Context, tenantID uint64, kbID string, tagID string) ([]*types.KnowledgeTagBinding, error)
	GetByID(ctx context.Context, tenantID uint64, kbID string, id string) (*types.KnowledgeTag, error)
	SetStatus(ctx context.Context, tenantID uint64, kbID string, id string, status string) error
}

// KnowledgeTagWikiStateRepository is the durable state machine for derived
// tag Wiki pages. LockVersion makes worker writes conditional on the latest
// queued source change.
type KnowledgeTagWikiStateRepository interface {
	MarkPending(ctx context.Context, tenantID uint64, kbID string, tagID string) (*types.KnowledgeTagWikiState, error)
	Get(ctx context.Context, tenantID uint64, kbID string, tagID string) (*types.KnowledgeTagWikiState, error)
	Claim(ctx context.Context, tenantID uint64, kbID string, tagID string, lockVersion uint64) (bool, error)
	MarkReady(ctx context.Context, tenantID uint64, kbID string, tagID string, lockVersion uint64, pageID string, sourceHash string, contentHash string) (bool, error)
	MarkFailed(ctx context.Context, tenantID uint64, kbID string, tagID string, lockVersion uint64, lastError string) error
	ListRecoverable(ctx context.Context) ([]*types.KnowledgeTagWikiState, error)
}

// KnowledgeTagWikiService schedules and compiles a Wiki page for an active
// knowledge tag. Pending state is durable so startup recovery can re-arm work.
type KnowledgeTagWikiService interface {
	Enqueue(ctx context.Context, tenantID uint64, kbID string, tagID string) error
	Search(ctx context.Context, tenantID uint64, kbID string, query string, topK int) ([]*types.KnowledgeTagWikiSearchResult, error)
	Handle(ctx context.Context, task *asynq.Task) error
	Recover(ctx context.Context) error
}
