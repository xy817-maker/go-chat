package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-redis/redis/v8"
	"kama_chat_server/internal/dao"
	"kama_chat_server/internal/dto/request"
	"kama_chat_server/internal/dto/respond"
	"kama_chat_server/internal/model"
	myredis "kama_chat_server/internal/service/redis"
	"kama_chat_server/pkg/constants"
	"kama_chat_server/pkg/enum/message/message_status_enum"
	"kama_chat_server/pkg/enum/message/message_type_enum"
	"kama_chat_server/pkg/util/random"
	"kama_chat_server/pkg/zlog"
	"log"
	"strings"
	"sync"
	"time"
)

// 将 https://127.0.0.1:8000/static/xxx 转为 /static/xxx
func normalizePath(path string) string {
	// 查找 "/static/" 的位置
	if path == "https://cube.elemecdn.com/0/88/03b0d39583f48206768a7534e55bcpng.png" {
		return path
	}
	staticIndex := strings.Index(path, "/static/")
	if staticIndex < 0 {
		log.Println(path)
		zlog.Error("路径不合法")
	}
	// 返回从 "/static/" 开始的部分
	return path[staticIndex:]
}

// dispatchChatMessage 处理一条聊天消息：落库 -> 在线转发 -> Redis 缓存。
// channel 与 RocketMQ 两种传输模式共享此逻辑，避免双份重复实现。
// clients/mu 由调用方（channel 模式的 Server 或 RocketMQ 模式的 MQServer）传入。
func dispatchChatMessage(data []byte, clients map[string]*Client, mu *sync.Mutex) {
	if data == nil {
		return
	}
	var chatMessageReq request.ChatMessageRequest
	if err := json.Unmarshal(data, &chatMessageReq); err != nil {
		zlog.Error(err.Error())
	}
	// log.Println("原消息为：", data, "反序列化后为：", chatMessageReq)
	if chatMessageReq.Type == message_type_enum.Text {
		// 存message
		message := model.Message{
			Uuid:       fmt.Sprintf("M%s", random.GetNowAndLenRandomString(11)),
			SessionId:  chatMessageReq.SessionId,
			Type:       chatMessageReq.Type,
			Content:    chatMessageReq.Content,
			Url:        "",
			SendId:     chatMessageReq.SendId,
			SendName:   chatMessageReq.SendName,
			SendAvatar: chatMessageReq.SendAvatar,
			ReceiveId:  chatMessageReq.ReceiveId,
			FileSize:   "0B",
			FileType:   "",
			FileName:   "",
			Status:     message_status_enum.Unsent,
			CreatedAt:  time.Now(),
			AVdata:     "",
		}
		// 对SendAvatar去除前面/static之前的所有内容，防止ip前缀引入
		message.SendAvatar = normalizePath(message.SendAvatar)
		if res := dao.GormDB.Create(&message); res.Error != nil {
			zlog.Error(res.Error.Error())
		}
		if message.ReceiveId[0] == 'U' { // 发送给User
			// 如果能找到ReceiveId，说明在线，可以发送，否则存表后跳过
			// 因为在线的时候是通过websocket更新消息记录的，离线后通过存表，登录时只调用一次数据库操作
			// 切换chat对象后，前端的messageList也会改变，获取messageList从第二次就是从redis中获取
			messageRsp := respond.GetMessageListRespond{
				SendId:     message.SendId,
				SendName:   message.SendName,
				SendAvatar: chatMessageReq.SendAvatar,
				ReceiveId:  message.ReceiveId,
				Type:       message.Type,
				Content:    message.Content,
				Url:        message.Url,
				FileSize:   message.FileSize,
				FileName:   message.FileName,
				FileType:   message.FileType,
				CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
			}
			jsonMessage, err := json.Marshal(messageRsp)
			if err != nil {
				zlog.Error(err.Error())
			}
			log.Println("返回的消息为：", messageRsp, "序列化后为：", jsonMessage)
			var messageBack = &MessageBack{
				Message: jsonMessage,
				Uuid:    message.Uuid,
			}
			mu.Lock()
			if receiveClient, ok := clients[message.ReceiveId]; ok {
				//messageBack.Message = jsonMessage
				//messageBack.Uuid = message.Uuid
				receiveClient.SendBack <- messageBack // 向client.Send发送
			}
			// 因为send_id肯定在线，所以这里在后端进行在线回显message，其实优化的话前端可以直接回显
			// 问题在于前后端的req和rsp结构不同，前端存储message的messageList不能存req，只能存rsp
			// 所以这里后端进行回显，前端不回显
			sendClient := clients[message.SendId]
			sendClient.SendBack <- messageBack
			mu.Unlock()

			// redis
			var rspString string
			rspString, err = myredis.GetKeyNilIsErr("message_list_" + message.SendId + "_" + message.ReceiveId)
			if err == nil {
				var rsp []respond.GetMessageListRespond
				if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
					zlog.Error(err.Error())
				}
				rsp = append(rsp, messageRsp)
				rspByte, err := json.Marshal(rsp)
				if err != nil {
					zlog.Error(err.Error())
				}
				if err := myredis.SetKeyEx("message_list_"+message.SendId+"_"+message.ReceiveId, string(rspByte), time.Minute*constants.REDIS_TIMEOUT); err != nil {
					zlog.Error(err.Error())
				}
			} else {
				if !errors.Is(err, redis.Nil) {
					zlog.Error(err.Error())
				}
			}

		} else if message.ReceiveId[0] == 'G' { // 发送给Group
			messageRsp := respond.GetGroupMessageListRespond{
				SendId:     message.SendId,
				SendName:   message.SendName,
				SendAvatar: chatMessageReq.SendAvatar,
				ReceiveId:  message.ReceiveId,
				Type:       message.Type,
				Content:    message.Content,
				Url:        message.Url,
				FileSize:   message.FileSize,
				FileName:   message.FileName,
				FileType:   message.FileType,
				CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
			}
			jsonMessage, err := json.Marshal(messageRsp)
			if err != nil {
				zlog.Error(err.Error())
			}
			log.Println("返回的消息为：", messageRsp, "序列化后为：", jsonMessage)
			var messageBack = &MessageBack{
				Message: jsonMessage,
				Uuid:    message.Uuid,
			}
			var group model.GroupInfo
			if res := dao.GormDB.Where("uuid = ?", message.ReceiveId).First(&group); res.Error != nil {
				zlog.Error(res.Error.Error())
			}
			var members []string
			if err := json.Unmarshal(group.Members, &members); err != nil {
				zlog.Error(err.Error())
			}
			mu.Lock()
			for _, member := range members {
				if member != message.SendId {
					if receiveClient, ok := clients[member]; ok {
						receiveClient.SendBack <- messageBack
					}
				} else {
					sendClient := clients[message.SendId]
					sendClient.SendBack <- messageBack
				}
			}
			mu.Unlock()

			// redis
			var rspString string
			rspString, err = myredis.GetKeyNilIsErr("group_messagelist_" + message.ReceiveId)
			if err == nil {
				var rsp []respond.GetGroupMessageListRespond
				if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
					zlog.Error(err.Error())
				}
				rsp = append(rsp, messageRsp)
				rspByte, err := json.Marshal(rsp)
				if err != nil {
					zlog.Error(err.Error())
				}
				if err := myredis.SetKeyEx("group_messagelist_"+message.ReceiveId, string(rspByte), time.Minute*constants.REDIS_TIMEOUT); err != nil {
					zlog.Error(err.Error())
				}
			} else {
				if !errors.Is(err, redis.Nil) {
					zlog.Error(err.Error())
				}
			}
		}
	} else if chatMessageReq.Type == message_type_enum.File {
		// 存message
		message := model.Message{
			Uuid:       fmt.Sprintf("M%s", random.GetNowAndLenRandomString(11)),
			SessionId:  chatMessageReq.SessionId,
			Type:       chatMessageReq.Type,
			Content:    "",
			Url:        chatMessageReq.Url,
			SendId:     chatMessageReq.SendId,
			SendName:   chatMessageReq.SendName,
			SendAvatar: chatMessageReq.SendAvatar,
			ReceiveId:  chatMessageReq.ReceiveId,
			FileSize:   chatMessageReq.FileSize,
			FileType:   chatMessageReq.FileType,
			FileName:   chatMessageReq.FileName,
			Status:     message_status_enum.Unsent,
			CreatedAt:  time.Now(),
			AVdata:     "",
		}
		// 对SendAvatar去除前面/static之前的所有内容，防止ip前缀引入
		message.SendAvatar = normalizePath(message.SendAvatar)
		if res := dao.GormDB.Create(&message); res.Error != nil {
			zlog.Error(res.Error.Error())
		}
		if message.ReceiveId[0] == 'U' { // 发送给User
			// 如果能找到ReceiveId，说明在线，可以发送，否则存表后跳过
			// 因为在线的时候是通过websocket更新消息记录的，离线后通过存表，登录时只调用一次数据库操作
			// 切换chat对象后，前端的messageList也会改变，获取messageList从第二次就是从redis中获取
			messageRsp := respond.GetMessageListRespond{
				SendId:     message.SendId,
				SendName:   message.SendName,
				SendAvatar: chatMessageReq.SendAvatar,
				ReceiveId:  message.ReceiveId,
				Type:       message.Type,
				Content:    message.Content,
				Url:        message.Url,
				FileSize:   message.FileSize,
				FileName:   message.FileName,
				FileType:   message.FileType,
				CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
			}
			jsonMessage, err := json.Marshal(messageRsp)
			if err != nil {
				zlog.Error(err.Error())
			}
			log.Println("返回的消息为：", messageRsp, "序列化后为：", jsonMessage)
			var messageBack = &MessageBack{
				Message: jsonMessage,
				Uuid:    message.Uuid,
			}
			mu.Lock()
			if receiveClient, ok := clients[message.ReceiveId]; ok {
				//messageBack.Message = jsonMessage
				//messageBack.Uuid = message.Uuid
				receiveClient.SendBack <- messageBack // 向client.Send发送
			}
			// 因为send_id肯定在线，所以这里在后端进行在线回显message，其实优化的话前端可以直接回显
			// 问题在于前后端的req和rsp结构不同，前端存储message的messageList不能存req，只能存rsp
			// 所以这里后端进行回显，前端不回显
			sendClient := clients[message.SendId]
			sendClient.SendBack <- messageBack
			mu.Unlock()

			// redis
			var rspString string
			rspString, err = myredis.GetKeyNilIsErr("message_list_" + message.SendId + "_" + message.ReceiveId)
			if err == nil {
				var rsp []respond.GetMessageListRespond
				if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
					zlog.Error(err.Error())
				}
				rsp = append(rsp, messageRsp)
				rspByte, err := json.Marshal(rsp)
				if err != nil {
					zlog.Error(err.Error())
				}
				if err := myredis.SetKeyEx("message_list_"+message.SendId+"_"+message.ReceiveId, string(rspByte), time.Minute*constants.REDIS_TIMEOUT); err != nil {
					zlog.Error(err.Error())
				}
			} else {
				if !errors.Is(err, redis.Nil) {
					zlog.Error(err.Error())
				}
			}
		} else {
			messageRsp := respond.GetGroupMessageListRespond{
				SendId:     message.SendId,
				SendName:   message.SendName,
				SendAvatar: chatMessageReq.SendAvatar,
				ReceiveId:  message.ReceiveId,
				Type:       message.Type,
				Content:    message.Content,
				Url:        message.Url,
				FileSize:   message.FileSize,
				FileName:   message.FileName,
				FileType:   message.FileType,
				CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
			}
			jsonMessage, err := json.Marshal(messageRsp)
			if err != nil {
				zlog.Error(err.Error())
			}
			log.Println("返回的消息为：", messageRsp, "序列化后为：", jsonMessage)
			var messageBack = &MessageBack{
				Message: jsonMessage,
				Uuid:    message.Uuid,
			}
			var group model.GroupInfo
			if res := dao.GormDB.Where("uuid = ?", message.ReceiveId).First(&group); res.Error != nil {
				zlog.Error(res.Error.Error())
			}
			var members []string
			if err := json.Unmarshal(group.Members, &members); err != nil {
				zlog.Error(err.Error())
			}
			mu.Lock()
			for _, member := range members {
				if member != message.SendId {
					if receiveClient, ok := clients[member]; ok {
						receiveClient.SendBack <- messageBack
					}
				} else {
					sendClient := clients[message.SendId]
					sendClient.SendBack <- messageBack
				}
			}
			mu.Unlock()

			// redis
			var rspString string
			rspString, err = myredis.GetKeyNilIsErr("group_messagelist_" + message.ReceiveId)
			if err == nil {
				var rsp []respond.GetGroupMessageListRespond
				if err := json.Unmarshal([]byte(rspString), &rsp); err != nil {
					zlog.Error(err.Error())
				}
				rsp = append(rsp, messageRsp)
				rspByte, err := json.Marshal(rsp)
				if err != nil {
					zlog.Error(err.Error())
				}
				if err := myredis.SetKeyEx("group_messagelist_"+message.ReceiveId, string(rspByte), time.Minute*constants.REDIS_TIMEOUT); err != nil {
					zlog.Error(err.Error())
				}
			} else {
				if !errors.Is(err, redis.Nil) {
					zlog.Error(err.Error())
				}
			}
		}
	} else if chatMessageReq.Type == message_type_enum.AudioOrVideo {
		var avData request.AVData
		if err := json.Unmarshal([]byte(chatMessageReq.AVdata), &avData); err != nil {
			zlog.Error(err.Error())
		}
		//log.Println(avData)
		message := model.Message{
			Uuid:       fmt.Sprintf("M%s", random.GetNowAndLenRandomString(11)),
			SessionId:  chatMessageReq.SessionId,
			Type:       chatMessageReq.Type,
			Content:    "",
			Url:        "",
			SendId:     chatMessageReq.SendId,
			SendName:   chatMessageReq.SendName,
			SendAvatar: chatMessageReq.SendAvatar,
			ReceiveId:  chatMessageReq.ReceiveId,
			FileSize:   "",
			FileType:   "",
			FileName:   "",
			Status:     message_status_enum.Unsent,
			CreatedAt:  time.Now(),
			AVdata:     chatMessageReq.AVdata,
		}
		// 通话状态管理：忙线判断
		if avData.MessageId == "PROXY" {
			if avData.Type == "start_call" {
				// 检查忙线：主叫或被叫正在通话中
				if CallState.IsBusy(message.SendId) || CallState.IsBusy(message.ReceiveId) {
					zlog.Info(fmt.Sprintf("忙线拒绝：%s -> %s", message.SendId, message.ReceiveId))
					// 返回忙线拒绝给发起方
					rejectRsp := respond.AVMessageRespond{
						SendId:     message.ReceiveId,
						SendName:   "",
						SendAvatar: "",
						ReceiveId:  message.SendId,
						Type:       message.Type,
						Content:    "",
						Url:        "",
						FileSize:   "",
						FileName:   "",
						FileType:   "",
						CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
						AVdata:     `{"messageId":"PROXY","type":"reject_call","reason":"busy"}`,
					}
					rejectMsg, _ := json.Marshal(rejectRsp)
					mu.Lock()
					if sendClient, ok := clients[message.SendId]; ok {
						sendClient.SendBack <- &MessageBack{Message: rejectMsg, Uuid: message.Uuid}
					}
					mu.Unlock()
					return // 不转发，跳过后续逻辑
				}
				// 双方空闲，记录通话状态
				if err := CallState.StartCall(message.SendId, message.ReceiveId); err != nil {
					return // 忙线，不转发
				}
			} else if avData.Type == "receive_call" {
				// 对方接听，状态更新为 in_call
				CallState.AcceptCall(message.SendId, message.ReceiveId)
			} else if avData.Type == "reject_call" || avData.Type == "end_call" {
				// 拒绝或挂断，清除通话状态
				CallState.RejectCall(message.SendId, message.ReceiveId)
			}
		}

		if avData.MessageId == "PROXY" && (avData.Type == "start_call" || avData.Type == "receive_call" || avData.Type == "reject_call" || avData.Type == "end_call") {
			// 存message
			// 对SendAvatar去除前面/static之前的所有内容，防止ip前缀引入
			message.SendAvatar = normalizePath(message.SendAvatar)
			if res := dao.GormDB.Create(&message); res.Error != nil {
				zlog.Error(res.Error.Error())
			}
		}

		if chatMessageReq.ReceiveId[0] == 'U' { // 发送给User
			// 如果能找到ReceiveId，说明在线，可以发送，否则存表后跳过
			// 因为在线的时候是通过websocket更新消息记录的，离线后通过存表，登录时只调用一次数据库操作
			// 切换chat对象后，前端的messageList也会改变，获取messageList从第二次就是从redis中获取
			messageRsp := respond.AVMessageRespond{
				SendId:     message.SendId,
				SendName:   message.SendName,
				SendAvatar: message.SendAvatar,
				ReceiveId:  message.ReceiveId,
				Type:       message.Type,
				Content:    message.Content,
				Url:        message.Url,
				FileSize:   message.FileSize,
				FileName:   message.FileName,
				FileType:   message.FileType,
				CreatedAt:  message.CreatedAt.Format("2006-01-02 15:04:05"),
				AVdata:     message.AVdata,
			}
			jsonMessage, err := json.Marshal(messageRsp)
			if err != nil {
				zlog.Error(err.Error())
			}
			// log.Println("返回的消息为：", messageRsp, "序列化后为：", jsonMessage)
			log.Println("返回的消息为：", messageRsp)
			var messageBack = &MessageBack{
				Message: jsonMessage,
				Uuid:    message.Uuid,
			}
			mu.Lock()
			if receiveClient, ok := clients[message.ReceiveId]; ok {
				//messageBack.Message = jsonMessage
				//messageBack.Uuid = message.Uuid
				receiveClient.SendBack <- messageBack // 向client.Send发送
			}
			// 通话这不能回显，发回去的话就会出现两个start_call。
			//sendClient := clients[message.SendId]
			//sendClient.SendBack <- messageBack
			mu.Unlock()
		}
	}
}
