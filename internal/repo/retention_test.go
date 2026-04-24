package repo_test

import (
	"testing"
	"time"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func makeSnaps(times []string) []*repo.Snapshot {
	snaps := make([]*repo.Snapshot, len(times))
	for i, ts := range times {
		t, _ := time.Parse("2006-01-02", ts)
		snaps[i] = &repo.Snapshot{ID: ts, Time: t.UTC()}
	}
	return snaps
}

func TestRetention_KeepLast(t *testing.T) {
	snaps := makeSnaps([]string{"2026-01-01", "2026-01-02", "2026-01-03", "2026-01-04", "2026-01-05"})
	keep, remove := repo.RetentionPolicy{KeepLast: 3}.Apply(snaps)
	if len(keep) != 3 {
		t.Errorf("keep: got %d, want 3", len(keep))
	}
	if len(remove) != 2 {
		t.Errorf("remove: got %d, want 2", len(remove))
	}
	// newest 3 should be kept
	for _, s := range keep {
		if s.ID < "2026-01-03" {
			t.Errorf("expected only newest 3 kept, but got %s", s.ID)
		}
	}
}

func TestRetention_KeepDaily(t *testing.T) {
	// two snapshots on same day – only one should count toward the daily quota
	snaps := makeSnaps([]string{
		"2026-01-01", "2026-01-01",
		"2026-01-02",
		"2026-01-03",
		"2026-01-04",
		"2026-01-05",
	})
	keep, _ := repo.RetentionPolicy{KeepDaily: 3}.Apply(snaps)
	// 3 distinct days kept, but multiple snapshots on a day may each be marked
	days := make(map[string]bool)
	for _, s := range keep {
		days[s.Time.Format("2006-01-02")] = true
	}
	if len(days) > 3 {
		t.Errorf("expected at most 3 distinct days kept, got %d", len(days))
	}
}

func TestRetention_KeepWeekly(t *testing.T) {
	snaps := makeSnaps([]string{
		"2026-01-05", // week 2
		"2026-01-12", // week 3
		"2026-01-19", // week 4
		"2026-01-26", // week 5
		"2026-02-02", // week 6
	})
	keep, remove := repo.RetentionPolicy{KeepWeekly: 2}.Apply(snaps)
	if len(keep) != 2 {
		t.Errorf("keep: got %d, want 2", len(keep))
	}
	if len(remove) != 3 {
		t.Errorf("remove: got %d, want 3", len(remove))
	}
}

func TestRetention_Combined(t *testing.T) {
	snaps := makeSnaps([]string{
		"2026-01-01",
		"2026-02-01",
		"2026-03-01",
		"2026-04-01",
		"2026-04-15",
		"2026-04-22",
		"2026-04-23",
		"2026-04-24",
	})
	policy := repo.RetentionPolicy{KeepLast: 2, KeepDaily: 3, KeepMonthly: 2}
	keep, _ := policy.Apply(snaps)
	// all kept IDs should be unique
	seen := make(map[string]bool)
	for _, s := range keep {
		if seen[s.ID] {
			t.Errorf("duplicate in keep: %s", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestRetention_Empty(t *testing.T) {
	keep, remove := repo.RetentionPolicy{KeepLast: 5}.Apply(nil)
	if len(keep) != 0 || len(remove) != 0 {
		t.Error("expected empty results for empty input")
	}
}

func TestRetention_KeepAll(t *testing.T) {
	snaps := makeSnaps([]string{"2026-01-01", "2026-02-01", "2026-03-01"})
	keep, remove := repo.RetentionPolicy{KeepLast: 10}.Apply(snaps)
	if len(keep) != 3 || len(remove) != 0 {
		t.Errorf("keep=%d remove=%d, want keep=3 remove=0", len(keep), len(remove))
	}
}
