package chat

import (
	"fmt"
	"kama_chat_server/pkg/zlog"
	"sync"
)

// 通话状态常量
const (
	CallIdle    = "idle"     // 空闲
	CallCalling = "calling"  // 等待对方接听
	CallInCall  = "in_call"  // 通话中
)

// CallSession 一次通话记录
type CallSession struct {
	Caller string // 主叫用户 ID
	Callee string // 被叫用户 ID
	Status string // calling / in_call
}

// CallStateManager 通话状态管理器
type CallStateManager struct {
	callMap map[string]*CallSession // userId → 当前通话
	mutex   *sync.Mutex
}

// CallState 全局通话状态管理器实例
var CallState = &CallStateManager{
	callMap: make(map[string]*CallSession),
	mutex:   &sync.Mutex{},
}

// StartCall 发起通话，如果任一方忙线返回错误
func (c *CallStateManager) StartCall(caller, callee string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// 检查被叫是否忙线
	if session, exists := c.callMap[callee]; exists {
		if session.Status == CallCalling || session.Status == CallInCall {
			zlog.Info(fmt.Sprintf("忙线拒绝：被叫 %s 正在通话中，状态=%s", callee, session.Status))
			return fmt.Errorf("busy")
		}
	}

	// 检查主叫是否忙线
	if session, exists := c.callMap[caller]; exists {
		if session.Status == CallCalling || session.Status == CallInCall {
			zlog.Info(fmt.Sprintf("忙线拒绝：主叫 %s 正在通话中，状态=%s", caller, session.Status))
			return fmt.Errorf("busy")
		}
	}

	// 双方都空闲，记录通话状态
	c.callMap[caller] = &CallSession{
		Caller: caller,
		Callee: callee,
		Status: CallCalling,
	}
	c.callMap[callee] = &CallSession{
		Caller: caller,
		Callee: callee,
		Status: CallCalling,
	}

	zlog.Info(fmt.Sprintf("通话发起：%s → %s，状态=calling", caller, callee))
	return nil
}

// AcceptCall 接听通话，状态从 calling 变为 in_call
func (c *CallStateManager) AcceptCall(caller, callee string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if session, exists := c.callMap[caller]; exists {
		session.Status = CallInCall
	}
	if session, exists := c.callMap[callee]; exists {
		session.Status = CallInCall
	}

	zlog.Info(fmt.Sprintf("通话接听：%s ↔ %s，状态=in_call", caller, callee))
}

// EndCall 结束通话，清除状态
func (c *CallStateManager) EndCall(userId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if session, exists := c.callMap[userId]; exists {
		// 同时清除对方的状态
		otherId := session.Callee
		if userId == session.Callee {
			otherId = session.Caller
		}
		delete(c.callMap, userId)
		delete(c.callMap, otherId)
		zlog.Info(fmt.Sprintf("通话结束：%s ↔ %s，状态已清除", userId, otherId))
	}
}

// RejectCall 拒绝通话，清除状态
func (c *CallStateManager) RejectCall(caller, callee string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.callMap, caller)
	delete(c.callMap, callee)

	zlog.Info(fmt.Sprintf("通话拒绝：%s ↔ %s，状态已清除", caller, callee))
}

// IsBusy 检查用户是否忙线
func (c *CallStateManager) IsBusy(userId string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if session, exists := c.callMap[userId]; exists {
		if session.Status == CallCalling || session.Status == CallInCall {
			return true
		}
	}
	return false
}
