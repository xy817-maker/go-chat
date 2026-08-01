package chat

import (
	"fmt"
	"github.com/gorilla/websocket"
	"kama_chat_server/pkg/zlog"
	"sync"
)

// MQServer 是 RocketMQ 模式下的聊天服务器。
// 与 channel 模式的 Server 区别在于：消息不再走内存 Transmit 通道，
// 而是由 mq 包的 push consumer 消费后回调 DispatchMessage 触发分发。
type MQServer struct {
	Clients map[string]*Client
	mutex   *sync.Mutex
	Login   chan *Client // 登录通道
	Logout  chan *Client // 退出登录通道
}

var MQChatServer *MQServer

func init() {
	if MQChatServer == nil {
		MQChatServer = &MQServer{
			Clients: make(map[string]*Client),
			mutex:   &sync.Mutex{},
			Login:   make(chan *Client),
			Logout:  make(chan *Client),
		}
	}
}

// Start 启动 RocketMQ 模式的聊天服务器。
// 消息消费由 mq 包的 push consumer 回调驱动（回调中调用 DispatchMessage），
// 这里只处理登录/登出的在线连接管理。
func (k *MQServer) Start() {
	for {
		select {
		case client := <-k.Login:
			{
				k.mutex.Lock()
				k.Clients[client.Uuid] = client
				k.mutex.Unlock()
				zlog.Debug(fmt.Sprintf("欢迎来到kama聊天服务器，亲爱的用户%s\n", client.Uuid))
				err := client.Conn.WriteMessage(websocket.TextMessage, []byte("欢迎来到kama聊天服务器"))
				if err != nil {
					zlog.Error(err.Error())
				}
			}

		case client := <-k.Logout:
			{
				k.mutex.Lock()
				delete(k.Clients, client.Uuid)
				k.mutex.Unlock()
				zlog.Info(fmt.Sprintf("用户%s退出登录\n", client.Uuid))
				if err := client.Conn.WriteMessage(websocket.TextMessage, []byte("已退出登录")); err != nil {
					zlog.Error(err.Error())
				}
			}
		}
	}
}

// DispatchMessage 处理一条从 RocketMQ 消费到的消息，供 mq 包回调注入。
func (k *MQServer) DispatchMessage(data []byte) {
	dispatchChatMessage(data, k.Clients, k.mutex)
}

func (k *MQServer) Close() {
	close(k.Login)
	close(k.Logout)
}

func (k *MQServer) SendClientToLogin(client *Client) {
	k.mutex.Lock()
	k.Login <- client
	k.mutex.Unlock()
}

func (k *MQServer) SendClientToLogout(client *Client) {
	k.mutex.Lock()
	k.Logout <- client
	k.mutex.Unlock()
}

func (k *MQServer) RemoveClient(uuid string) {
	k.mutex.Lock()
	delete(k.Clients, uuid)
	k.mutex.Unlock()
}
