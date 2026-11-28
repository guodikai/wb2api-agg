// Package creds 统一凭据存储：每个平台一个子目录，一个账号一个 JSON 文件。
//
// 三条硬规则（来自实测，写进代码而不是靠记性）：
//  1. **单一写者**：WorkBuddy/豆包由签到引擎刷、网关只读；码道由网关独占（RT 一次性轮换）。
//  2. **锁**：每次读改写都拿 flock，避免并发刷新把 RT 用掉两次。
//  3. **按 exp 取新**：写回前比较有效期，旧的绝不覆盖新的（跨进程同步的最后一道闸）。
package creds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Store 凭据仓库根目录（例如 /opt/wb2api-agg/creds）。
type Store struct {
	Dir string
}

// New 创建仓库并确保目录存在。
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建凭据目录失败: %w", err)
	}
	return &Store{Dir: dir}, nil
}

func (s *Store) path(platform, id string) string {
	return filepath.Join(s.Dir, platform, id+".json")
}

// List 列出某平台下已存的账号 id（排序保证可复现）。
func (s *Store) List(platform string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, platform))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, strings.TrimSuffix(e.Name(), ".json"))
	}
	sort.Strings(out)
	return out, nil
}

// Load 读取一条记录（不存在的账号返回 os.ErrNotExist）。
func (s *Store) Load(platform, id string, v any) error {
	b, err := os.ReadFile(s.path(platform, id))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Save 原子写入（临时文件 + rename），并加文件锁。
func (s *Store) Save(platform, id string, v any) error {
	dir := filepath.Join(s.Dir, platform)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.withLock(s.path(platform, id), func() error {
		tmp := s.path(platform, id) + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, s.path(platform, id))
	})
}

// Update 在锁内做「读-改-写」，用于可能并发刷新的场景。
// fn 返回 updated=false 时不写盘（例如别的进程刚刷过，值已经更新）。
func (s *Store) Update(platform, id string, v any, fn func(v any) (bool, error)) error {
	dir := filepath.Join(s.Dir, platform)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return s.withLock(s.path(platform, id), func() error {
		if err := s.Load(platform, id, v); err != nil && !os.IsNotExist(err) {
			return err
		}
		changed, err := fn(v)
		if err != nil || !changed {
			return err
		}
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		tmp := s.path(platform, id) + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		return os.Rename(tmp, s.path(platform, id))
	})
}

// withLock 对指定路径加独占 flock（进程内 + 跨进程都生效）。
// 锁文件是目标路径本身旁边的 .lock，避免锁住数据文件导致 rename 失败。
func (s *Store) withLock(target string, fn func() error) error {
	lockPath := target + ".lock"
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("加锁失败 %s: %w", lockPath, err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	return fn()
}


// Delete 删除一个账号的凭据文件（连同锁文件）。不存在时返回 os.ErrNotExist。
func (s *Store) Delete(platform, id string) error {
	p := s.path(platform, id)
	if err := os.Remove(p); err != nil {
		return err
	}
	_ = os.Remove(p + ".lock")
	return nil
}
