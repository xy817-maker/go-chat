package mq

import (
	"context"
	"github.com/apache/rocketmq-client-go/v2"
	"github.com/apache/rocketmq-client-go/v2/consumer"
	"github.com/apache/rocketmq-client-go/v2/primitive"
	"github.com/apache/rocketmq-client-go/v2/producer"
	myconfig "kama_chat_server/internal/config"
	"kama_chat_server/pkg/zlog"
)

var ctx = context.Background()

type mqService struct {
	producer rocketmq.Producer
	consumer rocketmq.PushConsumer
}

// Dispatch 由 chat 包注入的消息分发函数。
// 通过函数变量注入而非直接 import chat，避免 mq <-> chat 循环依赖：
// chat 包 import mq 用于生产消息，mq 包在消费回调中回调 Dispatch 回到 chat。
var Dispatch func([]byte)

var MQService = new(mqService)

// MQInit 初始化 RocketMQ producer 与 push consumer
func (m *mqService) MQInit() {
	mqConfig := myconfig.GetConfig().MessageQueueConfig
	nameServer := []string{mqConfig.HostPort}

	// producer
	p, err := rocketmq.NewProducer(
		producer.WithNameServer(nameServer),
		producer.WithGroupName(mqConfig.GroupName+"_producer"),
		producer.WithRetry(2),
	)
	if err != nil {
		zlog.Fatal(err.Error())
	}
	if err := p.Start(); err != nil {
		zlog.Fatal(err.Error())
	}
	m.producer = p

	// push consumer：订阅 chat topic，回调中调用注入的分发函数
	c, err := rocketmq.NewPushConsumer(
		consumer.WithNameServer(nameServer),
		consumer.WithGroupName(mqConfig.GroupName+"_consumer"),
		consumer.WithConsumeFromWhere(consumer.ConsumeFromLastOffset),
	)
	if err != nil {
		zlog.Fatal(err.Error())
	}
	if err := c.Subscribe(mqConfig.ChatTopic, consumer.MessageSelector{}, func(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
		for _, msg := range msgs {
			if Dispatch != nil {
				Dispatch(msg.Body)
			}
		}
		return consumer.ConsumeSuccess, nil
	}); err != nil {
		zlog.Fatal(err.Error())
	}
	if err := c.Start(); err != nil {
		zlog.Fatal(err.Error())
	}
	m.consumer = c

	zlog.Info("RocketMQ producer & consumer 已启动")
}

// SendMessage 生产一条聊天消息到 chat topic
func (m *mqService) SendMessage(data []byte) {
	mqConfig := myconfig.GetConfig().MessageQueueConfig
	_, err := m.producer.SendSync(ctx, &primitive.Message{
		Topic: mqConfig.ChatTopic,
		Body:  data,
	})
	if err != nil {
		zlog.Error(err.Error())
		return
	}
	zlog.Info("已发送消息：" + string(data))
}

// MQClose 关闭 producer 与 consumer
func (m *mqService) MQClose() {
	if m.producer != nil {
		if err := m.producer.Shutdown(); err != nil {
			zlog.Error(err.Error())
		}
	}
	if m.consumer != nil {
		if err := m.consumer.Shutdown(); err != nil {
			zlog.Error(err.Error())
		}
	}
}
