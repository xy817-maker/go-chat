package request

type GetMessageListRequest struct {
	SessionId string `json:"session_id"`
	UserOneId string `json:"user_one_id"`
	UserTwoId string `json:"user_two_id"`
}
