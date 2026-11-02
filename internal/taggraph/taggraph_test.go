package taggraph

import "testing"

func TestValidRelationType(t *testing.T) {
	for _, relationType := range []string{RelationIsA, RelationSynonymOf, RelationRelatedTo} {
		if !ValidRelationType(relationType) {
			t.Errorf("ValidRelationType(%q) = false", relationType)
		}
	}
	if ValidRelationType("RELATED") {
		t.Fatal("unknown relation type was accepted")
	}
}

func TestWouldCreateCycle(t *testing.T) {
	tests := []struct {
		name      string
		edges     []Edge
		child     string
		parent    string
		wantCycle bool
	}{
		{name: "new branch", edges: []Edge{{FromTagID: "a", ToTagID: "b"}}, child: "c", parent: "b"},
		{name: "closes indirect cycle", edges: []Edge{{FromTagID: "a", ToTagID: "b"}, {FromTagID: "b", ToTagID: "c"}}, child: "c", parent: "a", wantCycle: true},
		{name: "self edge", child: "a", parent: "a", wantCycle: true},
		{name: "empty endpoint", child: "", parent: "a", wantCycle: true},
		{name: "unrelated cycle does not block", edges: []Edge{{FromTagID: "x", ToTagID: "y"}, {FromTagID: "y", ToTagID: "x"}}, child: "a", parent: "b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WouldCreateCycle(tt.edges, tt.child, tt.parent); got != tt.wantCycle {
				t.Fatalf("WouldCreateCycle() = %v, want %v", got, tt.wantCycle)
			}
		})
	}
}
