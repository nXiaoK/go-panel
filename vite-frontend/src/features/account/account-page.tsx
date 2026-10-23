import { ProfilePage } from "./page";
import { SettingsPage } from "./settings-page";
import { PageHeader } from "@/components/patterns/page-header";
import { useSession } from "@/hooks/use-session";
import { Route } from "@/routes/_app.account";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";

export function AccountPage() {
  const { tab, forcePassword } = Route.useSearch();
  const navigate = Route.useNavigate();
  const { mustChangePassword } = useSession();
  const forced = mustChangePassword || forcePassword === 1;
  return (
    <div className="mx-auto min-w-0 max-w-3xl space-y-6">
      <PageHeader title="账户" />
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
        <TabsContent value="security" className={forced ? "mt-0" : "mt-6"}>
          <ProfilePage />
        </TabsContent>
        {!forced && (
          <TabsContent value="preferences" className="mt-6">
            <SettingsPage />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
