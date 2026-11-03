// Package kvstore 提供一个纯内存、支持 TTL 的并发安全 KV 存储引擎。
//
// 这是 Agent 端"内嵌轻量 KV 节点"的核心：数据只存在内存，
// 进程退出即清空，不落盘、不持久化，符合缓存场景的定位。
package kvstore

import (
	"sync"
	"time"
)

// Options 配置 Store。
type Options struct {
	// Shards 分片数量，用于降低锁竞争；0 时取默认值 64。
	Shards int
	// DefaultTTL 默认过期时间；0 表示永不过期。
	DefaultTTL time.Duration
	// MaxBytes 存储容量上限（所有值的总字节数）；0 表示不限制。
	MaxBytes uint64
	// OnEvict 在条目过期或删除时回调（可为 nil），可用于统计与上报。
	OnEvict func(key string, value []byte)
}

// entry 存储单元。
type entry struct {
	value  []byte
	expire int64 // unix nano 过期时刻；0 表示永不过期
}

// shard 单个分片，独立加锁减少竞争。
type shard struct {
	mu   sync.RWMutex
	data map[string]*entry
}

// Store 并发安全的内存 KV 引擎。
type Store struct {
	shards []*shard
	opts   Options

	mu      sync.Mutex
	used    uint64 // 当前已用字节数（值总和）
	onEvict func(key string, value []byte)
}

// New 创建 Store。Shards 会被向上取整到 2 的幂。
func New(opts Options) *Store {
	shards := opts.Shards
	if shards <= 0 {
		shards = 64
	}
	// 向上取整到 2 的幂，便于取模。
	p := 1
	for p < shards {
		p <<= 1
	}
	ss := make([]*shard, p)
	for i := range ss {
		ss[i] = &shard{data: make(map[string]*entry)}
	}
	return &Store{shards: ss, opts: opts, onEvict: opts.OnEvict}
}

func (s *Store) idx(key string) int {
	h := fnv32(key)
	return int(h & uint32(len(s.shards)-1))
}

// Set 写入键值。ttl<=0 时使用 Options.DefaultTTL；若 DefaultTTL 也为 0 则永不过期。
func (s *Store) Set(key string, value []byte, ttl time.Duration) {
	ttl = s.resolveTTL(ttl)
	var expire int64
	if ttl > 0 {
		expire = time.Now().Add(ttl).UnixNano()
	}
	e := &entry{value: append([]byte(nil), value...), expire: expire}

	sh := s.shards[s.idx(key)]
	sh.mu.Lock()
	old, existed := sh.data[key]
	sh.data[key] = e
	sh.mu.Unlock()

	if existed {
		s.adjustUsed(int64(len(old.value)), int64(len(value)))
	} else {
		s.adjustUsed(0, int64(len(value)))
	}
}

// Get 读取键值。过期条目视为不存在并删除。
func (s *Store) Get(key string) ([]byte, bool) {
	sh := s.shards[s.idx(key)]
	sh.mu.RLock()
	e, ok := sh.data[key]
	sh.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if s.expired(e) {
		s.Delete(key)
		return nil, false
	}
	// 返回拷贝，防止调用方修改内部数据。
	v := make([]byte, len(e.value))
	copy(v, e.value)
	return v, true
}

// Delete 删除键，返回是否存在。
func (s *Store) Delete(key string) bool {
	sh := s.shards[s.idx(key)]
	sh.mu.Lock()
	e, ok := sh.data[key]
	if ok {
		delete(sh.data, key)
	}
	sh.mu.Unlock()
	if ok {
		s.adjustUsed(int64(len(e.value)), 0)
		s.fireEvict(key, e.value)
	}
	return ok
}

// Len 返回当前条目数（含未清理的过期条目，惰性清理）。
func (s *Store) Len() int {
	n := 0
	for _, sh := range s.shards {
		sh.mu.RLock()
		n += len(sh.data)
		sh.mu.RUnlock()
	}
	return n
}

// UsedBytes 返回当前值占用的总字节数。
func (s *Store) UsedBytes() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.used
}

// Capacity 返回存储容量上限；0 表示不限制。
func (s *Store) Capacity() uint64 {
	return s.opts.MaxBytes
}

// Purge 清理所有过期条目，返回清理数量。
func (s *Store) Purge() int {
	now := time.Now().UnixNano()
	total := 0
	for _, sh := range s.shards {
		sh.mu.Lock()
		for k, e := range sh.data {
			if e.expire > 0 && e.expire <= now {
				delete(sh.data, k)
				s.adjustUsed(int64(len(e.value)), 0)
				s.fireEvict(k, e.value)
				total++
			}
		}
		sh.mu.Unlock()
	}
	return total
}

// Clear 清空所有数据。
func (s *Store) Clear() {
	for _, sh := range s.shards {
		sh.mu.Lock()
		sh.data = make(map[string]*entry)
		sh.mu.Unlock()
	}
	s.mu.Lock()
	s.used = 0
	s.mu.Unlock()
}

func (s *Store) resolveTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		ttl = s.opts.DefaultTTL
	}
	return ttl
}

func (s *Store) expired(e *entry) bool {
	return e.expire > 0 && e.expire <= time.Now().UnixNano()
}

func (s *Store) adjustUsed(deltaOld, deltaNew int64) {
	s.mu.Lock()
	if deltaOld >= 0 {
		s.used -= uint64(deltaOld)
	}
	if deltaNew >= 0 {
		s.used += uint64(deltaNew)
	}
	s.mu.Unlock()
}

func (s *Store) fireEvict(key string, value []byte) {
	if s.onEvict != nil {
		s.onEvict(key, value)
	}
}

// fnv32 使用 FNV-1a 哈希把 key 均匀散列到分片。
func fnv32(key string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return h
}
