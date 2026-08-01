package chat

import (
	"fmt"
	"github.com/gorilla/websocket"
	"kama_chat_server/pkg/constants"
	"kama_chat_server/pkg/zlog"
	"sync"
)

type Server struct {
	Clients   map[string]*Client
	mutex     *sync.Mutex
	Transmit  chan []byte  // 转发通道
	Login     chan *Client // 登录通道
	Logout    chan *Client // 退出登录通道
	closeOnce sync.Once
}

var ChatServer *Server

func init() {
	if ChatServer == nil {
		ChatServer = &Server{
			Clients:  make(map[string]*Client),
			mutex:    &sync.Mutex{},
			Transmit: make(chan []byte, constants.CHANNEL_SIZE),
			Login:    make(chan *Client, constants.CHANNEL_SIZE),
			Logout:   make(chan *Client, constants.CHANNEL_SIZE),
		}
	}
}

// Start 启动函数，Server端用主进程起，Client端可以用协程起
func (s *Server) Start() {
	for {
		select {
		case client := <-s.Login:
			{
				if client == nil {
					return
				}
				s.mutex.Lock()
				s.Clients[client.Uuid] = client
				s.mutex.Unlock()
				zlog.Debug(fmt.Sprintf("欢迎来到kama聊天服务器，亲爱的用户%s\n", client.Uuid))
				err := client.Conn.WriteMessage(websocket.TextMessage, []byte("欢迎来到kama聊天服务器"))
				if err != nil {
					zlog.Error(err.Error())
				}
			}

		case client := <-s.Logout:
			{
				if client == nil {
					return
				}
				s.mutex.Lock()
				delete(s.Clients, client.Uuid)
				s.mutex.Unlock()
				zlog.Info(fmt.Sprintf("用户%s退出登录\n", client.Uuid))
				if err := client.Conn.WriteMessage(websocket.TextMessage, []byte("已退出登录")); err != nil {
					zlog.Error(err.Error())
				}
			}

		case data := <-s.Transmit:
			{
				// channel 模式：从转发通道取出消息，交由共享分发逻辑处理
				dispatchChatMessage(data, s.Clients, s.mutex)
			}
		}
	}
}

func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.Login)
		close(s.Logout)
		close(s.Transmit)
	})
}

func (s *Server) SendClientToLogin(client *Client) {
	s.mutex.Lock()
	s.Login <- client
	s.mutex.Unlock()
}

func (s *Server) SendClientToLogout(client *Client) {
	s.mutex.Lock()
	s.Logout <- client
	s.mutex.Unlock()
}

func (s *Server) SendMessageToTransmit(message []byte) {
	s.mutex.Lock()
	s.Transmit <- message
	s.mutex.Unlock()
}

func (s *Server) RemoveClient(uuid string) {
	s.mutex.Lock()
	delete(s.Clients, uuid)
	s.mutex.Unlock()
}
