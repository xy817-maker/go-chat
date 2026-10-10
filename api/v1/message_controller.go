package v1

import (
	"github.com/gin-gonic/gin"
	"kama_chat_server/internal/dto/request"
	"kama_chat_server/internal/service/gorm"
	"kama_chat_server/pkg/constants"
	"net/http"
)

// GetMessageList 获取聊天记录
func GetMessageList(c *gin.Context) {
	var req request.GetMessageListRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code":    500,
			"message": constants.SYSTEM_ERROR,
		})
		return
	}
	message, rsp, ret := gorm.MessageService.GetMessageList(req.SessionId, req.UserOneId, req.UserTwoId)
	JsonBack(c, message, ret, rsp)
}

// GetGroupMessageList 获取群聊消息记录
func GetGroupMessageList(c *gin.Context) {
	var req request.GetGroupMessageListRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code":    500,
			"message": constants.SYSTEM_ERROR,
		})
		return
	}
	message, rsp, ret := gorm.MessageService.GetGroupMessageList(req.GroupId)
	JsonBack(c, message, ret, rsp)
}

// UploadAvatar 上传头像
func UploadAvatar(c *gin.Context) {
	message, ret := gorm.MessageService.UploadAvatar(c)
	JsonBack(c, message, ret, nil)
}

// UploadFile 上传头像
func UploadFile(c *gin.Context) {
	message, ret := gorm.MessageService.UploadFile(c)
	JsonBack(c, message, ret, nil)
}

// GetOfflineDigest 上线拉取所有会话摘要（最后消息 + 未读数）
func GetOfflineDigest(c *gin.Context) {
	var req request.GetOfflineDigestRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code":    500,
			"message": constants.SYSTEM_ERROR,
		})
		return
	}
	rsp, ret := gorm.MessageService.GetOfflineDigest(req.UserId)
	JsonBack(c, "", ret, rsp)
}

// MarkSessionRead 标记会话已读
func MarkSessionRead(c *gin.Context) {
	var req request.MarkSessionReadRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code":    500,
			"message": constants.SYSTEM_ERROR,
		})
		return
	}
	message, ret := gorm.MessageService.MarkSessionRead(req.UserId, req.SessionId)
	JsonBack(c, message, ret, nil)
}
