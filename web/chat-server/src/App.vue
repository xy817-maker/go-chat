<template>
  <router-view />
</template>

<script>
import { onMounted } from "vue";
import { useStore } from "vuex";
import { useRouter } from "vue-router";
import { ElMessage } from "element-plus";
import axios from "axios";
import { connectSocket, disconnectSocket } from "./store";
export default {
  name: "App",
  setup() {
    const store = useStore();
    const router = useRouter();
    const getUserInfo = async () => {
      try {
        const req = {
          uuid: store.state.userInfo.uuid,
        };
        const rsp = await axios.post(
          store.state.backendUrl + "/user/getUserInfo",
          req
        );
        if (rsp.data.code == 200) {
          if (!rsp.data.data.avatar.startsWith("http")) {
            rsp.data.data.avatar = store.state.backendUrl + rsp.data.data.avatar;
          }
          store.commit("setUserInfo", rsp.data.data);
        } else {
          console.error(rsp.data.message);
        }
        console.log(rsp);
      } catch (error) {
        console.log(error);
      }
    };
    // 账号被封禁时的登出：主动断开（不重连）→ 通知服务端 → 清理本地信息
    const logout = async () => {
      const ownerId = store.state.userInfo.uuid;
      disconnectSocket(store);
      const rsp = await axios.post(store.state.backendUrl + "/user/wsLogout", {
        owner_id: ownerId,
      });
      store.commit("cleanUserInfo");
      if (rsp.data.code == 200) {
        router.push("/login");
        ElMessage.success("账号被封禁，退出登录");
      } else {
        ElMessage.error(rsp.data.message);
      }
    };
    onMounted(async () => {
      if (store.state.userInfo.uuid) {
        await getUserInfo();
        if (store.state.userInfo.status == 1) {
          logout();
          return;
        }
        // 通过统一入口建立连接（内含心跳、断线重连）
        connectSocket(store);
      }
    });
  },
};
</script>

<style>
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box; /* 推荐使用，以确保布局计算的一致性 */
}
</style>