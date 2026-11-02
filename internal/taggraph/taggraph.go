// Package taggraph contains the deterministic rules for knowledge-tag
// relationships. IS_A edges point from a child tag to its parent tag.
package taggraph

import "errors"

const (
	RelationIsA       = "IS_A"
	RelationSynonymOf = "SYNONYM_OF"
	RelationRelatedTo = "RELATED_TO"
)

var (
	ErrCycle                  = errors.New("tag relation would create an IS_A cycle")
	ErrInvalidRelation        = errors.New("invalid tag relation type")
	ErrTagEndpointMissing     = errors.New("tag relation endpoint does not belong to the knowledge base")
	ErrTagCandidateNotPending = errors.New("tag candidate is not pending review")
	ErrInvalidTagStatus       = errors.New("invalid knowledge tag status")
)

// Edge is a directed child-to-parent relationship used by cycle detection.
type Edge struct {
	FromTagID string
	ToTagID   string
}

// ValidRelationType reports whether relationType is supported by the tag graph.
func ValidRelationType(relationType string) bool {
	switch relationType {
	case RelationIsA, RelationSynonymOf, RelationRelatedTo:
		return true
	default:
		return false
	}
}

// WouldCreateCycle reports whether adding childID -> parentID would make the
// existing child-to-parent IS_A graph cyclic. A self-edge is always a cycle.
func WouldCreateCycle(edges []Edge, childID, parentID string) bool {
	if childID == "" || parentID == "" || childID == parentID {
		return true
	}

	parents := make(map[string][]string, len(edges))
	for _, edge := range edges {
		if edge.FromTagID == "" || edge.ToTagID == "" {
			continue
		}
		parents[edge.FromTagID] = append(parents[edge.FromTagID], edge.ToTagID)
	}

	seen := make(map[string]struct{}, len(edges))
	queue := []string{parentID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == childID {
			return true
		}
		if _, ok := seen[current]; ok {
			continue
		}
		seen[current] = struct{}{}
		queue = append(queue, parents[current]...)
	}
	return false
}
