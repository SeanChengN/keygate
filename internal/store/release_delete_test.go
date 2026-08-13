package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tabloy/keygate/internal/model"
	"github.com/tabloy/keygate/internal/store"
)

func TestDeleteRelease_AllowsDraftAndYanked(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()

	for _, status := range []string{model.ReleaseStatusDraft, model.ReleaseStatusYanked} {
		t.Run(status, func(t *testing.T) {
			releaseID, artifactID := setupSignedDraft(t, s, ctx, true)
			if status == model.ReleaseStatusYanked {
				if err := s.PublishRelease(ctx, releaseID, true); err != nil {
					t.Fatalf("publish release: %v", err)
				}
				if err := s.YankRelease(ctx, releaseID, "test cleanup"); err != nil {
					t.Fatalf("yank release: %v", err)
				}
			}

			deleted, fileKeys, err := s.DeleteRelease(ctx, releaseID)
			if err != nil {
				t.Fatalf("delete release: %v", err)
			}
			if deleted.ID != releaseID || deleted.Status != status || deleted.ProductID == "" || deleted.Version == "" {
				t.Fatalf("unexpected audit snapshot: %#v", deleted)
			}
			if len(fileKeys) != 1 || fileKeys[0] == "" {
				t.Fatalf("expected artifact file key, got %#v", fileKeys)
			}
			if _, err := s.FindReleaseByID(ctx, releaseID); !errors.Is(err, store.ErrReleaseNotFound) {
				t.Fatalf("release should be deleted, got %v", err)
			}
			if _, err := s.FindArtifact(ctx, artifactID); !errors.Is(err, store.ErrArtifactNotFound) {
				t.Fatalf("artifact should be cascade-deleted, got %v", err)
			}
		})
	}
}

func TestDeleteRelease_RequiresYankForPublished(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	ctx := context.Background()
	releaseID, _ := setupSignedDraft(t, s, ctx, true)
	if err := s.PublishRelease(ctx, releaseID, true); err != nil {
		t.Fatalf("publish release: %v", err)
	}

	if _, _, err := s.DeleteRelease(ctx, releaseID); !errors.Is(err, store.ErrReleaseNotDeletable) {
		t.Fatalf("expected ErrReleaseNotDeletable, got %v", err)
	}
	if rel, err := s.FindReleaseByID(ctx, releaseID); err != nil || rel.Status != model.ReleaseStatusPublished {
		t.Fatalf("published release must remain intact: release=%#v err=%v", rel, err)
	}
}

func TestDeleteRelease_Missing(t *testing.T) {
	s := setupTestDB(t)
	defer s.Close()
	if _, _, err := s.DeleteRelease(context.Background(), "missing-release"); !errors.Is(err, store.ErrReleaseNotFound) {
		t.Fatalf("expected ErrReleaseNotFound, got %v", err)
	}
}
