import { clsx, type ClassValue } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// 将设计字号登记为font-size类，避免与text-ink等颜色互相覆盖；text-body只作为颜色使用。
const mergeClasses = extendTailwindMerge({
  extend: {
    classGroups: {
      "font-size": [
        {
          text: [
            "page-title",
            "section-title",
            "card-title",
            "body-strong",
            "body-lg",
            "caption",
            "caption-strong",
            "eyebrow",
            "metric-lg",
            "metric-md",
            "code",
            "code-strong",
            "button",
          ],
        },
      ],
    },
  },
});
export function cn(...inputs: ClassValue[]) {
  return mergeClasses(clsx(inputs));
}
