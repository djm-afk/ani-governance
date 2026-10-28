package service

import (
	"testing"
	"time"
)

func TestResourceUsageWindowNormalizesSmallClockSkew(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	start := now.Add(-time.Hour).Format(time.RFC3339)
	future := now.Add(3 * time.Second)
	_, end, err := resourceUsageWindow(start, future.Format(time.RFC3339))
	if err != nil || end == nil || end.AsTime().After(time.Now()) || !end.AsTime().Before(future) {
		t.Fatalf("small skew normalization: end=%v err=%v", end, err)
	}
	for _, span := range [][2]string{
		{start, now.Add(time.Minute).Format(time.RFC3339)},
		{now.Add(-31 * 24 * time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)},
		{now.Format(time.RFC3339), start},
	} {
		if _, _, err := resourceUsageWindow(span[0], span[1]); err == nil {
			t.Fatalf("accepted window %v", span)
		}
	}
}
