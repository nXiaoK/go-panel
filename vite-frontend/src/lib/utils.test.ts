import { expect, it } from "vitest";
import { cn } from "./utils";
it("设计字号与语义颜色共存，调用方仍可覆盖字号", () => {
  expect(cn("text-section-title text-foreground")).toBe("text-section-title text-foreground");
  expect(cn("text-button", "text-on-primary")).toBe("text-button text-on-primary");
  expect(cn("text-caption text-body", "text-sm")).toBe("text-body text-sm");
});
