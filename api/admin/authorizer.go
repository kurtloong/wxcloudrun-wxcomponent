package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/errno"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/log"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx"
	wxbase "github.com/WeixinCloud/wxcloudrun-wxcomponent/comm/wx/base"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/dao"
	"github.com/WeixinCloud/wxcloudrun-wxcomponent/db/model"
	"github.com/gin-gonic/gin"
)

type getAuthorizerListReq struct {
	ComponentAppid string `wx:"component_appid"`
	Offset         int    `wx:"offset"`
	Count          int    `wx:"count"`
}

type authorizerInfo struct {
	AuthorizerAppid string `wx:"authorizer_appid"`
	RefreshToken    string `wx:"refresh_token"`
	AuthTime        int64  `wx:"auth_time"`
}
type getAuthorizerListResp struct {
	TotalCount int              `wx:"total_count"`
	List       []authorizerInfo `wx:"list"`
}

type getAuthorizerInfoResp struct {
	model.Authorizer
	RegisterType  int                       `json:"registerType"`
	AccountStatus int                       `json:"accountStatus"`
	BasicConfig   *wx.AuthorizerBasicConfig `json:"basicConfig"`
}

func pullAuthorizerListHandler(c *gin.Context) {
	go func() {
		count := 100
		offset := 0
		total := 0
		now := time.Now()
		for {
			var resp getAuthorizerListResp
			if err := getAuthorizerList(offset, count, &resp); err != nil {
				log.Error(err)
				return
			}
			if total == 0 {
				total = resp.TotalCount
			}
			// 插入数据库
			length := len(resp.List)
			records := make([]model.Authorizer, length)
			var wg sync.WaitGroup
			wg.Add(length)
			for i, info := range resp.List {
				go constructAuthorizerRecord(info, &records[i], &wg)
			}
			wg.Wait()
			dao.BatchCreateOrUpdateAuthorizerRecord(&records)

			if length < count {
				break
			}
			offset += count
		}

		// 删除记录
		if err := dao.ClearAuthorizerRecordsBefore(now); err != nil {
			log.Error(err)
			return
		}
	}()
	c.JSON(http.StatusOK, errno.OK)
}

func copyAuthorizerInfo(appinfo *wx.AuthorizerInfoResp, record *model.Authorizer) {
	record.AppType = appinfo.AuthorizerInfo.AppType
	record.ServiceType = appinfo.AuthorizerInfo.ServiceType.Id
	record.NickName = appinfo.AuthorizerInfo.NickName
	record.UserName = appinfo.AuthorizerInfo.UserName
	record.HeadImg = appinfo.AuthorizerInfo.HeadImg
	record.QrcodeUrl = appinfo.AuthorizerInfo.QrcodeUrl
	record.PrincipalName = appinfo.AuthorizerInfo.PrincipalName
	record.FuncInfo = appinfo.AuthorizationInfo.StrFuncInfo
	record.VerifyInfo = appinfo.AuthorizerInfo.VerifyInfo.Id
}

func constructAuthorizerRecord(info authorizerInfo, record *model.Authorizer, wg *sync.WaitGroup) error {
	defer wg.Done()
	record.Appid = info.AuthorizerAppid
	record.AuthTime = time.Unix(info.AuthTime, 0)
	record.RefreshToken = info.RefreshToken
	var appinfo wx.AuthorizerInfoResp

	if err := wx.GetAuthorizerInfo(record.Appid, &appinfo); err != nil {
		log.Errorf("GetAuthorizerInfo fail %v", err)
		return err
	}
	copyAuthorizerInfo(&appinfo, record)
	return nil
}

func getAuthorizerList(offset, count int, resp *getAuthorizerListResp) error {
	req := getAuthorizerListReq{
		ComponentAppid: wxbase.GetAppid(),
		Offset:         offset,
		Count:          count,
	}
	_, body, err := wx.PostWxJsonWithComponentToken("/cgi-bin/component/api_get_authorizer_list", "", req)
	if err != nil {
		return err
	}
	if err := wx.WxJson.Unmarshal(body, &resp); err != nil {
		log.Errorf("Unmarshal err, %v", err)
		return err
	}
	return nil
}

func getAuthorizerListHandler(c *gin.Context) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrInvalidParam.WithData(err.Error()))
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "10"))
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrInvalidParam.WithData(err.Error()))
		return
	}
	if limit > 20 {
		c.JSON(http.StatusOK, errno.ErrInvalidParam)
		return
	}
	appid := c.DefaultQuery("appid", "")
	records, total, err := dao.GetAuthorizerRecords(appid, offset, limit)
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}
	// 拉取最新的数据
	wg := &sync.WaitGroup{}
	wg.Add(len(records))
	resp := make([]getAuthorizerInfoResp, len(records))
	for i, record := range records {
		go func(i int, record *model.Authorizer) {
			defer wg.Done()
			resp[i].Appid = record.Appid
			resp[i].AuthTime = record.AuthTime
			resp[i].RefreshToken = record.RefreshToken

			var appinfo wx.AuthorizerInfoResp
			if err := wx.GetAuthorizerInfo(record.Appid, &appinfo); err != nil {
				log.Errorf("GetAuthorizerInfo fail %v", err)
				return
			}
			copyAuthorizerInfo(&appinfo, &resp[i].Authorizer)
			resp[i].RegisterType = appinfo.AuthorizerInfo.RegisterType
			resp[i].AccountStatus = appinfo.AuthorizerInfo.AccountStatus
			resp[i].BasicConfig = appinfo.AuthorizerInfo.BasicConfig
		}(i, record)
	}
	wg.Wait()

	// 异步更新数据库
	go func(oldRecords []*model.Authorizer, newRecords *[]getAuthorizerInfoResp) {
		var updateRecords []model.Authorizer
		for i, newRecord := range *newRecords {
			newRecord.ID = oldRecords[i].ID
			if *oldRecords[i] != newRecord.Authorizer {
				updateRecords = append(updateRecords, newRecord.Authorizer)
			}
		}
		if len(updateRecords) != 0 {
			log.Info("update records: ", updateRecords)
			dao.BatchCreateOrUpdateAuthorizerRecord(&updateRecords)
		} else {
			log.Info("no update")
		}
	}(records, &resp)
	c.JSON(http.StatusOK, errno.OK.WithData(gin.H{"total": total, "records": resp}))
}

// 入参 appId 或者 originId
// 返回 授权信息
func getArticlesummaryHandler(c *gin.Context) {
	appId := c.DefaultQuery("appId", "")
	originId := c.DefaultQuery("originId", "")
	//begin_date 默认近一个月
	beginDate := c.DefaultQuery("beginDate", time.Now().AddDate(0, -1, 0).Format("20060102"))
	//end_date
	endDate := c.DefaultQuery("endDate", time.Now().Format("20060102"))



	// 如果都为空
	if appId == "" && originId == "" {
		log.Error("appId and originId are empty")	
		c.JSON(http.StatusOK, errno.ErrInvalidParam)
		return
	}

	// 如果appId为空，则根据originId查询
	if appId == "" {
		record := model.Authorizer{}
		db.Get().Table("authorizer_records").Where("username = ?", originId).First(&record)
		if record.Appid == "" {
			log.Error("authorizer not found")
			c.JSON(http.StatusOK, errno.ErrInvalidParam)
			return
		}

		appId = record.Appid
		log.Info("appId: ", appId)
	}

	token, err := wx.GetAuthorizerAccessToken(appId)
	log.Info("token: ", token)
	if err != nil {
		log.Error("GetAuthorizerAccessToken fail: ", err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	//https://api.weixin.qq.com/datacube/getarticlesummary?access_token=ACCESS_TOKEN
	//POST
	//{
	//"begin_date":"20170301",
	//"end_date":"20170301"
	//}

	// 组装请求体
	body := fmt.Sprintf(`{"begin_date":"%s","end_date":"%s"}`, beginDate, endDate)

	resp, err := http.Post(fmt.Sprintf("https://api.weixin.qq.com/datacube/getarticlesummary?access_token=%s", token), "application/json", bytes.NewBuffer([]byte(body)))
	log.Info("resp: ", resp)
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 解析响应
	var respData map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 返回响应
	c.JSON(http.StatusOK, errno.OK.WithData(respData))
}


// https://api.weixin.qq.com/datacube/getusersummary?access_token=ACCESS_TOKEN
// POST
// {
// "begin_date":"20170301",
// "end_date":"20170301"
// }

func getUsersummaryHandler(c *gin.Context) {
	appId := c.DefaultQuery("appId", "")
	originId := c.DefaultQuery("originId", "")
	beginDate := c.DefaultQuery("beginDate", time.Now().AddDate(0, -1, 0).Format("20060102"))
	endDate := c.DefaultQuery("endDate", time.Now().Format("20060102"))

	// 如果都为空
	if appId == "" && originId == "" {
		log.Error("appId and originId are empty")	
		c.JSON(http.StatusOK, errno.ErrInvalidParam)
		return
	}

	// 如果appId为空，则根据originId查询
	if appId == "" {
		record := model.Authorizer{}
		db.Get().Table("authorizer_records").Where("username = ?", originId).First(&record)
		if record.Appid == "" {
			log.Error("authorizer not found")
			c.JSON(http.StatusOK, errno.ErrInvalidParam)
			return
		}

		appId = record.Appid
	}

	token, err := wx.GetAuthorizerAccessToken(appId)
	log.Info("token: ", token)
	if err != nil {
		log.Error("GetAuthorizerAccessToken fail: ", err)
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 组装请求体
	body := fmt.Sprintf(`{"begin_date":"%s","end_date":"%s"}`, beginDate, endDate)

	resp, err := http.Post(fmt.Sprintf("https://api.weixin.qq.com/datacube/getusersummary?access_token=%s", token), "application/json", bytes.NewBuffer([]byte(body)))
	log.Info("resp: ", resp)
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 解析响应
	var respData map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&respData)
	if err != nil {
		c.JSON(http.StatusOK, errno.ErrSystemError.WithData(err.Error()))
		return
	}

	// 返回响应
	log.Info("respData: ", respData)
	c.JSON(http.StatusOK, errno.OK.WithData(respData))
}
