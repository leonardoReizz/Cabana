package httpapi

import (
	"testing"
	"time"

	"github.com/leonardoreis/cabana/internal/domain"
	"github.com/leonardoreis/cabana/internal/store"
)

func TestBackupDue(t *testing.T) {
	// A Wednesday at 03:00 UTC.
	now := time.Date(2026, 7, 22, 3, 0, 0, 0, time.UTC)
	hoursAgo := func(h int) *time.Time { u := now.Add(time.Duration(-h) * time.Hour); return &u }

	svc := func(schedule string, hour int32) domain.Service {
		return domain.Service{BackupSchedule: schedule, BackupHour: hour}
	}

	cases := []struct {
		name string
		ss   store.ScheduledService
		want bool
	}{
		{"daily due, never backed up", store.ScheduledService{Service: svc("daily", 3)}, true},
		{"daily due, last backup 25h ago", store.ScheduledService{Service: svc("daily", 3), LastBackup: hoursAgo(25)}, true},
		{"daily not due, backed up 2h ago", store.ScheduledService{Service: svc("daily", 3), LastBackup: hoursAgo(2)}, false},
		{"daily wrong hour", store.ScheduledService{Service: svc("daily", 5)}, false},
		{"weekly on wednesday is not sunday", store.ScheduledService{Service: svc("weekly", 3)}, false},
		{"off never runs", store.ScheduledService{Service: svc("off", 3)}, false},
	}
	for _, c := range cases {
		if got := backupDue(c.ss, now); got != c.want {
			t.Errorf("%s: backupDue = %v, want %v", c.name, got, c.want)
		}
	}

	// Same schedule, on a Sunday, is due.
	sunday := time.Date(2026, 7, 19, 3, 0, 0, 0, time.UTC)
	if sunday.Weekday() != time.Sunday {
		t.Fatal("test date is not a Sunday")
	}
	if !backupDue(store.ScheduledService{Service: svc("weekly", 3)}, sunday) {
		t.Error("weekly on Sunday at the right hour should be due")
	}
}
