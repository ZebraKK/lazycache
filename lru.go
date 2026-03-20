package lazycache

import "sync"

// lruNode represents a node in the doubly-linked list
type lruNode struct {
	key  string
	prev *lruNode
	next *lruNode
}

// lruList implements LRU tracking using a doubly-linked list
type lruList struct {
	mu    sync.Mutex
	nodes map[string]*lruNode
	head  *lruNode // most recently used
	tail  *lruNode // least recently used
}

// newLRUList creates a new LRU list
func newLRUList() *lruList {
	return &lruList{
		nodes: make(map[string]*lruNode),
	}
}

// Touch moves a key to the front of the list (most recently used)
func (l *lruList) Touch(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, exists := l.nodes[key]
	if !exists {
		// Create new node
		node = &lruNode{key: key}
		l.nodes[key] = node
	} else {
		// Remove from current position
		l.remove(node)
	}

	// Add to front
	l.addToFront(node)
}

// RemoveLast removes and returns the least recently used key
func (l *lruList) RemoveLast() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.tail == nil {
		return ""
	}

	key := l.tail.key
	l.remove(l.tail)
	delete(l.nodes, key)
	return key
}

// Remove removes a key from the list
func (l *lruList) Remove(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	node, exists := l.nodes[key]
	if !exists {
		return
	}

	l.remove(node)
	delete(l.nodes, key)
}

// remove removes a node from the list (not thread-safe, must hold lock)
func (l *lruList) remove(node *lruNode) {
	if node.prev != nil {
		node.prev.next = node.next
	} else {
		l.head = node.next
	}

	if node.next != nil {
		node.next.prev = node.prev
	} else {
		l.tail = node.prev
	}

	node.prev = nil
	node.next = nil
}

// addToFront adds a node to the front of the list (not thread-safe, must hold lock)
func (l *lruList) addToFront(node *lruNode) {
	node.next = l.head
	node.prev = nil

	if l.head != nil {
		l.head.prev = node
	}
	l.head = node

	if l.tail == nil {
		l.tail = node
	}
}
