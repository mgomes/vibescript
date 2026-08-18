package value

import (
	"sync"
	"testing"
)

func TestPublishRefAdvancesFreshToSoleToShared(t *testing.T) {
	t.Parallel()

	v := NewArray([]Value{NewInt(1)})
	if !v.Unpublished() {
		t.Fatal("PublishRef(fresh) started published, want unpublished")
	}
	v.PublishRef()
	if v.Unpublished() || !v.SoleRef() {
		t.Fatal("PublishRef(fresh) = unpublished or shared, want sole")
	}
	v.PublishRef()
	if v.SoleRef() {
		t.Fatal("PublishRef(sole) = sole, want shared")
	}
	v.PublishRef()
	if v.SoleRef() {
		t.Fatal("PublishRef(shared) = sole, want shared")
	}
}

func TestPublishRefDoesNotLowerSharedToSole(t *testing.T) {
	t.Parallel()

	v := NewArray([]Value{NewInt(1)})
	v.MarkSharedRef()
	v.PublishRef()
	if v.SoleRef() {
		t.Fatal("PublishRef after MarkSharedRef() = sole, want shared")
	}
}

func TestPublishRefConcurrentPublishersShare(t *testing.T) {
	t.Parallel()

	v := NewArray([]Value{NewInt(1)})
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			v.PublishRef()
		})
	}
	wg.Wait()
	if v.SoleRef() {
		t.Fatal("32 concurrent PublishRef calls left wrapper sole, want shared")
	}
}
