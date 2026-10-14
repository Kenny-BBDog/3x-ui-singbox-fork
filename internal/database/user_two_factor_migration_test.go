package database

import (
	"path/filepath"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The panel-wide factor predates per-account ones, so every upgrade of a live
// panel passes through this migration. Getting it wrong locks the operator out
// of their own panel, which is why the column move and the secret move are
// covered separately here.
const legacyUserSettingDDL = `
CREATE TABLE "users" ("id" integer PRIMARY KEY AUTOINCREMENT,"username" text,"password" text,"login_epoch" integer DEFAULT 0);
CREATE TABLE "settings" ("id" integer PRIMARY KEY AUTOINCREMENT,"key" text,"value" text);
`

// seedLegacyPanel writes a pre-migration panel: two accounts, and a panel-wide
// 2FA pair in settings. An empty enableValue leaves the panel without one.
func seedLegacyPanel(t *testing.T, enableValue, tokenValue string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "x-ui.db")
	legacy, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if err := legacy.Exec(legacyUserSettingDDL).Error; err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	seed := `INSERT INTO users (username, password, login_epoch) VALUES ('admin', 'x', 0), ('ops', 'y', 0);`
	if enableValue != "" {
		seed += `INSERT INTO settings ("key", "value") VALUES ('twoFactorEnable', '` + enableValue + `'), ('twoFactorToken', '` + tokenValue + `');`
	}
	if err := legacy.Exec(seed).Error; err != nil {
		t.Fatalf("seed legacy rows: %v", err)
	}
	sqlDB, err := legacy.DB()
	if err != nil {
		t.Fatalf("legacy db handle: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}
	return dbPath
}

func loadAccount(t *testing.T, username string) model.User {
	t.Helper()
	var user model.User
	if err := GetDB().Where("username = ?", username).First(&user).Error; err != nil {
		t.Fatalf("load account %q: %v", username, err)
	}
	return user
}

func TestMigrateUserTwoFactorMovesTheFactorOntoTheFirstAccount(t *testing.T) {
	dbPath := seedLegacyPanel(t, "true", "JBSWY3DPEHPK3PXP")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB over legacy schema: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	if !GetDB().Migrator().HasColumn(&model.User{}, "two_factor_token") {
		t.Fatal("two_factor_token column missing after migration")
	}

	owner := loadAccount(t, "admin")
	if !owner.TwoFactorEnable || owner.TwoFactorToken != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("first account kept no factor: enabled=%v secret=%q", owner.TwoFactorEnable, owner.TwoFactorToken)
	}

	other := loadAccount(t, "ops")
	if other.TwoFactorEnable || other.TwoFactorToken != "" {
		t.Fatalf("second account inherited the panel-wide factor: enabled=%v", other.TwoFactorEnable)
	}

	// Re-running must not resurrect the old secret over a factor set since.
	if err := GetDB().Model(&model.User{}).Where("id = ?", owner.Id).
		Updates(map[string]any{"two_factor_token": "REBOUNDSECRET234567"}).Error; err != nil {
		t.Fatalf("rebind factor: %v", err)
	}
	if err := migrateUserTwoFactorColumns(); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
	if got := loadAccount(t, "admin").TwoFactorToken; got != "REBOUNDSECRET234567" {
		t.Fatalf("re-running the migration overwrote the factor with %q", got)
	}
}

func TestMigrateUserTwoFactorLeavesAPanelWithoutOneAlone(t *testing.T) {
	dbPath := seedLegacyPanel(t, "false", "JBSWY3DPEHPK3PXP")
	if err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB over legacy schema: %v", err)
	}
	t.Cleanup(func() { _ = CloseDB() })

	// Enabling it here would demand a code from an operator who never had an
	// authenticator — the lockout this migration exists to avoid.
	owner := loadAccount(t, "admin")
	if owner.TwoFactorEnable || owner.TwoFactorToken != "" {
		t.Fatalf("a panel with the factor off acquired one: enabled=%v secret=%q", owner.TwoFactorEnable, owner.TwoFactorToken)
	}
}
