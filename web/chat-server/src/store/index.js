import { createStore } from "vuex";

// ============ 心跳常量 ============
const HEARTBEAT_TYPE = -1; // 心跳消息类型
const HEARTBEAT_INTERVAL = 15000; // 心跳周期：15 秒
const PONG_TIMEOUT = 5000; // 发出探测后等待 pong 的时间：5 秒
const MAX_MISSED_HEARTBEATS = 3; // 连续 3 次探测无响应才判定断线

// ============ 重连常量 ============
const RECONNECT_BASE_DELAY = 1000; // 首次重连间隔：1 秒
const RECONNECT_MAX_DELAY = 30000; // 重连间隔封顶：30 秒

// ============ 可靠发送常量 ============
export const ACK_TYPE = 4; // 服务端 ACK 消息类型
export const ACK_TIMEOUT = 3000; // 等待 ACK 的超时时间：3 秒
export const MAX_RETRIES = 3; // 最大重发次数

// ============ 心跳 / 连接的模块级状态 ============
let heartbeatTimer = null;
let pongTimer = null;
let missedHeartbeats = 0; // 连续未收到 pong 的次数

let reconnectTimer = null;
let reconnectAttempts = 0;
let manualClose = false; // 用户主动关闭（登出）时为 true，不触发重连
let hasOpenedBefore = false; // 本次连接周期是否已成功打开过，用于区分首连与重连

// 事件监听者
const openListeners = []; // 重连成功后的回调
const messageListeners = []; // 业务消息回调（pong / ACK 已在内部处理）

// ============ 心跳逻辑 ============
// 判断是否为心跳响应消息
export const isPongMessage = (data) => {
  try {
    return JSON.parse(data).type === HEARTBEAT_TYPE;
  } catch (e) {
    return false;
  }
};

// 启动心跳：每 15 秒发一次 ping，连续 MAX_MISSED_HEARTBEATS 次无 pong 才断开
export const startHeartbeat = (store) => {
  stopHeartbeat();
  heartbeatTimer = setInterval(() => {
    const socket = store.state.socket;
    if (socket && socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: HEARTBEAT_TYPE }));
    }
    // 为本次探测设置 pong 超时计时
    if (pongTimer) clearTimeout(pongTimer);
    pongTimer = setTimeout(() => {
      missedHeartbeats += 1;
      console.warn(
        `[心跳] 第 ${missedHeartbeats}/${MAX_MISSED_HEARTBEATS} 次未收到响应`
      );
      if (missedHeartbeats >= MAX_MISSED_HEARTBEATS) {
        const socket = store.state.socket;
        if (socket) {
          try {
            socket.close(); // 主动断开，close 事件会触发重连流程
          } catch (e) {}
        }
      }
    }, PONG_TIMEOUT);
  }, HEARTBEAT_INTERVAL);
};

// 停止心跳，清理所有定时器
export const stopHeartbeat = () => {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
  if (pongTimer) {
    clearTimeout(pongTimer);
    pongTimer = null;
  }
};

// 收到 pong：失败计数清零并取消本次超时
export const handlePong = () => {
  missedHeartbeats = 0;
  if (pongTimer) {
    clearTimeout(pongTimer);
    pongTimer = null;
  }
};

// ============ 连接管理（统一建连 / 重连 / 事件订阅） ============
// 订阅“重连成功”事件，返回取消订阅函数
export const onSocketOpen = (listener) => {
  openListeners.push(listener);
  return () => {
    const index = openListeners.indexOf(listener);
    if (index >= 0) openListeners.splice(index, 1);
  };
};

// 订阅业务消息（pong / ACK 已由连接管理内部处理），返回取消订阅函数
export const onSocketMessage = (listener) => {
  messageListeners.push(listener);
  return () => {
    const index = messageListeners.indexOf(listener);
    if (index >= 0) messageListeners.splice(index, 1);
  };
};

// 通知所有页面：连接已恢复
const notifyOpen = () => {
  openListeners.slice().forEach((fn) => {
    try {
      fn();
    } catch (e) {
      console.error("[连接] 打开事件回调异常:", e);
    }
  });
};

// 把业务消息分发给所有页面监听者
const dispatchMessage = (rawData) => {
  messageListeners.slice().forEach((fn) => {
    try {
      fn(rawData);
    } catch (e) {
      console.error("[连接] 消息回调异常:", e);
    }
  });
};

// 安排重连：指数退避（1s → 2s → 4s …）+ 随机抖动，封顶 30s
const scheduleReconnect = (store) => {
  if (manualClose || reconnectTimer) return;
  const exponentialDelay = Math.min(
    RECONNECT_BASE_DELAY * Math.pow(2, reconnectAttempts),
    RECONNECT_MAX_DELAY
  );
  const jitter = Math.floor(Math.random() * 500); // 0~500ms 随机抖动，避免多客户端同时重连
  const delay = exponentialDelay + jitter;
  reconnectAttempts += 1;
  console.log(
    `[连接] ${delay}ms 后进行第 ${reconnectAttempts} 次重连`
  );
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectSocket(store);
  }, delay);
};

// 建立 WebSocket 连接（登录页与根组件统一调用此入口）
export const connectSocket = (store) => {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  manualClose = false;
  const socket = new WebSocket(
    store.state.wsUrl + "/wss?client_id=" + store.state.userInfo.uuid
  );
  store.state.socket = socket;

  socket.onopen = () => {
    const isReconnect = hasOpenedBefore;
    missedHeartbeats = 0;
    reconnectAttempts = 0;
    hasOpenedBefore = true;
    startHeartbeat(store);
    console.log("[连接] 已打开" + (isReconnect ? "（重连）" : ""));
    if (isReconnect) notifyOpen();
  };

  socket.onmessage = (event) => {
    const rawData = event.data;
    if (isPongMessage(rawData)) {
      handlePong();
      return;
    }
    if (isAckMessage(rawData)) {
      handleAck(store, rawData);
      return;
    }
    dispatchMessage(rawData);
  };

  socket.onclose = () => {
    stopHeartbeat();
    if (manualClose) {
      console.log("[连接] 已手动关闭，不再重连");
      return;
    }
    console.warn("[连接] 已断开，准备重连");
    scheduleReconnect(store);
  };

  socket.onerror = (err) => {
    console.error("[连接] 发生错误:", err);
    // 浏览器随后会触发 close 事件，重连交给 onclose 处理
  };
};

// 主动关闭连接（用户登出时调用），关闭后不重连
export const disconnectSocket = (store) => {
  manualClose = true;
  hasOpenedBefore = false;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  stopHeartbeat();
  const socket = store.state.socket;
  if (socket) {
    try {
      socket.close();
    } catch (e) {}
  }
  store.state.socket = null;
};

// ============ ACK 处理与可靠发送 ============
// 记录等待 ACK 的消息：{ clientMsgId: { timer, socket, request } }
const pendingAckTimers = {};

// 判断是否为 ACK 消息
export const isAckMessage = (data) => {
  try {
    const msg = JSON.parse(data);
    return msg.type === ACK_TYPE && msg.client_msg_id;
  } catch (e) {
    return false;
  }
};

// 处理服务端 ACK：清除重发定时器，标记消息发送成功
export const handleAck = (store, data) => {
  try {
    const ackMsg = JSON.parse(data);
    const clientMsgId = ackMsg.client_msg_id;

    // 清除该消息的重发定时器
    if (pendingAckTimers[clientMsgId]) {
      clearTimeout(pendingAckTimers[clientMsgId].timer);
      delete pendingAckTimers[clientMsgId];
    }

    // 更新消息状态为已发送
    store.commit("updateMessageSendStatus", {
      clientMsgId,
      status: "sent",
    });
    console.log(`收到 ACK，消息 ${clientMsgId} 已被服务端确认`);
  } catch (e) {
    console.error("处理 ACK 失败:", e);
  }
};

// 生成客户端消息 ID（C + 13位时间戳 + 6位随机字符），用于幂等去重
export const generateClientMsgId = () => {
  return "C" + Date.now() + Math.random().toString(36).slice(2, 8);
};

// 可靠发送消息：发送后等待 ACK，超时未收到则重发，最多重发 MAX_RETRIES 次
export const sendReliableMessage = (store, socket, request) => {
  const clientMsgId = request.client_msg_id;

  // 标记消息为发送中
  store.commit("updateMessageSendStatus", {
    clientMsgId,
    status: "sending",
  });

  const send = () => {
    if (socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify(request));
      console.log(`消息 ${clientMsgId} 已发送，等待 ACK...`);
    } else {
      console.warn("连接未打开，消息暂存等待重连");
    }
  };

  // 等待 ACK 超时后重发
  const waitForAck = (retriesLeft) => {
    if (pendingAckTimers[clientMsgId]) {
      clearTimeout(pendingAckTimers[clientMsgId].timer);
    }

    pendingAckTimers[clientMsgId] = {
      timer: setTimeout(() => {
        if (retriesLeft > 0) {
          console.warn(
            `消息 ${clientMsgId} 未收到 ACK，剩余重发次数 ${retriesLeft}，正在重发...`
          );
          send();
          waitForAck(retriesLeft - 1);
        } else {
          console.error(`消息 ${clientMsgId} 重发 ${MAX_RETRIES} 次后仍失败`);
          store.commit("updateMessageSendStatus", {
            clientMsgId,
            status: "failed",
          });
          delete pendingAckTimers[clientMsgId];
        }
      }, ACK_TIMEOUT),
      socket,
      request,
    };
  };

  // 首次发送并开始等待 ACK
  send();
  waitForAck(MAX_RETRIES);

  return { clientMsgId };
};

// ============ Vuex 状态管理 ============
const store = createStore({
  state: {
    backendUrl: "https://123.56.164.220:8000",
    wsUrl: "wss://123.56.164.220:8000",
    userInfo: JSON.parse(sessionStorage.getItem("userInfo") || "{}"),
    socket: null,
    // 消息发送状态：{ [clientMsgId]: 'sending' | 'sent' | 'failed' }
    messageSendStatus: {},
  },
  mutations: {
    setUserInfo(state, userInfo) {
      state.userInfo = userInfo;
    },
    cleanUserInfo(state) {
      state.userInfo = {};
      sessionStorage.clear();
    },
    // 更新消息发送状态；sent 状态在前端渲染后直接清除
    updateMessageSendStatus(state, { clientMsgId, status }) {
      if (status === "sent") {
        delete state.messageSendStatus[clientMsgId];
      } else {
        state.messageSendStatus[clientMsgId] = status;
      }
    },
  },
});

export default store;
