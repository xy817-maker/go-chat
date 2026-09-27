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

export default createStore({
  state: {
    // web服务器地址
    // backendUrl: 'https://192.168.2.114:8000',
    // wsUrl: 'wss://192.168.2.114:8000',
    backendUrl: 'https://123.56.164.220:8000',
    wsUrl: 'wss://123.56.164.220:8000',
    // 信令服务器地址
    // signalUrl: 'wss://127.0.0.1:8001',
    userInfo: (sessionStorage.getItem('userInfo') && JSON.parse(sessionStorage.getItem('userInfo'))) || {},
    socket: null,
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
    }
  },
  actions: {
  },
  modules: {
  }
})
