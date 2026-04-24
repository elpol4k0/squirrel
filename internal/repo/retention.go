package repo

import (
	"fmt"
	"time"
)

type RetentionPolicy struct {
	KeepLast    int
	KeepDaily   int
	KeepWeekly  int
	KeepMonthly int
	KeepYearly  int
}

// Apply returns two slices: keep and remove, given a time-sorted (oldest first) snapshot list.
func (p RetentionPolicy) Apply(snaps []*Snapshot) (keep, remove []*Snapshot) {
	if len(snaps) == 0 {
		return
	}

	keepSet := make(map[string]bool)

	// work newest-first for bucket logic
	sorted := make([]*Snapshot, len(snaps))
	copy(sorted, snaps)
	// assume already sorted oldest→newest, reverse for processing
	for i, j := 0, len(sorted)-1; i < j; i, j = i+1, j-1 {
		sorted[i], sorted[j] = sorted[j], sorted[i]
	}

	if p.KeepLast > 0 {
		for i := 0; i < p.KeepLast && i < len(sorted); i++ {
			keepSet[sorted[i].ID] = true
		}
	}

	bucketKeep(sorted, keepSet, p.KeepDaily, func(t time.Time) string {
		return t.Format("2006-01-02")
	})
	bucketKeep(sorted, keepSet, p.KeepWeekly, func(t time.Time) string {
		y, w := t.ISOWeek()
		return fmt.Sprintf("%d-W%02d", y, w)
	})
	bucketKeep(sorted, keepSet, p.KeepMonthly, func(t time.Time) string {
		return t.Format("2006-01")
	})
	bucketKeep(sorted, keepSet, p.KeepYearly, func(t time.Time) string {
		return t.Format("2006")
	})

	for _, s := range snaps {
		if keepSet[s.ID] {
			keep = append(keep, s)
		} else {
			remove = append(remove, s)
		}
	}
	return
}

func bucketKeep(sorted []*Snapshot, keepSet map[string]bool, n int, key func(time.Time) string) {
	if n <= 0 {
		return
	}
	seen := make(map[string]bool)
	kept := 0
	for _, s := range sorted {
		k := key(s.Time)
		if !seen[k] {
			seen[k] = true
			keepSet[s.ID] = true
			kept++
			if kept >= n {
				break
			}
		}
	}
}
