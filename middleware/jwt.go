package middleware

import (
	"net/http"
	"time"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/log"

	"github.com/gin-gonic/gin"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/utils"
)

// JWTMiddleWare 中间件
func JWTMiddleWare(c *gin.Context) {
	code := errno.OK
	strToken := c.Request.Header.Get("Authorization")
	apiKey := c.Request.Header.Get("apikey")
	token := utils.GetToken(strToken)
	log.Debugf("jwt[%s], apikey[%s]", token, apiKey)

	var err error
	var claims *utils.Claims

	// 检查 API Key
	if apiKey == "endata2025" {
		c.Next()
		return
	}

	if token == "" {
		code = errno.ErrNotAuthorized
	} else {
		claims, err = utils.ParseToken(token)
		if err != nil {
			code = errno.ErrAuthTokenErr
		} else if time.Now().Unix() > claims.ExpiresAt.Unix() {
			code = errno.ErrAuthTimeout
		}
	}

	if code != errno.OK {
		c.JSON(http.StatusOK, code)
		c.Abort()
		return
	}

	log.Debugf("id:%s UserName:%s", claims.ID, claims.UserName)

	c.Set("jwt", claims)

	c.Next()
}
