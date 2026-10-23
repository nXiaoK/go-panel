import { ProfilePage } from "./page";
import { SettingsPage } from "./settings-page";
import { useSession } from "@/hooks/use-session";
import { Route } from "@/routes/_app.account";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";

/** U03 的可用账户适配：保留现有资料/改密/偏好行为，U09 再统一分区样式。 */
export function AccountPage() {
  const { tab, forcePassword } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { mustChangePassword } = useSession();
  const forced = mustChangePassword || forcePassword === 1;
  return (
    <Tabs
      value={forced ? "security" : tab}
      onValueChange={(value) =>
        void navigate({ search: { tab: value as "security" | "preferences" } })
      }
    >
      {!forced && (
        <TabsList aria-label="账户分区">
          <TabsTrigger value="security">资料与安全</TabsTrigger>
          <TabsTrigger value="preferences">偏好</TabsTrigger>
        </TabsList>
      )}
      <TabsContent value="security">
        <ProfilePage />
      </TabsContent>
      <TabsContent value="preferences">
        <SettingsPage />
      </TabsContent>
    </Tabs>
  );
}
