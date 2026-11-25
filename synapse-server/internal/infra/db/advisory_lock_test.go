package db

import "testing"

func TestLockKeyIDDeterminismAndNamespaceIsolation(t *testing.T) {
	t.Parallel()

	id1 := LockKeyID("cron", "automation-daily")
	id2 := LockKeyID("cron", "automation-daily")
	if id1 != id2 {
		t.Fatalf("LockKeyID is not deterministic: %d != %d", id1, id2)
	}

	cronA := LockKeyID("cron", "a")
	migrateA := LockKeyID("migrate", "a")
	if cronA == migrateA {
		t.Fatalf("expected distinct LockKeyID across namespaces, both = %d", cronA)
	}

	cronB := LockKeyID("cron", "b")
	if cronA == cronB {
		t.Fatalf("expected distinct LockKeyID across keys, both = %d", cronA)
	}
}
