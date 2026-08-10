import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { Label } from "../src/components/ui/label";
import { Input } from "../src/components/ui/input";
import { Button } from "../src/components/ui/button";
import { FormField, QueryErrorNotice } from "../src/components/page";
import { Pencil, Trash2 } from "lucide-react";

/**
 * Contract fixtures for audited form labels and icon-only controls.
 * Source pages must keep matching id/htmlFor and aria-label strings.
 */
function UserFormFields() {
  return (
    <div>
      <Label htmlFor="user-form-username">用户名</Label>
      <Input id="user-form-username" />
      <Label htmlFor="user-form-name">昵称</Label>
      <Input id="user-form-name" />
      <Label htmlFor="user-form-password">密码</Label>
      <Input id="user-form-password" type="password" />
      <Label htmlFor="user-form-flow">流量限制 (GB)</Label>
      <Input id="user-form-flow" type="number" />
      <Label htmlFor="user-form-num">转发数量</Label>
      <Input id="user-form-num" type="number" />
    </div>
  );
}

function LimitFormFields() {
  return (
    <div>
      <Label htmlFor="limit-form-name">策略名称</Label>
      <Input id="limit-form-name" />
      <Label htmlFor="limit-form-speed">限速速率 (Mbps)</Label>
      <Input id="limit-form-speed" type="number" />
      <Button variant="ghost" size="icon" aria-label="编辑限速策略">
        <Pencil className="h-4 w-4" />
      </Button>
      <Button variant="ghost" size="icon" aria-label="删除限速策略">
        <Trash2 className="h-4 w-4" />
      </Button>
    </div>
  );
}

describe("accessibility contracts", () => {
  it("exposes labeled user form fields", () => {
    render(<UserFormFields />);
    expect(screen.getByLabelText("用户名")).toBeTruthy();
    expect(screen.getByLabelText("昵称")).toBeTruthy();
    expect(screen.getByLabelText("密码")).toBeTruthy();
    expect(screen.getByLabelText("流量限制 (GB)")).toBeTruthy();
    expect(screen.getByLabelText("转发数量")).toBeTruthy();
  });

  it("exposes labeled limit form fields and named icon buttons", () => {
    render(<LimitFormFields />);
    expect(screen.getByLabelText("策略名称")).toBeTruthy();
    expect(screen.getByLabelText("限速速率 (Mbps)")).toBeTruthy();
    expect(screen.getByRole("button", { name: "编辑限速策略" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "删除限速策略" })).toBeTruthy();
  });

  it("announces query failures and exposes a retry action", () => {
    render(<QueryErrorNotice error={new Error("network unavailable")} onRetry={() => {}} />);
    expect(screen.getByRole("alert").textContent).toContain("network unavailable");
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
  });

  it("connects shared form-field labels to their controls", () => {
    render(
      <FormField label="节点名称" htmlFor="node-name">
        <Input id="node-name" />
      </FormField>,
    );
    expect(screen.getByLabelText("节点名称")).toBeTruthy();
  });

  it("keeps HeadContent mounted in the SPA root", () => {
    const root = readFileSync(resolve(__dirname, "../src/routes/__root.tsx"), "utf8");
    expect(root).toMatch(/HeadContent/);
    expect(root).toMatch(/<HeadContent\s*\/>/);
  });

  it("wires stable ids in user and limit routes", () => {
    const user = readFileSync(resolve(__dirname, "../src/routes/_app.user.tsx"), "utf8");
    const limit = readFileSync(resolve(__dirname, "../src/routes/_app.limit.tsx"), "utf8");
    const node = readFileSync(resolve(__dirname, "../src/routes/_app.node.tsx"), "utf8");
    const tunnel = readFileSync(resolve(__dirname, "../src/routes/_app.tunnel.tsx"), "utf8");
    const forward = readFileSync(resolve(__dirname, "../src/routes/_app.forward.tsx"), "utf8");
    expect(user).toContain('htmlFor="user-form-username"');
    expect(user).toContain('id="user-form-username"');
    expect(user).toContain('aria-label="用户操作"');
    expect(limit).toContain('htmlFor="limit-form-name"');
    expect(limit).toContain('aria-label="编辑限速策略"');
    expect(limit).toContain('aria-label="删除限速策略"');
    expect(node).toContain("aria-label={`节点操作：${node.name}`}");
    expect(tunnel).toContain("aria-label={`隧道操作：${t.name}`}");
    expect(forward).toContain("aria-label={`转发操作：${f.name}`}");
  });
});
