package nodeid

import "testing"

func TestGenerate_Deterministic(t *testing.T) {
	a, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if a == "" || b == "" {
		t.Fatal("node id should not be empty")
	}
	if a != b {
		t.Fatalf("node id not stable across calls: %s vs %s", a, b)
	}
}

func TestGenerate_Format(t *testing.T) {
	id, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// sha256 前 8 字节 = 16 个 hex 字符。
	if len(id) != 16 {
		t.Fatalf("node id length = %d, want 16", len(id))
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("node id contains non-hex char: %c", c)
		}
	}
}
