package chat

import (
	"encoding/json"
	"fmt"
	"kama_chat_server/internal/dao"
	"kama_chat_server/internal/dto/request"
	"kama_chat_server/internal/dto/respond"
	"kama_chat_server/internal/model"
	myredis "kama_chat_server/internal/service/redis"
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

// trySend 非阻塞地向客户端推送消息：通道满则跳过本次实时推送（消息已落库，客户端可拉取补回），
// 避免慢连接阻塞分发协程、导致故障扩散。对 nil 客户端（离线）安全。
func trySend(c *Client, msg *MessageBack) {
	if c == nil {
		return
	}
	select {
	case c.SendBack <- msg:
	default:
		zlog.Info(fmt.Sprintf("用户 %s 推送通道已满，跳过本次实时推送，待其拉取补回", c.Uuid))
	}
}

// resolveMsgUuid 优先使用客户端生成的消息ID（用于幂等），为空则回退到服务端生成
func resolveMsgUuid(clientMsgId string) string {
	if clientMsgId != "" {
		return clientMsgId
	}
	return fmt.Sprintf("M%s", random.GetNowAndLenRandomString(11))
}

// messageExists 检查消息是否已落库（幂等检查，查 MySQL）
func messageExists(uuid string) bool {
	var count int64
	dao.GormDB.Model(&model.Message{}).Where("uuid = ?", uuid).Count(&count)
	return count > 0
}

// saveMessageIfAbsent 幂等落库。
// 先查 MySQL 判断消息是否已存在（客户端超时重发场景）：
//   - duplicate=true：消息已存在，直接回 ACK，不重复落库/推送
//   - saved=true：新消息落库成功
//   - 两者均 false：落库失败，不回 ACK，客户端超时重发
// 注：channel 模式事件循环单线程串行处理，不存在并发竞争，无需唯一索引冲突兜底。
func saveMessageIfAbsent(message *model.Message) (saved bool, duplicate bool) {
	if messageExists(message.Uuid) {
		return false, true
	}
	if err := dao.GormDB.Create(message).Error; err != nil {
		zlog.Error("消息落库失败: " + err.Error())
		return false, false
	}
	return true, false
}

// updateSessionLastMessage 更新单聊会话的最后一条消息预览（用于会话列表展示），群聊无 session 表不调用
func updateSessionLastMessage(message *model.Message) {
	if message.SessionId == "" || message.ReceiveId[0] != 'U' {
		return
	}
	lastMsg := message.Content
	if message.Type == message_type_enum.File {
		lastMsg = "[文件] " + message.FileName
	}
	if res := dao.GormDB.Model(&model.Session{}).
		Where("uuid = ?", message.SessionId).
		Updates(map[string]interface{}{
			"last_message":    lastMsg,
			"last_message_at": time.Now(),
		}); res.Error != nil {
		zlog.Error("更新会话最后消息失败: " + res.Error.Error())
	}
}

// sendAck 非阻塞地向发送方回应用层 ACK。ACK 丢失没关系，客户端会超时重发。
func sendAck(clients map[string]*Client, mu *sync.Mutex, sendId, clientMsgId string) {
	if clientMsgId == "" {
		return // 客户端没传 ID，不回 ACK（兼容旧逻辑）
	}
	ackBytes, err := json.Marshal(map[string]interface{}{
		"type":          message_type_enum.Ack,
		"client_msg_id": clientMsgId,
	})
	if err != nil {
		zlog.Error(err.Error())
		return
	}
	mu.Lock()
	if client, ok := clients[sendId]; ok {
		select {
		case client.SendBack <- &MessageBack{Message: ackBytes}:
		default:
			zlog.Info(fmt.Sprintf("用户 %s ACK 通道已满，跳过 ACK，客户端将超时重发", sendId))
		}
	}
	mu.Unlock()
}

// dispatchChatMessage 处理一条聊天消息：落库 -> 在线转发 -> Redis 缓存。
// channel 与 RocketMQ 两种传输模式共享此逻辑，避免双份重复实现。
// clients/mu 由调用方（channel 模式的 Server 或 RocketMQ 模式的 MQServer）传入。
// 返回值：true=处理成功或无需重试；false=落库失败，RocketMQ 模式下触发 Broker 重试
func dispatchChatMessage(data []byte, clients map[string]*Client, mu *sync.Mutex) bool {
	if data == nil {
		return true // 无效消息，不重试
	}
	var chatMessageReq request.ChatMessageRequest
	if err := json.Unmarshal(data, &chatMessageReq); err != nil {
		zlog.Error(err.Error())
	}
	// log.Println("原消息为：", data, "反序列化后为：", chatMessageReq)
	if chatMessageReq.Type == message_type_enum.Text {
		// 存message
		message := model.Message{
			Uuid:       resolveMsgUuid(chatMessageReq.ClientMsgId),
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
			CreatedAt:  time.Now(),
			AVdata:     "",
		}
		// 对SendAvatar去除前面/static之前的所有内容，防止ip前缀引入
		message.SendAvatar = normalizePath(message.SendAvatar)
		saved, duplicate := saveMessageIfAbsent(&message)
		if duplicate {
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)
			return true
		}
		if !saved {
			return false // 落库失败：RocketMQ 触发 Broker 重试，channel 靠客户端超时重发
		}
		if message.ReceiveId[0] == 'U' { // 发送给User
			// 更新单聊会话最后一条消息预览（用于会话列表展示）
			updateSessionLastMessage(&message)
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
			messageBack := &MessageBack{Message: jsonMessage}
			// 只推送给接收方：发送方靠前端乐观回显，不重复推送。非阻塞，离线/通道满则跳过，消息已落库可拉取补回
			mu.Lock()
			trySend(clients[message.ReceiveId], messageBack)
			mu.Unlock()

			// redis：写库后删除该会话缓存。单聊会话双方共读 message_list_<sessionId> 一份，删一次即双方同时失效
			if err := myredis.DelKeyIfExists("message_list_" + message.SessionId); err != nil {
				zlog.Error(err.Error())
			}
			// 文本消息落库成功，回发送方应用层 ACK
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)

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
				if member == message.SendId {
					continue // 发送方靠前端乐观回显，不重复推送
				}
				// 在线成员非阻塞推送；离线或通道满则跳过，消息已落库、上线拉取补回
				trySend(clients[member], messageBack)
			}
			mu.Unlock()

			// redis：写库后删除缓存，下次读时回源重建（旁路缓存策略）
			if err := myredis.DelKeyIfExists("group_messagelist_" + message.ReceiveId); err != nil {
				zlog.Error(err.Error())
			}
			// 文本消息落库成功，回应用层 ACK
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)
		}
	} else if chatMessageReq.Type == message_type_enum.File {
		// 存message
		message := model.Message{
			Uuid:       resolveMsgUuid(chatMessageReq.ClientMsgId),
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
			CreatedAt:  time.Now(),
			AVdata:     "",
		}
		// 对SendAvatar去除前面/static之前的所有内容，防止ip前缀引入
		message.SendAvatar = normalizePath(message.SendAvatar)
		saved, duplicate := saveMessageIfAbsent(&message)
		if duplicate {
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)
			return true
		}
		if !saved {
			return false // 落库失败：RocketMQ 触发 Broker 重试，channel 靠客户端超时重发
		}
		if message.ReceiveId[0] == 'U' { // 发送给User
			// 更新单聊会话最后一条消息预览（用于会话列表展示）
			updateSessionLastMessage(&message)
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
			messageBack := &MessageBack{Message: jsonMessage}
			// 只推送给接收方：发送方靠前端乐观回显，不重复推送。非阻塞，离线/通道满则跳过，消息已落库可拉取补回
			mu.Lock()
			trySend(clients[message.ReceiveId], messageBack)
			mu.Unlock()

			// redis：写库后删除该会话缓存。单聊会话双方共读 message_list_<sessionId> 一份，删一次即双方同时失效
			if err := myredis.DelKeyIfExists("message_list_" + message.SessionId); err != nil {
				zlog.Error(err.Error())
			}
			// 文件消息落库成功，回发送方应用层 ACK
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)
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
				if member == message.SendId {
					continue // 发送方靠前端乐观回显，不重复推送
				}
				// 在线成员非阻塞推送；离线或通道满则跳过，消息已落库、上线拉取补回
				trySend(clients[member], messageBack)
			}
			mu.Unlock()

			// redis：写库后删除缓存，下次读时回源重建（旁路缓存策略）
			if err := myredis.DelKeyIfExists("group_messagelist_" + message.ReceiveId); err != nil {
				zlog.Error(err.Error())
			}
			// 文件消息落库成功，回应用层 ACK
			sendAck(clients, mu, message.SendId, chatMessageReq.ClientMsgId)
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
						sendClient.SendBack <- &MessageBack{Message: rejectMsg}
					}
					mu.Unlock()
					return true // 忙线不转发，无需重试
				}
				// 双方空闲，记录通话状态
				if err := CallState.StartCall(message.SendId, message.ReceiveId); err != nil {
					return true // 忙线，不转发
				}
			} else if avData.Type == "receive_call" {
				// 对方接听，状态更新为 in_call
				CallState.AcceptCall(message.SendId, message.ReceiveId)
			} else if avData.Type == "reject_call" || avData.Type == "end_call" {
				// 拒绝或挂断，清除通话状态
				CallState.RejectCall(message.SendId, message.ReceiveId)
			}
		}

		// 正常挂断（PEER_LEAVE）或 end_call：清理服务端通话状态，并向发起方回 ACK
		if avData.MessageId == "PEER_LEAVE" ||
			(avData.MessageId == "PROXY" && avData.Type == "end_call") {
			CallState.EndCall(message.SendId)
			ackRsp := respond.AVMessageRespond{
				SendId:    message.ReceiveId,
				ReceiveId: message.SendId,
				Type:      message.Type,
				CreatedAt: message.CreatedAt.Format("2006-01-02 15:04:05"),
				AVdata:    `{"messageId":"CALL_ACK","type":"end_ack"}`,
			}
			ackMsg, _ := json.Marshal(ackRsp)
			mu.Lock()
			if sendClient, ok := clients[message.SendId]; ok {
				sendClient.SendBack <- &MessageBack{Message: ackMsg}
			}
			mu.Unlock()
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
			}
			mu.Lock()
			if receiveClient, ok := clients[message.ReceiveId]; ok {
				//messageBack.Message = jsonMessage
				receiveClient.SendBack <- messageBack // 向client.Send发送
			}
			// 通话这不能回显，发回去的话就会出现两个start_call。
			//sendClient := clients[message.SendId]
			//sendClient.SendBack <- messageBack
			mu.Unlock()
		}
	}
	return true
}
