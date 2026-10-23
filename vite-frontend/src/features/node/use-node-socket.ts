import { useEffect } from "react";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { useSession } from "@/hooks/use-session";
import { buildPanelWsUrl } from "@/lib/panel-ws";
import { resourceKeys } from "@/lib/query-keys";
import { reconnectDelay } from "@/lib/reconnect";
import { readSession, subscribeSession } from "@/lib/session";
import { nodeLiveStore, type NodeLiveMessage } from "./live-store";

type SocketLike = Pick<
  WebSocket,
  "readyState" | "close" | "onopen" | "onmessage" | "onerror" | "onclose"
>;
export function createNodeSocketManager(deps: {
  connect: (token: string) => SocketLike;
  receive: (message: NodeLiveMessage) => void;
  clear: () => void;
}) {
  let socket: SocketLike | null = null;
  let token: string | null = null;
  let attempts = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const leases = new Map<symbol, (() => void) | undefined>();
  function stop() {
    clearTimeout(timer);
    timer = undefined;
    attempts = 0;
    if (socket) {
      const old = socket;
      socket = null;
      old.onopen = old.onmessage = old.onerror = old.onclose = null;
      old.close();
    }
  }
  function schedule() {
    if (!leases.size || !token) return;
    clearTimeout(timer);
    timer = setTimeout(connect, reconnectDelay(attempts++));
  }
  function connect() {
    if (!leases.size || !token || socket) return;
    try {
      const current = deps.connect(token);
      socket = current;
      current.onopen = () => {
        if (socket === current) attempts = 0;
      };
      current.onmessage = (event) => {
        if (socket !== current) return;
        try {
          const message = JSON.parse(String(event.data)) as NodeLiveMessage;
          if (!message || typeof message !== "object") return;
          deps.receive(message);
          if (message.type === "status" && message.data === 1) {
            // 仅失效 REST 元数据；实时指标和速率基准始终留在独立 store 中。
            new Set(leases.values()).forEach((listener) => listener?.());
          }
        } catch {
          /* 忽略不完整的推送，不破坏现有指标。 */
        }
      };
      current.onerror = () => {
        /* close 统一处理重连，避免重复计时器。 */
      };
      current.onclose = () => {
        if (socket !== current) return;
        socket = null;
        // 管理员连接没有初始快照；断流期间的离线广播可能丢失，撤销旧 live 权威以回退 REST。
        deps.clear();
        schedule();
      };
    } catch {
      schedule();
    }
  }
  function retryNow() {
    if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
    clearTimeout(timer);
    timer = undefined;
    attempts = 0;
    connect();
  }
  function events(add: boolean) {
    if (typeof window === "undefined") return;
    if (add) {
      window.addEventListener("online", retryNow);
      document.addEventListener("visibilitychange", retryNow);
    } else {
      window.removeEventListener("online", retryNow);
      document.removeEventListener("visibilitychange", retryNow);
    }
  }
  function reset() {
    stop();
    events(false);
    leases.clear();
    token = null;
    deps.clear();
  }
  return {
    reset,
    getToken: () => token,
    acquire(nextToken: string, onOnline?: () => void) {
      if (token && token !== nextToken) reset();
      token = nextToken;
      const id = Symbol();
      const first = leases.size === 0;
      leases.set(id, onOnline);
      if (first) events(true);
      connect();
      return () => {
        if (!leases.delete(id)) return;
        if (!leases.size) {
          stop();
          events(false);
          deps.clear();
        }
      };
    },
  };
}

const manager = createNodeSocketManager({
  connect: (token) => new WebSocket(buildPanelWsUrl(), [`jwt.${token}`]),
  receive: (message) => nodeLiveStore.receive(message),
  clear: () => nodeLiveStore.clear(),
});
subscribeSession(() => {
  const session = readSession();
  if (session.token !== manager.getToken() || session.roleId !== 0 || session.mustChangePassword)
    manager.reset();
});

// 相同 QueryClient 的多个消费者复用失效回调，单个上线事件只触发一次列表刷新。
const invalidators = new WeakMap<QueryClient, () => void>();
function nodeInvalidator(client: QueryClient) {
  let callback = invalidators.get(client);
  if (!callback) {
    callback = () => {
      void client.invalidateQueries({ queryKey: resourceKeys.node.list() });
    };
    invalidators.set(client, callback);
  }
  return callback;
}

/** 节点页和管理员控制台共用一条连接；最后一个消费者卸载时关闭，身份变化时清空指标。 */
export function useNodeSocket(enabled = true) {
  const { token, isAdmin, mustChangePassword } = useSession();
  const client = useQueryClient();
  useEffect(() => {
    if (!enabled || !token || !isAdmin || mustChangePassword) return;
    return manager.acquire(token, nodeInvalidator(client));
  }, [client, enabled, token, isAdmin, mustChangePassword]);
}
