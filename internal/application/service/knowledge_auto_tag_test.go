package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type autoTagRecordingQueue struct {
	interfaces.TaskEnqueuer
	task *asynq.Task
}

func (q *autoTagRecordingQueue) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.task = task
	return &asynq.TaskInfo{ID: "auto-tag", Type: task.Type()}, nil
}

func confidencePtr(value float64) *float64 { return &value }

// ── stubs: only the methods the auto-tag handler touches are implemented;
// everything else stays nil so an unexpected call fails loudly. ──

type autoTagKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge         *types.Knowledge
	existing          map[string][]*types.KnowledgeTag
	added             []string
	bindings          []types.KnowledgeTagBinding
	activeBindings    []types.KnowledgeTagBinding
	candidateBindings []types.KnowledgeTagBinding
	addErr            error
	writeCtx          context.Context
}

func (r *autoTagKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *autoTagKnowledgeRepo) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return r.existing, nil
}

func (r *autoTagKnowledgeRepo) PersistKnowledgeTagClassification(
	ctx context.Context, _ uint64, _, _ string,
	activeBindings []types.KnowledgeTagBinding, candidateBindings []types.KnowledgeTagBinding,
) error {
	r.writeCtx = ctx
	if r.addErr != nil {
		return r.addErr
	}
	for _, binding := range activeBindings {
		r.bindings = append(r.bindings, binding)
		r.activeBindings = append(r.activeBindings, binding)
		r.added = append(r.added, binding.TagID)
	}
	for _, binding := range candidateBindings {
		r.bindings = append(r.bindings, binding)
		r.candidateBindings = append(r.candidateBindings, binding)
	}
	return nil
}

type autoTagTagRepo struct {
	interfaces.KnowledgeTagRepository
	tags  []*types.KnowledgeTag
	total int64
}

func (r *autoTagTagRepo) ListByKB(
	_ context.Context, _ uint64, _ string, page *types.Pagination, _ string,
) ([]*types.KnowledgeTag, int64, error) {
	total := r.total
	if total == 0 {
		total = int64(len(r.tags))
	}
	tags := r.tags
	if page != nil && page.Limit() > 0 && len(tags) > page.Limit() {
		tags = tags[:page.Limit()]
	}
	return tags, total, nil
}

func (r *autoTagTagRepo) ListActiveByKB(
	_ context.Context, _ uint64, _ string, page *types.Pagination,
) ([]*types.KnowledgeTag, int64, error) {
	active := make([]*types.KnowledgeTag, 0, len(r.tags))
	for _, tag := range r.tags {
		if tag != nil && (tag.Status == "" || tag.Status == types.KnowledgeTagStatusActive) {
			active = append(active, tag)
		}
	}
	total := r.total
	if total == 0 {
		total = int64(len(active))
	}
	if page != nil && page.Limit() > 0 && len(active) > page.Limit() {
		active = active[:page.Limit()]
	}
	return active, total, nil
}

func (r *autoTagTagRepo) GetByName(
	_ context.Context, _ uint64, _ string, name string,
) (*types.KnowledgeTag, error) {
	normalized := types.NormalizeKnowledgeTagName(name)
	for _, tag := range r.tags {
		if tag != nil && types.NormalizeKnowledgeTagName(tag.Name) == normalized {
			return tag, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

type autoTagLifecycleService struct {
	interfaces.KnowledgeTagLifecycleService
	tagRepo *autoTagTagRepo
	created []*types.KnowledgeTag
}

func (s *autoTagLifecycleService) CreateCandidate(
	_ context.Context, kbID, name, description string, confidence float64,
) (*types.KnowledgeTag, error) {
	tag := &types.KnowledgeTag{
		ID: uuid.NewString(), KnowledgeBaseID: kbID, Name: name, Description: description,
		Status: types.KnowledgeTagStatusPending, Source: types.KnowledgeTagSourceAuto,
		Confidence: confidence,
	}
	s.tagRepo.tags = append(s.tagRepo.tags, tag)
	s.created = append(s.created, tag)
	return tag, nil
}

type autoTagKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *autoTagKBService) GetKnowledgeBaseByIDOnly(
	context.Context, string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type autoTagChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *autoTagChunkRepo) ListChunksByKnowledgeIDAndTypes(
	_ context.Context, _ uint64, _ string, chunkTypes []types.ChunkType,
) ([]*types.Chunk, error) {
	wanted := make(map[types.ChunkType]struct{}, len(chunkTypes))
	for _, t := range chunkTypes {
		wanted[t] = struct{}{}
	}
	out := make([]*types.Chunk, 0, len(r.chunks))
	for _, c := range r.chunks {
		if _, ok := wanted[c.ChunkType]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

type autoTagChatModel struct {
	response string
	messages []chat.Message
}

func (m *autoTagChatModel) Chat(
	_ context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.messages = append([]chat.Message(nil), messages...)
	return &types.ChatResponse{Content: m.response}, nil
}

func (m *autoTagChatModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (m *autoTagChatModel) GetModelName() string { return "auto-tag" }
func (m *autoTagChatModel) GetModelID() string   { return "auto-tag" }

type autoTagFixture struct {
	service   *KnowledgeAutoTagService
	repo      *autoTagKnowledgeRepo
	tagRepo   *autoTagTagRepo
	lifecycle *autoTagLifecycleService
	chatModel *autoTagChatModel
}

func newAutoTagFixture(t *testing.T, response string, opts ...func(*autoTagFixture)) *autoTagFixture {
	t.Helper()
	repo := &autoTagKnowledgeRepo{
		knowledge: &types.Knowledge{
			ID: "doc", TenantID: 7, KnowledgeBaseID: "kb",
			FileName: "policy.pdf", ParseStatus: types.ParseStatusFinalizing,
		},
		existing: map[string][]*types.KnowledgeTag{},
	}
	chatModel := &autoTagChatModel{response: response}
	tagRepo := &autoTagTagRepo{tags: []*types.KnowledgeTag{
		{ID: "tag-a", Name: "HR"},
		{ID: "tag-b", Name: "Finance"},
	}}
	lifecycle := &autoTagLifecycleService{tagRepo: tagRepo}
	fixture := &autoTagFixture{repo: repo, tagRepo: tagRepo, lifecycle: lifecycle, chatModel: chatModel}
	fixture.service = &KnowledgeAutoTagService{
		knowledgeRepo:       repo,
		tagRepo:             tagRepo,
		tagLifecycleService: lifecycle,
		kbService: &autoTagKBService{kb: &types.KnowledgeBase{
			ID: "kb", TenantID: 7, Type: types.KnowledgeBaseTypeDocument,
			SummaryModelID: "model-1",
			AutoTagConfig:  &types.AutoTagConfig{Enabled: true, MaxTags: 3},
		}},
		chunkRepo: &autoTagChunkRepo{chunks: []*types.Chunk{
			{ID: "chunk-1", ChunkType: types.ChunkTypeText, Content: "quarterly HR policy", StartAt: 1},
		}},
		modelService: &stubModelService{chatModel: chatModel},
	}
	for _, opt := range opts {
		opt(fixture)
	}
	return fixture
}

func (f *autoTagFixture) handle(t *testing.T) error {
	t.Helper()
	payload, err := json.Marshal(types.KnowledgeAutoTagPayload{
		TenantID: 7, KnowledgeID: "doc", KnowledgeBaseID: "kb", Attempt: 1,
	})
	require.NoError(t, err)
	return f.service.Handle(context.Background(), asynq.NewTask(types.TypeKnowledgeAutoTag, payload))
}

func TestAutoTagHandleAttachesMatchedTags(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
	require.Len(t, fixture.repo.bindings, 1)
	assert.Equal(t, "chunk-1", fixture.repo.bindings[0].ChunkID)
	assert.Equal(t, "quarterly HR policy", fixture.repo.bindings[0].Evidence)
	assert.Equal(t, autoTagExtractorVersion, fixture.repo.bindings[0].ExtractorVersion)
	require.Zero(t, types.CallerFromContext(fixture.repo.writeCtx).TenantID)
	require.NoError(t, access.RequireKBWrite(fixture.repo.writeCtx, &types.KnowledgeBase{ID: "kb", TenantID: 7}))
	require.Error(t, access.RequireKBWrite(fixture.repo.writeCtx, &types.KnowledgeBase{ID: "other", TenantID: 7}))
}

// A missing confidence must not be read as zero: models frequently omit the
// field while still returning a deliberate selection.
func TestAutoTagHandleKeepsMatchesWithoutConfidence(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":2,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-b"}, fixture.repo.added)
}

// The model reply is wrapped in prose containing a bracketed citation, which
// the previous LastIndexAny-based extraction truncated into invalid JSON.
func TestAutoTagHandleParsesJSONWrappedInProse(t *testing.T) {
	fixture := newAutoTagFixture(t,
		"Here you go: {\"matches\":[{\"index\":1,\"confidence\":0.88,\"evidence_chunk_index\":1,\"evidence_quote\":\"quarterly HR policy\"}]} — see section [1].")
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// An ordinal outside the candidate list is a model hallucination and must not
// resolve to an unrelated tag.
func TestAutoTagHandleDropsOutOfRangeIndex(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":99,"confidence":0.99,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

func TestAutoTagHandleNeverClassifiesPendingCandidates(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	fixture.service.tagRepo = &autoTagTagRepo{tags: []*types.KnowledgeTag{
		{ID: "pending-tag", Name: "Pending", Status: types.KnowledgeTagStatusPending},
		{ID: "active-tag", Name: "Active", Status: types.KnowledgeTagStatusActive},
	}}
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"active-tag"}, fixture.repo.added)
	assert.NotContains(t, fixture.chatModel.messages[1].Content, "Pending")
}

func TestAutoTagHandleCreatesPendingCandidateWithEvidenceOnly(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[],"new_candidates":[{"name":"Quarterly Safety Review","description":"Recurring safety review process","confidence":0.93,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.lifecycle.created, 1)
	candidate := fixture.lifecycle.created[0]
	assert.Equal(t, "Quarterly Safety Review", candidate.Name)
	assert.Equal(t, types.KnowledgeTagStatusPending, candidate.Status)
	assert.Empty(t, fixture.repo.added, "pending candidates are not document tags")
	require.Len(t, fixture.repo.candidateBindings, 1)
	assert.Equal(t, candidate.ID, fixture.repo.candidateBindings[0].TagID)
	assert.Equal(t, "chunk-1", fixture.repo.candidateBindings[0].ChunkID)
	assert.Equal(t, "quarterly HR policy", fixture.repo.candidateBindings[0].Evidence)
	assert.Empty(t, fixture.repo.activeBindings)
}

func TestAutoTagHandleCanCreateCandidateWithoutExistingTags(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[],"new_candidates":[{"name":"Safety Review","description":"Safety review process","confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	fixture.tagRepo.tags = nil
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.lifecycle.created, 1)
	require.Len(t, fixture.repo.candidateBindings, 1)
	assert.Empty(t, fixture.repo.added)
	assert.Contains(t, fixture.chatModel.messages[0].Content, "new_candidates")
	assert.Contains(t, fixture.chatModel.messages[1].Content, "(no existing tags)")
}

// skip_if_tagged defaults to on, so a manually classified document is left
// alone and never reaches the model.
func TestAutoTagHandleSkipsDocumentThatAlreadyHasTags(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "manual"}}}
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
	assert.Empty(t, fixture.chatModel.messages, "tagged documents must not cost an LLM call")
}

func TestAutoTagHandleAppendsWhenSkipIfTaggedDisabled(t *testing.T) {
	disabled := false
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`,
		func(f *autoTagFixture) {
			f.service.kbService.(*autoTagKBService).kb.AutoTagConfig.SkipIfTagged = &disabled
		})
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "manual"}}}
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// Re-running against a document whose only tags came from a previous auto-tag
// pass must stay idempotent rather than re-adding them.
func TestAutoTagHandleSkipsTagsAlreadyAttached(t *testing.T) {
	disabled := false
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`,
		func(f *autoTagFixture) {
			f.service.kbService.(*autoTagKBService).kb.AutoTagConfig.SkipIfTagged = &disabled
		})
	fixture.repo.existing = map[string][]*types.KnowledgeTag{"doc": {{ID: "tag-a"}}}
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

func TestAutoTagHandleSkipsKnowledgeOutsidePayloadScope(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.knowledge.TenantID = 99
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

func TestAutoTagHandleSkipsCancelledKnowledge(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9}]}`)
	fixture.repo.knowledge.ParseStatus = types.ParseStatusCancelled
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
}

// An oversized tag set is classified against a stable prefix rather than
// abandoning the task, which used to disable the feature outright.
func TestAutoTagHandleClassifiesPrefixWhenCandidatesExceedCap(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`,
		func(f *autoTagFixture) {
			tags := make([]*types.KnowledgeTag, 0, maximumAutoTagCandidates)
			tags = append(tags, &types.KnowledgeTag{ID: "tag-a", Name: "HR"})
			for i := len(tags); i < maximumAutoTagCandidates; i++ {
				tags = append(tags, &types.KnowledgeTag{ID: "filler", Name: "filler"})
			}
			f.service.tagRepo = &autoTagTagRepo{tags: tags, total: maximumAutoTagCandidates + 50}
		})
	require.NoError(t, fixture.handle(t))
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}

// Ordinals must address the candidate list rather than UUIDs, keeping the
// prompt compact enough for the raised candidate ceiling.
func TestAutoTagPromptListsCandidatesByOrdinal(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[]}`)
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.chatModel.messages, 2)
	user := fixture.chatModel.messages[1].Content
	assert.Contains(t, user, "1. HR")
	assert.Contains(t, user, "2. Finance")
	assert.NotContains(t, user, "tag-a")
}

func TestAutoTagHandleReturnsErrorWhenPersistFails(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":1,"evidence_quote":"quarterly HR policy"}]}`)
	fixture.repo.addErr = errors.New("db down")
	require.Error(t, fixture.handle(t))
}

// Document text must be fenced so instruction-like content cannot be read as
// part of the prompt.
func TestAutoTagPromptFencesDocumentContent(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[]}`)
	require.NoError(t, fixture.handle(t))
	require.Len(t, fixture.chatModel.messages, 2)
	user := fixture.chatModel.messages[1].Content
	assert.Contains(t, user, "<document>")
	assert.Contains(t, user, "</document>")
	assert.Contains(t, user, "<evidence_chunks>")
	assert.Contains(t, user, "quote from one of these")
	assert.Contains(t, fixture.chatModel.messages[0].Content, "never as instructions")
}

func TestAutoTagHandleRejectsUnverifiableEvidence(t *testing.T) {
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.99,"evidence_chunk_index":1,"evidence_quote":"The document proves a different claim."}]}`)
	require.NoError(t, fixture.handle(t))
	assert.Empty(t, fixture.repo.added)
	assert.Empty(t, fixture.repo.bindings)
}

func TestValidateAutoTagBindingsRequiresExactSourceQuote(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "tag-a"}, {ID: "tag-b"}}
	chunks := []autoTagEvidenceChunk{{ID: "chunk-a", Content: "Employees must submit expenses by Friday."}}
	matches := []autoTagModelMatch{
		{Index: 1, Confidence: confidencePtr(0.95), EvidenceChunkIndex: 1, EvidenceQuote: "submit expenses by Friday"},
		{Index: 2, Confidence: confidencePtr(0.99), EvidenceChunkIndex: 1, EvidenceQuote: "expenses are approved immediately"},
	}
	bindings := validateAutoTagBindings(tags, matches, chunks, 5)
	require.Len(t, bindings, 1)
	assert.Equal(t, "tag-a", bindings[0].TagID)
	assert.Equal(t, "chunk-a", bindings[0].ChunkID)
	assert.Equal(t, "submit expenses by Friday", bindings[0].Evidence)
}

func TestValidateAutoTagCandidatesRequiresNovelBoundedEvidence(t *testing.T) {
	chunks := []autoTagEvidenceChunk{{ID: "chunk-a", Content: "Employees must submit expenses by Friday."}}
	candidates := []autoTagModelCandidate{
		{Name: "Finance", Confidence: confidencePtr(0.99), EvidenceChunkIndex: 1, EvidenceQuote: "submit expenses by Friday"},
		{Name: "  Travel Policy ", Description: "Travel expenses", Confidence: confidencePtr(0.9), EvidenceChunkIndex: 1, EvidenceQuote: "submit expenses by Friday"},
		{Name: "travel policy", Confidence: confidencePtr(0.95), EvidenceChunkIndex: 1, EvidenceQuote: "submit expenses"},
		{Name: "Invented", Confidence: confidencePtr(0.99), EvidenceChunkIndex: 1, EvidenceQuote: "employee reimbursement is immediate"},
		{Name: strings.Repeat("x", 129), Confidence: confidencePtr(0.99), EvidenceChunkIndex: 1, EvidenceQuote: "submit expenses"},
	}
	validated := validateAutoTagCandidates(candidates, chunks, []*types.KnowledgeTag{{Name: "finance"}}, 5)
	require.Len(t, validated, 1)
	assert.Equal(t, "Travel Policy", validated[0].Name)
	assert.Equal(t, "chunk-a", validated[0].ChunkID)
	assert.Equal(t, "submit expenses by Friday", validated[0].Evidence)
}

func TestValidateAutoTagMatches(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	matches := []autoTagModelMatch{
		{Index: 42, Confidence: confidencePtr(1)}, // out of range
		{Index: 1, Confidence: confidencePtr(0.70)},
		{Index: 2, Confidence: confidencePtr(0.90)},
		{Index: 2, Confidence: confidencePtr(0.95)},
		{Index: 3, Confidence: confidencePtr(0.80)},
	}
	assert.Equal(t, []string{"b", "c"}, validateAutoTagMatches(tags, matches, 2))
}

func TestValidateAutoTagMatchesRejectsNonPositiveIndex(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}}
	matches := []autoTagModelMatch{
		{Index: 0, Confidence: confidencePtr(0.99)},
		{Index: -1, Confidence: confidencePtr(0.99)},
	}
	assert.Empty(t, validateAutoTagMatches(tags, matches, 5))
}

func TestValidateAutoTagMatchesNormalizesConfidenceScale(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	matches := []autoTagModelMatch{
		{Index: 1}, // omitted → deliberate selection
		{Index: 2, Confidence: confidencePtr(90)}, // percentage scale
		{Index: 3, Confidence: confidencePtr(10)}, // percentage scale, below threshold
	}
	assert.Equal(t, []string{"a", "b"}, validateAutoTagMatches(tags, matches, 5))
}

func TestValidateAutoTagMatchesClampsMaxTagsToConfigBounds(t *testing.T) {
	tags := []*types.KnowledgeTag{{ID: "a"}, {ID: "b"}}
	matches := []autoTagModelMatch{
		{Index: 1, Confidence: confidencePtr(0.9)},
		{Index: 2, Confidence: confidencePtr(0.9)},
	}
	// 0 falls back to the shared default rather than selecting nothing.
	assert.Len(t, validateAutoTagMatches(tags, matches, 0), 2)
}

func TestBuildAutoTagDocumentContentSamplesAndFilters(t *testing.T) {
	knowledge := &types.Knowledge{FileName: "policy.pdf", Description: "HR policy"}
	large := strings.Repeat("x", maximumAutoTagContentRunes+1000)
	chunks := []*types.Chunk{
		nil,
		{ChunkType: types.ChunkTypeText, Content: large, StartAt: 2},
		{ChunkType: types.ChunkTypeText, Content: "first", StartAt: 1},
	}
	content := buildAutoTagDocumentContent(knowledge, chunks)
	require.NotEmpty(t, content)
	assert.Contains(t, content, "Document name: policy.pdf")
	assert.Contains(t, content, "Existing summary: HR policy")
	assert.LessOrEqual(t, len([]rune(content)), maximumAutoTagContentRunes)
}

func TestEnqueueAutoTagTaskCarriesScopeWithoutOwningFinalizingSlot(t *testing.T) {
	queue := &autoTagRecordingQueue{}
	service := &KnowledgePostProcessService{taskEnqueuer: queue}
	ok := service.enqueueAutoTagTask(context.Background(), types.KnowledgePostProcessPayload{
		TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "doc", Language: "zh-CN",
	}, 4)
	require.True(t, ok)
	require.NotNil(t, queue.task)
	assert.Equal(t, types.TypeKnowledgeAutoTag, queue.task.Type())
	var payload types.KnowledgeAutoTagPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	assert.Equal(t, uint64(7), payload.TenantID)
	assert.Equal(t, "kb", payload.KnowledgeBaseID)
	assert.Equal(t, "doc", payload.KnowledgeID)
	assert.Equal(t, 4, payload.Attempt)
}

// For a scanned PDF parsed with OCR the text chunks hold only image-reference
// placeholders while the real content lives in the image_ocr / image_caption
// children. The classifier must receive the OCR text, not placeholders alone.
func TestAutoTagHandleFeedsOCRChunksToClassifier(t *testing.T) {
	scanned := []*types.Chunk{
		{
			ID: "p1", ChunkType: types.ChunkTypeText,
			Content: "![page_1.jpg](resource://k/scanned/page_1.jpg)", StartAt: 1,
		},
		{
			ID: "ocr1", ChunkType: types.ChunkTypeImageOCR,
			Content: "quarterly HR policy for construction safety", ParentChunkID: "p1",
		},
		{
			ID: "cap1", ChunkType: types.ChunkTypeImageCaption,
			Content: "figure: policy cover", ParentChunkID: "p1",
		},
	}
	fixture := newAutoTagFixture(t, `{"matches":[{"index":1,"confidence":0.9,"evidence_chunk_index":2,"evidence_quote":"quarterly HR policy for construction safety"}]}`)
	fixture.service.chunkRepo = &autoTagChunkRepo{chunks: scanned}
	require.NoError(t, fixture.handle(t))

	prompt := ""
	for _, m := range fixture.chatModel.messages {
		prompt += m.Content + "\n"
	}
	assert.Contains(t, prompt, "quarterly HR policy for construction safety",
		"OCR text must reach the classifier")
	assert.Contains(t, prompt, "figure: policy cover", "captions must reach the classifier")
	assert.Contains(t, prompt, "resource://k/scanned/page_1.jpg",
		"the placeholder itself is still part of the document")
	assert.Equal(t, []string{"tag-a"}, fixture.repo.added)
}
