package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/dbtest"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/web/locale"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/discord"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
)

func TestValidateRegex(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSettingController(router.Group("/panel/api"))

	tests := []struct {
		name    string
		body    string
		success bool
	}{
		{name: "Go RE2 inline flag", body: `{"regex":"(?m)^general-purpose$"}`, success: true},
		{name: "invalid expression", body: `{"regex":"["}`, success: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/validateRegex", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()

			router.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			needle := `"success":true`
			if !tt.success {
				needle = `"success":false`
			}
			if !strings.Contains(resp.Body.String(), needle) {
				t.Fatalf("body = %s, want %s", resp.Body.String(), needle)
			}
		})
	}
}

func TestAPITokenMutationRoutesEnforceExpectedScope(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	row := &model.ApiToken{Name: "route-scope", Token: crypto.HashTokenSHA256("token"), Enabled: true, Scope: model.ApiScopeNodeSync}
	if err := database.GetDB().Create(row).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSettingController(router.Group("/panel/api"))
	for _, path := range []string{
		"/panel/api/setting/apiTokens/delete/" + strconv.Itoa(row.Id),
		"/panel/api/setting/apiTokens/setEnabled/" + strconv.Itoa(row.Id),
	} {
		body := `{"expectedScope":"admin","enabled":false}`
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if !strings.Contains(resp.Body.String(), `"success":false`) {
			t.Fatalf("%s accepted wrong expected scope: %s", path, resp.Body.String())
		}
	}
	var stored model.ApiToken
	if err := database.GetDB().First(&stored, row.Id).Error; err != nil {
		t.Fatalf("token was deleted by wrong scope: %v", err)
	}
	if !stored.Enabled {
		t.Fatal("token was disabled by wrong scope")
	}
}

// GHSA-xqqw-jqqv-99h6: the settings save used to be the way to rebind the
// authenticator, so it had to demand a code in case it did. The second factor
// now belongs to the account, behind an endpoint that enforces that itself, so
// these payload fields are inert here. This fails if either is wired back up
// without the check returning.
func TestUpdateSettingCannotTouchTwoFactor(t *testing.T) {
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))

	const secret = "ORIGINALSECRET234567"
	userService := panel.UserService{}
	account, err := userService.CreateUser("ops", "correct-horse-battery")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := userService.SetTwoFactor(account.Id, true, secret, ""); err != nil {
		t.Fatalf("seed 2FA: %v", err)
	}

	settingService := service.SettingService{}
	base, err := settingService.GetAllSetting()
	if err != nil {
		t.Fatalf("GetAllSetting: %v", err)
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := map[string]any{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	body["twoFactorEnable"] = false
	body["twoFactorToken"] = "ATTACKERSECRET567890"
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSettingController(router.Group("/panel/api"))
	req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/update", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if got := resp.Body.String(); !strings.Contains(got, `"success":true`) {
		t.Fatalf("ordinary settings save rejected: %s", got)
	}

	var stored model.User
	if err := database.GetDB().First(&stored, account.Id).Error; err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if !stored.TwoFactorEnable || stored.TwoFactorToken != secret {
		t.Fatalf("settings save rewrote the account's second factor: enabled=%v secret=%q", stored.TwoFactorEnable, stored.TwoFactorToken)
	}
}

func TestTestDiscordEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Service not initialized
	SetDiscordService(nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return key })
		c.Next()
	})
	NewSettingController(router.Group("/panel/api"))

	req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/testDiscord", nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if !strings.Contains(resp.Body.String(), `"success":false`) || !strings.Contains(resp.Body.String(), "pages.settings.discordNotInitialized") {
		t.Fatalf("expected uninitialized error, got %s", resp.Body.String())
	}

	// Setup DB
	t.Setenv("XUI_DB_FOLDER", t.TempDir())
	dbtest.InitDB(t, filepath.Join(t.TempDir(), "x-ui.db"))
	t.Cleanup(func() { SetDiscordService(nil) })

	settingService := service.SettingService{}
	svc := discord.NewDiscordService(settingService)
	SetDiscordService(svc)

	// 2. Discord bot disabled
	_ = settingService.SetDiscordBotEnable(false)
	req = httptest.NewRequest(http.MethodPost, "/panel/api/setting/testDiscord", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if !strings.Contains(resp.Body.String(), `"success":false`) || !strings.Contains(resp.Body.String(), "pages.settings.discordBotNotEnabled") {
		t.Fatalf("expected disabled error, got %s", resp.Body.String())
	}

	// 3. Discord bot enabled but missing config
	_ = settingService.SetDiscordBotEnable(true)
	req = httptest.NewRequest(http.MethodPost, "/panel/api/setting/testDiscord", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if !strings.Contains(resp.Body.String(), `"success":false`) || !strings.Contains(resp.Body.String(), "pages.settings.discordTestFailed") {
		t.Fatalf("expected send failure error, got %s", resp.Body.String())
	}

	// 4. Discord bot enabled with working server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "msg-1"}`))
	}))
	defer server.Close()

	_ = settingService.SetDiscordBotToken("test-bot-token")
	_ = settingService.SetDiscordChannelId("123456789")
	svc.SetBaseURL(server.URL)
	svc.SetHTTPClient(server.Client())

	req = httptest.NewRequest(http.MethodPost, "/panel/api/setting/testDiscord", nil)
	resp = httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if !strings.Contains(resp.Body.String(), `"success":true`) || !strings.Contains(resp.Body.String(), "pages.settings.discordTestSuccess") {
		t.Fatalf("expected success, got %s", resp.Body.String())
	}
}
