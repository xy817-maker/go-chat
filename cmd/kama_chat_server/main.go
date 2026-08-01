package main

import (
	"fmt"
	"kama_chat_server/internal/config"
	"kama_chat_server/internal/https_server"
	"kama_chat_server/internal/service/chat"
	"kama_chat_server/internal/service/mq"
	myredis "kama_chat_server/internal/service/redis"
	"kama_chat_server/pkg/zlog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	conf := config.GetConfig()
	host := conf.MainConfig.Host
	port := conf.MainConfig.Port
	mqConfig := conf.MessageQueueConfig
	if mqConfig.MessageMode == "rocketmq" {
		// 先注入分发函数，再初始化 MQ：consumer 回调中将调用它回到 chat 包，避免循环依赖
		mq.Dispatch = chat.MQChatServer.DispatchMessage
		mq.MQService.MQInit()
	}

	if mqConfig.MessageMode == "channel" {
		go chat.ChatServer.Start()
	} else {
		go chat.MQChatServer.Start()
	}

	go func() {
		// 本地开发使用 HTTP
		if err := https_server.GE.Run(fmt.Sprintf("%s:%d", host, port)); err != nil {
			zlog.Fatal("server running fault")
			return
		}
	}()

	// 设置信号监听
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// 等待信号
	<-quit

	if mqConfig.MessageMode == "rocketmq" {
		mq.MQService.MQClose()
	}

	chat.ChatServer.Close()

	zlog.Info("关闭服务器...")

	// 删除所有Redis键
	if err := myredis.DeleteAllRedisKeys(); err != nil {
		zlog.Error(err.Error())
	} else {
		zlog.Info("所有Redis键已删除")
	}

	zlog.Info("服务器已关闭")

}
