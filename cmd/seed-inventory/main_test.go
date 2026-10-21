package main

import (
	"testing"
	"time"

	"github.com/necly/services/internal/domain"
)

// TestSeedCapacityHonorsFiveUserRule guards the Fase 2 rule: the dev seed must
// spread capacity across several accounts instead of creating or extending an
// account beyond five seats.
func TestSeedCapacityHonorsFiveUserRule(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	cases := []struct {
		name     string
		accounts []domain.InventoryAccount
		want     int
		capacity int
	}{
		{
			name:     "first account with spare capacity is used",
			accounts: []domain.InventoryAccount{{ID: "a", Status: "active", SharingLimit: 5, TotalSlots: 2}},
			want:     0,
			capacity: 3,
		},
		{
			name: "full and legacy accounts are skipped",
			accounts: []domain.InventoryAccount{
				{ID: "full", Status: "active", SharingLimit: 5, TotalSlots: 5},
				{ID: "legacy", Status: "active", SharingLimit: 10, TotalSlots: 0},
			},
			want: -1,
		},
		{
			name: "inactive and expired accounts are skipped",
			accounts: []domain.InventoryAccount{
				{ID: "inactive", Status: "inactive", SharingLimit: 5, TotalSlots: 0},
				{ID: "expired", Status: "active", SharingLimit: 5, TotalSlots: 0, ExpiresAt: &past},
				{ID: "usable", Status: "active", SharingLimit: 4, TotalSlots: 1},
			},
			want:     2,
			capacity: 3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			index, capacity := seedCapacity(tc.accounts, now)
			if index != tc.want || capacity != tc.capacity {
				t.Fatalf("seedCapacity = %d, %d; want %d, %d", index, capacity, tc.want, tc.capacity)
			}
		})
	}
}
