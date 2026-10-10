package gorm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"kama_chat_server/internal/config"
	"kama_chat_server/internal/dao"
	"kama_chat_server/internal/dto/respond"
	"kama_chat_server/internal/model"
	myredis "kama_chat_server/internal/service/redis"
	"kama_chat_server/pkg/constants"
	"kama_chat_server/pkg/zlog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

type messageService struct {
}

var MessageService = new(messageService)

// GetMessageList 获取聊天记录
// 缓存 key 用会话 ID（双方共读一份，写库后删除即失效）
func (m *messageService) GetMessageList(sessionId, userOneId, userTwoId string) (string, []respond.GetMessageListRespond, int) {
	rspString, err := myredis.GetKeyNilIsErr("message_list_" + sessionId)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			zlog.Info(err.Error())
			zlog.Info(fmt.Sprintf("%s %s", userTwoId, userTwoId))
			var messageList []model.Message
			if res := dao.GormDB.Where("(send_id = ? AND receive_id = ?) OR (send_id = ? AND receive_id = ?)", userOneId, userTwoId, userTwoId, userOneId).Order("created_at ASC").Find(&messageList); res.Error != nil {
				zlog.Error(res.Error.Error())
				return constants.SYSTEM_ERROR, nil, -1
			}
			var rspList []respond.GetMessageListRespond
			for _, message := range messageList {
				rspList = append(rspList, respond.GetMessageListRespond{
					SendId:     message.SendId,
					SendName:   message.SendName,
					SendAvatar: message.SendAvatar,
					ReceiveId:  message.ReceiveId,
					Content:    message.Content,
					Url:        message.Url,
					Type:       message.Type,
					FileType:   message.FileType,
					FileName:   message.FileName,
					FileSize:   message.FileSize,
					CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
				})
			}
			rspString, err := json.Marshal(rspList)
			if err != nil {
				zlog.Error(err.Error())
			}
			if err := myredis.SetKeyEx("message_list_"+sessionId, string(rspString), time.Minute*constants.REDIS_TIMEOUT); err != nil {
				zlog.Error(err.Error())
			}
			return "获取聊天记录成功", rspList, 0
		} else {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, nil, -1
		}
	}
	var rsp []respond.GetMessageListRespond
	if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
		zlog.Error(err.Error())
	}
	return "获取群聊记录成功", rsp, 0
}

// GetGroupMessageList 获取群聊消息记录
func (m *messageService) GetGroupMessageList(groupId string) (string, []respond.GetGroupMessageListRespond, int) {
	rspString, err := myredis.GetKeyNilIsErr("group_messagelist_" + groupId)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			var messageList []model.Message
			if res := dao.GormDB.Where("receive_id = ?", groupId).Order("created_at ASC").Find(&messageList); res.Error != nil {
				zlog.Error(res.Error.Error())
				return constants.SYSTEM_ERROR, nil, -1
			}
			var rspList []respond.GetGroupMessageListRespond
			for _, message := range messageList {
				rsp := respond.GetGroupMessageListRespond{
					SendId:     message.SendId,
					SendName:   message.SendName,
					SendAvatar: message.SendAvatar,
					ReceiveId:  message.ReceiveId,
					Content:    message.Content,
					Url:        message.Url,
					Type:       message.Type,
					FileType:   message.FileType,
					FileName:   message.FileName,
					FileSize:   message.FileSize,
					CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
				}
				rspList = append(rspList, rsp)
			}
			rspString, err := json.Marshal(rspList)
			if err != nil {
				zlog.Error(err.Error())
			}
			// 回源后回写缓存，后续打开群聊直接命中 Redis
			if err := myredis.SetKeyEx("group_messagelist_"+groupId, string(rspString), time.Minute*constants.REDIS_TIMEOUT); err != nil {
				zlog.Error(err.Error())
			}
			return "获取聊天记录成功", rspList, 0
		} else {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, nil, -1
		}
	}
	var rsp []respond.GetGroupMessageListRespond
	if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
		zlog.Error(err.Error())
	}
	return "获取聊天记录成功", rsp, 0
}

// UploadAvatar 上传头像
func (m *messageService) UploadAvatar(c *gin.Context) (string, int) {
	if err := c.Request.ParseMultipartForm(constants.FILE_MAX_SIZE); err != nil {
		zlog.Error(err.Error())
		return constants.SYSTEM_ERROR, -1
	}
	mForm := c.Request.MultipartForm
	for key, _ := range mForm.File {
		file, fileHeader, err := c.Request.FormFile(key)
		if err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		defer file.Close()
		zlog.Info(fmt.Sprintf("文件名：%s，文件大小：%d", fileHeader.Filename, fileHeader.Size))
		// 原来Filename应该是213451545.xxx，将Filename修改为avatar_ownerId.xxx
		ext := filepath.Ext(fileHeader.Filename)
		zlog.Info(ext)
		localFileName := config.GetConfig().StaticAvatarPath + "/" + fileHeader.Filename
		out, err := os.Create(localFileName)
		if err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		defer out.Close()
		if _, err := io.Copy(out, file); err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		zlog.Info("完成文件上传")
	}
	return "上传成功", 0
}

// UploadFile 上传文件
func (m *messageService) UploadFile(c *gin.Context) (string, int) {
	if err := c.Request.ParseMultipartForm(constants.FILE_MAX_SIZE); err != nil {
		zlog.Error(err.Error())
		return constants.SYSTEM_ERROR, -1
	}
	mForm := c.Request.MultipartForm
	for key, _ := range mForm.File {
		file, fileHeader, err := c.Request.FormFile(key)
		if err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		defer file.Close()
		zlog.Info(fmt.Sprintf("文件名：%s，文件大小：%d", fileHeader.Filename, fileHeader.Size))
		// 原来Filename应该是213451545.xxx，将Filename修改为avatar_ownerId.xxx
		ext := filepath.Ext(fileHeader.Filename)
		zlog.Info(ext)
		localFileName := config.GetConfig().StaticFilePath + "/" + fileHeader.Filename
		out, err := os.Create(localFileName)
		if err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		defer out.Close()
		if _, err := io.Copy(out, file); err != nil {
			zlog.Error(err.Error())
			return constants.SYSTEM_ERROR, -1
		}
		zlog.Info("完成文件上传")
	}
	return "上传成功", 0
}

// GetOfflineDigest 上线拉取所有会话的摘要（最后消息 + 未读数），用于会话列表展示
func (m *messageService) GetOfflineDigest(userId string) ([]respond.OfflineDigestItem, int) {
	var result []respond.OfflineDigestItem

	// 1. 单聊会话（最后消息已存在 session 表）
	var sessions []model.Session
	if res := dao.GormDB.Where("send_id = ? OR receive_id = ?", userId, userId).Find(&sessions); res.Error != nil {
		zlog.Error("查单聊会话失败: " + res.Error.Error())
	}
	// 按联系人去重：会话唯一化后正常一个联系人一条；历史上可能残留 A→B、B→A 两条，
	// 保留最早创建的那条（与 OpenSession 双向查找取最早一条一致，该条会被后续消息持续更新）
	dedup := make(map[string]model.Session)
	for _, session := range sessions {
		contactId := session.ReceiveId
		if session.ReceiveId == userId {
			contactId = session.SendId
		}
		if exist, ok := dedup[contactId]; !ok || session.CreatedAt.Before(exist.CreatedAt) {
			dedup[contactId] = session
		}
	}
	for contactId, session := range dedup {
		var user model.UserInfo
		dao.GormDB.Where("uuid = ?", contactId).First(&user)
		lastMsgTime := ""
		if session.LastMessageAt.Valid {
			lastMsgTime = session.LastMessageAt.Time.Format("2006-01-02 15:04:05")
		}
		result = append(result, respond.OfflineDigestItem{
			SessionId:       session.Uuid,
			ContactId:       contactId,
			ContactName:     user.Nickname,
			ContactAvatar:   user.Avatar,
			ContactType:     "U",
			LastMessage:     session.LastMessage,
			LastMessageTime: lastMsgTime,
			UnreadCount:     m.countUnread(userId, contactId, session.Uuid, "session"),
		})
	}

	// 2. 群聊会话（最后消息需查 message 表）
	var groups []model.GroupInfo
	if res := dao.GormDB.Where("status = 0").Find(&groups); res.Error != nil {
		zlog.Error("查群聊失败: " + res.Error.Error())
	}
	for _, group := range groups {
		var members []string
		if err := json.Unmarshal(group.Members, &members); err != nil {
			continue
		}
		isMember := false
		for _, member := range members {
			if member == userId {
				isMember = true
				break
			}
		}
		if !isMember {
			continue
		}
		var lastMsg model.Message
		lastMsgRes := dao.GormDB.Where("receive_id = ?", group.Uuid).Order("created_at DESC").First(&lastMsg)
		lastMessage := ""
		lastMsgTime := ""
		if lastMsgRes.Error == nil {
			if lastMsg.Type == 2 {
				lastMessage = "[文件] " + lastMsg.FileName
			} else {
				lastMessage = lastMsg.Content
			}
			lastMsgTime = lastMsg.CreatedAt.Format("2006-01-02 15:04:05")
		}
		result = append(result, respond.OfflineDigestItem{
			SessionId:       group.Uuid,
			ContactId:       group.Uuid,
			ContactName:     group.Name,
			ContactAvatar:   group.Avatar,
			ContactType:     "G",
			LastMessage:     lastMessage,
			LastMessageTime: lastMsgTime,
			UnreadCount:     m.countUnread(userId, "", group.Uuid, "group"),
		})
	}

	// 3. 按最后消息时间倒序排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].LastMessageTime > result[j].LastMessageTime
	})

	return result, 0
}

// countUnread 统计某个会话的未读数（基于 Redis 最后查看时间，无记录则全部算未读）。
// 单聊(session)：按双方 ID 精确统计“对方(peerId)发给我”的消息，不依赖 session_id，
//
//	避免历史会话双方 session_id 不一致而漏算。
//
// 群聊(group)：统计“别人发到该群”的消息，用 send_id <> userId 排除自己发的。
func (m *messageService) countUnread(userId, peerId, sessionId, sessionType string) int64 {
	key := "last_read_time_" + userId + "_" + sessionId
	lastReadTime, err := myredis.GetKeyNilIsErr(key)
	if err != nil {
		lastReadTime = "2000-01-01 00:00:00"
	}
	var count int64
	if sessionType == "session" {
		dao.GormDB.Model(&model.Message{}).
			Where("send_id = ? AND receive_id = ? AND created_at > ?", peerId, userId, lastReadTime).
			Count(&count)
	} else {
		dao.GormDB.Model(&model.Message{}).
			Where("receive_id = ? AND send_id <> ? AND created_at > ?", sessionId, userId, lastReadTime).
			Count(&count)
	}
	return count
}

// MarkSessionRead 标记会话已读（更新 Redis 最后查看时间，TTL 7天）
func (m *messageService) MarkSessionRead(userId, sessionId string) (string, int) {
	key := "last_read_time_" + userId + "_" + sessionId
	if err := myredis.SetKeyEx(key, time.Now().Format("2006-01-02 15:04:05"), time.Hour*24*7); err != nil {
		zlog.Error("标记会话已读失败: " + err.Error())
		return constants.SYSTEM_ERROR, -1
	}
	return "标记成功", 0
}
