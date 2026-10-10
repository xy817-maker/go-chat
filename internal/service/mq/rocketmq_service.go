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
// 返回 true=处理成功/无需重试；false=落库失败需要重试。
// 通过函数变量注入而非直接 import chat，避免 mq <-> chat 循环依赖：
// chat 包 import mq 用于生产消息，mq 包在消费回调中回调 Dispatch 回到 chat。
var Dispatch func([]byte) bool

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
	// WithConsumerOrder(true) 开启顺序消费：同一个 queue 由单线程顺序处理，保证同一会话消息有序
	c, err := rocketmq.NewPushConsumer(
		consumer.WithNameServer(nameServer),
		consumer.WithGroupName(mqConfig.GroupName+"_consumer"),
		consumer.WithConsumeFromWhere(consumer.ConsumeFromLastOffset),
		consumer.WithConsumerOrder(true),
	)
	if err != nil {
		zlog.Fatal(err.Error())
	}
	if err := c.Subscribe(mqConfig.ChatTopic, consumer.MessageSelector{}, func(ctx context.Context, msgs ...*primitive.MessageExt) (consumer.ConsumeResult, error) {
		for _, msg := range msgs {
			if Dispatch != nil {
				if !Dispatch(msg.Body) {
					// 落库失败：顺序消费模式下挂起当前 queue，稍后重试同一条消息，保证不丢
					return consumer.SuspendCurrentQueueAMoment, nil
				}
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
// shardingKey 用于顺序消息：相同 shardingKey 的消息进入同一个 queue，保证存储有序；
// 传空字符串则不设置，消息轮询发到所有 queue（音视频信令等不需要顺序的场景）
func (m *mqService) SendMessage(data []byte, shardingKey string) {
	mqConfig := myconfig.GetConfig().MessageQueueConfig
	msg := &primitive.Message{
		Topic: mqConfig.ChatTopic,
		Body:  data,
	}
	if shardingKey != "" {
		msg.WithShardingKey(shardingKey)
	}
	_, err := m.producer.SendSync(ctx, msg)
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
