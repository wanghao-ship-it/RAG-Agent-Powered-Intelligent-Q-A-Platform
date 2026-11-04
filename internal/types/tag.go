package types

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	KnowledgeTagStatusPending    = "pending"
	KnowledgeTagStatusActive     = "active"
	KnowledgeTagStatusDeprecated = "deprecated"

	KnowledgeTagSourceManual = "manual"
	KnowledgeTagSourceAuto   = "auto"
)

// NormalizeKnowledgeTagName creates the stable lookup key used to prevent
// duplicate tags that differ only by case or surrounding whitespace.
func NormalizeKnowledgeTagName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// KnowledgeTag represents a tag (category) under a specific knowledge base.
// Tags are scoped by knowledge base (and tenant) and are used to categorize
// Knowledge (documents) and FAQ Chunks.
type KnowledgeTag struct {
	// Unique identifier of the tag (UUID)
	ID string `json:"id"                gorm:"type:varchar(36);primaryKey" extensions:"x-agent=tag_id"`
	// SeqID is an auto-increment integer ID for external API usage
	SeqID int64 `json:"seq_id"            gorm:"type:bigint;uniqueIndex;autoIncrement" extensions:"x-agent=tag_seq_id"`
	// Workspace ID
	TenantID uint64 `json:"tenant_id" gorm:"not null;index:idx_knowledge_tags_scope_name,priority:1;index:idx_knowledge_tags_scope_status,priority:1"`
	// Knowledge base ID that this tag belongs to
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"type:varchar(36);index:idx_knowledge_tags_scope_name,priority:2;index:idx_knowledge_tags_scope_status,priority:2"`
	// Tag name, unique within the same knowledge base
	Name string `json:"name"              gorm:"type:varchar(128);not null" extensions:"x-agent"`
	// NormalizedName is the lowercase, trimmed lookup key for deduplication.
	NormalizedName string `json:"normalized_name" gorm:"type:varchar(256);not null;index:idx_knowledge_tags_scope_name,priority:3"`
	// Description is the optional semantic definition for automatic tags.
	Description string `json:"description" gorm:"type:text;not null"`
	// Status keeps unreviewed candidates out of active retrieval paths.
	Status string `json:"status" gorm:"type:varchar(16);not null;default:active;index:idx_knowledge_tags_scope_status,priority:3"`
	// Confidence is the tag extractor's confidence in [0,1].
	Confidence float64 `json:"confidence" gorm:"not null"`
	// Source records whether the tag was entered manually or generated automatically.
	Source string `json:"source" gorm:"type:varchar(24);not null;default:manual"`
	// Optional display color
	Color string `json:"color"             gorm:"type:varchar(32)"`
	// Sort order within the same knowledge base
	SortOrder int `json:"sort_order"        gorm:"default:0"`
	// Creation time
	CreatedAt time.Time `json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `json:"updated_at"`
}

// BeforeSave fills stable defaults for tags created through existing code
// paths. A zero confidence is preserved because it is valid for a candidate.
func (t *KnowledgeTag) BeforeSave(_ *gorm.DB) error {
	t.NormalizedName = NormalizeKnowledgeTagName(t.Name)
	if t.Status == "" {
		t.Status = KnowledgeTagStatusActive
	}
	if t.Source == "" {
		t.Source = KnowledgeTagSourceManual
	}
	return nil
}

// BeforeCreate ensures SeqID is populated for databases that don't support
// autoIncrement on non-primary-key columns (e.g. SQLite).
// On PostgreSQL/MySQL the DB sequence handles this, so we skip to avoid
// duplicate key races under concurrent inserts.
func (t *KnowledgeTag) BeforeCreate(tx *gorm.DB) error {
	if tx.Dialector.Name() != "sqlite" {
		return nil
	}
	if t.SeqID == 0 {
		var maxSeqID *int64
		tx.Unscoped().Model(&KnowledgeTag{}).
			Select("MAX(seq_id)").
			Scan(&maxSeqID)
		if maxSeqID != nil {
			t.SeqID = *maxSeqID + 1
		} else {
			t.SeqID = 1
		}
	}
	return nil
}

// KnowledgeTagWithStats represents tag information along with usage statistics.
type KnowledgeTagWithStats struct {
	KnowledgeTag
	KnowledgeCount int64 `json:"knowledge_count" extensions:"x-agent"`
	ChunkCount     int64 `json:"chunk_count"`
}

// TagReferenceCounts holds the reference counts for a tag.
type TagReferenceCounts struct {
	KnowledgeCount int64
	ChunkCount     int64
}

// KnowledgeTagRelation represents a many-to-many association between
// a document knowledge entry and a tag in the knowledge_tag_relations table.
type KnowledgeTagRelation struct {
	KnowledgeID string    `gorm:"type:varchar(36);primaryKey"`
	TagID       string    `gorm:"type:varchar(36);primaryKey"`
	CreatedAt   time.Time `gorm:"autoCreateTime"`
}

// TableName overrides the default table name.
func (KnowledgeTagRelation) TableName() string {
	return "knowledge_tag_relations"
}

// KnowledgeTagBinding records why an automatic tag was attached to a document.
// The quote is copied from the referenced source chunk so the association can
// be audited without treating a model-generated explanation as evidence.
type KnowledgeTagBinding struct {
	KnowledgeID      string    `json:"knowledge_id" gorm:"type:varchar(36);primaryKey"`
	TagID            string    `json:"tag_id" gorm:"type:varchar(36);primaryKey"`
	TenantID         uint64    `json:"tenant_id" gorm:"not null;index:idx_knowledge_tag_bindings_scope_tag,priority:1"`
	KnowledgeBaseID  string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_knowledge_tag_bindings_scope_tag,priority:2"`
	ChunkID          string    `json:"chunk_id" gorm:"type:varchar(36);not null"`
	Confidence       float64   `json:"confidence" gorm:"not null"`
	Evidence         string    `json:"evidence" gorm:"type:text;not null"`
	ExtractorVersion string    `json:"extractor_version" gorm:"type:varchar(64);not null"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// TableName overrides the default table name.
func (KnowledgeTagBinding) TableName() string {
	return "knowledge_tag_bindings"
}

const (
	KnowledgeTagWikiStatusPending    = "pending"
	KnowledgeTagWikiStatusProcessing = "processing"
	KnowledgeTagWikiStatusReady      = "ready"
	KnowledgeTagWikiStatusFailed     = "failed"
)

// KnowledgeTagWikiState tracks the derived Wiki page for one active tag.
// PostgreSQL remains the source of truth; this row is the durable generation
// state and prevents an older concurrent worker from publishing stale output.
type KnowledgeTagWikiState struct {
	TagID           string    `json:"tag_id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;index:idx_tag_wiki_state_scope_status,priority:1"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;index:idx_tag_wiki_state_scope_status,priority:2"`
	WikiPageID      string    `json:"wiki_page_id,omitempty" gorm:"type:varchar(36)"`
	SourceHash      string    `json:"source_hash,omitempty" gorm:"type:varchar(64)"`
	ContentHash     string    `json:"content_hash,omitempty" gorm:"type:varchar(64)"`
	Status          string    `json:"status" gorm:"type:varchar(16);not null;default:'pending';index:idx_tag_wiki_state_scope_status,priority:3"`
	LastError       string    `json:"last_error,omitempty" gorm:"type:text"`
	LockVersion     uint64    `json:"lock_version" gorm:"not null;default:1"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// KnowledgeTagWikiSearchResult is one vector-ranked Tag-Wiki page.
type KnowledgeTagWikiSearchResult struct {
	Page  *WikiPage `json:"page"`
	Score float64   `json:"score"`
}

// TableName binds the tag Wiki generation state to its durable table.
func (KnowledgeTagWikiState) TableName() string {
	return "tag_wiki_states"
}

// KnowledgeTagEdge represents a typed relationship between two tags in one
// knowledge base. IS_A edges point from a child tag to its parent tag.
type KnowledgeTagEdge struct {
	ID              string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64    `json:"tenant_id" gorm:"not null;index:idx_knowledge_tag_edges_scope"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_knowledge_tag_edges_unique;index:idx_knowledge_tag_edges_scope"`
	FromTagID       string    `json:"from_tag_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_knowledge_tag_edges_unique;index:idx_knowledge_tag_edges_from"`
	ToTagID         string    `json:"to_tag_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_knowledge_tag_edges_unique;index:idx_knowledge_tag_edges_to"`
	RelationType    string    `json:"relation_type" gorm:"type:varchar(24);not null;uniqueIndex:idx_knowledge_tag_edges_unique"`
	Confidence      float64   `json:"confidence" gorm:"not null;default:1"`
	CreatedAt       time.Time `json:"created_at"`
}

// TableName overrides the default table name.
func (KnowledgeTagEdge) TableName() string {
	return "knowledge_tag_edges"
}
