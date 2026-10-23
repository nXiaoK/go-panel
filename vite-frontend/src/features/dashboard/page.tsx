import { useState } from "react";
import { Plus, RefreshCw } from "lucide-react";
import { PageHeader } from "@/components/patterns/page-header";
import { Button } from "@/components/ui/button";
import { useSession } from "@/hooks/use-session";
import { useNodeSocket } from "@/features/node/use-node-socket";
import { useNodeConnections } from "@/features/node/use-node-live";
import { useDashboardSources } from "./queries";
import { trafficTunnelRequestId, type TrafficRange } from "./traffic";
import { AdminStats, NodeSection, AttentionSection } from "./components/admin-dashboard";
import { UserStats, MyTunnels } from "./components/user-dashboard";
import { TrafficSection } from "./components/traffic-section";

export function Dashboard() {
  const { isAdmin } = useSession();
  const [range, setRange] = useState<TrafficRange>("24h");
  const [tunnel, setTunnel] = useState("all");
  const queries = useDashboardSources(range, trafficTunnelRequestId(tunnel));
  useNodeSocket(isAdmin);
  const connections = useNodeConnections();
  return (
    <div className="min-w-0 space-y-6">
      <PageHeader
        title="控制台"
        actions={
          <>
            <span className="text-caption text-mute">
              上次同步{" "}
              <time className="font-mono">
                {queries.lastSync
                  ? new Intl.DateTimeFormat("zh-CN", {
                      hour: "2-digit",
                      minute: "2-digit",
                      second: "2-digit",
                      hourCycle: "h23",
                    }).format(queries.lastSync)
                  : "—"}
              </time>
            </span>
            <Button
              variant="outline"
              loading={queries.isFetching}
              onClick={() => {
                void queries.refetch();
              }}
            >
              <RefreshCw aria-hidden="true" />
              刷新
            </Button>
            <Button asChild>
              <a href="/forward?create=1">
                <Plus aria-hidden="true" />
                新建转发
              </a>
            </Button>
          </>
        }
      />
      {isAdmin ? (
        <AdminStats queries={queries} connections={connections} />
      ) : (
        <UserStats queries={queries} />
      )}
      <TrafficSection
        queries={queries}
        range={range}
        setRange={setRange}
        tunnel={tunnel}
        setTunnel={setTunnel}
      />
      {isAdmin ? (
        <div className="grid min-w-0 gap-6 xl:grid-cols-[minmax(0,1.65fr)_minmax(0,1fr)]">
          <NodeSection queries={queries} connections={connections} />
          <AttentionSection queries={queries} connections={connections} />
        </div>
      ) : (
        <MyTunnels queries={queries} />
      )}
    </div>
  );
}
