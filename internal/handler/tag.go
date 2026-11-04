package handler

import (
	"context"
	stderrors "errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// TagHandler handles knowledge base tag operations.
//
// All KB-access checks (own / org-shared / via shared agent) are now
// performed by the route-level g.KBAccessRead / g.KBAccessWrite
// guards in router.go — the guard rewrites c.Request.Context() to
// carry the effective tenant ID, so handlers below just use
// c.Request.Context() the way they always did.
type TagHandler struct {
	tagService          interfaces.KnowledgeTagService
	tagEdgeService      interfaces.KnowledgeTagEdgeService
	tagLifecycleService interfaces.KnowledgeTagLifecycleService
	tagWikiService      interfaces.KnowledgeTagWikiService
	wikiPageService     interfaces.WikiPageService
	tagRepo             interfaces.KnowledgeTagRepository
	chunkRepo           interfaces.ChunkRepository
}

// DeleteTagRequest represents the request body for deleting a tag
type DeleteTagRequest struct {
	ExcludeIDs []int64 `json:"exclude_ids"` // Chunk seq_ids to exclude from deletion
}

type TagWikiSearchRequest struct {
	Query string `json:"query" binding:"required"`
	TopK  int    `json:"top_k,omitempty"`
}

// NewTagHandler creates a new TagHandler.
func NewTagHandler(
	tagService interfaces.KnowledgeTagService,
	tagEdgeService interfaces.KnowledgeTagEdgeService,
	tagLifecycleService interfaces.KnowledgeTagLifecycleService,
	tagWikiService interfaces.KnowledgeTagWikiService,
	wikiPageService interfaces.WikiPageService,
	tagRepo interfaces.KnowledgeTagRepository,
	chunkRepo interfaces.ChunkRepository,
) *TagHandler {
	return &TagHandler{
		tagService: tagService, tagEdgeService: tagEdgeService, tagLifecycleService: tagLifecycleService, tagWikiService: tagWikiService, wikiPageService: wikiPageService,
		tagRepo: tagRepo, chunkRepo: chunkRepo,
	}
}

// GenerateTagWiki queues a minimal, evidence-backed Wiki page for an active tag.
// @Summary      生成标签 Wiki
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        tag_id path string true "标签ID"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/{tag_id}/wiki/generate [post]
func (h *TagHandler) GenerateTagWiki(c *gin.Context) {
	if h.tagWikiService == nil {
		c.Error(errors.NewBadRequestError("标签 Wiki 功能未启用"))
		return
	}
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tagID := secutils.SanitizeForLog(c.Param("tag_id"))
	tenantID, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok {
		c.Error(errors.NewUnauthorizedError("租户上下文不存在"))
		return
	}
	tag, err := h.tagRepo.GetByID(c.Request.Context(), tenantID, tagID)
	if err != nil {
		c.Error(err)
		return
	}
	if tag.KnowledgeBaseID != kbID {
		c.Error(errors.NewNotFoundError("标签不存在"))
		return
	}
	if err := h.tagWikiService.Enqueue(c.Request.Context(), tag.TenantID, kbID, tagID); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"success": true, "data": gin.H{"tag_id": tagID, "status": "queued"}})
}

// GetTagWiki returns the generated Wiki page for an active tag.
// @Summary      获取标签 Wiki
// @Tags         标签管理
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        tag_id path string true "标签ID"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/{tag_id}/wiki [get]
func (h *TagHandler) GetTagWiki(c *gin.Context) {
	if h.wikiPageService == nil {
		c.Error(errors.NewNotFoundError("标签 Wiki 页面不存在"))
		return
	}
	page, err := h.wikiPageService.GetPageBySlug(
		c.Request.Context(),
		secutils.SanitizeForLog(c.Param("id")),
		"tag/"+secutils.SanitizeForLog(c.Param("tag_id")),
	)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": page})
}

// SearchTagWiki performs bge-m3 vector retrieval over generated Tag-Wiki pages.
// @Summary      检索标签 Wiki
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        request body handler.TagWikiSearchRequest true "检索请求"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/wiki/search [post]
func (h *TagHandler) SearchTagWiki(c *gin.Context) {
	if h.tagWikiService == nil {
		c.Error(errors.NewBadRequestError("标签 Wiki 功能未启用"))
		return
	}
	var req TagWikiSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("query is required"))
		return
	}
	tenantID, ok := types.TenantIDFromContext(c.Request.Context())
	if !ok {
		c.Error(errors.NewUnauthorizedError("租户上下文不存在"))
		return
	}
	results, err := h.tagWikiService.Search(c.Request.Context(), tenantID, secutils.SanitizeForLog(c.Param("id")), req.Query, req.TopK)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": results})
}

// CreateTagCandidateRequest creates an unreviewed automatic tag candidate.
type CreateTagCandidateRequest struct {
	Name        string   `json:"name" binding:"required"`
	Description string   `json:"description,omitempty"`
	Confidence  *float64 `json:"confidence,omitempty"`
}

// ReviewTagCandidateRequest approves or rejects one pending candidate.
type ReviewTagCandidateRequest struct {
	Action string `json:"action" binding:"required"`
}

// ListTagCandidates lists tags awaiting human review.
// @Summary      获取待审核标签
// @Tags         标签管理
// @Produce      json
// @Param        id path string true "知识库ID"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/candidates [get]
func (h *TagHandler) ListTagCandidates(c *gin.Context) {
	candidates, err := h.tagLifecycleService.ListPending(c.Request.Context(), secutils.SanitizeForLog(c.Param("id")))
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": candidates})
}

// ListTagCandidateEvidence returns the document quotes that support a pending tag candidate.
// @Summary      获取标签候选证据
// @Tags         标签管理
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        tag_id path string true "标签ID"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/candidates/{tag_id}/evidence [get]
func (h *TagHandler) ListTagCandidateEvidence(c *gin.Context) {
	evidence, err := h.tagLifecycleService.ListCandidateEvidence(
		c.Request.Context(),
		secutils.SanitizeForLog(c.Param("id")),
		secutils.SanitizeForLog(c.Param("tag_id")),
	)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": evidence})
}

// CreateTagCandidate stores a pending tag candidate with confidence and evidence description.
// @Summary      创建待审核标签
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        request body handler.CreateTagCandidateRequest true "标签候选"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/candidates [post]
func (h *TagHandler) CreateTagCandidate(c *gin.Context) {
	var req CreateTagCandidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("标签候选请求参数不合法").WithDetails(err.Error()))
		return
	}
	confidence := 0.0
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	candidate, err := h.tagLifecycleService.CreateCandidate(
		c.Request.Context(),
		secutils.SanitizeForLog(c.Param("id")),
		req.Name,
		req.Description,
		confidence,
	)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": candidate})
}

// ReviewTagCandidate changes a pending candidate to active or deprecated.
// @Summary      审核标签候选
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id path string true "知识库ID"
// @Param        tag_id path string true "标签ID"
// @Param        request body handler.ReviewTagCandidateRequest true "审核动作"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/candidates/{tag_id}/review [post]
func (h *TagHandler) ReviewTagCandidate(c *gin.Context) {
	var req ReviewTagCandidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("标签审核请求参数不合法").WithDetails(err.Error()))
		return
	}
	candidate, err := h.tagLifecycleService.ReviewCandidate(
		c.Request.Context(),
		secutils.SanitizeForLog(c.Param("id")),
		secutils.SanitizeForLog(c.Param("tag_id")),
		secutils.SanitizeForLog(req.Action),
	)
	if err != nil {
		c.Error(err)
		return
	}
	if candidate != nil && candidate.Status == types.KnowledgeTagStatusActive && h.tagWikiService != nil {
		if err := h.tagWikiService.Enqueue(c.Request.Context(), candidate.TenantID, candidate.KnowledgeBaseID, candidate.ID); err != nil {
			c.Error(err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": candidate})
}

// CreateTagRelationRequest creates a typed edge between two tags.
type CreateTagRelationRequest struct {
	FromTagID    string   `json:"from_tag_id" binding:"required"`
	ToTagID      string   `json:"to_tag_id" binding:"required"`
	RelationType string   `json:"relation_type" binding:"required"`
	Confidence   *float64 `json:"confidence,omitempty"`
}

// ListTagRelations returns the relationships attached to a tag or KB.
// @Summary      获取标签关系
// @Description  列出知识库中的标签关系；可用 tag_id 限定某个标签的入边和出边
// @Tags         标签管理
// @Produce      json
// @Param        id      path  string  true   "知识库ID"
// @Param        tag_id  query string  false  "标签ID"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/relations [get]
func (h *TagHandler) ListTagRelations(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tagID := secutils.SanitizeForLog(c.Query("tag_id"))
	relations, err := h.tagEdgeService.ListRelations(ctx, kbID, tagID)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": relations})
}

// CreateTagRelation adds IS_A, SYNONYM_OF, or RELATED_TO between tags.
// IS_A is directed from the child tag to the parent tag.
// @Summary      创建标签关系
// @Description  建立标签层级、同义或相关关系；IS_A 子标签指向父标签，并拒绝环路
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id       path string true "知识库ID"
// @Param        request  body handler.CreateTagRelationRequest true "关系信息"
// @Success      200 {object} handler.DataResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/relations [post]
func (h *TagHandler) CreateTagRelation(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	var req CreateTagRelationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("标签关系请求参数不合法").WithDetails(err.Error()))
		return
	}
	confidence := 1.0
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	relation, err := h.tagEdgeService.CreateRelation(ctx, kbID,
		secutils.SanitizeForLog(req.FromTagID), secutils.SanitizeForLog(req.ToTagID),
		secutils.SanitizeForLog(req.RelationType), confidence)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": relation})
}

// DeleteTagRelation removes one relationship without deleting either tag.
// @Summary      删除标签关系
// @Tags         标签管理
// @Produce      json
// @Param        id           path string true "知识库ID"
// @Param        relation_id  path string true "关系ID"
// @Success      200 {object} handler.SuccessResponse
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/tags/relations/{relation_id} [delete]
func (h *TagHandler) DeleteTagRelation(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	relationID := secutils.SanitizeForLog(c.Param("relation_id"))
	if err := h.tagEdgeService.DeleteRelation(ctx, kbID, relationID); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// resolveTagID resolves tag_id parameter which can be either UUID or seq_id (integer).
// Uses tenant from c's context — which the route-level KB-access guard
// has already rewritten to the effective tenant for shared KBs.
func (h *TagHandler) resolveTagID(c *gin.Context) (string, error) {
	return h.resolveTagIDWithCtx(c, c.Request.Context())
}

// resolveTagIDWithCtx resolves tag_id using the given context for tenant.
func (h *TagHandler) resolveTagIDWithCtx(c *gin.Context, ctx context.Context) (string, error) {
	tagIDParam := secutils.SanitizeForLog(c.Param("tag_id"))

	if seqID, err := strconv.ParseInt(tagIDParam, 10, 64); err == nil {
		tenantID := types.MustTenantIDFromContext(ctx)
		tag, err := h.tagRepo.GetBySeqID(ctx, tenantID, seqID)
		if err != nil {
			return "", errors.NewNotFoundError("标签不存在")
		}
		return tag.ID, nil
	}
	return tagIDParam, nil
}

// getChunksBySeqIDs retrieves chunks by their seq_ids.
func (h *TagHandler) getChunksBySeqIDs(ctx context.Context, tenantID uint64, seqIDs []int64) ([]*types.Chunk, error) {
	return h.chunkRepo.ListChunksBySeqID(ctx, tenantID, seqIDs)
}

// ListTags godoc
// @Summary      获取标签列表
// @Description  获取知识库下的所有标签及统计信息
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id         path      string  true   "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        page       query     int     false  "页码"  minimum(1)  extensions(x-agent,x-agent-maximum=10000)
// @Param        page_size  query     int     false  "每页数量"  minimum(1)  extensions(x-agent,x-agent-maximum=50)
// @Param        keyword    query     string  false  "关键词搜索"  extensions(x-agent)
// @Success      200        {object}  handler.DataResponse{data=handler.TagPageResult}  "标签列表"
// @Failure      400        {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge_base.list_tags","risk":"read","root":"data.data","max_items":50}
// @x-agent-hint "Tags of a knowledge base with how many documents use each. meta.total is the full count; pass tag ids to knowledge.list tag_ids. Ask for at most 50 per page."
// @Router       /knowledge-bases/{id}/tags [get]
func (h *TagHandler) ListTags(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var page types.Pagination
	if err := c.ShouldBindQuery(&page); err != nil {
		logger.Error(ctx, "Failed to bind pagination query", err)
		c.Error(errors.NewBadRequestError("分页参数不合法").WithDetails(err.Error()))
		return
	}

	keyword := secutils.SanitizeForLog(c.Query("keyword"))

	tags, err := h.tagService.ListTags(ctx, kbID, &page, keyword)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tags,
	})
}

// CreateTagRequest is the body of POST /knowledge-bases/{id}/tags.
type CreateTagRequest struct {
	// Tag name, unique in this knowledge base
	Name      string `json:"name"       binding:"required" extensions:"x-agent"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
}

// CreateTag godoc
// @Summary      创建标签
// @Description  在知识库下创建新标签
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                 true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        request  body      handler.CreateTagRequest true  "标签信息"
// @Success      200      {object}  handler.DataResponse{data=types.KnowledgeTag}  "创建的标签"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge_base.create_tag","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Creates a tag in the knowledge base. Pass the returned tag id to knowledge.set_tags."
// @Router       /knowledge-bases/{id}/tags [post]
func (h *TagHandler) CreateTag(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var req CreateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind create tag payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	tag, err := h.tagService.CreateTag(ctx, kbID,
		secutils.SanitizeForLog(req.Name), secutils.SanitizeForLog(req.Color), req.SortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"kb_id": kbID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tag,
	})
}

// UpdateTagRequest is the body of PUT /knowledge-bases/{id}/tags/{tag_id}.
type UpdateTagRequest struct {
	// New name. Omit to keep the current one.
	Name      *string `json:"name" extensions:"x-agent"`
	Color     *string `json:"color"`
	SortOrder *int    `json:"sort_order"`
}

// UpdateTag godoc
// @Summary      更新标签
// @Description  更新标签信息
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id       path      string                   true  "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        tag_id   path      string                   true  "标签ID (UUID或seq_id)"  extensions(x-agent=tag_id)
// @Param        request  body      handler.UpdateTagRequest true  "标签更新信息"
// @Success      200      {object}  handler.DataResponse{data=types.KnowledgeTag}  "更新后的标签"
// @Failure      400      {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge_base.update_tag","risk":"write","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Renames a tag. tag_id comes from knowledge_base.list_tags. A field left out is kept."
// @Router       /knowledge-bases/{id}/tags/{tag_id} [put]
func (h *TagHandler) UpdateTag(c *gin.Context) {
	ctx := c.Request.Context()

	tagID, err := h.resolveTagID(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req UpdateTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Error(ctx, "Failed to bind update tag payload", err)
		c.Error(errors.NewBadRequestError("请求参数不合法").WithDetails(err.Error()))
		return
	}

	tag, err := h.tagService.UpdateTag(ctx, tagID, req.Name, req.Color, req.SortOrder)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tag_id": tagID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    tag,
	})
}

// DeleteTag godoc
// @Summary      删除标签
// @Description  删除标签，可使用force=true强制删除被引用的标签，content_only=true仅删除标签下的内容而保留标签本身
// @Tags         标签管理
// @Accept       json
// @Produce      json
// @Param        id            path      string              true   "知识库ID"  extensions(x-agent=knowledge_base_id)
// @Param        tag_id        path      string              true   "标签ID (UUID或seq_id)"  extensions(x-agent=tag_id)
// @Param        force         query     bool                false  "强制删除"
// @Param        content_only  query     bool                false  "仅删除内容，保留标签"
// @Param        body          body      DeleteTagRequest    false  "删除选项"
// @Success      200           {object}  handler.SuccessResponse  "删除成功"
// @Failure      400           {object}  errors.AppError         "请求参数错误"
// @Security     Bearer
// @Security     ApiKeyAuth
// @x-agent      {"id":"knowledge_base.delete_tag","risk":"destructive","target":"knowledge_base.get","label":"name"}
// @x-agent-hint "Deletes a tag that no document uses. If documents still use it, this fails; take the tag off them with knowledge.set_tags first. Documents are not deleted."
// @Router       /knowledge-bases/{id}/tags/{tag_id} [delete]
func (h *TagHandler) DeleteTag(c *gin.Context) {
	ctx := c.Request.Context()

	tagID, err := h.resolveTagID(c)
	if err != nil {
		c.Error(err)
		return
	}

	force := c.Query("force") == "true"
	contentOnly := c.Query("content_only") == "true"

	var req DeleteTagRequest
	if err := c.ShouldBindJSON(&req); err != nil && !stderrors.Is(err, io.EOF) {
		_ = c.Error(errors.NewBadRequestError("删除选项不合法"))
		return
	}

	var excludeUUIDs []string
	if len(req.ExcludeIDs) > 0 {
		tenantID := types.MustTenantIDFromContext(ctx)
		wanted := make(map[int64]bool, len(req.ExcludeIDs))
		for _, id := range req.ExcludeIDs {
			if id <= 0 {
				_ = c.Error(errors.NewBadRequestError("排除条目 ID 必须为正整数"))
				return
			}
			wanted[id] = true
		}
		chunks, err := h.getChunksBySeqIDs(ctx, tenantID, req.ExcludeIDs)
		if err != nil {
			_ = c.Error(err)
			return
		}
		for _, chunk := range chunks {
			if chunk == nil || !wanted[chunk.SeqID] {
				continue
			}
			if chunk.TenantID != tenantID || chunk.KnowledgeBaseID != c.Param("id") ||
				chunk.ChunkType != types.ChunkTypeFAQ {
				_ = c.Error(errors.NewForbiddenError("排除条目不属于当前知识库"))
				return
			}
			excludeUUIDs = append(excludeUUIDs, chunk.ID)
			delete(wanted, chunk.SeqID)
		}
		if len(wanted) != 0 {
			_ = c.Error(errors.NewNotFoundError("排除条目不存在"))
			return
		}
	}

	if err := h.tagService.DeleteTag(ctx, tagID, force, contentOnly, excludeUUIDs); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tag_id": tagID,
		})
		c.Error(err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// NOTE: TagHandler currently exposes CRUD for tags and statistics.
// Knowledge / Chunk tagging is handled via dedicated knowledge and FAQ APIs.
