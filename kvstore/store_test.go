package kvstore

import (
	"sync"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New(Options{})
	s.Set("k", []byte("v"), 0)
	v, ok := s.Get("k")
	if !ok || string(v) != "v" {
		t.Fatalf("Get = %q,%v; want v,true", v, ok)
	}
}

func TestGetMissing(t *testing.T) {
	s := New(Options{})
	if _, ok := s.Get("nope"); ok {
		t.Fatal("Get missing should return false")
	}
}

func TestOverwrite(t *testing.T) {
	s := New(Options{})
	s.Set("k", []byte("v1"), 0)
	s.Set("k", []byte("v2"), 0)
	v, _ := s.Get("k")
	if string(v) != "v2" {
		t.Fatalf("value = %q, want v2", v)
	}
}

func TestTTLExpiry(t *testing.T) {
	s := New(Options{})
	s.Set("k", []byte("v"), 50*time.Millisecond)
	if _, ok := s.Get("k"); !ok {
		t.Fatal("should exist before expiry")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("should be gone after expiry")
	}
}

func TestDefaultTTL(t *testing.T) {
	s := New(Options{DefaultTTL: 40 * time.Millisecond})
	s.Set("k", []byte("v"), 0)
	time.Sleep(70 * time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("default ttl should apply")
	}
}

func TestDelete(t *testing.T) {
	s := New(Options{})
	s.Set("k", []byte("v"), 0)
	if !s.Delete("k") {
		t.Fatal("Delete should return true")
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("key should be gone")
	}
	if s.Delete("k") {
		t.Fatal("second Delete should return false")
	}
}

func TestUsedBytes(t *testing.T) {
	s := New(Options{})
	s.Set("a", make([]byte, 100), 0)
	s.Set("b", make([]byte, 50), 0)
	if got := s.UsedBytes(); got != 150 {
		t.Fatalf("UsedBytes = %d, want 150", got)
	}
	s.Delete("a")
	if got := s.UsedBytes(); got != 50 {
		t.Fatalf("UsedBytes after delete = %d, want 50", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New(Options{Shards: 16})
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := string(rune('a' + worker))
				s.Set(key, []byte{byte(i)}, 0)
				_, _ = s.Get(key)
				_ = s.Len()
			}
		}(w)
	}
	wg.Wait()
}

func TestClear(t *testing.T) {
	s := New(Options{})
	s.Set("a", []byte("1"), 0)
	s.Set("b", []byte("2"), 0)
	s.Clear()
	if n := s.Len(); n != 0 {
		t.Fatalf("Len after clear = %d, want 0", n)
	}
	if u := s.UsedBytes(); u != 0 {
		t.Fatalf("UsedBytes after clear = %d, want 0", u)
	}
}

func TestPurge(t *testing.T) {
	s := New(Options{})
	s.Set("a", []byte("1"), 30*time.Millisecond)
	s.Set("b", []byte("2"), 0)
	time.Sleep(50 * time.Millisecond)
	n := s.Purge()
	if n != 1 {
		t.Fatalf("Purge = %d, want 1", n)
	}
	if _, ok := s.Get("b"); !ok {
		t.Fatal("b should survive")
	}
}

func TestValueCopyIsolation(t *testing.T) {
	s := New(Options{})
	orig := []byte("hello")
	s.Set("k", orig, 0)
	orig[0] = 'X'
	v, _ := s.Get("k")
	if string(v) != "hello" {
		t.Fatalf("mutating caller buffer changed stored value: %q", v)
	}
	v[0] = 'Y'
	v2, _ := s.Get("k")
	if string(v2) != "hello" {
		t.Fatalf("mutating returned copy changed stored value: %q", v2)
	}
}

func TestCanFit(t *testing.T) {
	s := New(Options{MaxBytes: 100})
	s.Set("a", make([]byte, 40), 0)
	if !s.CanFit(60) {
		t.Fatal("CanFit(60) should be true (40+60=100)")
	}
	if s.CanFit(61) {
		t.Fatal("CanFit(61) should be false (40+61>100)")
	}
	// 上限 0 = 不限。
	s2 := New(Options{})
	if !s2.CanFit(1 << 40) {
		t.Fatal("unlimited store should always fit")
	}
}

func TestCanFitOverwrite(t *testing.T) {
	// 上限 100：先写 40 字节，再覆盖同一键写 40 字节，实际总占用仍是 40，必须允许。
	s := New(Options{MaxBytes: 100})
	s.Set("a", make([]byte, 40), 0)
	s.Set("b", make([]byte, 60), 0)

	if got := s.UsedBytes(); got != 100 {
		t.Fatalf("used = %d, want 100", got)
	}
	// 覆盖 a（40 字节）为新值 40 字节：总占用不变，不应被判定超限。
	if !s.CanFitSet("a", 40) {
		t.Fatal("overwriting same key with same size should fit")
	}
	if got := s.UsedAfterSet("a", 40); got != 100 {
		t.Fatalf("used after overwrite = %d, want 100", got)
	}
	// 覆盖为 50 字节：100-40+50=110 > 100，应拒绝。
	if s.CanFitSet("a", 50) {
		t.Fatal("overwriting with 50 bytes should not fit (110 > 100)")
	}
	// 新键 1 字节：100+1 > 100，应拒绝。
	if s.CanFitSet("c", 1) {
		t.Fatal("new key should not fit")
	}
	// 上限 0 = 不限。
	s2 := New(Options{})
	if !s2.CanFitSet("a", 1<<40) {
		t.Fatal("unlimited store should always fit")
	}
}

func TestTTLQuery(t *testing.T) {
	s := New(Options{})
	s.Set("k", []byte("v"), 100*time.Millisecond)

	d, ok := s.TTL("k")
	if !ok {
		t.Fatal("TTL should report existing key")
	}
	if d <= 0 || d > 100*time.Millisecond {
		t.Fatalf("TTL = %v, want in (0,100ms]", d)
	}

	// 永不过期键。
	s.Set("perm", []byte("v"), 0)
	d, ok = s.TTL("perm")
	if !ok || d != -1 {
		t.Fatalf("permanent TTL = %v,%v; want -1,true", d, ok)
	}

	// 不存在键。
	if _, ok := s.TTL("nope"); ok {
		t.Fatal("TTL missing key should be false")
	}

	// 过期后视为不存在。
	time.Sleep(120 * time.Millisecond)
	if _, ok := s.TTL("k"); ok {
		t.Fatal("TTL expired key should be false")
	}
}
