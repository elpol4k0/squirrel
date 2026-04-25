package repo_test

import (
	"context"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func TestAddKey_NewPasswordUnlocksRepo(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	const second = "second-password-456"
	id, err := r.AddKey(ctx, []byte(second))
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	if id == "" {
		t.Fatal("AddKey returned empty ID")
	}

	// New password must open the repo.
	if _, err := repo.Open(dir, []byte(second)); err != nil {
		t.Errorf("Open with new password failed: %v", err)
	}

	// Original password must still work.
	if _, err := repo.Open(dir, []byte(testPassword)); err != nil {
		t.Errorf("original password broke after AddKey: %v", err)
	}
}

func TestAddKey_WrongNewPasswordBlocked(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))
	r.AddKey(ctx, []byte("extra"))

	if _, err := repo.Open(dir, []byte("wrong-password")); err == nil {
		t.Error("Open with an unknown password should fail")
	}
}

func TestListKeys_CountMatchesAddRemove(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	keys, err := r.ListKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after init, got %d", len(keys))
	}

	addedID, _ := r.AddKey(ctx, []byte("pw2"))

	keys, _ = r.ListKeys(ctx)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys after AddKey, got %d", len(keys))
	}

	if err := r.RemoveKey(ctx, addedID); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	keys, _ = r.ListKeys(ctx)
	if len(keys) != 1 {
		t.Fatalf("expected 1 key after RemoveKey, got %d", len(keys))
	}
}

func TestRemoveKey_RefusesLastKey(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	keys, _ := r.ListKeys(ctx)
	if len(keys) != 1 {
		t.Fatalf("precondition: expected 1 key, got %d", len(keys))
	}

	if err := r.RemoveKey(ctx, keys[0]); err == nil {
		t.Error("RemoveKey should refuse to remove the last key")
	}
}

func TestRemoveKey_RemovedPasswordBlocked(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))
	addedID, _ := r.AddKey(ctx, []byte("temp-pw"))

	if err := r.RemoveKey(ctx, addedID); err != nil {
		t.Fatalf("RemoveKey: %v", err)
	}

	// The removed password must no longer unlock the repo.
	if _, err := repo.Open(dir, []byte("temp-pw")); err == nil {
		t.Error("removed password still opens the repo")
	}
}
