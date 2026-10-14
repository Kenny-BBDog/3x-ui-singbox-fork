package controller

import (
	"strconv"

	"github.com/mhsanaei/3x-ui/v3/internal/util/common"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
	"github.com/mhsanaei/3x-ui/v3/internal/web/session"

	"github.com/gin-gonic/gin"
)

// UserController manages the panel's administrator accounts. Every account has
// the same reach over the panel, so the guards here are about not locking the
// panel out rather than about limiting what an administrator may do.
type UserController struct {
	userService panel.UserService
}

func NewUserController(g *gin.RouterGroup) *UserController {
	a := &UserController{}
	a.initRouter(g)
	return a
}

func (a *UserController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.list)
	g.POST("/add", a.add)
	g.POST("/del/:id", a.del)
	g.POST("/twoFactor", a.twoFactor)
}

func (a *UserController) list(c *gin.Context) {
	actor := session.GetLoginUser(c)
	if actor == nil {
		jsonMsg(c, I18nWeb(c, "pages.login.loginAgain"), common.NewError("not signed in"))
		return
	}
	users, err := a.userService.ListUsers(actor.Id)
	jsonObj(c, users, err)
}

type userAddForm struct {
	Username string `json:"username" form:"username"`
	Password string `json:"password" form:"password"`
}

func (a *UserController) add(c *gin.Context) {
	form := &userAddForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.users.toasts.failed"), err)
		return
	}
	user, err := a.userService.CreateUser(form.Username, form.Password)
	jsonMsgObj(c, I18nWeb(c, "pages.settings.users.toasts.added"), user, err)
}

func (a *UserController) del(c *gin.Context) {
	actor := session.GetLoginUser(c)
	if actor == nil {
		jsonMsg(c, I18nWeb(c, "pages.login.loginAgain"), common.NewError("not signed in"))
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.users.toasts.failed"), common.NewError("no such account"))
		return
	}
	err = a.userService.DeleteUser(id, actor.Id)
	jsonMsg(c, I18nWeb(c, "pages.settings.users.toasts.deleted"), err)
}

type userTwoFactorForm struct {
	Enable bool   `json:"enable" form:"enable"`
	Token  string `json:"token" form:"token"`
	Code   string `json:"code" form:"code"`
}

// twoFactor binds or clears the signed-in account's own authenticator. The
// service refuses a rebind or a clear that does not come with a current code.
func (a *UserController) twoFactor(c *gin.Context) {
	actor := session.GetLoginUser(c)
	if actor == nil {
		jsonMsg(c, I18nWeb(c, "pages.login.loginAgain"), common.NewError("not signed in"))
		return
	}
	form := &userTwoFactorForm{}
	if err := c.ShouldBind(form); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.users.toasts.failed"), err)
		return
	}
	err := a.userService.SetTwoFactor(actor.Id, form.Enable, form.Token, form.Code)
	jsonMsg(c, I18nWeb(c, "pages.settings.users.toasts.twoFactorSaved"), err)
}
