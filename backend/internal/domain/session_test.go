package domain

import (
	"testing"
	"time"
)

func TestActiveOrchestrator(t *testing.T) {
	base := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	sessions := []SessionRecord{
		{ID: "old", Kind: KindOrchestrator, CreatedAt: base},
		{ID: "new", Kind: KindOrchestrator, CreatedAt: base.Add(time.Hour)},
		{ID: "dead", Kind: KindOrchestrator, CreatedAt: base.Add(2 * time.Hour), IsTerminated: true},
		{ID: "exited", Kind: KindOrchestrator, CreatedAt: base.Add(3 * time.Hour), Activity: Activity{State: ActivityExited}},
		{ID: "w", Kind: KindWorker, CreatedAt: base.Add(4 * time.Hour)},
	}
	got, ok := ActiveOrchestrator(sessions)
	if !ok || got.ID != "new" {
		t.Fatalf("ActiveOrchestrator() = %+v, %v; want new, true", got, ok)
	}
	if got, ok := ActiveOrchestrator(nil); ok || got.ID != "" {
		t.Fatalf("ActiveOrchestrator(nil) = %+v, %v; want zero, false", got, ok)
	}
}

func TestFileIdentityGenerationIsStableEnoughToMatch(t *testing.T) {
	withGeneration := FileIdentity{Dev: 7, Ino: 11, Generation: 13}
	if !withGeneration.SameFile(withGeneration) {
		t.Fatal("a stable file generation did not identify the same file")
	}
	bareInode := FileIdentity{Dev: 7, Ino: 11}
	if bareInode.SameFile(bareInode) {
		t.Fatal("a bare device and inode matched without reuse protection")
	}
}
