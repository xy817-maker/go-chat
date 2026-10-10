package respond

// OfflineDigestItem 会话摘要项（上线后拉取，用于会话列表展示最后消息与未读数）
type OfflineDigestItem struct {
	SessionId      string `json:"session_id"`
	ContactId      string `json:"contact_id"`
	ContactName    string `json:"contact_name"`
	ContactAvatar  string `json:"contact_avatar"`
	ContactType    string `json:"contact_type"` // U=单聊, G=群聊
	LastMessage    string `json:"last_message"`
	LastMessageTime string `json:"last_message_time"`
	UnreadCount    int64  `json:"unread_count"`
}
