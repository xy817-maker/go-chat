package request

// GetOfflineDigestRequest 上线拉取会话摘要请求
type GetOfflineDigestRequest struct {
	UserId string `json:"user_id"`
}

// MarkSessionReadRequest 标记会话已读请求
type MarkSessionReadRequest struct {
	UserId    string `json:"user_id"`
	SessionId string `json:"session_id"`
}
