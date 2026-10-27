package syncutil

import (
	"sync"
)

// userEntry holds a mutex and an active reference count for a specific user ID.
type userEntry struct {
	mu  sync.Mutex
	ref int
}

// UserLockManager provides fine-grained, per-user mutual exclusion.
// It ensures that concurrent operations targeting the same user ID (e.g. role assignment,
// verification, profile updates) are serialized, while operations for different users
// execute concurrently in parallel across all available CPU cores.
type UserLockManager struct {
	globalMu sync.Mutex
	locks    map[string]*userEntry
}

// NewUserLockManager initializes a new per-user lock manager.
func NewUserLockManager() *UserLockManager {
	return &UserLockManager{
		locks: make(map[string]*userEntry),
	}
}

// Lock acquires a mutex for the specified userID and returns an unlock function.
// It is designed to be used with defer:
//
//	unlock := lockMgr.Lock(userID)
//	defer unlock()
func (m *UserLockManager) Lock(userID string) func() {
	if userID == "" {
		return func() {}
	}

	m.globalMu.Lock()
	entry, exists := m.locks[userID]
	if !exists {
		entry = &userEntry{}
		m.locks[userID] = entry
	}
	entry.ref++
	m.globalMu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()

		m.globalMu.Lock()
		entry.ref--
		if entry.ref <= 0 {
			delete(m.locks, userID)
		}
		m.globalMu.Unlock()
	}
}

// ActiveLocksCount returns the number of user IDs currently tracked in the lock pool.
func (m *UserLockManager) ActiveLocksCount() int {
	m.globalMu.Lock()
	defer m.globalMu.Unlock()
	return len(m.locks)
}

