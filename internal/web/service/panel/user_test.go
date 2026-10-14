package panel

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xlzd/gotp"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

const userTestSecret = "JBSWY3DPEHPK3PXP"

func setupUserTestDB(t *testing.T) {
	t.Helper()
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	dbtest.InitDB(t, filepath.Join(dbDir, "x-ui.db"))
}

func userTestCode() string {
	return gotp.NewDefaultTOTP(userTestSecret).Now()
}

func mustCreateUser(t *testing.T, svc *UserService, username, password string) *UserView {
	t.Helper()
	user, err := svc.CreateUser(username, password)
	if err != nil {
		t.Fatalf("create %s: %v", username, err)
	}
	return user
}

func reloadUser(t *testing.T, id int) model.User {
	t.Helper()
	var stored model.User
	if err := database.GetDB().First(&stored, id).Error; err != nil {
		t.Fatalf("reload account %d: %v", id, err)
	}
	return stored
}

// The point of a per-account factor: an administrator added without an
// authenticator signs in with no code while the first one keeps theirs. This is
// what fails if the factor ever goes back to being a panel-wide flag.
func TestCheckUserAsksForACodeOnlyFromTheAccountThatHasOne(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}

	owner := mustCreateUser(t, svc, "owner", "owner-password")
	if err := svc.SetTwoFactor(owner.Id, true, userTestSecret, ""); err != nil {
		t.Fatalf("enable owner factor: %v", err)
	}
	mustCreateUser(t, svc, "helper", "helper-password")

	if _, err := svc.CheckUser("helper", "helper-password", ""); err != nil {
		t.Fatalf("account with no factor was asked for one: %v", err)
	}
	if _, err := svc.CheckUser("owner", "owner-password", ""); err == nil {
		t.Fatal("account with a factor signed in with no code")
	}
	if _, err := svc.CheckUser("owner", "owner-password", userTestCode()); err != nil {
		t.Fatalf("valid code rejected: %v", err)
	}
}

// GHSA-xqqw-jqqv-99h6 held that 2FA could not be rebound without a current code.
// The check moved with the secret onto the account; it has to hold for rebinding
// and for clearing alike, or a stolen session can strip the factor.
func TestSetTwoFactorRefusesAChangeWithNoCurrentCode(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	owner := mustCreateUser(t, svc, "owner", "owner-password")
	if err := svc.SetTwoFactor(owner.Id, true, userTestSecret, ""); err != nil {
		t.Fatalf("enable owner factor: %v", err)
	}

	for _, tc := range []struct {
		name   string
		enable bool
		token  string
	}{
		{name: "rebind", enable: true, token: "ATTACKERSECRET234567"},
		{name: "clear", enable: false, token: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := svc.SetTwoFactor(owner.Id, tc.enable, tc.token, ""); err == nil {
				t.Fatal("change accepted with no current code")
			}
			stored := reloadUser(t, owner.Id)
			if !stored.TwoFactorEnable || stored.TwoFactorToken != userTestSecret {
				t.Fatalf("factor changed anyway: enabled=%v secret=%q", stored.TwoFactorEnable, stored.TwoFactorToken)
			}
		})
	}

	if err := svc.SetTwoFactor(owner.Id, true, "NEWSECRET234567890", userTestCode()); err != nil {
		t.Fatalf("rebind with a current code rejected: %v", err)
	}
	if stored := reloadUser(t, owner.Id); stored.TwoFactorToken != "NEWSECRET234567890" {
		t.Fatalf("secret = %q, want the rebound one", stored.TwoFactorToken)
	}
}

// Enabling with no secret leaves the login check demanding a code that no
// authenticator can produce — an account nobody can ever sign into.
func TestSetTwoFactorRefusesEnableWithoutASecret(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	owner := mustCreateUser(t, svc, "owner", "owner-password")

	if err := svc.SetTwoFactor(owner.Id, true, "   ", ""); err == nil {
		t.Fatal("enabling with a blank secret was accepted")
	}
	if stored := reloadUser(t, owner.Id); stored.TwoFactorEnable {
		t.Fatal("account left demanding a code it can never produce")
	}
}

func TestCreateUserRefusesADuplicateUsername(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	mustCreateUser(t, svc, "owner", "owner-password")

	if _, err := svc.CreateUser("owner", "different-password"); err == nil {
		t.Fatal("duplicate username accepted; sign-in would be ambiguous")
	}
}

func TestDeleteUserRefusesTheSignedInAccount(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	owner := mustCreateUser(t, svc, "owner", "owner-password")
	helper := mustCreateUser(t, svc, "helper", "helper-password")

	if err := svc.DeleteUser(owner.Id, owner.Id); err == nil {
		t.Fatal("deleting the signed-in account was accepted")
	}
	if err := svc.DeleteUser(helper.Id, owner.Id); err != nil {
		t.Fatalf("deleting another account failed: %v", err)
	}
	var remaining int64
	if err := database.GetDB().Model(model.User{}).Where("id = ?", helper.Id).Count(&remaining).Error; err != nil {
		t.Fatalf("count accounts: %v", err)
	}
	if remaining != 0 {
		t.Fatal("the removed account is still there")
	}
}

// The list feeds the settings page, so it must carry neither what authenticates
// nor what unlocks. Returning the model instead of the view would leak both.
func TestListUsersCarriesNoCredentialsOrSecrets(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	owner := mustCreateUser(t, svc, "owner", "owner-password")
	if err := svc.SetTwoFactor(owner.Id, true, userTestSecret, ""); err != nil {
		t.Fatalf("enable owner factor: %v", err)
	}

	views, err := svc.ListUsers(owner.Id)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var self *UserView
	for i := range views {
		if views[i].Id == owner.Id {
			self = &views[i]
		}
	}
	if self == nil {
		t.Fatalf("the signed-in account is missing from %+v", views)
	}
	if !self.IsSelf {
		t.Fatalf("the signed-in account was not marked as self: %+v", *self)
	}

	raw, err := json.Marshal(views)
	if err != nil {
		t.Fatalf("marshal views: %v", err)
	}
	for _, leak := range []string{userTestSecret, reloadUser(t, owner.Id).Password} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("user list leaked %q: %s", leak, raw)
		}
	}
}

// The CLI's -resetTwoFactor is the way back in for an operator who lost the
// authenticator, so it has to clear every account rather than just the first.
func TestResetAllTwoFactorClearsEveryAccount(t *testing.T) {
	setupUserTestDB(t)
	svc := &UserService{}
	owner := mustCreateUser(t, svc, "owner", "owner-password")
	helper := mustCreateUser(t, svc, "helper", "helper-password")
	if err := svc.SetTwoFactor(owner.Id, true, userTestSecret, ""); err != nil {
		t.Fatalf("enable owner factor: %v", err)
	}
	if err := svc.SetTwoFactor(helper.Id, true, "JBSWY3DPEHPK3PXQ", ""); err != nil {
		t.Fatalf("enable helper factor: %v", err)
	}

	if err := svc.ResetAllTwoFactor(); err != nil {
		t.Fatalf("ResetAllTwoFactor: %v", err)
	}

	for _, id := range []int{owner.Id, helper.Id} {
		if stored := reloadUser(t, id); stored.TwoFactorEnable || stored.TwoFactorToken != "" {
			t.Fatalf("account %d kept its factor: enabled=%v secret=%q", id, stored.TwoFactorEnable, stored.TwoFactorToken)
		}
	}
}
