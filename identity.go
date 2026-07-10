package main

import (
	"os/user"
	"sync"
)

type uidNameResolver struct {
	mutex    sync.RWMutex
	names    map[string]string
	pending  map[string]bool
	requests chan string
	lookup   func(string) (*user.User, error)
}

var processUIDNames = newUIDNameResolver(user.LookupId)

func newUIDNameResolver(lookup func(string) (*user.User, error)) *uidNameResolver {
	resolver := &uidNameResolver{
		names:    map[string]string{},
		pending:  map[string]bool{},
		requests: make(chan string, 256),
		lookup:   lookup,
	}
	go resolver.resolveRequests()
	return resolver
}

func resolvedUser(uid string) string {
	return processUIDNames.name(uid)
}

func (resolver *uidNameResolver) name(uid string) string {
	if uid == "" {
		return ""
	}
	resolver.mutex.RLock()
	name, found := resolver.names[uid]
	resolver.mutex.RUnlock()
	if found {
		return name
	}
	resolver.queue(uid)
	return uid
}

func (resolver *uidNameResolver) queue(uid string) {
	resolver.mutex.Lock()
	defer resolver.mutex.Unlock()
	if resolver.pending[uid] {
		return
	}
	select {
	case resolver.requests <- uid:
		resolver.pending[uid] = true
	default:
	}
}

func (resolver *uidNameResolver) resolveRequests() {
	for uid := range resolver.requests {
		name := uid
		account, err := resolver.lookup(uid)
		if err == nil && account.Username != "" {
			name = account.Username
		}
		resolver.mutex.Lock()
		resolver.names[uid] = name
		delete(resolver.pending, uid)
		resolver.mutex.Unlock()
	}
}
