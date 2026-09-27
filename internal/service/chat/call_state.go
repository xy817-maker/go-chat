package chat

import (
	"fmt"
	"kama_chat_server/pkg/zlog"
	"sync"
	"time"
)

// 通话状态常量
const (
	CallIdle    = "idle"    // 空闲
	CallCalling = "calling" // 等待对方接听
	CallInCall  = "in_call" // 通话中
)

// 通话状态回收参数
const (
	ringingTTL    = 30 * time.Second // 振铃中状态最长保留时间（无人接听自动回收）
	heartbeatTTL  = 45 * time.Second // 通话中超过该时长没有心跳续期则回收
	sweepInterval = 10 * time.Second // 后台扫描间隔
)

// CallSession 一次通话记录
type CallSession struct {
	Caller    string    // 主叫用户 ID
	Callee    string    // 被叫用户 ID
	Status    string    // calling / in_call
	StartedAt time.Time // 通话发起时间
	UpdatedAt time.Time // 最近一次状态变更/心跳续期时间
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

func init() {
	go CallState.sweepLoop()
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
	now := time.Now()
	session := &CallSession{
		Caller:    caller,
		Callee:    callee,
		Status:    CallCalling,
		StartedAt: now,
		UpdatedAt: now,
	}
	c.callMap[caller] = session
	c.callMap[callee] = session

	zlog.Info(fmt.Sprintf("通话发起：%s → %s，状态=calling", caller, callee))
	return nil
}

// AcceptCall 接听通话，状态从 calling 变为 in_call
func (c *CallStateManager) AcceptCall(caller, callee string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if session, exists := c.callMap[caller]; exists {
		session.Status = CallInCall
		session.UpdatedAt = time.Now()
	}
	if session, exists := c.callMap[callee]; exists {
		session.Status = CallInCall
		session.UpdatedAt = time.Now()
	}

	zlog.Info(fmt.Sprintf("通话接听：%s ↔ %s，状态=in_call", caller, callee))
}

// Heartbeat 心跳续期：刷新用户当前通话的最近活跃时间，防止通话中状态被误回收
func (c *CallStateManager) Heartbeat(userId string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if session, exists := c.callMap[userId]; exists {
		session.UpdatedAt = time.Now()
	}
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

// sweepExpired 清理超时状态：振铃超时无人接、通话中长时间无心跳
func (c *CallStateManager) sweepExpired() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	now := time.Now()
	seen := make(map[*CallSession]bool)
	for _, session := range c.callMap {
		if seen[session] {
			continue
		}
		var expired bool
		switch session.Status {
		case CallCalling:
			expired = now.Sub(session.StartedAt) > ringingTTL
		case CallInCall:
			expired = now.Sub(session.UpdatedAt) > heartbeatTTL
		}
		if expired {
			seen[session] = true
			delete(c.callMap, session.Caller)
			delete(c.callMap, session.Callee)
			zlog.Info(fmt.Sprintf("通话状态超时回收：%s ↔ %s，状态=%s", session.Caller, session.Callee, session.Status))
		}
	}
}

// sweepLoop 后台定期扫描回收超时通话状态
func (c *CallStateManager) sweepLoop() {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		c.sweepExpired()
	}
}
