package service

import "testing"

func TestTagWikiCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
		ok   bool
	}{
		{name: "same direction", a: []float32{1, 0}, b: []float32{2, 0}, want: 1, ok: true},
		{name: "orthogonal", a: []float32{1, 0}, b: []float32{0, 1}, want: 0, ok: true},
		{name: "dimension mismatch", a: []float32{1}, b: []float32{1, 0}, ok: false},
		{name: "zero vector", a: []float32{0, 0}, b: []float32{1, 0}, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tagWikiCosineSimilarity(tt.a, tt.b)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if tt.ok && got != tt.want {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsBGEM3Model(t *testing.T) {
	if !isBGEM3Model("embedding-prod", "BAAI/bge-m3") {
		t.Fatal("expected bge-m3 model name to be accepted")
	}
	if !isBGEM3Model("bge-m3", "") {
		t.Fatal("expected bge-m3 model id to be accepted")
	}
	if isBGEM3Model("embedding-prod", "text-embedding-3-large") {
		t.Fatal("non-bge-m3 model must be rejected")
	}
}
