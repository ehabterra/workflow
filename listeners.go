package workflow

import "sync"

// listenerRef records where a handle's listener lives: which event type's list
// and at which index. Tracking the event type per handle keeps removal of one
// event type's listener from corrupting the indices of another's.
type listenerRef struct {
	eventType EventType
	index     int
}

// listenerSet is the concurrency-safe listener registry shared by Definition,
// Manager, and Workflow. All mutation and iteration goes through it, so
// listeners can be added or removed while transitions fire on other goroutines.
//
// snapshot returns the current slice under the read lock; it stays safe to
// iterate after the lock is released because add only appends (iteration is
// bounded by the snapshot's length) and remove replaces the slice with a fresh
// copy instead of shifting the shared backing array.
type listenerSet struct {
	mu      sync.RWMutex
	lists   map[EventType][]any
	handles map[uint64]listenerRef
	nextID  uint64
}

// add registers a listener and returns a removal handle owned by owner (the
// Definition, Manager, or Workflow the caller exposes to its users).
func (s *listenerSet) add(eventType EventType, listener any, owner any) *ListenerHandle {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lists == nil {
		s.lists = make(map[EventType][]any)
	}
	if s.handles == nil {
		s.handles = make(map[uint64]listenerRef)
	}

	s.nextID++
	s.lists[eventType] = append(s.lists[eventType], listener)
	s.handles[s.nextID] = listenerRef{eventType: eventType, index: len(s.lists[eventType]) - 1}

	return &ListenerHandle{
		id:        s.nextID,
		eventType: eventType,
		owner:     owner,
	}
}

// remove unregisters the listener behind handle. Unknown or already-removed
// handles are a no-op.
func (s *listenerSet) remove(handle *ListenerHandle) {
	if handle == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ref, ok := s.handles[handle.id]
	if !ok {
		return
	}

	old := s.lists[ref.eventType]
	if ref.index >= len(old) {
		return
	}

	// Copy-on-write removal: snapshots handed out earlier keep iterating their
	// own backing array untouched.
	replacement := make([]any, 0, len(old)-1)
	replacement = append(replacement, old[:ref.index]...)
	replacement = append(replacement, old[ref.index+1:]...)
	s.lists[ref.eventType] = replacement

	// Shift the recorded indices of this event type's later listeners.
	for id, r := range s.handles {
		if r.eventType == ref.eventType && r.index > ref.index {
			s.handles[id] = listenerRef{eventType: r.eventType, index: r.index - 1}
		}
	}
	delete(s.handles, handle.id)
}

// snapshot returns the listeners for eventType as of now; see the type comment
// for why the returned slice is safe to iterate without the lock.
func (s *listenerSet) snapshot(eventType EventType) []any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lists[eventType]
}

// count returns how many listeners are registered for eventType.
func (s *listenerSet) count(eventType EventType) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.lists[eventType])
}

// dispatchListeners invokes each listener with the event, stopping at the first
// error. Guard events go to GuardEventListener entries, everything else to
// EventListener entries; a listener of the wrong kind for the event is skipped.
func dispatchListeners(listeners []any, event Event) error {
	for _, l := range listeners {
		switch event.Type() {
		case EventGuard:
			if gl, ok := l.(GuardEventListener); ok {
				if err := gl(event.(*GuardEvent)); err != nil {
					return err
				}
			}
		default:
			if el, ok := l.(EventListener); ok {
				if err := el(event); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
