package main

import (
	"os/user"
	"runtime"
	"testing"
	"time"
)

func TestUIDNameResolutionDoesNotBlockSnapshotPath(t *testing.T) {
	release := make(chan struct{})
	resolver := newUIDNameResolver(func(uid string) (*user.User, error) {
		<-release
		return &user.User{Uid: uid, Username: "resolved-user"}, nil
	})
	if actual := resolver.name("1000"); actual != "1000" {
		t.Fatalf("expected unresolved UID immediately, received %q", actual)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if resolver.name("1000") == "resolved-user" {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("expected background UID lookup to populate cache")
}
