import { createStore } from 'vuex'

// ===== WebSocket 心跳保活 =====
const HEARTBEAT_INTERVAL = 15000 // 心跳发送间隔
const PONG_TIMEOUT = 5000        // 等待 pong 的超时时间

let heartbeatTimer = null
let pongTimeoutTimer = null

// startHeartbeat 启动周期心跳；超时收不到 pong 则主动关闭连接，触发 onclose 的重连/清理
export function startHeartbeat(socket, userId) {
  stopHeartbeat()
  const sendPing = () => {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    socket.send(JSON.stringify({
      type: -1,
      content: 'ping',
      send_id: userId,
    }))
    pongTimeoutTimer = setTimeout(() => {
      pongTimeoutTimer = null
      // 判定连接失活，主动关闭
      try { socket.close() } catch (e) { /* ignore */ }
    }, PONG_TIMEOUT)
  }
  heartbeatTimer = setInterval(sendPing, HEARTBEAT_INTERVAL)
}

// stopHeartbeat 停止心跳与 pong 超时计时
export function stopHeartbeat() {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer)
    heartbeatTimer = null
  }
  if (pongTimeoutTimer) {
    clearTimeout(pongTimeoutTimer)
    pongTimeoutTimer = null
  }
}

// handlePong 收到服务端 pong 时调用，取消本次超时判定
export function handlePong() {
  if (pongTimeoutTimer) {
    clearTimeout(pongTimeoutTimer)
    pongTimeoutTimer = null
  }
}

// isPongMessage 判断一条消息是否为心跳 pong
export function isPongMessage(data) {
  try {
    const m = JSON.parse(data)
    return m.type === -1 && m.content === 'pong'
  } catch (e) {
    return false
  }
}

// ===== 应用层 ACK 可靠发送 =====
const ACK_TIMEOUT = 3000   // 等待 ACK 的超时时间（毫秒）
const MAX_RETRIES = 3      // 最大重发次数
const ACK_TYPE = 4         // 服务端 ACK 报文类型

// 模块级：待确认消息的定时器与重发计数（非响应式，不进 Vuex）
// { [clientMsgId]: { timer, retryCount, message } }
const pendingTimers = {}

// generateClientMsgId 生成 20 位客户端消息 ID：C + 13位时间戳 + 6位随机
export function generateClientMsgId() {
  const ts = Date.now().toString()
  const rand = Math.random().toString(36).slice(2, 8)
  return 'C' + ts + rand
}

// sendReliableMessage 可靠发送一条消息：
// 生成 clientMsgId → 标记 sending → 发送 → 启动超时 → 超时重发（最多 MAX_RETRIES 次）
// 返回 { clientMsgId }，调用方把 clientMsgId 挂到消息对象上用于状态展示
export function sendReliableMessage(store, socket, message) {
  if (!socket || socket.readyState !== WebSocket.OPEN) {
    return { clientMsgId: null }
  }
  const clientMsgId = message.client_msg_id || generateClientMsgId()
  const msgWithId = { ...message, client_msg_id: clientMsgId }

  // 重发时清掉旧定时器
  if (pendingTimers[clientMsgId]) {
    clearTimeout(pendingTimers[clientMsgId].timer)
  }

  const doSend = () => {
    try {
      socket.send(JSON.stringify(msgWithId))
    } catch (e) {
      console.error('消息发送失败', e)
    }
  }

  const scheduleRetry = () => {
    const pending = pendingTimers[clientMsgId]
    if (!pending) return
    if (pending.retryCount >= MAX_RETRIES) {
      // 超过重发上限，标记失败
      clearTimeout(pending.timer)
      delete pendingTimers[clientMsgId]
      store.commit('markMessageFailed', clientMsgId)
      return
    }
    pending.retryCount++
    doSend()
    pending.timer = setTimeout(scheduleRetry, ACK_TIMEOUT)
  }

  pendingTimers[clientMsgId] = {
    message: msgWithId,
    retryCount: 0,
    timer: setTimeout(scheduleRetry, ACK_TIMEOUT),
  }

  store.commit('markMessageSending', clientMsgId)
  doSend()
  return { clientMsgId }
}

// handleAck 收到服务端 ACK 时调用：清定时器、清除 sending 状态
export function handleAck(store, data) {
  try {
    const m = JSON.parse(data)
    if (m.type !== ACK_TYPE || !m.client_msg_id) return false
    const pending = pendingTimers[m.client_msg_id]
    if (pending) {
      clearTimeout(pending.timer)
      delete pendingTimers[m.client_msg_id]
    }
    store.commit('clearMessageStatus', m.client_msg_id)
    return true
  } catch (e) {
    return false
  }
}

// isAckMessage 判断一条原始报文是否为 ACK
export function isAckMessage(data) {
  try {
    const m = JSON.parse(data)
    return m.type === ACK_TYPE
  } catch (e) {
    return false
  }
}

// clearPendingMessages 清理所有待确认消息（登出/断线时调用）
export function clearPendingMessages(store) {
  Object.values(pendingTimers).forEach(p => clearTimeout(p.timer))
  Object.keys(pendingTimers).forEach(k => delete pendingTimers[k])
  store.commit('resetMessageStatus')
}

export default createStore({
  state: {
    // web服务器地址
    backendUrl: 'https://123.56.164.220:8000',
    wsUrl: 'wss://123.56.164.220:8000',
    userInfo: (sessionStorage.getItem('userInfo') && JSON.parse(sessionStorage.getItem('userInfo'))) || {},
    socket: null,
    // 消息发送状态：{ [clientMsgId]: 'sending' | 'failed' }，成功后清除
    messageSendStatus: {},
  },
  getters: {
  },
  mutations: {
    setUserInfo(state, userInfo) {
      state.userInfo = userInfo;
      sessionStorage.setItem('userInfo', JSON.stringify(userInfo));
    },
    cleanUserInfo(state) {
      state.userInfo = {};
      sessionStorage.removeItem('userInfo');
    },
    markMessageSending(state, clientMsgId) {
      state.messageSendStatus = { ...state.messageSendStatus, [clientMsgId]: 'sending' }
    },
    markMessageFailed(state, clientMsgId) {
      state.messageSendStatus = { ...state.messageSendStatus, [clientMsgId]: 'failed' }
    },
    clearMessageStatus(state, clientMsgId) {
      const next = { ...state.messageSendStatus }
      delete next[clientMsgId]
      state.messageSendStatus = next
    },
    resetMessageStatus(state) {
      state.messageSendStatus = {}
    },
  },
  actions: {
  },
  modules: {
  }
})
