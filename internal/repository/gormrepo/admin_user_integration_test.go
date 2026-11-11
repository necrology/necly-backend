package gormrepo

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/domain"
	"github.com/necly/services/internal/migrate"
	"github.com/necly/services/internal/platform/testdb"
	"github.com/necly/services/internal/service"
	"gorm.io/gorm"
)

func openAdminUserDB(t *testing.T) *gorm.DB {
	t.Helper()
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	dir, err := filepath.Abs(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrate.Run(context.Background(), db, migrate.Options{Dir: dir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func createAdminUserFixture(t *testing.T, db *gorm.DB, role domain.UserRole, active bool) *domain.User {
	t.Helper()
	now := time.Now().UTC()
	user := &domain.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.test",
		Name: "Fixture " + string(role), PasswordHash: "not-a-real-hash",
		Role: role, Active: active, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("create user fixture: %v", err)
	}
	return user
}

func countActiveAdminsFixture(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&domain.User{}).
		Where("role = ? AND active = true AND deleted_at IS NULL", domain.RoleAdmin).
		Count(&count).Error; err != nil {
		t.Fatalf("count active admins: %v", err)
	}
	return count
}

func rolePtr(role domain.UserRole) *domain.UserRole { return &role }

func TestAdminUserIntegration_LastAdminCannotBeRemovedByAnyPath(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)
	svc := service.NewAdminUserService(store, store)

	lastAdmin := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	actor := createAdminUserFixture(t, db, domain.RoleCustomer, true)

	t.Run("demote via service", func(t *testing.T) {
		_, err := svc.UpdateUser(ctx, service.AdminActor{UserID: actor.ID}, lastAdmin.ID, service.AdminUserUpdate{
			Role: rolePtr(domain.RoleCustomer),
		})
		if !errors.Is(err, service.ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin, got %v", err)
		}
		assertUserRoleAndActive(t, db, lastAdmin.ID, domain.RoleAdmin, true)
	})

	t.Run("deactivate via service", func(t *testing.T) {
		inactive := false
		_, err := svc.UpdateUser(ctx, service.AdminActor{UserID: actor.ID}, lastAdmin.ID, service.AdminUserUpdate{
			Active: &inactive,
		})
		if !errors.Is(err, service.ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin, got %v", err)
		}
		assertUserRoleAndActive(t, db, lastAdmin.ID, domain.RoleAdmin, true)
	})

	t.Run("delete via service", func(t *testing.T) {
		if err := svc.DeleteUser(ctx, service.AdminActor{UserID: actor.ID}, lastAdmin.ID); !errors.Is(err, service.ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin, got %v", err)
		}
		assertUserRoleAndActive(t, db, lastAdmin.ID, domain.RoleAdmin, true)
	})

	t.Run("repository guard rejects a direct write", func(t *testing.T) {
		_, err := store.ApplyUserUpdate(ctx, lastAdmin.ID, service.AdminUserUpdate{Role: rolePtr(domain.RoleSeller)}, time.Now().UTC())
		if !errors.Is(err, service.ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin, got %v", err)
		}
		if err := store.DeleteUser(ctx, lastAdmin.ID, time.Now().UTC()); !errors.Is(err, service.ErrLastAdmin) {
			t.Fatalf("expected ErrLastAdmin from delete, got %v", err)
		}
		assertUserRoleAndActive(t, db, lastAdmin.ID, domain.RoleAdmin, true)
	})

	if got := countActiveAdminsFixture(t, db); got != 1 {
		t.Fatalf("expected the last admin to survive every path, got %d active admins", got)
	}

	var audits []domain.AuditLog
	if err := db.Order("created_at ASC").Find(&audits).Error; err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 0 {
		t.Fatalf("rejected changes must not be audited as successful changes, got %d rows", len(audits))
	}
}

func TestAdminUserIntegration_SelfAccessChangesAreRejected(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)
	svc := service.NewAdminUserService(store, store)

	admin := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	actor := service.AdminActor{UserID: admin.ID, IPAddress: "203.0.113.77"}

	if _, err := svc.UpdateUser(ctx, actor, admin.ID, service.AdminUserUpdate{Role: rolePtr(domain.RoleCustomer)}); !errors.Is(err, service.ErrSelfRoleChange) {
		t.Fatalf("expected ErrSelfRoleChange, got %v", err)
	}
	assertUserRoleAndActive(t, db, admin.ID, domain.RoleAdmin, true)

	inactive := false
	if _, err := svc.UpdateUser(ctx, actor, admin.ID, service.AdminUserUpdate{Active: &inactive}); !errors.Is(err, service.ErrSelfDeactivate) {
		t.Fatalf("expected ErrSelfDeactivate, got %v", err)
	}
	assertUserRoleAndActive(t, db, admin.ID, domain.RoleAdmin, true)

	if err := svc.DeleteUser(ctx, actor, admin.ID); !errors.Is(err, service.ErrSelfDelete) {
		t.Fatalf("expected ErrSelfDelete, got %v", err)
	}
	assertUserRoleAndActive(t, db, admin.ID, domain.RoleAdmin, true)

	var audits []domain.AuditLog
	if err := db.Order("created_at ASC").Find(&audits).Error; err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 0 {
		t.Fatalf("rejected self changes must not be audited as successful changes, got %d rows", len(audits))
	}
}

func TestAdminUserIntegration_RoleAndStatusChangesAreAudited(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)
	svc := service.NewAdminUserService(store, store)

	actor := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	target := createAdminUserFixture(t, db, domain.RoleAdmin, true)

	updated, err := svc.UpdateUser(ctx, service.AdminActor{UserID: actor.ID, IPAddress: "203.0.113.99"}, target.ID, service.AdminUserUpdate{
		Role:   rolePtr(domain.RoleSeller),
		Active: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("update user: %v", err)
	}
	if updated.Role != domain.RoleSeller || updated.Active {
		t.Fatalf("unexpected updated user: role=%s active=%v", updated.Role, updated.Active)
	}
	if updated.PasswordHash != "" {
		t.Fatal("service response leaked the password hash")
	}

	var audits []domain.AuditLog
	if err := db.Order("created_at ASC").Find(&audits).Error; err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 2 {
		t.Fatalf("expected role and status audit rows, got %d", len(audits))
	}
	actions := map[string]domain.AuditLog{}
	for _, entry := range audits {
		actions[entry.Action] = entry
		if entry.ActorUserID == nil || *entry.ActorUserID != actor.ID {
			t.Fatalf("audit actor = %v, want %s", entry.ActorUserID, actor.ID)
		}
		if entry.ResourceType != "user" || entry.ResourceID == nil || *entry.ResourceID != target.ID {
			t.Fatalf("audit target = %s/%v, want user/%s", entry.ResourceType, entry.ResourceID, target.ID)
		}
		if entry.IPAddress != "203.0.113.99" {
			t.Fatalf("audit ip = %q, want the request address", entry.IPAddress)
		}
	}

	roleAudit, ok := actions[service.AuditAdminUserRoleChanged]
	if !ok {
		t.Fatal("role change was not audited")
	}
	var roleMetadata map[string]any
	if err := json.Unmarshal(roleAudit.Metadata, &roleMetadata); err != nil {
		t.Fatalf("decode role metadata: %v", err)
	}
	if roleMetadata["previous_role"] != "admin" || roleMetadata["new_role"] != "seller" {
		t.Fatalf("role audit metadata = %#v", roleMetadata)
	}

	activeAudit, ok := actions[service.AuditAdminUserDeactivated]
	if !ok {
		t.Fatal("deactivation was not audited")
	}
	var activeMetadata map[string]any
	if err := json.Unmarshal(activeAudit.Metadata, &activeMetadata); err != nil {
		t.Fatalf("decode active metadata: %v", err)
	}
	if activeMetadata["previous_active"] != true || activeMetadata["new_active"] != false {
		t.Fatalf("active audit metadata = %#v", activeMetadata)
	}

	for _, entry := range audits {
		if string(entry.Metadata) == "" {
			continue
		}
		if containsSecretField(entry.Metadata) {
			t.Fatal("audit metadata must never contain password or credential fields")
		}
	}
}

func TestAdminUserIntegration_ConcurrentDemotionsKeepOneAdmin(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)

	first := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	second := createAdminUserFixture(t, db, domain.RoleAdmin, true)

	type result struct {
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, target := range []*domain.User{first, second} {
		wg.Add(1)
		go func(targetID string) {
			defer wg.Done()
			<-start
			_, err := store.ApplyUserUpdate(ctx, targetID, service.AdminUserUpdate{Role: rolePtr(domain.RoleCustomer)}, time.Now().UTC())
			results <- result{err: err}
		}(target.ID)
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, rejected int
	for res := range results {
		switch {
		case res.err == nil:
			successes++
		case errors.Is(res.err, service.ErrLastAdmin):
			rejected++
		default:
			t.Fatalf("unexpected concurrent update error: %v", res.err)
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("expected exactly one demotion and one rejection, got %d/%d", successes, rejected)
	}
	if got := countActiveAdminsFixture(t, db); got != 1 {
		t.Fatalf("concurrent demotions left %d active admins, want 1", got)
	}
}

func TestAdminUserIntegration_NonAdminCanBeDeactivatedAndDeleted(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)
	svc := service.NewAdminUserService(store, store)

	actor := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	seller := createAdminUserFixture(t, db, domain.RoleSeller, true)

	inactive := false
	updated, err := svc.UpdateUser(ctx, service.AdminActor{UserID: actor.ID}, seller.ID, service.AdminUserUpdate{Active: &inactive})
	if err != nil {
		t.Fatalf("deactivate seller: %v", err)
	}
	if updated.Active {
		t.Fatal("seller should be inactive")
	}

	if err := svc.DeleteUser(ctx, service.AdminActor{UserID: actor.ID}, seller.ID); err != nil {
		t.Fatalf("delete seller: %v", err)
	}
	var stored domain.User
	if err := db.Unscoped().Where("id = ?", seller.ID).First(&stored).Error; err != nil {
		t.Fatalf("read deleted seller: %v", err)
	}
	if stored.DeletedAt == nil {
		t.Fatal("seller should be soft-deleted")
	}
	if got := countActiveAdminsFixture(t, db); got != 1 {
		t.Fatalf("admin count changed unexpectedly: %d", got)
	}
}

func TestAdminUserIntegration_SoftDeletedUserIsHiddenFromAdminPaths(t *testing.T) {
	ctx := context.Background()
	db := openAdminUserDB(t)
	store := NewStore(db)
	svc := service.NewAdminUserService(store, store)

	actor := createAdminUserFixture(t, db, domain.RoleAdmin, true)
	target := createAdminUserFixture(t, db, domain.RoleCustomer, true)

	if err := svc.DeleteUser(ctx, service.AdminActor{UserID: actor.ID}, target.ID); err != nil {
		t.Fatalf("delete customer: %v", err)
	}

	t.Run("admin get returns not found", func(t *testing.T) {
		if _, err := svc.GetUser(ctx, target.ID); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound from admin get, got %v", err)
		}
	})

	t.Run("admin update does not resurrect the account", func(t *testing.T) {
		name := "Resurrected"
		if _, err := svc.UpdateUser(ctx, service.AdminActor{UserID: actor.ID}, target.ID, service.AdminUserUpdate{Name: &name}); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound from admin update, got %v", err)
		}
	})

	t.Run("admin delete returns not found", func(t *testing.T) {
		if err := svc.DeleteUser(ctx, service.AdminActor{UserID: actor.ID}, target.ID); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("expected ErrNotFound from admin delete, got %v", err)
		}
	})

	// AuthorizeSession still uses UserByID and must keep seeing the row so it
	// can reject the deleted account's session instead of treating it as gone.
	authUser, err := store.UserByID(ctx, target.ID)
	if err != nil {
		t.Fatalf("auth read of deleted user: %v", err)
	}
	if authUser.DeletedAt == nil {
		t.Fatal("auth read should still expose the soft-deleted row")
	}

	var stored domain.User
	if err := db.Unscoped().Where("id = ?", target.ID).First(&stored).Error; err != nil {
		t.Fatalf("read deleted customer: %v", err)
	}
	if stored.DeletedAt == nil {
		t.Fatal("customer should remain soft-deleted")
	}
	if stored.Name == "Resurrected" {
		t.Fatal("admin update must not mutate a soft-deleted account")
	}
}

func assertUserRoleAndActive(t *testing.T, db *gorm.DB, id string, role domain.UserRole, active bool) {
	t.Helper()
	var user domain.User
	if err := db.Where("id = ?", id).First(&user).Error; err != nil {
		t.Fatalf("read user: %v", err)
	}
	if user.Role != role || user.Active != active {
		t.Fatalf("user state = %s/%v, want %s/%v", user.Role, user.Active, role, active)
	}
}

func boolPtr(value bool) *bool { return &value }

func containsSecretField(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	for _, needle := range []string{"password", "credential", "secret", "token"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}
