package panel

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	ldaputil "github.com/mhsanaei/3x-ui/v3/internal/util/ldap"
	"github.com/mhsanaei/3x-ui/v3/internal/util/totp"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// UserService provides business logic for panel accounts. Every account is an
// administrator over the whole panel; the second factor belongs to the account.
type UserService struct {
	settingService service.SettingService
}

func (s *UserService) db() *gorm.DB {
	return database.GetDB()
}

// GetFirstUser retrieves the first user from the database.
// This is typically used for initial setup or when there's only one admin user.
func (s *UserService) GetFirstUser() (*model.User, error) {
	user := &model.User{}
	if err := s.db().Model(model.User{}).First(user).Error; err != nil {
		return nil, err
	}
	return user, nil
}

// UserView is the browser-safe projection of an account: the password hash and
// the authenticator secret never leave the panel.
type UserView struct {
	Id              int    `json:"id" example:"1"`
	Username        string `json:"username" example:"admin"`
	TwoFactorEnable bool   `json:"twoFactorEnable" example:"true"`
	IsSelf          bool   `json:"isSelf" example:"false"`
}

// ListUsers returns every panel account, oldest first. actingUserId marks which
// row the caller is, so the UI knows which one it may not remove.
func (s *UserService) ListUsers(actingUserId int) ([]UserView, error) {
	var users []*model.User
	if err := s.db().Model(model.User{}).Order("id ASC").Find(&users).Error; err != nil {
		return nil, err
	}
	views := make([]UserView, 0, len(users))
	for _, user := range users {
		views = append(views, UserView{
			Id:              user.Id,
			Username:        user.Username,
			TwoFactorEnable: user.TwoFactorEnable,
			IsSelf:          user.Id == actingUserId,
		})
	}
	return views, nil
}

// AnyTwoFactorEnabled tells the login page whether to offer the code field. It
// cannot answer per username: doing that before the password is checked would
// turn the login response into an oracle for which accounts exist.
func (s *UserService) AnyTwoFactorEnabled() (bool, error) {
	var count int64
	err := s.db().Model(model.User{}).Where("two_factor_enable = ?", true).Count(&count).Error
	return count > 0, err
}

func (s *UserService) CheckUser(username string, password string, twoFactorCode string) (*model.User, error) {
	user := &model.User{}

	err := s.db().Model(model.User{}).
		Where("username = ?", username).
		First(user).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errors.New("invalid credentials")
	} else if err != nil {
		logger.Warning("check user err:", err)
		return nil, err
	}

	if !crypto.CheckPasswordHash(user.Password, password) {
		ldapEnabled, _ := s.settingService.GetLdapEnable()
		if !ldapEnabled {
			return nil, errors.New("invalid credentials")
		}

		host, _ := s.settingService.GetLdapHost()
		port, _ := s.settingService.GetLdapPort()
		useTLS, _ := s.settingService.GetLdapUseTLS()
		skipVerify, _ := s.settingService.GetLdapInsecureSkipVerify()
		bindDN, _ := s.settingService.GetLdapBindDN()
		ldapPass, _ := s.settingService.GetLdapPassword()
		baseDN, _ := s.settingService.GetLdapBaseDN()
		userFilter, _ := s.settingService.GetLdapUserFilter()
		userAttr, _ := s.settingService.GetLdapUserAttr()

		cfg := ldaputil.Config{
			Host:               host,
			Port:               port,
			UseTLS:             useTLS,
			InsecureSkipVerify: skipVerify,
			BindDN:             bindDN,
			Password:           ldapPass,
			BaseDN:             baseDN,
			UserFilter:         userFilter,
			UserAttr:           userAttr,
		}
		ok, err := ldaputil.AuthenticateUser(cfg, username, password)
		if err != nil || !ok {
			return nil, errors.New("invalid credentials")
		}
	}

	if user.TwoFactorEnable {
		if !totp.VerifyWithSkew(user.TwoFactorToken, twoFactorCode, time.Now()) {
			return nil, errors.New("invalid 2fa code")
		}
	}

	return user, nil
}

// CreateUser adds a panel account. It is created without a second factor: the
// operator who adds an account is handing over access, not setting up its
// authenticator, which only the account's own holder should bind.
func (s *UserService) CreateUser(username string, password string) (*UserView, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, common.NewError("username can not be empty")
	}
	if password == "" {
		return nil, common.NewError("password can not be empty")
	}
	hashedPassword, err := crypto.HashPasswordAsBcrypt(password)
	if err != nil {
		return nil, err
	}

	var taken int64
	if err := s.db().Model(model.User{}).Where("username = ?", username).Count(&taken).Error; err != nil {
		return nil, err
	}
	if taken > 0 {
		return nil, common.NewError("username is already taken")
	}

	user := &model.User{Username: username, Password: hashedPassword}
	if err := s.db().Model(model.User{}).Create(user).Error; err != nil {
		return nil, err
	}
	return &UserView{Id: user.Id, Username: user.Username, TwoFactorEnable: false}, nil
}

// DeleteUser removes another panel account. Removing your own is refused rather
// than allowed-then-invalidated: the caller's session is keyed on the row.
func (s *UserService) DeleteUser(id int, actingUserId int) error {
	if id == actingUserId {
		return common.NewError("you can not delete the account you are signed in with")
	}
	var exists int64
	if err := s.db().Model(model.User{}).Where("id = ?", id).Count(&exists).Error; err != nil {
		return err
	}
	if exists == 0 {
		return common.NewError("no such account")
	}
	return s.db().Model(model.User{}).Where("id = ?", id).Delete(&model.User{}).Error
}

// SetTwoFactor binds or clears one account's authenticator. Rebinding or
// clearing a factor that is already set requires a current code, so a stolen
// session cannot quietly replace or drop it (GHSA-xqqw-jqqv-99h6). Enabling
// without a secret is refused: that combination is a permanent lockout, since
// the login check would then demand a code no authenticator can produce.
// Enabling also ends that account's sessions, so its holder has to prove the
// factor works before the change is relied on, as the panel-wide flag did.
func (s *UserService) SetTwoFactor(id int, enable bool, token string, code string) error {
	var user model.User
	if err := s.db().Model(model.User{}).Where("id = ?", id).First(&user).Error; err != nil {
		return common.NewError("no such account")
	}
	if user.TwoFactorEnable {
		if err := s.VerifyTwoFactorCode(&user, code); err != nil {
			return err
		}
	}

	token = strings.TrimSpace(token)
	if enable && token == "" {
		return common.NewError("a secret is required to enable two-factor authentication")
	}
	if !enable {
		token = ""
	}
	updates := map[string]any{"two_factor_enable": enable, "two_factor_token": token}
	if enable {
		updates["login_epoch"] = gorm.Expr("login_epoch + 1")
	}
	return s.db().Model(model.User{}).Where("id = ?", id).Updates(updates).Error
}

// VerifyTwoFactorCode checks a code against one account's own secret, and is a
// no-op for an account that has no second factor.
func (s *UserService) VerifyTwoFactorCode(user *model.User, code string) error {
	if user == nil {
		return common.NewError("not signed in")
	}
	if !user.TwoFactorEnable {
		return nil
	}
	if !totp.VerifyWithSkew(user.TwoFactorToken, code, time.Now()) {
		return common.NewError("invalid 2fa code")
	}
	return nil
}

func (s *UserService) BumpLoginEpoch() error {
	return s.db().Model(model.User{}).
		Where("1 = 1").
		Update("login_epoch", gorm.Expr("login_epoch + 1")).
		Error
}

// ResetAllTwoFactor clears the second factor on every account. This is the
// lockout recovery path, so it deliberately cannot be partial.
func (s *UserService) ResetAllTwoFactor() error {
	return s.db().Model(model.User{}).
		Where("1 = 1").
		Updates(map[string]any{"two_factor_enable": false, "two_factor_token": ""}).
		Error
}

// UpdateUser changes an account's own credentials. The second factor goes with
// them, as it did when it was a panel-wide flag, and dropping it needs the same
// current code SetTwoFactor asks for.
func (s *UserService) UpdateUser(id int, username string, password string, code string) error {
	if err := s.SetTwoFactor(id, false, "", code); err != nil {
		return err
	}
	hashedPassword, err := crypto.HashPasswordAsBcrypt(password)
	if err != nil {
		return err
	}

	return s.db().Model(model.User{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"username":    username,
			"password":    hashedPassword,
			"login_epoch": gorm.Expr("login_epoch + 1"),
		}).
		Error
}

func (s *UserService) UpdateFirstUser(username string, password string) error {
	if username == "" {
		return errors.New("username can not be empty")
	} else if password == "" {
		return errors.New("password can not be empty")
	}
	hashedPassword, er := crypto.HashPasswordAsBcrypt(password)

	if er != nil {
		return er
	}

	user := &model.User{}
	err := s.db().Model(model.User{}).First(user).Error
	if database.IsNotFound(err) {
		user.Username = username
		user.Password = hashedPassword
		return s.db().Model(model.User{}).Create(user).Error
	} else if err != nil {
		return err
	}
	user.Username = username
	user.Password = hashedPassword
	user.LoginEpoch++
	return s.db().Save(user).Error
}
