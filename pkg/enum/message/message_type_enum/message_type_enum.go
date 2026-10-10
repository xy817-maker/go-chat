package message_type_enum

const (
	Text = iota
	// 语音
	Voice
	// 文件
	File
	// 通话
	AudioOrVideo
	// 应用层ACK确认（服务端落库成功后回给发送方）
	Ack
)
