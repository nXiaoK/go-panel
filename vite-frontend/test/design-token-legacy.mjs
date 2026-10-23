// U01 前既有违规的精确数量；减少时必须同步收紧，不得新增文件/值/次数。
// 旧值/次数不再存在时必须移除对应项，不能保留可回填额度。
// owner 是负责清理的任务，U12 必须删除全部允许项并将计数归零。
export const legacyViolations = {
  "src/components/app-sidebar.tsx": {
    owner: "U03",
    values: {
      "text-[10px]": 3,
    },
  },
  "src/components/app-topbar.tsx": {
    owner: "U03",
    values: {
      "text-[10px]": 3,
      "text-[11px]": 2,
    },
  },
  "src/features/auth/page.tsx": {
    owner: "U03",
    values: {
      "text-[11px]": 1,
    },
  },
  "src/features/config/page.tsx": {
    owner: "U11",
    values: {
      "amber-500": 4,
      "amber-700": 3,
      "amber-300": 3,
      "text-[11px]": 1,
    },
  },
  "src/features/dashboard/page.tsx": {
    owner: "U04",
    values: {
      "text-[10px]": 10,
      "text-[11px]": 12,
      "emerald-500": 2,
      "amber-500": 2,
      "text-[9px]": 1,
    },
  },
  "src/features/forward/components/exit-member-picker.tsx": {
    owner: "U05",
    values: {
      "text-[11px]": 2,
      "text-[10px]": 1,
    },
  },
  "src/features/forward/components/nft-import-dialog.tsx": {
    owner: "U05",
    values: {
      "text-[10px]": 2,
      "text-[11px]": 2,
    },
  },
  "src/features/forward/page.tsx": {
    owner: "U05",
    values: {
      "text-[10px]": 8,
      "text-[11px]": 3,
      "emerald-500": 2,
      "emerald-400": 1,
    },
  },
  "src/features/node/page.tsx": {
    owner: "U07",
    values: {
      "text-[11px]": 7,
      "amber-500": 5,
      "text-[10px]": 4,
      "emerald-500": 3,
      "emerald-400": 4,
      "amber-400": 1,
    },
  },
  "src/features/subscription/page.tsx": {
    owner: "U10",
    values: {
      "blue-100": 1,
      "blue-700": 1,
      "blue-950": 1,
      "blue-300": 1,
      "indigo-100": 1,
      "indigo-700": 1,
      "indigo-950": 1,
      "indigo-300": 1,
      "purple-100": 1,
      "purple-700": 1,
      "purple-950": 1,
      "purple-300": 1,
      "amber-100": 1,
      "amber-700": 1,
      "amber-950": 1,
      "amber-300": 1,
      "emerald-100": 1,
      "emerald-700": 1,
      "emerald-950": 1,
      "emerald-300": 1,
      "cyan-100": 1,
      "cyan-700": 1,
      "cyan-950": 1,
      "cyan-300": 1,
      "emerald-500": 3,
      "emerald-600": 2,
      "emerald-400": 2,
    },
  },
  "src/features/system/components/r2-backup-card.tsx": {
    owner: "U11",
    values: {
      "amber-500": 2,
      "amber-700": 1,
      "amber-300": 1,
    },
  },
  "src/features/system/components/system-update-dialog.tsx": {
    owner: "U03",
    values: {
      "text-[11px]": 1,
    },
  },
  "src/features/tunnel/page.tsx": {
    owner: "U06",
    values: {
      "text-[10px]": 2,
      "text-[11px]": 2,
      "emerald-400": 1,
      "emerald-500": 2,
    },
  },
  "src/features/user/page.tsx": {
    owner: "U08",
    values: {
      "text-[11px]": 2,
    },
  },
};
